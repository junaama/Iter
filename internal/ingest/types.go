package ingest

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	StreamPrefix   = "ingest:queue:"
	ConsumerGroup  = "ingest-consumers"
	EmbedQueue     = "embed:queue"
	MessageField   = "message"
	RetriesField   = "retries"
	DefaultWorkers = 4
	MaxRetries     = 5
	claimMinIdle   = 30 * time.Second
	readBlock      = time.Second
	readBatchSize  = 8
	defaultHarness = "unknown"
	defaultModel   = "unknown"
	defaultPrompt  = "[prompt unavailable]"
)

// QueuedEvent is the Redis Stream payload emitted by the WS handler and
// consumed by the ingestion worker. Payload stays raw JSON until after the
// defense-in-depth redaction pass.
type QueuedEvent struct {
	TenantID   uuid.UUID       `json:"tenant_id"`
	UserID     uuid.UUID       `json:"user_id"`
	MsgID      uuid.UUID       `json:"msg_id"`
	SessionID  uuid.UUID       `json:"session_id"`
	EventID    uuid.UUID       `json:"event_id"`
	EventType  string          `json:"event_type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	ReceivedAt time.Time       `json:"received_at"`
}

// EmbedJob is the durable request shape pushed to embed:queue after the DB
// commit succeeds. Issue 045 owns the worker that consumes it.
type EmbedJob struct {
	TenantID       uuid.UUID `json:"tenant_id"`
	SessionID      uuid.UUID `json:"session_id"`
	RedactedPrompt string    `json:"redacted_prompt"`
	QueuedAt       time.Time `json:"queued_at"`
}

type sessionProjection struct {
	Harness        string
	Model          string
	Effort         *string
	Tools          []string
	RepoHash       *string
	GitBranch      *string
	RedactedPrompt string
	RedactedSystem *string
	ParentID       *uuid.UUID
	StartedAt      time.Time
	EndedAt        *time.Time
}

func StreamName(tenantID uuid.UUID) string {
	return StreamPrefix + tenantID.String()
}

func DLQName(tenantID uuid.UUID) string {
	return "ingest:queue:dlq:" + tenantID.String()
}

func DLQNameFromStream(stream string) string {
	if len(stream) <= len(StreamPrefix) {
		return "ingest:queue:dlq:unknown"
	}
	return "ingest:queue:dlq:" + stream[len(StreamPrefix):]
}

func parseEventType(v string) (contracts.EventType, error) {
	t := contracts.EventType(v)
	switch t {
	case contracts.EventPromptSent,
		contracts.EventToolCall,
		contracts.EventSubagentSpawned,
		contracts.EventTurnCompleted,
		contracts.EventSessionCompleted,
		contracts.EventUserOverride,
		contracts.EventGitCommit,
		contracts.EventGitRevert,
		contracts.EventPROpened,
		contracts.EventPRMerged,
		contracts.EventPRReverted,
		contracts.EventIncidentLinked,
		contracts.EventPeerReuse,
		contracts.EventSelfReuse,
		contracts.EventSuggestionAccepted,
		contracts.EventSuggestionRejected:
		return t, nil
	default:
		return "", fmt.Errorf("invalid event_type %q", v)
	}
}
