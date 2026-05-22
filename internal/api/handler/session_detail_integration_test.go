//go:build integration

package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/api/handler"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestSessionDetailAndScores_OwnSession200(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, userID := seedAPITenancy(ctx, t, tdb, "detail-own")
	sessionID := seedAPISession(ctx, t, tdb, tenantID, userID, nil, time.Now().UTC(), "root prompt")
	base := time.Now().UTC().Truncate(time.Microsecond)
	seedAPIEvent(ctx, t, tdb, tenantID, sessionID, contracts.EventTurnCompleted, base.Add(20*time.Millisecond), `{"turn":2}`)
	seedAPIEvent(ctx, t, tdb, tenantID, sessionID, contracts.EventPromptSent, base.Add(10*time.Millisecond), `{"prompt":"redacted"}`)
	seedAPIOutcome(ctx, t, tdb, tenantID, sessionID, repo.OutcomePRMerged, "https://github.com/acme/repo/pull/1", `{"number":1}`)
	seedAPIScore(ctx, t, tdb, tenantID, sessionID, "v0.3", 0.61, `{"durability_7d":0.7}`, "first pass", 0.4, base.Add(-time.Hour))
	seedAPIScore(ctx, t, tdb, tenantID, sessionID, "v0.4", 0.82, `{"durability_7d":0.9,"new_signal":2}`, "rescored", 0.6, base)

	deps := app.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	detailRec := serveSessionHandler(t, tdb, tenantID, userID, handler.SessionDetailHandler(deps), "id", sessionID.String(), "/v1/sessions/"+sessionID.String())
	if detailRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/sessions/:id: want 200 got %d body=%s", detailRec.Code, detailRec.Body.String())
	}
	var detail contracts.SessionDetailResponse
	if err := json.NewDecoder(detailRec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Session.ID != sessionID {
		t.Fatalf("session id: want %s got %s", sessionID, detail.Session.ID)
	}
	if len(detail.Events.Items) != 2 {
		t.Fatalf("events len: want 2 got %d", len(detail.Events.Items))
	}
	if detail.Events.Items[0].Type != contracts.EventPromptSent || detail.Events.Items[1].Type != contracts.EventTurnCompleted {
		t.Fatalf("events not ordered by occurred_at ASC: %+v", detail.Events.Items)
	}
	if detail.Events.NextCursor != nil {
		t.Fatalf("small event list should not have next cursor: %q", *detail.Events.NextCursor)
	}
	if len(detail.Outcomes) != 1 || detail.Outcomes[0].OutcomeType != contracts.OutcomeType(repo.OutcomePRMerged) {
		t.Fatalf("outcomes mismatch: %+v", detail.Outcomes)
	}
	if detail.Subagents.Truncated || len(detail.Subagents.Items) != 0 {
		t.Fatalf("unexpected subagent tree: %+v", detail.Subagents)
	}

	scoresRec := serveSessionHandler(t, tdb, tenantID, userID, handler.SessionScoresHandler(deps), "session_id", sessionID.String(), "/v1/scores/"+sessionID.String())
	if scoresRec.Code != http.StatusOK {
		t.Fatalf("GET /v1/scores/:session_id: want 200 got %d body=%s", scoresRec.Code, scoresRec.Body.String())
	}
	var scores contracts.SessionScoresResponse
	if err := json.NewDecoder(scoresRec.Body).Decode(&scores); err != nil {
		t.Fatalf("decode scores: %v", err)
	}
	if len(scores.Scores) != 2 {
		t.Fatalf("scores len: want 2 got %d", len(scores.Scores))
	}
	if scores.Scores[0].ScorerVersion != "v0.4" || scores.Scores[1].ScorerVersion != "v0.3" {
		t.Fatalf("scores not ordered by scored_at DESC: %+v", scores.Scores)
	}
	if scores.Scores[0].Signals.Durability7d == nil || *scores.Scores[0].Signals.Durability7d != 0.9 {
		t.Fatalf("score signals not decoded: %+v", scores.Scores[0].Signals)
	}
}

