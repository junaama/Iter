package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// VoyageProvider speaks the Voyage AI embeddings API directly over HTTP.
//
// Why HTTP and not the official SDK: same rationale as internal/llm's
// Anthropic client — the surface is a single endpoint with a stable JSON
// shape, and going direct keeps the dep graph minimal. The Provider
// interface is the boundary, so swapping to an SDK later doesn't touch
// callers.
//
// Default model: voyage-code-3 (1536-dim) — matches the
// session_embeddings.embedding vector(1536) and suggestions.source_embedding
// vector(1536) columns in migrations/0001_initial.sql. The dim must NEVER
// drift from the column definition; callers do not assert dim today
// (issue 054 keeps the contract simple), but a follow-up slice can layer
// a dim-check in the embedding worker before INSERT.
type VoyageProvider struct {
	apiKey  string
	baseURL string
	client  *http.Client
}

// VoyageConfig configures the provider. Empty APIKey is allowed — Embed
// will return ErrProviderNotConfigured so the router can fall through.
// BaseURL defaults to the public Voyage endpoint; tests inject a stub
// server URL.
type VoyageConfig struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

const (
	voyageDefaultBaseURL = "https://api.voyageai.com"
	// voyageDefaultModel is the v1 default (1536-dim). Documented in
	// DECISIONS.md so a model bump is a deliberate decision (vector
	// dimension is wired into the schema and an HNSW rebuild).
	voyageDefaultModel = "voyage-code-3"
)

// NewVoyageProvider builds a provider. Reads VOYAGE_API_KEY from env when
// APIKey is empty (the common cmd/server path).
func NewVoyageProvider(cfg VoyageConfig) *VoyageProvider {
	if cfg.APIKey == "" {
		cfg.APIKey = os.Getenv("VOYAGE_API_KEY")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = voyageDefaultBaseURL
	}
	if cfg.Client == nil {
		// Per-attempt timeout is a defense-in-depth ceiling; the caller's
		// ctx.Deadline is the primary budget.
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	return &VoyageProvider{
		apiKey:  cfg.APIKey,
		baseURL: cfg.BaseURL,
		client:  cfg.Client,
	}
}

// Name implements Provider.
func (*VoyageProvider) Name() string { return "voyage" }

// voyageRequest is the on-the-wire request shape.
type voyageRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// voyageResponse is the on-the-wire response shape. We only consume the
// Embedding fields and the input-token count; other fields are ignored.
type voyageResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed implements Provider.
func (p *VoyageProvider) Embed(ctx context.Context, req EmbedRequest) (EmbedResponse, error) {
	if p.apiKey == "" {
		return EmbedResponse{}, ErrProviderNotConfigured
	}
	if len(req.Inputs) == 0 {
		return EmbedResponse{}, fmt.Errorf("voyage: inputs must be non-empty")
	}

	model := req.Model
	if model == "" {
		model = voyageDefaultModel
	}
	body := voyageRequest{Input: req.Inputs, Model: model}
	buf, err := json.Marshal(body)
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("voyage: marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/embeddings", bytes.NewReader(buf))
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("voyage: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return EmbedResponse{}, fmt.Errorf("voyage: http: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded snippet for the log; never surface raw bytes
		// to the user.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		if resp.StatusCode == http.StatusTooManyRequests {
			return EmbedResponse{}, fmt.Errorf("voyage: http %d: %s: %w", resp.StatusCode, string(snippet), ErrRateLimited)
		}
		return EmbedResponse{}, fmt.Errorf("voyage: http %d: %s", resp.StatusCode, string(snippet))
	}

	var parsed voyageResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return EmbedResponse{}, fmt.Errorf("voyage: decode: %w", err)
	}
	if len(parsed.Data) != len(req.Inputs) {
		return EmbedResponse{}, fmt.Errorf("voyage: expected %d vectors, got %d", len(req.Inputs), len(parsed.Data))
	}

	// Voyage returns data items with explicit `index` fields; sort by
	// index so Vectors[i] matches Inputs[i] regardless of server order.
	vectors := make([][]float32, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return EmbedResponse{}, fmt.Errorf("voyage: out-of-range index %d", d.Index)
		}
		vectors[d.Index] = d.Embedding
	}
	for i, v := range vectors {
		if v == nil {
			return EmbedResponse{}, fmt.Errorf("voyage: missing vector at index %d", i)
		}
	}

	return EmbedResponse{
		Vectors:  vectors,
		TokensIn: parsed.Usage.TotalTokens,
	}, nil
}
