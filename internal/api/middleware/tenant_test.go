//go:build integration

// Integration tests for the tenant-context middleware (issue 034).
//
// Gated behind the `integration` build tag because every assertion
// needs a real Postgres connection — SET LOCAL app.current_tenant is
// only meaningful inside a real transaction on a server that
// understands RLS. miniredis can't substitute here (rate-limit /
// idempotency mock against in-process Redis; we have no in-process
// Postgres). Run with `make test-rls`.
//
// Coverage matrix (issue 034 acceptance):
//
//   - Happy path: handler observes SET LOCAL app.current_tenant set to
//     the Principal's TenantID via SELECT current_setting(...).
//   - Whitelist /health bypass: handler runs without a tx on the ctx
//     (db.FromContext returns nil).
//   - Missing Principal: 500 internal — wiring bug, not a credential
//     issue.
//   - 4xx response: tx rolled back — row INSERTed inside the handler
//     is NOT visible afterwards via the superuser conn.
//   - 2xx response: tx committed — row IS visible afterwards.
//   - 5xx response: tx rolled back — same shape as 4xx.
//   - nil pool: pass-through with a warn log.

package middleware_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/api/middleware"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/pkg/contracts"
)

// seedPrincipal mints a tenant and returns (Principal, tenantID-as-string).
// Runs as superuser through the dbtest harness.
func seedPrincipal(ctx context.Context, t *testing.T, tdb *dbtest.TestDB, name string) (contracts.Principal, string) {
	t.Helper()
	tenantID := tdb.SeedTenant(ctx, t, name)
	userID := tdb.SeedUser(ctx, t, name+"@example.com", "User-"+name)
	tdb.SeedMembership(ctx, t, tenantID, userID, "owner")
	p := contracts.Principal{
		UserID:   uuid.MustParse(userID),
		TenantID: uuid.MustParse(tenantID),
		TokenID:  "test-jti",
	}
	return p, tenantID
}

// attachPrincipal attaches p to r's context.
func attachPrincipal(r *http.Request, p contracts.Principal) *http.Request {
	return r.WithContext(contracts.WithPrincipal(r.Context(), p))
}

// captureLogger returns an slog.Logger that writes JSON to a shared
// buffer so tests can assert on emitted events.
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func TestTenant_HappyPath_SETLOCALVisibleInHandler(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	principal, tenantID := seedPrincipal(ctx, t, tdb, "acme-happy")

	var observedTenant string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Pull the active tx out of the ctx — the same way every
		// repo function does — and verify SET LOCAL took effect.
		tx := db.FromContext(r.Context())
		if tx == nil {
			t.Errorf("handler: expected pgx.Tx on ctx, got nil")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if err := tx.QueryRow(r.Context(),
			"SELECT current_setting('app.current_tenant')",
		).Scan(&observedTenant); err != nil {
			t.Errorf("handler: SELECT current_setting: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	mw := middleware.Tenant(tdb.AppPool)(h)

	req := attachPrincipal(httptest.NewRequest(http.MethodPost, "/v1/anything", nil), principal)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if observedTenant != tenantID {
		t.Fatalf("current_setting: got %q want %q", observedTenant, tenantID)
	}
}

func TestTenant_MissingPrincipal_500(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	logger, buf := captureLogger()

	handlerCalled := false
	h := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		handlerCalled = true
	})

	mw := middleware.Tenant(tdb.AppPool, middleware.WithTenantLogger(logger))(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/anything", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	if handlerCalled {
		t.Fatalf("handler must not run when Principal is missing")
	}
	if !strings.Contains(buf.String(), "tenant_middleware_missing_principal") {
		t.Fatalf("expected missing-principal log; got: %s", buf.String())
	}
}

func TestTenant_HealthBypass_NoTxOpened(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	var sawTx bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTx = db.FromContext(r.Context()) != nil
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.Tenant(tdb.AppPool)(h)

	// /health is in the default skip list. No Principal needed — the
	// middleware short-circuits before checking.
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if sawTx {
		t.Fatalf("handler saw a tx on ctx for /health — middleware should have bypassed")
	}
}

func TestTenant_WebhookBypass_NoTxOpened(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	var sawTx bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTx = db.FromContext(r.Context()) != nil
		w.WriteHeader(http.StatusOK)
	})
	mw := middleware.Tenant(tdb.AppPool)(h)

	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if sawTx {
		t.Fatalf("handler saw a tx on ctx for /v1/webhooks — middleware should have bypassed")
	}
}

