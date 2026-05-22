package handler

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

type fakeSessionSummaryLister struct {
	rows            []repo.SessionListRow
	err             error
	calls           int
	filter          repo.SessionSummaryFilter
	limit           int
	cursorStartedAt time.Time
	cursorID        uuid.UUID
}

func (f *fakeSessionSummaryLister) ListSessionSummaries(
	_ *http.Request,
	filter repo.SessionSummaryFilter,
	limit int,
	cursorStartedAt time.Time,
	cursorID uuid.UUID,
) ([]repo.SessionListRow, error) {
	f.calls++
	f.filter = filter
	f.limit = limit
	f.cursorStartedAt = cursorStartedAt
	f.cursorID = cursorID
	return f.rows, f.err
}

func TestListSessionsRejectsNLSearch(t *testing.T) {
	lister := &fakeSessionSummaryLister{}
	rec := doListSessionsRequest(t, lister, testPrincipal(nil), "/v1/sessions?q=find+good+prompts")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 body=%s", rec.Code, rec.Body.String())
	}
	var body apiError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != "nl_search_not_supported" || body.See != "ARCHITECTURE.md#anti-screens" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if lister.calls != 0 {
		t.Fatalf("lister should not be called, got %d calls", lister.calls)
	}
}

func TestListSessionsNonAdminOverridesUserAndExcludesDirty(t *testing.T) {
	principal := testPrincipal(nil)
	otherUser := uuid.New()
	lister := &fakeSessionSummaryLister{}

	rec := doListSessionsRequest(t, lister, principal, "/v1/sessions?user_id="+otherUser.String())

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if lister.calls != 1 {
		t.Fatalf("lister calls: got %d want 1", lister.calls)
	}
	if lister.filter.UserID == nil || *lister.filter.UserID != principal.UserID {
		t.Fatalf("non-admin user_id not forced to principal: %+v", lister.filter.UserID)
	}
	if !lister.filter.ExcludeDirty {
		t.Fatalf("non-admin list should exclude dirty sessions by default")
	}
}

func TestListSessionsNonAdminDirtyClassificationForbidden(t *testing.T) {
	lister := &fakeSessionSummaryLister{}
	rec := doListSessionsRequest(t, lister, testPrincipal(nil), "/v1/sessions?classification=dirty")

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d want 403 body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dirty_sessions_admin_only") {
		t.Fatalf("body missing dirty_sessions_admin_only: %s", rec.Body.String())
	}
	if lister.calls != 0 {
		t.Fatalf("lister should not be called, got %d calls", lister.calls)
	}
}

func TestListSessionsAdminFiltersAndLimitCap(t *testing.T) {
	admin := testPrincipal([]string{"admin"})
	userID := uuid.New()
	lister := &fakeSessionSummaryLister{}
	url := "/v1/sessions?user_id=" + userID.String() +
		"&classification=dirty&harness=codex&has_outcome=pr_merged&limit=500"

	rec := doListSessionsRequest(t, lister, admin, url)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if lister.limit != maxSessionsLimit+1 {
		t.Fatalf("handler should fetch capped limit + 1: got %d want %d", lister.limit, maxSessionsLimit+1)
	}
	if lister.filter.UserID == nil || *lister.filter.UserID != userID {
		t.Fatalf("admin user_id filter mismatch: %+v", lister.filter.UserID)
	}
	if lister.filter.Classification == nil || *lister.filter.Classification != "dirty" {
		t.Fatalf("classification filter mismatch: %+v", lister.filter.Classification)
	}
	if lister.filter.Harness == nil || *lister.filter.Harness != "codex" {
		t.Fatalf("harness filter mismatch: %+v", lister.filter.Harness)
	}
	if lister.filter.HasOutcome == nil || *lister.filter.HasOutcome != "pr_merged" {
		t.Fatalf("outcome filter mismatch: %+v", lister.filter.HasOutcome)
	}
}

