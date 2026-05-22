package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/db/repo"
)

// fakeSink is an in-memory webhookSink that records every call so the
// tests can assert dispatch behaviour without a real database. Lookup
// behaviour is configurable per test via the maps + error fields.
type fakeSink struct {
	outcomes         []recordedOutcome
	pending          []repo.PendingOutcome
	sessionsByID     map[uuid.UUID]repo.Session
	sessionsByCommit map[string]repo.Session // key = repoHash + "|" + sha
}

type recordedOutcome struct {
	TenantID uuid.UUID
	Outcome  repo.Outcome
}

func (s *fakeSink) InsertOutcome(_ context.Context, tenantID uuid.UUID, o repo.Outcome) error {
	s.outcomes = append(s.outcomes, recordedOutcome{TenantID: tenantID, Outcome: o})
	return nil
}

func (s *fakeSink) InsertPending(_ context.Context, p repo.PendingOutcome) error {
	s.pending = append(s.pending, p)
	return nil
}

func (s *fakeSink) LookupBySessionID(_ context.Context, id uuid.UUID) (repo.Session, error) {
	if s.sessionsByID == nil {
		return repo.Session{}, pgx.ErrNoRows
	}
	if sess, ok := s.sessionsByID[id]; ok {
		return sess, nil
	}
	return repo.Session{}, pgx.ErrNoRows
}

func (s *fakeSink) LookupByRepoCommit(_ context.Context, repoHash, commitSHA string) (repo.Session, error) {
	if s.sessionsByCommit == nil {
		return repo.Session{}, pgx.ErrNoRows
	}
	key := repoHash + "|" + commitSHA
	if sess, ok := s.sessionsByCommit[key]; ok {
		return sess, nil
	}
	return repo.Session{}, pgx.ErrNoRows
}

// silentLogger discards every log line so test output stays tidy.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// signGitHubBody returns the X-Hub-Signature-256 header value for body
// keyed by secret. Used by every happy-path test.
func signGitHubBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// newTestHandler builds a handler with a miniredis-backed client (so
// idempotency exercises the SETNX path) and the supplied fake sink.
// Tests that want to bypass Redis pass rdb=nil; tests that want
// idempotency assertions pass the returned client back into a second
// invocation.
func newTestHandler(t *testing.T, sink webhookSink, secret string, rdb *goredis.Client) http.HandlerFunc {
	t.Helper()
	return githubWebhookHandler(silentLogger(), rdb, secret, sink, func() time.Time {
		return time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC)
	})
}

func newMiniRedis(t *testing.T) *goredis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	return goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
}

