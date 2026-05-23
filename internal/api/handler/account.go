package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const accountDeleteDelayDays = 7

type accountStore interface {
	StartExport(r *http.Request, principal contracts.Principal, now time.Time) (repo.AccountExport, error)
	GetExport(r *http.Request, principal contracts.Principal, exportID uuid.UUID) (repo.AccountExport, error)
	ScheduleDeletion(r *http.Request, principal contracts.Principal, now time.Time) (repo.AccountDeletion, error)
}

type pgAccountStore struct{}

func (pgAccountStore) StartExport(r *http.Request, principal contracts.Principal, now time.Time) (repo.AccountExport, error) {
	tx, err := db.RequireTx(r.Context())
	if err != nil {
		return repo.AccountExport{}, err
	}
	return repo.StartAccountExport(r.Context(), tx, principal, now)
}

func (pgAccountStore) GetExport(r *http.Request, principal contracts.Principal, exportID uuid.UUID) (repo.AccountExport, error) {
	tx, err := db.RequireTx(r.Context())
	if err != nil {
		return repo.AccountExport{}, err
	}
	return repo.GetAccountExport(r.Context(), tx, principal, exportID)
}

func (pgAccountStore) ScheduleDeletion(r *http.Request, principal contracts.Principal, now time.Time) (repo.AccountDeletion, error) {
	tx, err := db.RequireTx(r.Context())
	if err != nil {
		return repo.AccountDeletion{}, err
	}
	return repo.ScheduleAccountDeletion(r.Context(), tx, principal, now)
}

// AccountExportStartHandler starts a tenant-scoped export for the signed-in user.
func AccountExportStartHandler(deps app.Deps) http.HandlerFunc {
	return accountExportStartHandler(deps.Logger, pgAccountStore{}, time.Now)
}

// AccountExportStatusHandler returns polling state for a previously requested export.
func AccountExportStatusHandler(deps app.Deps) http.HandlerFunc {
	return accountExportStatusHandler(deps.Logger, pgAccountStore{})
}

// AccountDeleteHandler schedules the signed-in user's seven-day deletion path.
func AccountDeleteHandler(deps app.Deps) http.HandlerFunc {
	return accountDeleteHandler(deps.Logger, pgAccountStore{}, time.Now)
}

func accountExportStartHandler(logger *slog.Logger, store accountStore, now func() time.Time) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}

		export, err := store.StartExport(r, principal, now().UTC())
		if err != nil {
			respondAccountError(w, r, logger, err, "account_export_start_failed")
			return
		}

		respond.JSON(w, http.StatusAccepted, contracts.AccountExportStartResponse{
			ExportID:    export.ID,
			Status:      contracts.AccountExportStatus(export.Status),
			StatusURL:   "/v1/account/export/" + export.ID.String(),
			RequestedAt: export.RequestedAt,
		})
	}
}

func accountExportStatusHandler(logger *slog.Logger, store accountStore) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}

		exportID, err := uuid.Parse(chi.URLParam(r, "id"))
		if err != nil {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_export_id"})
			return
		}

		export, err := store.GetExport(r, principal, exportID)
		if err != nil {
			respondAccountError(w, r, logger, err, "account_export_status_failed")
			return
		}

		respond.JSON(w, http.StatusOK, accountExportStatusResponse(export))
	}
}

func accountDeleteHandler(logger *slog.Logger, store accountStore, now func() time.Time) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}

		deletion, err := store.ScheduleDeletion(r, principal, now().UTC())
		if err != nil {
			respondAccountError(w, r, logger, err, "account_delete_failed")
			return
		}

		respond.JSON(w, http.StatusAccepted, contracts.AccountDeleteResponse{
			ScheduledDeletionAt:    deletion.ScheduledFor,
			CascadeDeleteAfterDays: accountDeleteDelayDays,
		})
	}
}

func accountExportStatusResponse(export repo.AccountExport) contracts.AccountExportStatusResponse {
	return contracts.AccountExportStatusResponse{
		ExportID:       export.ID,
		Status:         contracts.AccountExportStatus(export.Status),
		DownloadURL:    export.DownloadURL,
		ArchivePointer: export.ArchivePointer,
		RequestedAt:    export.RequestedAt,
		ReadyAt:        export.ReadyAt,
		FailedAt:       export.FailedAt,
		Error:          export.Error,
	}
}

func respondAccountError(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error, msg string) {
	switch {
	case errors.Is(err, repo.ErrAccountAccessDenied):
		respond.JSON(w, http.StatusForbidden, respond.Error{Error: "forbidden"})
	case errors.Is(err, pgx.ErrNoRows):
		respond.JSON(w, http.StatusNotFound, respond.Error{Error: "not_found"})
	default:
		logger.ErrorContext(r.Context(), msg, "err", err)
		respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
	}
}