func TestListSessionsReturnsSummariesAndNextCursor(t *testing.T) {
	principal := testPrincipal([]string{"owner"})
	base := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	scoreSessionID := uuid.New()
	rationale := "good reuse"
	lister := &fakeSessionSummaryLister{
		rows: []repo.SessionListRow{
			{
				Session: repo.Session{
					ID:             scoreSessionID,
					TenantID:       principal.TenantID,
					UserID:         principal.UserID,
					Harness:        "codex",
					Model:          "gpt-5",
					Tools:          nil,
					StartedAt:      base.Add(2 * time.Minute),
					RedactedPrompt: "first prompt",
					Classification: repo.ClassificationClean,
				},
				LatestScore: &repo.Score{
					ID:                uuid.New(),
					SessionID:         scoreSessionID,
					TenantID:          principal.TenantID,
					ScorerVersion:     "test",
					CompositeScore:    0.91,
					Signals:           []byte(`{"peer_reuse_count":2}`),
					Rationale:         &rationale,
					ContributorWeight: 0.7,
					ScoredAt:          base.Add(3 * time.Minute),
				},
			},
			{Session: testSessionRow(principal, base.Add(time.Minute), "second prompt")},
			{Session: testSessionRow(principal, base, "third prompt")},
		},
	}

	rec := doListSessionsRequest(t, lister, principal, "/v1/sessions?limit=2")

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if lister.limit != 3 {
		t.Fatalf("lister limit: got %d want 3", lister.limit)
	}
	var body contracts.ListSessionsResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(body.Sessions) != 2 {
		t.Fatalf("sessions len: got %d want 2", len(body.Sessions))
	}
	if body.Sessions[0].LatestScore == nil || body.Sessions[0].LatestScore.CompositeScore != 0.91 {
		t.Fatalf("latest score not mapped: %+v", body.Sessions[0].LatestScore)
	}
	if body.Sessions[0].Tools == nil || len(body.Sessions[0].Tools) != 0 {
		t.Fatalf("nil tools should map to empty list: %#v", body.Sessions[0].Tools)
	}
	if body.NextCursor == nil {
		t.Fatal("expected next_cursor")
	}
	startedAt, id, err := decodeSessionsCursor(*body.NextCursor)
	if err != nil {
		t.Fatalf("decode next_cursor: %v", err)
	}
	if !startedAt.Equal(lister.rows[1].Session.StartedAt) || id != lister.rows[1].Session.ID {
		t.Fatalf("next cursor anchors returned last row: got (%s,%s), want (%s,%s)",
			startedAt, id, lister.rows[1].Session.StartedAt, lister.rows[1].Session.ID)
	}
}

func TestListSessionsInvalidCombinationsReturnDetails(t *testing.T) {
	lister := &fakeSessionSummaryLister{}
	url := "/v1/sessions?min_score=0.9&max_score=0.1&started_after=2026-01-02T00:00:00Z&started_before=2026-01-01T00:00:00Z"
	rec := doListSessionsRequest(t, lister, testPrincipal([]string{"admin"}), url)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400 body=%s", rec.Code, rec.Body.String())
	}
	var body apiError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != "invalid_query" || len(body.Details) < 2 {
		t.Fatalf("expected invalid_query with details, got %+v", body)
	}
	if lister.calls != 0 {
		t.Fatalf("lister should not be called, got %d calls", lister.calls)
	}
}

func doListSessionsRequest(t *testing.T, lister *fakeSessionSummaryLister, principal contracts.Principal, url string) *httptest.ResponseRecorder {
	t.Helper()
	handler := listSessionsHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), lister)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req = req.WithContext(contracts.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func testPrincipal(roles []string) contracts.Principal {
	return contracts.Principal{
		UserID:   uuid.New(),
		TenantID: uuid.New(),
		Roles:    roles,
		TokenID:  "test-token",
	}
}

func testSessionRow(principal contracts.Principal, startedAt time.Time, prompt string) repo.Session {
	return repo.Session{
		ID:             uuid.New(),
		TenantID:       principal.TenantID,
		UserID:         principal.UserID,
		Harness:        "codex",
		Model:          "gpt-5",
		Tools:          []string{},
		StartedAt:      startedAt,
		RedactedPrompt: prompt,
		Classification: repo.ClassificationClean,
	}
}