func TestSessionDetailAndScores_OtherTenantReturns404(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantA, userA := seedAPITenancy(ctx, t, tdb, "detail-a")
	tenantB, userB := seedAPITenancy(ctx, t, tdb, "detail-b")
	sessionA := seedAPISession(ctx, t, tdb, tenantA, userA, nil, time.Now().UTC(), "tenant A prompt")

	deps := app.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	detailRec := serveSessionHandler(t, tdb, tenantB, userB, handler.SessionDetailHandler(deps), "id", sessionA.String(), "/v1/sessions/"+sessionA.String())
	if detailRec.Code != http.StatusNotFound {
		t.Fatalf("detail cross-tenant: want 404 got %d body=%s", detailRec.Code, detailRec.Body.String())
	}

	scoresRec := serveSessionHandler(t, tdb, tenantB, userB, handler.SessionScoresHandler(deps), "session_id", sessionA.String(), "/v1/scores/"+sessionA.String())
	if scoresRec.Code != http.StatusNotFound {
		t.Fatalf("scores cross-tenant: want 404 got %d body=%s", scoresRec.Code, scoresRec.Body.String())
	}
}

func TestSessionDetail_SubagentTreeDepthLimit(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, userID := seedAPITenancy(ctx, t, tdb, "detail-tree")
	rootID := seedAPISession(ctx, t, tdb, tenantID, userID, nil, time.Now().UTC(), "root")
	parentID := rootID
	chain := make([]uuid.UUID, 0, 6)
	for depth := 1; depth <= 6; depth++ {
		id := seedAPISession(ctx, t, tdb, tenantID, userID, &parentID, time.Now().UTC().Add(time.Duration(depth)*time.Millisecond), "child")
		chain = append(chain, id)
		parentID = id
	}

	deps := app.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	rec := serveSessionHandler(t, tdb, tenantID, userID, handler.SessionDetailHandler(deps), "id", rootID.String(), "/v1/sessions/"+rootID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("GET detail: want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var detail contracts.SessionDetailResponse
	if err := json.NewDecoder(rec.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if !detail.Subagents.Truncated {
		t.Fatalf("subagent tree should be truncated at depth 5: %+v", detail.Subagents)
	}
	node := firstSubagentNode(t, detail.Subagents.Items)
	for depth := 1; depth <= 5; depth++ {
		if node.Session.ID != chain[depth-1] || node.Depth != depth {
			t.Fatalf("depth %d node mismatch: got id=%s depth=%d want id=%s", depth, node.Session.ID, node.Depth, chain[depth-1])
		}
		if depth < 5 {
			node = firstSubagentNode(t, node.Children)
		}
	}
	if len(node.Children) != 0 {
		t.Fatalf("depth-6 child should be omitted: %+v", node.Children)
	}
}

func TestSessionDetail_EventsCapAndCursor(t *testing.T) {
	tdb := dbtest.Setup(t, "../../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	tenantID, userID := seedAPITenancy(ctx, t, tdb, "detail-events")
	sessionID := seedAPISession(ctx, t, tdb, tenantID, userID, nil, time.Now().UTC(), "lots of events")
	base := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := tdb.Super.ExecContext(ctx, `
		INSERT INTO session_events (session_id, tenant_id, event_type, payload, occurred_at)
		SELECT $1, $2, 'turn_completed', jsonb_build_object('n', g), $3::timestamptz + (g * interval '1 microsecond')
		  FROM generate_series(1, 10001) AS g
	`, sessionID.String(), tenantID.String(), base); err != nil {
		t.Fatalf("seed many events: %v", err)
	}

	deps := app.Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	firstRec := serveSessionHandler(t, tdb, tenantID, userID, handler.SessionDetailHandler(deps), "id", sessionID.String(), "/v1/sessions/"+sessionID.String())
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first page: want 200 got %d body=%s", firstRec.Code, firstRec.Body.String())
	}
	var first contracts.SessionDetailResponse
	if err := json.NewDecoder(firstRec.Body).Decode(&first); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(first.Events.Items) != 10000 {
		t.Fatalf("first page events len: want 10000 got %d", len(first.Events.Items))
	}
	if first.Events.NextCursor == nil {
		t.Fatal("first page should include next cursor")
	}

	nextTarget := "/v1/sessions/" + sessionID.String() + "?events_cursor=" + url.QueryEscape(*first.Events.NextCursor)
	secondRec := serveSessionHandler(t, tdb, tenantID, userID, handler.SessionDetailHandler(deps), "id", sessionID.String(), nextTarget)
	if secondRec.Code != http.StatusOK {
		t.Fatalf("second page: want 200 got %d body=%s", secondRec.Code, secondRec.Body.String())
	}
	var second contracts.SessionDetailResponse
	if err := json.NewDecoder(secondRec.Body).Decode(&second); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(second.Events.Items) != 1 {
		t.Fatalf("second page events len: want 1 got %d", len(second.Events.Items))
	}
	if second.Events.NextCursor != nil {
		t.Fatalf("second page should not include next cursor: %q", *second.Events.NextCursor)
	}
}

func firstSubagentNode(t *testing.T, nodes []contracts.SubagentSessionNode) contracts.SubagentSessionNode {
	t.Helper()
	if len(nodes) != 1 {
		t.Fatalf("want exactly one node, got %d: %+v", len(nodes), nodes)
	}
	return nodes[0]
}

func serveSessionHandler(
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	userID uuid.UUID,
	h http.Handler,
	paramName string,
	paramValue string,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()
	var rec *httptest.ResponseRecorder
	if err := db.WithTenant(context.Background(), tdb.AppPool, tenantID.String(), func(ctx context.Context, _ pgx.Tx) error {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req = req.WithContext(contracts.WithPrincipal(ctx, contracts.Principal{
			UserID:    userID,
			TenantID:  tenantID,
			TokenID:   "test-token",
			TokenType: "cli",
		}))
		req = withURLParam(req, paramName, paramValue)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return nil
	}); err != nil {
		t.Fatalf("serve with tenant: %v", err)
	}
	return rec
}

func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func seedAPITenancy(ctx context.Context, t *testing.T, tdb *dbtest.TestDB, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, name))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, name+"@example.com", "User "+name))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleOwner)
	return tenantID, userID
}

