package contracts

import (
	"fmt"

	"github.com/google/uuid"
)

// Action is the decision returned by the pure suggestion_action function in
// internal/suggest. The wire-level enum is locked: the daemon, CLI, and
// dashboard all key off these three values to decide what to do with a
// refined prompt at task-start.
//
// Locked thresholds (see contracts.py CONFIDENCE_SUPPRESS_BELOW /
// CONFIDENCE_REPLACE_AT, ARCHITECTURE.md §5, CLAUDE.md "Locked invariants"):
//
//	confidence <  0.50  → ActionSuppress
//	confidence <  0.80  → ActionAdvisory
//	confidence >= 0.80  → ActionReplace
//
// The string values match the lowercase tokens used in contracts.py-side
// fixtures and the dashboard UI state, so JSON marshaling is direct.
type Action string

const (
	// ActionSuppress means the suggestion should not be surfaced to the user.
	ActionSuppress Action = "suppress"
	// ActionAdvisory means the suggestion is surfaced as advisory; the user
	// keeps their original prompt unless they choose otherwise.
	ActionAdvisory Action = "advisory"
	// ActionReplace means the suggestion is offered as a clipboard-ready
	// replacement for the user's prompt. Per CLAUDE.md "Locked invariants",
	// the suggestion is never injected into a terminal directly.
	ActionReplace Action = "replace"
)

// SessionContext is the CLI/daemon-provided context used by POST /v1/suggest.
// The raw prompt is the text to refine; everything else helps the template
// tailor the refinement to the active harness and repository shape.
type SessionContext struct {
	Harness   string   `json:"harness"`
	Model     string   `json:"model"`
	Effort    string   `json:"effort,omitempty"`
	Tools     []string `json:"tools"`
	RepoHash  *string  `json:"repo_hash"`
	GitBranch *string  `json:"git_branch"`
	CWDFiles  []string `json:"cwd_files"`
	RawPrompt string   `json:"raw_prompt"`
}

// SuggestOptions controls optional response shape and request budget.
type SuggestOptions struct {
	MaxLatencyMS    int  `json:"max_latency_ms"`
	IncludeEvidence bool `json:"include_evidence"`
}

// DefaultSuggestOptions matches contracts.py SuggestOptions defaults.
func DefaultSuggestOptions() SuggestOptions {
	return SuggestOptions{MaxLatencyMS: 800}
}

// SuggestRequest is the POST /v1/suggest request envelope.
type SuggestRequest struct {
	UserID         uuid.UUID      `json:"user_id"`
	TenantID       uuid.UUID      `json:"tenant_id"`
	SessionContext SessionContext `json:"session_context"`
	Options        SuggestOptions `json:"options"`
}

// DefaultSuggestRequest returns a request with nested defaults prefilled so
// json.Unmarshal preserves them when optional fields are omitted.
func DefaultSuggestRequest() SuggestRequest {
	return SuggestRequest{Options: DefaultSuggestOptions()}
}

// Validate returns field-specific validation errors matching the strict
// pydantic constraints in contracts.py. The map is empty when the request is
// valid.
func (r SuggestRequest) Validate() map[string]string {
	errs := make(map[string]string)
	if r.UserID == uuid.Nil {
		errs["user_id"] = "required"
	}
	if r.TenantID == uuid.Nil {
		errs["tenant_id"] = "required"
	}
	if !validHarness(r.SessionContext.Harness) {
		errs["session_context.harness"] = "invalid"
	}
	if r.SessionContext.Model == "" {
		errs["session_context.model"] = "required"
	}
	if r.SessionContext.Effort != "" && !validEffort(r.SessionContext.Effort) {
		errs["session_context.effort"] = "invalid"
	}
	if r.SessionContext.RawPrompt == "" {
		errs["session_context.raw_prompt"] = "required"
	}
	if len(r.SessionContext.RawPrompt) > 200_000 {
		errs["session_context.raw_prompt"] = "max_length_exceeded"
	}
	if r.Options.MaxLatencyMS < 50 || r.Options.MaxLatencyMS > 5_000 {
		errs["options.max_latency_ms"] = "must be between 50 and 5000"
	}
	if len(errs) == 0 {
		return nil
	}
	return errs
}

func validHarness(h string) bool {
	switch h {
	case "claude_code", "codex", "gemini_cli", "opencode", "pi":
		return true
	default:
		return false
	}
}

func validEffort(e string) bool {
	switch e {
	case "low", "med", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// SuggestEvidence is the optional evidence row shown when the caller opts in.
type SuggestEvidence struct {
	SessionID              uuid.UUID `json:"session_id"`
	Outcome                string    `json:"outcome"`
	WallTimeMS             *int      `json:"wall_time_ms"`
	ContributorDisplayName string    `json:"contributor_display_name"`
}

// NoSuggestionReason is the closed enum returned when the system declines to
// surface a refinement.
type NoSuggestionReason string

const (
	NoSuggestionNoEvidence            NoSuggestionReason = "no_evidence"
	NoSuggestionLowConfidence         NoSuggestionReason = "low_confidence"
	NoSuggestionLLMUnavailable        NoSuggestionReason = "llm_unavailable"
	NoSuggestionLLMUnparseable        NoSuggestionReason = "llm_unparseable"
	NoSuggestionLatencyBudgetExceeded NoSuggestionReason = "latency_budget_exceeded"
	NoSuggestionUserOptedOut          NoSuggestionReason = "user_opted_out"
)

// SuggestResponse is the POST /v1/suggest response envelope.
type SuggestResponse struct {
	Action             Action              `json:"action"`
	SuggestionID       *uuid.UUID          `json:"suggestion_id"`
	RefinedPrompt      *string             `json:"refined_prompt"`
	Rationale          *string             `json:"rationale"`
	Confidence         float64             `json:"confidence"`
	Evidence           []SuggestEvidence   `json:"evidence"`
	NoSuggestionReason *NoSuggestionReason `json:"no_suggestion_reason"`
}

// Validate checks response invariants that are independent of transport.
func (r SuggestResponse) Validate() error {
	if r.Confidence < 0 || r.Confidence > 1 {
		return fmt.Errorf("confidence %.3f out of [0,1]", r.Confidence)
	}
	if r.RefinedPrompt != nil && *r.RefinedPrompt == "" {
		return fmt.Errorf("refined_prompt must be non-empty if present")
	}
	return nil
}