func doRequest(t *testing.T, h http.HandlerFunc, event, delivery, sig string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/github", bytes.NewReader(body))
	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("X-Hub-Signature-256", sig)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

// ---------------------------------------------------------------------------
// HMAC verification
// ---------------------------------------------------------------------------

func TestWebhook_HMAC_MissingSignatureRejected(t *testing.T) {
	sink := &fakeSink{}
	h := newTestHandler(t, sink, "shh", nil)
	rec := doRequest(t, h, "ping", "abc", "", []byte(`{}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 || len(sink.pending) != 0 {
		t.Fatalf("no writes should occur on bad signature")
	}
}

func TestWebhook_HMAC_BadSignatureRejected(t *testing.T) {
	sink := &fakeSink{}
	h := newTestHandler(t, sink, "shh", nil)
	rec := doRequest(t, h, "ping", "abc", "sha256=deadbeef", []byte(`{}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_signature") {
		t.Fatalf("body should be generic invalid_signature, got %q", rec.Body.String())
	}
}

func TestWebhook_HMAC_GoodSignatureAccepted(t *testing.T) {
	sink := &fakeSink{}
	secret := "shh"
	body := []byte(`{}`)
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "ping", "abc", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWebhook_HMAC_EmptySecretRejectsAll(t *testing.T) {
	sink := &fakeSink{}
	body := []byte(`{}`)
	// secret = "" — handler is misconfigured; every delivery 401s.
	h := newTestHandler(t, sink, "", nil)
	rec := doRequest(t, h, "ping", "abc", signGitHubBody("", body), body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 with empty secret, got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Idempotency
// ---------------------------------------------------------------------------

func TestWebhook_Idempotency_ReplayReturnsCachedOK(t *testing.T) {
	sink := &fakeSink{}
	secret := "shh"
	rdb := newMiniRedis(t)
	h := newTestHandler(t, sink, secret, rdb)

	body := buildPRMergedBody(t)
	delivery := "delivery-12345"
	sig := signGitHubBody(secret, body)

	rec1 := doRequest(t, h, "pull_request", delivery, sig, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call status: want 200 got %d", rec1.Code)
	}
	pendingAfterFirst := len(sink.pending)

	rec2 := doRequest(t, h, "pull_request", delivery, sig, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay status: want 200 got %d", rec2.Code)
	}
	if rec2.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("replay missing X-Idempotent-Replay header; got headers=%v", rec2.Header())
	}
	// Second call must NOT trigger another buffer/outcome insert.
	if len(sink.pending) != pendingAfterFirst {
		t.Fatalf("replay should not write pending again; before=%d after=%d", pendingAfterFirst, len(sink.pending))
	}
}

func TestWebhook_MissingDeliveryRejected(t *testing.T) {
	sink := &fakeSink{}
	secret := "shh"
	body := []byte(`{}`)
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// pull_request dispatch
// ---------------------------------------------------------------------------

func buildPRMergedBody(t *testing.T) []byte {
	t.Helper()
	ev := githubPullRequestEvent{
		Action: "closed",
		Repository: githubRepository{
			HTMLURL: "https://github.com/acme/widgets",
		},
		PullRequest: githubPullRequest{
			HTMLURL:        "https://github.com/acme/widgets/pull/42",
			Title:          "Add foo",
			Merged:         true,
			MergeCommitSHA: "deadbeefcafef00d1234567890abcdef00000000",
			Head:           githubPRRef{SHA: "headshacafef00d1234567890abcdef00000000"},
		},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWebhook_PRMerged_InsertsOutcomeOnMatch(t *testing.T) {
	body := buildPRMergedBody(t)
	tenantID := uuid.New()
	sessionID := uuid.New()
	repoHash := hashRepoURL("https://github.com/acme/widgets")
	sink := &fakeSink{
		sessionsByCommit: map[string]repo.Session{
			repoHash + "|" + "deadbeefcafef00d1234567890abcdef00000000": {
				ID: sessionID, TenantID: tenantID,
			},
		},
	}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "d1", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 1 {
		t.Fatalf("want 1 outcome got %d", len(sink.outcomes))
	}
	got := sink.outcomes[0]
	if got.TenantID != tenantID {
		t.Fatalf("wrong tenant: got %s want %s", got.TenantID, tenantID)
	}
	if got.Outcome.OutcomeType != repo.OutcomePRMerged {
		t.Fatalf("wrong outcome type: %s", got.Outcome.OutcomeType)
	}
	if got.Outcome.ExternalRef == nil || *got.Outcome.ExternalRef != "https://github.com/acme/widgets/pull/42" {
		t.Fatalf("wrong external_ref: %v", got.Outcome.ExternalRef)
	}
	if len(sink.pending) != 0 {
		t.Fatalf("matched event should not buffer; got %d pending", len(sink.pending))
	}
}

func TestWebhook_PRMerged_BuffersOnNoMatch(t *testing.T) {
	body := buildPRMergedBody(t)
	sink := &fakeSink{} // no sessions registered
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "d1", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 {
		t.Fatalf("expected no outcomes on miss; got %d", len(sink.outcomes))
	}
	if len(sink.pending) != 1 {
		t.Fatalf("expected 1 pending row; got %d", len(sink.pending))
	}
	if sink.pending[0].Source != repo.PendingSourceGitHub {
		t.Fatalf("wrong source: %s", sink.pending[0].Source)
	}
	if sink.pending[0].EventType != "pull_request" {
		t.Fatalf("wrong event_type: %s", sink.pending[0].EventType)
	}
}

func TestWebhook_PRRevert_DetectsByTitle(t *testing.T) {
	ev := githubPullRequestEvent{
		Action:     "closed",
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		PullRequest: githubPullRequest{
			HTMLURL: "https://github.com/acme/widgets/pull/77",
			Title:   "Revert \"add foo\"",
			Merged:  false,
			Head:    githubPRRef{SHA: "revertshacafef00d1234567890abcdef00000000"},
		},
	}
	body, _ := json.Marshal(ev)
	tenantID := uuid.New()
	sessionID := uuid.New()
	repoHash := hashRepoURL("https://github.com/acme/widgets")
	sink := &fakeSink{
		sessionsByCommit: map[string]repo.Session{
			repoHash + "|" + "revertshacafef00d1234567890abcdef00000000": {
				ID: sessionID, TenantID: tenantID,
			},
		},
	}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "d-rev", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 1 || sink.outcomes[0].Outcome.OutcomeType != repo.OutcomePRReverted {
		t.Fatalf("expected one pr_reverted outcome; got %+v", sink.outcomes)
	}
}

func TestWebhook_PRRevert_DetectsByLabel(t *testing.T) {
	ev := githubPullRequestEvent{
		Action:     "closed",
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		PullRequest: githubPullRequest{
			HTMLURL: "https://github.com/acme/widgets/pull/78",
			Title:   "Roll back",
			Merged:  false,
			Head:    githubPRRef{SHA: "labelshacafef00d1234567890abcdef00000000"},
			Labels:  []githubLabel{{Name: "revert-needed"}},
		},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "d-rev-lbl", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	// No session — buffered as pending. The detection itself happened
	// (otherwise we'd have ignored the event without buffering).
	if len(sink.pending) != 1 {
		t.Fatalf("expected revert label to buffer pending; got %d", len(sink.pending))
	}
}

func TestWebhook_PRClosedUnmerged_NoOpIgnored(t *testing.T) {
	ev := githubPullRequestEvent{
		Action:     "closed",
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		PullRequest: githubPullRequest{
			HTMLURL: "https://github.com/acme/widgets/pull/79",
			Title:   "Nope",
			Merged:  false,
		},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "d-nope", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 || len(sink.pending) != 0 {
		t.Fatalf("closed-unmerged-non-revert should be ignored; outcomes=%d pending=%d",
			len(sink.outcomes), len(sink.pending))
	}
}

// ---------------------------------------------------------------------------
// push dispatch — marker-style match
// ---------------------------------------------------------------------------

func TestWebhook_PushCommitMarker_InsertsOutcome(t *testing.T) {
	sessionID := uuid.New()
	tenantID := uuid.New()
	commitSHA := "00112233445566778899aabbccddeeff00112233"

	ev := githubPushEvent{
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		Commits: []githubCommit{
			{
				ID:      commitSHA,
				Message: "feat: add foo\n\nCloses session: " + sessionID.String(),
				URL:     "https://github.com/acme/widgets/commit/" + commitSHA,
			},
		},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{
		sessionsByID: map[uuid.UUID]repo.Session{
			sessionID: {ID: sessionID, TenantID: tenantID},
		},
	}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "push", "d-push", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 1 {
		t.Fatalf("want 1 outcome got %d", len(sink.outcomes))
	}
	o := sink.outcomes[0]
	if o.Outcome.OutcomeType != repo.OutcomeCommitLanded {
		t.Fatalf("want commit_landed got %s", o.Outcome.OutcomeType)
	}
	if o.TenantID != tenantID {
		t.Fatalf("wrong tenant: got %s want %s", o.TenantID, tenantID)
	}
}

func TestWebhook_PushNoMarker_NoOp(t *testing.T) {
	ev := githubPushEvent{
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		Commits: []githubCommit{
			{ID: "abc", Message: "feat: add foo", URL: "https://github.com/acme/widgets/commit/abc"},
		},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "push", "d-push-2", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 || len(sink.pending) != 0 {
		t.Fatalf("push without marker should not touch storage; outcomes=%d pending=%d",
			len(sink.outcomes), len(sink.pending))
	}
}

func TestWebhook_PushMarker_BuffersOnSessionMiss(t *testing.T) {
	commitSHA := "abc"
	sessionID := uuid.New()
	ev := githubPushEvent{
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		Commits: []githubCommit{
			{
				ID:      commitSHA,
				Message: "wip\nCloses session: " + sessionID.String(),
				URL:     "https://github.com/acme/widgets/commit/" + commitSHA,
			},
		},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{} // session not registered → ErrNoRows
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "push", "d-push-miss", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.pending) != 1 {
		t.Fatalf("want 1 pending row; got %d", len(sink.pending))
	}
}

// ---------------------------------------------------------------------------
// check_run dispatch
// ---------------------------------------------------------------------------

func TestWebhook_CheckRun_SuccessMapsToTestsPassed(t *testing.T) {
	ev := githubCheckRunEvent{
		Action:     "completed",
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		CheckRun: githubCheckRun{
			HeadSHA:    "ffeeddccbbaa99887766554433221100ffeeddcc",
			Conclusion: "success",
			HTMLURL:    "https://github.com/acme/widgets/runs/9",
		},
	}
	body, _ := json.Marshal(ev)
	tenantID := uuid.New()
	sessionID := uuid.New()
	repoHash := hashRepoURL("https://github.com/acme/widgets")
	sink := &fakeSink{
		sessionsByCommit: map[string]repo.Session{
			repoHash + "|" + "ffeeddccbbaa99887766554433221100ffeeddcc": {
				ID: sessionID, TenantID: tenantID,
			},
		},
	}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "check_run", "d-cr-1", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 1 || sink.outcomes[0].Outcome.OutcomeType != repo.OutcomeTestsPassed {
		t.Fatalf("want tests_passed; outcomes=%+v", sink.outcomes)
	}
}

func TestWebhook_CheckRun_FailureMapsToTestsFailed(t *testing.T) {
	ev := githubCheckRunEvent{
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		CheckRun: githubCheckRun{
			HeadSHA:    "11ffeeddccbbaa99887766554433221100ffee99",
			Conclusion: "failure",
			HTMLURL:    "https://github.com/acme/widgets/runs/10",
		},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "check_run", "d-cr-2", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.pending) != 1 {
		t.Fatalf("expected buffered on miss; got %d", len(sink.pending))
	}
	if sink.pending[0].EventType != "check_run" {
		t.Fatalf("wrong event_type %s", sink.pending[0].EventType)
	}
}

func TestWebhook_CheckRun_NeutralIgnored(t *testing.T) {
	ev := githubCheckRunEvent{
		Repository: githubRepository{HTMLURL: "https://github.com/acme/widgets"},
		CheckRun:   githubCheckRun{Conclusion: "neutral"},
	}
	body, _ := json.Marshal(ev)
	sink := &fakeSink{}
	secret := "shh"
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "check_run", "d-cr-3", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusOK || len(sink.outcomes) != 0 || len(sink.pending) != 0 {
		t.Fatalf("want ignored noop; got code=%d outcomes=%d pending=%d",
			rec.Code, len(sink.outcomes), len(sink.pending))
	}
}

// ---------------------------------------------------------------------------
// Malformed payloads
// ---------------------------------------------------------------------------

func TestWebhook_MalformedJSON_Returns400(t *testing.T) {
	sink := &fakeSink{}
	secret := "shh"
	body := []byte(`not json`)
	h := newTestHandler(t, sink, secret, nil)
	rec := doRequest(t, h, "pull_request", "d-bad", signGitHubBody(secret, body), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%q", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// hashRepoURL determinism
// ---------------------------------------------------------------------------

func TestHashRepoURL_StripsDotGitAndLowercases(t *testing.T) {
	a := hashRepoURL("https://github.com/Acme/Widgets.git")
	b := hashRepoURL("https://github.com/acme/widgets")
	if a != b {
		t.Fatalf("hashRepoURL must be stable across .git suffix and case; got %s vs %s", a, b)
	}
}

func TestParseSessionMarker_RoundTrips(t *testing.T) {
	id := uuid.New()
	msg := "feat: thing\n\nCloses session: " + id.String() + "\n"
	got, ok := parseSessionMarker(msg)
	if !ok || got != id {
		t.Fatalf("parse: ok=%v got=%v want=%v", ok, got, id)
	}
	if _, ok := parseSessionMarker("no marker here"); ok {
		t.Fatalf("should not match arbitrary message")
	}
}