func seedAPISession(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	userID uuid.UUID,
	parentID *uuid.UUID,
	startedAt time.Time,
	redactedPrompt string,
) uuid.UUID {
	t.Helper()
	var parent sql.NullString
	if parentID != nil {
		parent = sql.NullString{String: parentID.String(), Valid: true}
	}
	var id string
	if err := tdb.Super.QueryRowContext(ctx, `
		INSERT INTO sessions (
		  tenant_id, user_id, parent_session_id, harness, model, effort,
		  tools, repo_hash, git_branch, started_at, ended_at, wall_time_ms,
		  turn_count, total_tokens_in, total_tokens_out, redacted_prompt,
		  redacted_system, classification
		) VALUES (
		  $1, $2, $3, 'claude_code', 'claude-sonnet-4', 'med',
		  ARRAY['shell','git']::text[], 'repo-hash', 'main', $4, $5, 1234,
		  5, 100, 200, $6, 'redacted system', 'clean'
		)
		RETURNING id
	`, tenantID.String(), userID.String(), parent, startedAt, startedAt.Add(2*time.Minute), redactedPrompt).Scan(&id); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return uuid.MustParse(id)
}

func seedAPIEvent(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	eventType contracts.EventType,
	occurredAt time.Time,
	payload string,
) {
	t.Helper()
	if _, err := tdb.Super.ExecContext(ctx, `
		INSERT INTO session_events (session_id, tenant_id, event_type, payload, occurred_at)
		VALUES ($1, $2, $3, $4::jsonb, $5)
	`, sessionID.String(), tenantID.String(), string(eventType), payload, occurredAt); err != nil {
		t.Fatalf("seed event: %v", err)
	}
}

func seedAPIOutcome(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	outcomeType string,
	externalRef string,
	details string,
) {
	t.Helper()
	if _, err := tdb.Super.ExecContext(ctx, `
		INSERT INTO outcomes (session_id, tenant_id, outcome_type, external_ref, details, observed_at)
		VALUES ($1, $2, $3, $4, $5::jsonb, now())
	`, sessionID.String(), tenantID.String(), outcomeType, externalRef, details); err != nil {
		t.Fatalf("seed outcome: %v", err)
	}
}

func seedAPIScore(
	ctx context.Context,
	t *testing.T,
	tdb *dbtest.TestDB,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	scorerVersion string,
	compositeScore float64,
	signals string,
	rationale string,
	contributorWeight float64,
	scoredAt time.Time,
) {
	t.Helper()
	if _, err := tdb.Super.ExecContext(ctx, `
		INSERT INTO session_scores (
		  session_id, tenant_id, scorer_version, composite_score,
		  signals, rationale, contributor_weight, scored_at
		) VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8)
	`, sessionID.String(), tenantID.String(), scorerVersion, compositeScore, signals, rationale, contributorWeight, scoredAt); err != nil {
		t.Fatalf("seed score: %v", err)
	}
}
