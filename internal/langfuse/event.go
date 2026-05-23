package langfuse

import (
	"time"

	"github.com/google/uuid"
)

// Event is one envelope in a /api/public/ingestion batch. The Langfuse
// v3 ingestion API accepts a `{ "batch": [<event>, ...] }` body where
// each event is `{ id, type, timestamp, body }`. We only emit a single
// type at v1 — "generation-create" — but the struct is shaped generically
// so a later slice can add "score-create" without breaking call sites.
//
// Field names match the Langfuse wire format exactly; do NOT rename. See
// https://api.reference.langfuse.com and
// https://langfuse.com/docs/observability/data-model.
type Event struct {
	ID        string         `json:"id"`        // envelope id, also dedups requests
	Type      string         `json:"type"`      // e.g. "generation-create"
	Timestamp string         `json:"timestamp"` // RFC3339Nano (UTC)
	Body      map[string]any `json:"body"`
}

// Level mirrors the Langfuse observation level enum.
type Level string

const (
	// LevelDefault is the success path.
	LevelDefault Level = "DEFAULT"
	// LevelError marks a failed generation — paired with StatusMessage so
	// the trace UI surfaces the cause without us also posting an
	// observation-level exception.
	LevelError Level = "ERROR"
)

// Usage is the token accounting payload nested inside a generation body.
// Langfuse accepts other units (CHARACTERS, SECONDS) but everything we
// emit is in tokens, so we hard-code that here.
type Usage struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Total  int `json:"total"`
	// Unit is always "TOKENS" for our emissions.
	Unit string `json:"unit"`
}

// Generation is the typed input to NewGenerationEvent. We accept this
// rather than a free-form map so the call site in the LLM router can't
// drift away from the wire shape silently.
type Generation struct {
	// TraceID groups related observations. The LLM router uses one trace
	// per Complete() call until upstream context plumbing lands.
	TraceID string

	// ObservationID is this generation's own id. NewGenerationEvent fills
	// in a v4 UUID when empty.
	ObservationID string

	// Name is the human-readable label shown in the Langfuse UI. The
	// router uses "<provider>.<tier>", e.g. "anthropic.cheap_hot".
	Name string

	// StartTime / EndTime bracket the underlying provider call.
	StartTime time.Time
	EndTime   time.Time

	// Model is the concrete model identifier the provider used
	// (e.g. "claude-haiku-4.5", "gpt-4o-mini").
	Model string

	// Input is the prompt sent to the provider. For multi-turn chats we
	// JSON-encode the messages slice upstream and pass the resulting
	// string here; the wire field accepts either a string or any.
	Input string

	// Output is the completion text. Empty on error.
	Output string

	// Usage carries the token counts surfaced by the provider response.
	// Zero values are accepted; Langfuse renders "—" in that case.
	Usage Usage

	// Level is LevelDefault on success, LevelError on a provider failure.
	Level Level

	// StatusMessage is the error string when Level == LevelError;
	// otherwise empty. Never carries a secret — the router only forwards
	// err.Error() which provider implementations are responsible for
	// keeping clean.
	StatusMessage string

	// Metadata is a free-form bag attached to the generation. The router
	// populates {tier, provider, tenant_id} when available.
	Metadata map[string]string
}

// NewGenerationEvent builds the "generation-create" envelope. Missing UUIDs
// are filled in with fresh v4s. Times are serialized as RFC3339Nano UTC.
// Returns the event ready for Client.Submit.
func NewGenerationEvent(g Generation) Event {
	if g.ObservationID == "" {
		g.ObservationID = uuid.NewString()
	}
	if g.TraceID == "" {
		g.TraceID = uuid.NewString()
	}
	if g.Level == "" {
		g.Level = LevelDefault
	}

	body := map[string]any{
		"id":        g.ObservationID,
		"traceId":   g.TraceID,
		"name":      g.Name,
		"startTime": g.StartTime.UTC().Format(time.RFC3339Nano),
		"endTime":   g.EndTime.UTC().Format(time.RFC3339Nano),
		"model":     g.Model,
		"input":     g.Input,
		"output":    g.Output,
		"usage": Usage{
			Input:  g.Usage.Input,
			Output: g.Usage.Output,
			Total:  g.Usage.Total,
			Unit:   "TOKENS",
		},
		"level": string(g.Level),
	}
	if g.StatusMessage != "" {
		body["statusMessage"] = g.StatusMessage
	}
	if len(g.Metadata) > 0 {
		// Copy so callers can't mutate the event after submission.
		md := make(map[string]string, len(g.Metadata))
		for k, v := range g.Metadata {
			md[k] = v
		}
		body["metadata"] = md
	}

	return Event{
		ID:        uuid.NewString(),
		Type:      "generation-create",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Body:      body,
	}
}
