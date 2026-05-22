package contracts

import (
	"time"
)

// EventType mirrors contracts.py EventType (the closed enum of session-event
// kinds the daemon can emit). The string values match the wire tokens written
// to session_events.event_type in Postgres (see migrations/0001_initial.sql).
type EventType string

const (
	EventPromptSent         EventType = "prompt_sent"
	EventToolCall           EventType = "tool_call"
	EventSubagentSpawned    EventType = "subagent_spawned"
	EventTurnCompleted      EventType = "turn_completed"
	EventSessionCompleted   EventType = "session_completed"
	EventUserOverride       EventType = "user_override"
	EventGitCommit          EventType = "git_commit"
	EventGitRevert          EventType = "git_revert"
	EventPROpened           EventType = "pr_opened"
	EventPRMerged           EventType = "pr_merged"
	EventPRReverted         EventType = "pr_reverted"
	EventIncidentLinked     EventType = "incident_linked"
	EventPeerReuse          EventType = "peer_reuse"
	EventSelfReuse          EventType = "self_reuse"
	EventSuggestionAccepted EventType = "suggestion_accepted"
	EventSuggestionRejected EventType = "suggestion_rejected"
)

// SessionEvent is the in-process Go shape consumed by signal aggregation.
// It is the minimal projection of the wire-level TraceEvent (contracts.py)
// plus the persistence fields stored in the session_events table
// (migrations/0001_initial.sql).
//
// Field choices, recorded in DECISIONS.md alongside this file:
//
//   - ID is a string so the aggregator can dedup across replays. The wire
//     envelope carries a `msg_id` UUID; the daemon's SQLite WAL carries a
//     local row id. Either can be stuffed in here. Aggregate uses ID purely
//     as an equality key — it does not parse it.
//   - SessionID is the parent session this event belongs to. Subagent events
//     carry their own SessionID and reference the parent via ParentSessionID;
//     Aggregate uses SessionID to group, so callers MUST pre-filter to a
//     single session before invoking Aggregate.
//   - ParentSessionID, when non-nil, marks the event as belonging to a
//     subagent run. Subagent aggregation is independent of the parent's
//     signals (ARCHITECTURE.md §9 Step 5 "subagent independent scoring").
//   - Type is the closed EventType enum.
//   - OccurredAt is the daemon-stamped event time. Aggregate does not read
//     the wall clock; any time-derived signal must come from OccurredAt.
//   - Payload is opaque to the wire (shape varies by harness) but the
//     aggregator may peek at well-known keys per EventType.
type SessionEvent struct {
	ID              string         `json:"id"`
	SessionID       string         `json:"session_id"`
	ParentSessionID *string        `json:"parent_session_id,omitempty"`
	Type            EventType      `json:"event_type"`
	OccurredAt      time.Time      `json:"occurred_at"`
	Payload         map[string]any `json:"payload,omitempty"`
}

// IsSubagent reports whether the event belongs to a subagent run. Subagent
// events aggregate into their own ScoreSignals independent of the parent
// session.
func (e SessionEvent) IsSubagent() bool {
	return e.ParentSessionID != nil && *e.ParentSessionID != ""
}
