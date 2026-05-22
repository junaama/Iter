//go:build integration

package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestSuggestHandlerIntegration_HappyPathPersistsSuggestion(t *testing.T) {
	ctx := context.Background()
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "Acme"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "adam@example.com", "Adam"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), "member")
	sessionID := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), time.Now().UTC()))
	vec := testVector()

	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		if _, err := repo.UpsertEmbedding(ctx, tx, sessionID, tenantID, vec, "test-embedding"); err != nil {
			return err
		}
		_, err := repo.InsertScore(ctx, tx, repo.Score{
			SessionID:         sessionID,
			TenantID:          tenantID,
			ScorerVersion:     "integration",
			CompositeScore:    0.9,
			Signals:           json.RawMessage(`{}`),
			ContributorWeight: 0.5,
		})
		return err
	}); err != nil {
		t.Fatalf("seed tenant-scoped rows: %v", err)
	}

	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{vec: vec}, &fakeLLM{
		text: goodLLMJSON("Use the proven test-first shape and verify with go test.", 0.9),
	}, liveSuggestStore{pool: tdb.AppPool})

	rec := httptest.NewRecorder()
	err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(txCtx context.Context, _ pgx.Tx) error {
		req := httptest.NewRequest(http.MethodPost, "/v1/suggest",
			strings.NewReader(validSuggestBody(tenantID, userID, "help me finish this issue")))
		req = req.WithContext(contracts.WithPrincipal(txCtx, contracts.Principal{
			TenantID: tenantID,
			UserID:   userID,
			TokenID:  "jti-integration",
		}))
		handler.ServeHTTP(rec, req)
		return nil
	})
	if err != nil {
		t.Fatalf("handler tx: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeSuggestResponse(t, rec)
	if resp.Action != contracts.ActionReplace {
		t.Fatalf("action: got %q", resp.Action)
	}
	if resp.RefinedPrompt == nil || *resp.RefinedPrompt == "" {
		t.Fatalf("missing refined prompt: %#v", resp)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := tdb.Super.QueryRowContext(ctx,
			"SELECT count(*) FROM suggestions WHERE tenant_id = $1 AND source_prompt = $2",
			tenantID.String(), "help me finish this issue",
		).Scan(&count); err != nil {
			t.Fatalf("count suggestions: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("suggestion was not persisted")
}
