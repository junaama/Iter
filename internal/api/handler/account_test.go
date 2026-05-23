package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

type fakeAccountStore struct {
	startExport      func(*http.Request, contracts.Principal, time.Time) (repo.AccountExport, error)
	getExport        func(*http.Request, contracts.Principal, uuid.UUID) (repo.AccountExport, error)
	scheduleDeletion func(*http.Request, contracts.Principal, time.Time) (repo.AccountDeletion, error)
}

func (f fakeAccountStore) StartExport(r *http.Request, p contracts.Principal, now time.Time) (repo.AccountExport, error) {
	return f.startExport(r, p, now)
}

func (f fakeAccountStore) GetExport(r *http.Request, p contracts.Principal, id uuid.UUID) (repo.AccountExport, error) {
	return f.getExport(r, p, id)
}

func (f fakeAccountStore) ScheduleDeletion(r *http.Request, p contracts.Principal, now time.Time) (repo.AccountDeletion, error) {
	return f.scheduleDeletion(r, p, now)
}

func TestAccountExportStartReturnsStatusURL(t *testing.T) {
	now := time.Date(2026, 5, 22, 18, 0, 0, 0, time.UTC)
	exportID := uuid.New()
	archivePointer := "iter://account_exports/" + exportID.String()
	h := accountExportStartHandler(discardLogger(), fakeAccountStore{
		startExport: func(_ *http.Request, p contracts.Principal, got time.Time) (repo.AccountExport, error) {
			if p.UserID == uuid.Nil || p.TenantID == uuid.Nil {
				t.Fatalf("principal not passed through: %+v", p)
			}
			if !got.Equal(now) {
				t.Fatalf("now: got %s want %s", got, now)
			}
			readyAt := now
			return repo.AccountExport{
				ID:             exportID,
				Status:         repo.AccountExportStatusReady,
				ArchivePointer: &archivePointer,
				RequestedAt:    now,
				ReadyAt:        &readyAt,
			}, nil
		},
	}, func() time.Time { return now })

	req := accountRequest(http.MethodPost, "/v1/account/export", contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202 body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAccountJSON[contracts.AccountExportStartResponse](t, rec.Body.Bytes())
	if body.ExportID != exportID || body.Status != contracts.AccountExportReady {
		t.Fatalf("body mismatch: %+v", body)
	}
	if body.StatusURL != "/v1/account/export/"+exportID.String() {
		t.Fatalf("status_url: got %q", body.StatusURL)
	}
}

func TestAccountHandlersRequireAuth(t *testing.T) {
	h := accountExportStartHandler(discardLogger(), fakeAccountStore{}, time.Now)
	req := httptest.NewRequest(http.MethodPost, "/v1/account/export", nil)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
	assertAPIError(t, rec.Body.Bytes(), "unauthenticated")
}

func TestAccountExportStartTenantMismatchForbidden(t *testing.T) {
	h := accountExportStartHandler(discardLogger(), fakeAccountStore{
		startExport: func(*http.Request, contracts.Principal, time.Time) (repo.AccountExport, error) {
			return repo.AccountExport{}, repo.ErrAccountAccessDenied
		},
	}, time.Now)

	req := accountRequest(http.MethodPost, "/v1/account/export", contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want 403 body=%s", rec.Code, rec.Body.String())
	}
	assertAPIError(t, rec.Body.Bytes(), "forbidden")
}

func TestAccountDeleteSchedulesSevenDayCascade(t *testing.T) {
	now := time.Date(2026, 5, 22, 18, 0, 0, 0, time.UTC)
	want := now.Add(7 * 24 * time.Hour)
	h := accountDeleteHandler(discardLogger(), fakeAccountStore{
		scheduleDeletion: func(_ *http.Request, _ contracts.Principal, got time.Time) (repo.AccountDeletion, error) {
			if !got.Equal(now) {
				t.Fatalf("now: got %s want %s", got, now)
			}
			return repo.AccountDeletion{ScheduledFor: want}, nil
		},
	}, func() time.Time { return now })

	req := accountRequest(http.MethodPost, "/v1/account/delete", contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: got %d want 202 body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAccountJSON[contracts.AccountDeleteResponse](t, rec.Body.Bytes())
	if !body.ScheduledDeletionAt.Equal(want) || body.CascadeDeleteAfterDays != 7 {
		t.Fatalf("body mismatch: %+v", body)
	}
}

func TestAccountExportStatusMapsReadyPointer(t *testing.T) {
	exportID := uuid.New()
	readyAt := time.Date(2026, 5, 22, 18, 0, 0, 0, time.UTC)
	pointer := "iter://account_exports/" + exportID.String()
	h := accountExportStatusHandler(discardLogger(), fakeAccountStore{
		getExport: func(_ *http.Request, _ contracts.Principal, got uuid.UUID) (repo.AccountExport, error) {
			if got != exportID {
				t.Fatalf("export id: got %s want %s", got, exportID)
			}
			return repo.AccountExport{
				ID:             exportID,
				Status:         repo.AccountExportStatusReady,
				ArchivePointer: &pointer,
				RequestedAt:    readyAt,
				ReadyAt:        &readyAt,
			}, nil
		},
	})

	req := accountRequest(http.MethodGet, "/v1/account/export/"+exportID.String(), contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	req = withChiParam(req, "id", exportID.String())
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	body := decodeAccountJSON[contracts.AccountExportStatusResponse](t, rec.Body.Bytes())
	if body.ExportID != exportID || body.ArchivePointer == nil || *body.ArchivePointer != pointer {
		t.Fatalf("body mismatch: %+v", body)
	}
}

func TestAccountExportStatusNotFound(t *testing.T) {
	exportID := uuid.New()
	h := accountExportStatusHandler(discardLogger(), fakeAccountStore{
		getExport: func(*http.Request, contracts.Principal, uuid.UUID) (repo.AccountExport, error) {
			return repo.AccountExport{}, pgx.ErrNoRows
		},
	})

	req := accountRequest(http.MethodGet, "/v1/account/export/"+exportID.String(), contracts.Principal{UserID: uuid.New(), TenantID: uuid.New()})
	req = withChiParam(req, "id", exportID.String())
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want 404", rec.Code)
	}
}

func accountRequest(method, path string, principal contracts.Principal) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return req.WithContext(contracts.WithPrincipal(req.Context(), principal))
}

func withChiParam(req *http.Request, key, value string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func decodeAccountJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", string(body), err)
	}
	return out
}
