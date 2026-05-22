package contracts

import (
	"time"

	"github.com/google/uuid"
)

// StackPayload mirrors contracts.py StackPayload. It captures wrapped
// solutions only: harnesses, skills, doc references, and notes. Raw configs,
// env values, secrets, and MCP credentials are rejected before persistence.
type StackPayload struct {
	Name      string   `json:"name"`
	Harnesses []string `json:"harnesses"`
	Skills    []string `json:"skills"`
	Docs      []string `json:"docs"`
	Notes     *string  `json:"notes,omitempty"`
}

// StackUpsertRequest mirrors contracts.py StackUpsertRequest.
type StackUpsertRequest StackPayload

// StackResponse mirrors contracts.py StackResponse.
type StackResponse struct {
	ID             uuid.UUID      `json:"id"`
	UserID         uuid.UUID      `json:"user_id"`
	Payload        StackPayload   `json:"payload"`
	Classification Classification `json:"classification"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// StackShareRequest mirrors contracts.py StackShareRequest.
type StackShareRequest struct {
	SharedWithUserID uuid.UUID `json:"shared_with_user_id"`
}
