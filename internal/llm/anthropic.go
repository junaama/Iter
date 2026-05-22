package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/iter-dev/iter/pkg/contracts"
)

// AnthropicProvider speaks the v1 Anthropic Messages API directly over HTTP.
//
// Why HTTP and not the official SDK: the surface we need is a single
// endpoint with a stable JSON shape, and the SDK pulls in an extra ~30
// transitive packages that the rest of the binary doesn't need. Recorded
// as a v1 decision in DECISIONS.md; if streaming/tool-use becomes a
// requirement, swap to the SDK at that point — the Provider interface is
// the boundary, so callers don't notice.
//
// Models per tier (DECISIONS.md "LLM provider chain (issue 055)"):
//   - CheapHot → claude-haiku-4-5 (fast, low-cost, suggest path)
//   - Sonnet   → claude-sonnet-4-6 (scoring batch)
//   - Opus     → claude-opus-4-5 (deep reasoning, rarely invoked)
type AnthropicProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// AnthropicConfig configures the provider. Empty APIKey is allowed —
// Complete will return ErrProviderNotConfigured so the router can fall
// through. BaseURL defaults to the public Anthropic endpoint; tests
// inject a stub server URL.
type AnthropicConfig struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com"
	anthropicAPIVersion     = "2023-06-01"

	anthropicModelHaiku  = "claude-haiku-4-5"
	anthropicModelSonnet = "claude-sonnet-4-6"
	anthropicModelOpus   = "claude-opus-4-5"
)

// NewAnthropicProvider builds a provider. Reads ANTHROPIC_API_KEY from env
// when APIKey is empty (the common cmd/server path).
func NewAnthropicProvider(cfg AnthropicConfig) *AnthropicProvider {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = anthropicDefaultBaseURL
	}
	if cfg.Client == nil {
		// Per-attempt timeout is a defense-in-depth ceiling; the caller's
		// ctx.Deadline is the primary budget.
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &AnthropicProvider{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		client:  cfg.Client,
	}
}

// Name implements Provider.
func (*AnthropicProvider) Name() string { return "anthropic" }

// Supports implements Provider. Anthropic serves every tier.
func (*AnthropicProvider) Supports(tier contracts.LLMTier) bool {
	switch tier {
	case contracts.LLMTierCheapHot, contracts.LLMTierSonnet, contracts.LLMTierOpus:
		return true
	default:
		return false
	}
}

// modelForTier picks the concrete Anthropic model id per tier.
func (*AnthropicProvider) modelForTier(tier contracts.LLMTier) string {
	switch tier {
	case contracts.LLMTierCheapHot:
		return anthropicModelHaiku
	case contracts.LLMTierSonnet:
		return anthropicModelSonnet
	case contracts.LLMTierOpus:
		return anthropicModelOpus
	default:
		return anthropicModelHaiku
	}
}

// anthropicMessage is the on-the-wire chat turn. Anthropic does NOT accept
// a "system" role inside messages; the system prompt is a separate field.
type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature,omitempty"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete implements Provider.
func (p *AnthropicProvider) Complete(ctx context.Context, req contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	if p.apiKey == "" {
		return contracts.LLMCompletionResponse{}, ErrProviderNotConfigured
	}
	if req.MaxTokens <= 0 {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("anthropic: max_tokens must be > 0")
	}

	model := p.modelForTier(req.Tier)
	system, msgs := splitSystem(req.Messages)
	body := anthropicRequest{
		Model:       model,
		System:      system,
		Messages:    toAnthropicMessages(msgs),
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("anthropic: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("anthropic: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Api-Key", p.apiKey)
	httpReq.Header.Set("Anthropic-Version", anthropicAPIVersion)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("anthropic: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded amount of the error body for the log; never
		// surface raw response bytes to the user.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return contracts.LLMCompletionResponse{}, fmt.Errorf("anthropic: http %d: %s", resp.StatusCode, string(snippet))
	}

	var parsed anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return contracts.LLMCompletionResponse{}, fmt.Errorf("anthropic: decode: %w", err)
	}

	text := ""
	for _, c := range parsed.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}

	return contracts.LLMCompletionResponse{
		Provider:        p.Name(),
		Model:           parsed.Model,
		Text:            text,
		TokensIn:        parsed.Usage.InputTokens,
		TokensOut:       parsed.Usage.OutputTokens,
		CostUSDEstimate: anthropicCostUSD(model, parsed.Usage.InputTokens, parsed.Usage.OutputTokens),
		FinishReason:    parsed.StopReason,
	}, nil
}

// splitSystem pulls the leading system message (if any) into the dedicated
// `system` field that the Anthropic Messages API expects, returning the
// remainder for the `messages` array. Multiple system messages are
// concatenated with a blank line, matching the SDK's behavior.
func splitSystem(in []contracts.LLMMessage) (string, []contracts.LLMMessage) {
	var system string
	rest := make([]contracts.LLMMessage, 0, len(in))
	for _, m := range in {
		if m.Role == contracts.LLMRoleSystem {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
			continue
		}
		rest = append(rest, m)
	}
	return system, rest
}

// toAnthropicMessages converts to the on-wire shape. Roles are already
// "user"/"assistant" (system is split off above).
func toAnthropicMessages(in []contracts.LLMMessage) []anthropicMessage {
	out := make([]anthropicMessage, 0, len(in))
	for _, m := range in {
		out = append(out, anthropicMessage{Role: string(m.Role), Content: m.Content})
	}
	return out
}

// anthropicCostUSD estimates the spend for a single completion. Published
// per-million-token prices as of v1 launch; this is a lightweight estimate
// for the Step 7 Langfuse traces, not invoiced. Update alongside any
// provider price change.
func anthropicCostUSD(model string, tokensIn, tokensOut int) float64 {
	var inPerM, outPerM float64
	switch model {
	case anthropicModelHaiku:
		inPerM, outPerM = 1.0, 5.0
	case anthropicModelSonnet:
		inPerM, outPerM = 3.0, 15.0
	case anthropicModelOpus:
		inPerM, outPerM = 15.0, 75.0
	default:
		inPerM, outPerM = 3.0, 15.0
	}
	return (float64(tokensIn)*inPerM + float64(tokensOut)*outPerM) / 1_000_000
}
