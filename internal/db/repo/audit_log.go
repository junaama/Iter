package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AuditLog mirrors the audit_log table. The request path only writes
// tenant-scoped rows; nil-tenant rows are reserved for iter_batch jobs.
type AuditLog struct {
	ID          int64           `db:"id"`
	TenantID    uuid.UUID       `db:"tenant_id"`
	ActorUserID *uuid.UUID      `db:"actor_user_id"`
	ActorKind   string          `db:"actor_kind"`
	EventType   string          `db:"event_type"`
	TargetKind  *string         `db:"target_kind"`
	TargetID    *string         `db:"target_id"`
	Details     json.RawMessage `db:"details"`
	OccurredAt  time.Time       `db:"occurred_at"`
}

const (
	ActorKindUser = "user"

	AuditEventStackShared   = "stack_shared"
	AuditEventStackUnshared = "stack_unshared"
)

var validActorKinds = map[string]struct{}{
	"user":      {},
	"admin":     {},
	"system":    {},
	"batch_job": {},
}

var validAuditEvents = map[string]struct{}{
	"tenant_created":               {},
	"tenant_deleted":               {},
	"user_created":                 {},
	"user_deleted":                 {},
	"user_left_team":               {},
	AuditEventStackShared:          {},
	AuditEventStackUnshared:        {},
	"leak_detected_post_ingestion": {},
	"session_cascade_deleted":      {},
	"score_model_rollback":         {},
	"permissions_revoked":          {},
	"permissions_granted":          {},
	"admin_action":                 {},
	"data_export_requested":        {},
	"data_deletion_requested":      {},
}

// InsertAuditLog appends one audit event under the active tenant tx.
func InsertAuditLog(ctx context.Context, tx pgx.Tx, entry AuditLog) (AuditLog, error) {
	if entry.TenantID == uuid.Nil {
		return AuditLog{}, errors.New("repo.audit_log.insert: tenant_id required")
	}
	if _, ok := validActorKinds[entry.ActorKind]; !ok {
		return AuditLog{}, fmt.Errorf("repo.audit_log.insert: invalid actor_kind %q", entry.ActorKind)
	}
	if _, ok := validAuditEvents[entry.EventType]; !ok {
		return AuditLog{}, fmt.Errorf("repo.audit_log.insert: invalid event_type %q", entry.EventType)
	}
	if len(entry.Details) == 0 {
		entry.Details = json.RawMessage(`{}`)
	}

	var out AuditLog
	err := tx.QueryRow(ctx, `
		INSERT INTO audit_log (
		  tenant_id, actor_user_id, actor_kind, event_type,
		  target_kind, target_id, details
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING
		  id, tenant_id, actor_user_id, actor_kind, event_type,
		  target_kind, target_id, details, occurred_at
	`,
		entry.TenantID, entry.ActorUserID, entry.ActorKind, entry.EventType,
		entry.TargetKind, entry.TargetID, entry.Details,
	).Scan(
		&out.ID, &out.TenantID, &out.ActorUserID, &out.ActorKind,
		&out.EventType, &out.TargetKind, &out.TargetID,
		&out.Details, &out.OccurredAt,
	)
	if err != nil {
		return AuditLog{}, fmt.Errorf("repo.audit_log.insert: %w", err)
	}
	return out, nil
}