func TestTenant_2xx_TxCommits(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	principal, _ := seedPrincipal(ctx, t, tdb, "acme-commit")

	// The handler inserts a session row. After the middleware returns
	// with a 2xx response we read the row back via the SUPERUSER conn
	// (bypassing RLS) to confirm the insert was committed, not rolled
	// back.
	sessionID := uuid.New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := insertSessionInTx(r.Context(), principal.TenantID, principal.UserID, sessionID); err != nil {
			t.Errorf("handler insert: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.Tenant(tdb.AppPool)(h)
	req := attachPrincipal(httptest.NewRequest(http.MethodPost, "/v1/anything", nil), principal)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if !sessionPersists(ctx, t, tdb.Super, sessionID) {
		t.Fatalf("session %s NOT visible after 2xx — tx was rolled back instead of committed", sessionID)
	}
}

func TestTenant_4xx_TxRollsBack(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	principal, _ := seedPrincipal(ctx, t, tdb, "acme-4xx")

	sessionID := uuid.New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := insertSessionInTx(r.Context(), principal.TenantID, principal.UserID, sessionID); err != nil {
			t.Errorf("handler insert: %v", err)
			return
		}
		// Handler returns 400 to force the rollback path. The
		// session row above MUST NOT survive.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"client"}`))
	})

	mw := middleware.Tenant(tdb.AppPool)(h)
	req := attachPrincipal(httptest.NewRequest(http.MethodPost, "/v1/anything", nil), principal)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", rec.Code)
	}
	if sessionPersists(ctx, t, tdb.Super, sessionID) {
		t.Fatalf("session %s VISIBLE after 4xx — tx was committed instead of rolled back", sessionID)
	}
}

func TestTenant_5xx_TxRollsBack(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	principal, _ := seedPrincipal(ctx, t, tdb, "acme-5xx")

	sessionID := uuid.New()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := insertSessionInTx(r.Context(), principal.TenantID, principal.UserID, sessionID); err != nil {
			t.Errorf("handler insert: %v", err)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})

	mw := middleware.Tenant(tdb.AppPool)(h)
	req := attachPrincipal(httptest.NewRequest(http.MethodPost, "/v1/anything", nil), principal)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", rec.Code)
	}
	if sessionPersists(ctx, t, tdb.Super, sessionID) {
		t.Fatalf("session %s VISIBLE after 5xx — tx was committed instead of rolled back", sessionID)
	}
}

func TestTenant_NilPool_Passthrough(t *testing.T) {
	logger, buf := captureLogger()

	called := false
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// No tx on ctx — confirm that handlers downstream of a
		// nil-pool pass-through must not assume one. Reading
		// FromContext is fine; calling db.Querier would panic.
		if db.FromContext(r.Context()) != nil {
			t.Errorf("nil pool: expected no tx on ctx")
		}
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.Tenant(nil, middleware.WithTenantLogger(logger))(h)

	// Even without a Principal we expect pass-through — the nil-pool
	// branch fires first (the alternative would be a chicken-and-egg
	// failure during early bring-up where DATABASE_URL is unset and
	// nothing in the chain has a working backend).
	req := httptest.NewRequest(http.MethodPost, "/v1/anything", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if !called {
		t.Fatalf("handler must run when pool is nil (pass-through)")
	}
	if !strings.Contains(buf.String(), "tenant_middleware_nil_pool_passthrough") {
		t.Fatalf("expected nil-pool warn log; got: %s", buf.String())
	}
}

func TestTenant_WithTenantSkip_AppendsAndSkips(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	var sawTx bool
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawTx = db.FromContext(r.Context()) != nil
		w.WriteHeader(http.StatusOK)
	})
	mw := middleware.Tenant(tdb.AppPool, middleware.WithTenantSkip("/v1/public"))(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/public/info", nil)
	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if sawTx {
		t.Fatalf("custom skip prefix did not bypass: handler saw a tx on ctx")
	}
}

// ----- helpers -----

// insertSessionInTx writes a minimal session row using the active tx
// from the ctx. Mirrors what the future SessionsRepo.Insert will do,
// but kept local so this test file doesn't reach into the repo
// package's internal contract.
func insertSessionInTx(ctx context.Context, tenantID, userID, sessionID uuid.UUID) error {
	tx, ok := db.FromContext(ctx).(pgx.Tx)
	if !ok || tx == nil {
		return fmt.Errorf("no tx on ctx")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO sessions (id, tenant_id, user_id, harness, model, tools,
		                     started_at, redacted_prompt, classification)
		VALUES ($1, $2, $3, 'claude_code', 'm', ARRAY[]::text[], now(), 'p', 'clean')
	`, sessionID, tenantID, userID)
	return err
}

// sessionPersists checks via the superuser handle whether the session
// row exists. Bypasses RLS so a "row was inserted but RLS hides it"
// false positive is impossible.
func sessionPersists(ctx context.Context, t *testing.T, super *sql.DB, sessionID uuid.UUID) bool {
	t.Helper()
	var n int
	if err := super.QueryRowContext(ctx,
		"SELECT count(*) FROM sessions WHERE id = $1", sessionID,
	).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n > 0
}
