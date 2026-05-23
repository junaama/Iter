package contracts

import (
	"time"

	"github.com/google/uuid"
)

// AccountExportStatus is the polling state for a tenant-scoped user export.
type AccountExportStatus string

const (
	AccountExportPending AccountExportStatus = "pending"
	AccountExportReady   AccountExportStatus = "ready"
	AccountExportFailed  AccountExportStatus = "failed"
)

// AccountExportStartResponse is returned by POST /v1/account/export.
type AccountExportStartResponse struct {
	ExportID    uuid.UUID           `json:"export_id"`
	Status      AccountExportStatus `json:"status"`
	StatusURL   string              `json:"status_url"`
	RequestedAt time.Time           `json:"requested_at"`
}

// AccountExportStatusResponse is returned by GET /v1/account/export/{id}.
type AccountExportStatusResponse struct {
	ExportID       uuid.UUID           `json:"export_id"`
	Status         AccountExportStatus `json:"status"`
	DownloadURL    *string             `json:"download_url,omitempty"`
	ArchivePointer *string             `json:"archive_pointer,omitempty"`
	RequestedAt    time.Time           `json:"requested_at"`
	ReadyAt        *time.Time          `json:"ready_at,omitempty"`
	FailedAt       *time.Time          `json:"failed_at,omitempty"`
	Error          *string             `json:"error,omitempty"`
}

// AccountDeleteResponse is returned by POST /v1/account/delete.
type AccountDeleteResponse struct {
	ScheduledDeletionAt    time.Time `json:"scheduled_deletion_at"`
	CascadeDeleteAfterDays int       `json:"cascade_delete_after_days"`
}
