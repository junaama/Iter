package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	AccountExportStatusPending = "pending"
	AccountExportStatusReady   = "ready"
	AccountExportStatusFailed  = "failed"

	accountDeleteDelayDays = 7
)

// ErrAccountAccessDenied means the authenticated user is not an active member
// of the tenant carried by the token, or the token refers to a deleted user.
var ErrAccountAccessDenied = errors.New("repo.account_lifecycle: access denied")

// AccountExport mirrors account_exports.
type AccountExport struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	UserID         uuid.UUID
	Status         string
	ArchivePointer *string
	DownloadURL    *string
	Error          *string
	RequestedAt    time.Time
	ReadyAt        *time.Time
	FailedAt       *time.Time
	ExpiresAt      *time.Time
}

// AccountDeletion mirrors account_deletions.
type AccountDeletion struct {
	TenantID     uuid.UUID
	UserID       uuid.UUID
	RequestedAt  time.Time
	ScheduledFor time.Time
	CompletedAt  *time.Time
}

// StartAccountExport records a tenant-scoped export request and audits it. The
// row is marked ready with an internal archive pointer to this durable record;
// downloadable R2 bundle generation is a separate batch concern.
func StartAccountExport(ctx context.Context, tx pgx.Tx, principal contracts.Principal, now time.Time) (AccountExport, error) {
	if err := requireTenantMembership(ctx, tx, principal, true); err != nil {
		return AccountExport{}, err
	}

	id := uuid.New()
	archivePointer := fmt.Sprintf("iter://account_exports/%s", id)
	expiresAt := now.Add(7 * 24 * time.Hour)

	var out AccountExport
	err := tx.QueryRow(ctx, `
		INSERT INTO account_exports (
		  id, tenant_id, user_id, status, archive_pointer,
		  requested_at, ready_at, expires_at
		) VALUES ($1, $2, $3, 'ready', $4, $5, $5, $6)
		RETURNING
		  id, tenant_id, user_id, status, archive_pointer, download_url,
		  error, requested_at, ready_at, failed_at, expires_at
	`, id, principal.TenantID, principal.UserID, archivePointer, now, expiresAt).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.Status, &out.ArchivePointer,
		&out.DownloadURL, &out.Error, &out.RequestedAt, &out.ReadyAt,
		&out.FailedAt, &out.ExpiresAt,
	)
	if err != nil {
		return AccountExport{}, fmt.Errorf("repo.account_exports.insert: %w", err)
	}

	if err := auditAccountEvent(ctx, tx, principal, "data_export_requested", "account_export", out.ID.String(), map[string]any{
		"export_id": out.ID.String(),
		"status":    out.Status,
	}); err != nil {
		return AccountExport{}, err
	}

	return out, nil
}

// GetAccountExport returns one export owned by the authenticated user. RLS
// scopes tenant visibility; the user predicate prevents same-tenant users from
// reading each other's export state.
func GetAccountExport(ctx context.Context, tx pgx.Tx, principal contracts.Principal, exportID uuid.UUID) (AccountExport, error) {
	var out AccountExport
	err := tx.QueryRow(ctx, `
		SELECT
		  id, tenant_id, user_id, status, archive_pointer, download_url,
		  error, requested_at, ready_at, failed_at, expires_at
		  FROM account_exports
		 WHERE id = $1
		   AND user_id = $2
	`, exportID, principal.UserID).Scan(
		&out.ID, &out.TenantID, &out.UserID, &out.Status, &out.ArchivePointer,
		&out.DownloadURL, &out.Error, &out.RequestedAt, &out.ReadyAt,
		&out.FailedAt, &out.ExpiresAt,
	)
	if err != nil {
		return AccountExport{}, fmt.Errorf("repo.account_exports.get: %w", err)
	}
	return out, nil
}

// ScheduleAccountDeletion soft-disables the signed-in user and records the
// seven-day cascade-delete schedule for the current tenant.
func ScheduleAccountDeletion(ctx context.Context, tx pgx.Tx, principal contracts.Principal, now time.Time) (AccountDeletion, error) {
	if err := requireTenantMembership(ctx, tx, principal, false); err != nil {
		return AccountDeletion{}, err
	}

	scheduledFor := now.Add(accountDeleteDelayDays * 24 * time.Hour)
	var deletedAt time.Time
	if err := tx.QueryRow(ctx, `
		UPDATE users
		   SET deleted_at = COALESCE(deleted_at, $2)
		 WHERE id = $1
		RETURNING deleted_at
	`, principal.UserID, now).Scan(&deletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AccountDeletion{}, ErrAccountAccessDenied
		}
		return AccountDeletion{}, fmt.Errorf("repo.account_deletions.soft_disable_user: %w", err)
	}

	var out AccountDeletion
	err := tx.QueryRow(ctx, `
		INSERT INTO account_deletions (
		  tenant_id, user_id, requested_at, scheduled_for
		) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, user_id) DO UPDATE
		   SET scheduled_for = account_deletions.scheduled_for
		RETURNING tenant_id, user_id, requested_at, scheduled_for, completed_at
	`, principal.TenantID, principal.UserID, now, scheduledFor).Scan(
		&out.TenantID, &out.UserID, &out.RequestedAt, &out.ScheduledFor,
		&out.CompletedAt,
	)
	if err != nil {
		return AccountDeletion{}, fmt.Errorf("repo.account_deletions.upsert: %w", err)
	}

	if err := auditAccountEvent(ctx, tx, principal, "data_deletion_requested", "user", principal.UserID.String(), map[string]any{
		"scheduled_for": out.ScheduledFor.UTC().Format(time.RFC3339),
	}); err != nil {
		return AccountDeletion{}, err
	}

	return out, nil
}

func requireTenantMembership(ctx context.Context, tx pgx.Tx, principal contracts.Principal, requireActiveUser bool) error {
	if principal.UserID == uuid.Nil || principal.TenantID == uuid.Nil {
		return ErrAccountAccessDenied
	}

	query := `
		SELECT 1
		  FROM tenant_users tu
		  JOIN users u ON u.id = tu.user_id
		 WHERE tu.tenant_id = $1
		   AND tu.user_id = $2
	`
	if requireActiveUser {
		query += ` AND u.deleted_at IS NULL`
	}

	var one int
	err := tx.QueryRow(ctx, query, principal.TenantID, principal.UserID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAccountAccessDenied
		}
		return fmt.Errorf("repo.account_lifecycle.require_membership: %w", err)
	}
	return nil
}

func auditAccountEvent(ctx context.Context, tx pgx.Tx, principal contracts.Principal, eventType, targetKind, targetID string, details map[string]any) error {
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("repo.account_lifecycle.audit_details: %w", err)
	}
	actor := principal.UserID
	_, err = InsertAuditLog(ctx, tx, AuditLog{
		TenantID:    principal.TenantID,
		ActorUserID: &actor,
		ActorKind:   ActorKindUser,
		EventType:   eventType,
		TargetKind:  &targetKind,
		TargetID:    &targetID,
		Details:     detailsJSON,
	})
	if err != nil {
		return fmt.Errorf("repo.account_lifecycle.audit: %w", err)
	}
	return nil
}
