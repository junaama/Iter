package contracts

import (
	"time"

	"github.com/google/uuid"
)

// DaemonSuggestionAvailableResponse mirrors contracts.py
// DaemonSuggestionAvailableResponse for the local app-to-daemon Unix socket.
type DaemonSuggestionAvailableResponse struct {
	Available     bool              `json:"available"`
	ID            *string           `json:"id,omitempty"`
	SessionID     *uuid.UUID        `json:"session_id,omitempty"`
	Action        *Action           `json:"action,omitempty"`
	SuggestionID  *uuid.UUID        `json:"suggestion_id,omitempty"`
	RefinedPrompt *string           `json:"refined_prompt,omitempty"`
	Rationale     *string           `json:"rationale,omitempty"`
	Confidence    *float64          `json:"confidence,omitempty"`
	Evidence      []SuggestEvidence `json:"evidence"`
	CreatedAt     *time.Time        `json:"created_at,omitempty"`
}

// DaemonSuppressPatternRequest is the local IPC payload sent when the user
// chooses "Suppress this pattern" from the native suggestion notification.
type DaemonSuppressPatternRequest struct {
	RefinedPrompt string     `json:"refined_prompt"`
	SuggestionID   *uuid.UUID `json:"suggestion_id,omitempty"`
}

// DaemonSuppressPatternResponse flags that the cloud suppress endpoint has not
// landed yet while recording the suppression locally in the daemon.
type DaemonSuppressPatternResponse struct {
	Suppressed       bool   `json:"suppressed"`
	BackendEndpoint  string `json:"backend_endpoint"`
	PersistedLocally bool   `json:"persisted_locally"`
}
