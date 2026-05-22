package handler

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

func TestParseListSessionsRejectsNLSearch(t *testing.T) {
	_, _, _, _, qerr := parseListSessionsQuery(mustParseQuery(t, "q=find+good+prompts"), testSessionsPrincipal(), false)

	if qerr == nil {
		t.Fatal("expected query error")
	}
	if qerr.status != http.StatusBadRequest || qerr.body.Error != "nl_search_not_supported" ||
		qerr.body.See != "ARCHITECTURE.md#anti-screens" {
		t.Fatalf("unexpected query error: status=%d body=%+v", qerr.status, qerr.body)
	}
}

func TestParseListSessionsNonAdminOverridesUserAndExcludesDirty(t *testing.T) {
	principal := testSessionsPrincipal()
	otherUser := uuid.New()

	filter, limit, _, _, qerr := parseListSessionsQuery(
		mustParseQuery(t, "user_id="+otherUser.String()),
		principal,
		false,
	)

	if qerr != nil {
		t.Fatalf("unexpected query error: %+v", qerr)
	}
	if limit != defaultSessionsLimit {
		t.Fatalf("limit: got %d want %d", limit, defaultSessionsLimit)
	}
	if filter.UserID == nil || *filter.UserID != principal.UserID {
		t.Fatalf("non-admin user_id not forced to principal: %+v", filter.UserID)
	}
	if !filter.ExcludeDirty {
		t.Fatalf("non-admin list should exclude dirty sessions by default")
	}
}

func TestParseListSessionsNonAdminDirtyClassificationForbidden(t *testing.T) {
	_, _, _, _, qerr := parseListSessionsQuery(mustParseQuery(t, "classification=dirty"), testSessionsPrincipal(), false)

	if qerr == nil {
		t.Fatal("expected query error")
	}
	if qerr.status != http.StatusForbidden || qerr.body.Error != "dirty_sessions_admin_only" {
		t.Fatalf("unexpected query error: status=%d body=%+v", qerr.status, qerr.body)
	}
}

func TestParseListSessionsAdminFiltersAndLimitCap(t *testing.T) {
	userID := uuid.New()
	raw := "user_id=" + userID.String() +
		"&classification=dirty&harness=codex&has_outcome=pr_merged&limit=500"

	filter, limit, _, _, qerr := parseListSessionsQuery(mustParseQuery(t, raw), testSessionsPrincipal(), true)

	if qerr != nil {
		t.Fatalf("unexpected query error: %+v", qerr)
	}
	if limit != maxSessionsLimit {
		t.Fatalf("limit cap: got %d want %d", limit, maxSessionsLimit)
	}
	if filter.UserID == nil || *filter.UserID != userID {
		t.Fatalf("admin user_id filter mismatch: %+v", filter.UserID)
	}
	if filter.Classification == nil || *filter.Classification != "dirty" {
		t.Fatalf("classification filter mismatch: %+v", filter.Classification)
	}
	if filter.Harness == nil || *filter.Harness != "codex" {
		t.Fatalf("harness filter mismatch: %+v", filter.Harness)
	}
	if filter.HasOutcome == nil || *filter.HasOutcome != "pr_merged" {
		t.Fatalf("outcome filter mismatch: %+v", filter.HasOutcome)
	}
}

func TestParseListSessionsInvalidCombinationsReturnDetails(t *testing.T) {
	raw := "min_score=0.9&max_score=0.1&started_after=2026-01-02T00:00:00Z&started_before=2026-01-01T00:00:00Z"
	_, _, _, _, qerr := parseListSessionsQuery(mustParseQuery(t, raw), testSessionsPrincipal(), true)

	if qerr == nil {
		t.Fatal("expected query error")
	}
	if qerr.status != http.StatusBadRequest || qerr.body.Error != "invalid_query" || len(qerr.body.Details) < 2 {
		t.Fatalf("expected invalid_query with details, got status=%d body=%+v", qerr.status, qerr.body)
	}
}

func TestMapSessionSummariesReturnsScoresAndEmptyTools(t *testing.T) {
	principal := testSessionsPrincipal()
	base := time.Date(2026, 5, 22, 14, 0, 0, 0, time.UTC)
	scoreSessionID := uuid.New()
	rationale := "good reuse"

	body, err := mapSessionSummaries([]repo.SessionListRow{
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
	})
	if err != nil {
		t.Fatalf("mapSessionSummaries: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("sessions len: got %d want 1", len(body))
	}
	if body[0].LatestScore == nil || body[0].LatestScore.CompositeScore != 0.91 {
		t.Fatalf("latest score not mapped: %+v", body[0].LatestScore)
	}
	if body[0].Tools == nil || len(body[0].Tools) != 0 {
		t.Fatalf("nil tools should map to empty list: %#v", body[0].Tools)
	}
}

func mustParseQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	values, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("parse query %q: %v", raw, err)
	}
	return values
}

func testSessionsPrincipal() contracts.Principal {
	return contracts.Principal{
		UserID:   uuid.New(),
		TenantID: uuid.New(),
		TokenID:  "test-token",
	}
}
