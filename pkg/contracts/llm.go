// Package contracts: LLM wire types.
//
// These types are the boundary between the request path (the suggest handler
// in issue 035, scoring callers in issue 046, webhook classifiers) and the
// `internal/llm` provider abstraction (issue 055). They live in
// `pkg/contracts` rather than `internal/llm` so multiple internal packages
// can import them without an import cycle through the router.
//
// Wire shape mirrors `contracts.py:LLMCompletionRequest` /
// `LLMCompletionResponse` once those land on the Python side; today the Go
// side is canonical.
package contracts

// LLMTier is the locked enum naming a workload's quality/cost class. The
// router (internal/llm.Router) maps each tier to a per-tier ordered list of
// providers so callers don't have to know which concrete model to ask for.
//
// Locked values (do NOT add new tiers without an entry in DECISIONS.md):
//
//	CheapHot  — `iter suggest` synchronous path; Haiku / Flash / Qwen tier.
//	Sonnet    — scoring batch enrichment; mid-tier reasoning.
//	Opus      — deep reasoning, e.g. retrospective root-cause; rarely invoked.
type LLMTier string

const (
	// LLMTierCheapHot is the latency-sensitive cheap tier used by /v1/suggest.
	LLMTierCheapHot LLMTier = "cheap_hot"
	// LLMTierSonnet is the mid reasoning tier used by the nightly scorer.
	LLMTierSonnet LLMTier = "sonnet"
	// LLMTierOpus is the deepest-reasoning tier used sparingly.
	LLMTierOpus LLMTier = "opus"
)

// LLMRole is the message author. Mirrors the OpenAI/Anthropic chat shape,
// which the abstraction adapts per-provider.
type LLMRole string

const (
	// LLMRoleSystem is the system / developer-instruction message.
	LLMRoleSystem LLMRole = "system"
	// LLMRoleUser is a user-authored message.
	LLMRoleUser LLMRole = "user"
	// LLMRoleAssistant is a prior model response in the conversation.
	LLMRoleAssistant LLMRole = "assistant"
)

// LLMMessage is one chat turn. Content is plain text at v1; multi-part
// (images, tool calls) is deferred — the suggest path doesn't need it.
type LLMMessage struct {
	Role    LLMRole `json:"role"`
	Content string  `json:"content"`
}

// LLMCompletionRequest is the wire request to the router. `Tier` selects the
// per-tier provider chain; concrete model identifier is picked by the
// provider implementation. `MaxTokens` is required (zero is rejected) so a
// runaway prompt can't burn the whole budget.
type LLMCompletionRequest struct {
	Tier        LLMTier      `json:"tier"`
	Messages    []LLMMessage `json:"messages"`
	Temperature float64      `json:"temperature"`
	MaxTokens   int          `json:"max_tokens"`
}

// LLMCompletionResponse carries the completion text plus the
// cost-accounting fields used by the Step 7 Langfuse integration. `Provider`
// is the `Name()` of the provider that actually answered (post-fallback) so
// the suggest handler can log "served by anthropic" vs "served by google".
type LLMCompletionResponse struct {
	Provider        string  `json:"provider"`
	Model           string  `json:"model"`
	Text            string  `json:"text"`
	TokensIn        int     `json:"tokens_in"`
	TokensOut       int     `json:"tokens_out"`
	CostUSDEstimate float64 `json:"cost_usd_estimate"`
	FinishReason    string  `json:"finish_reason"`
}
