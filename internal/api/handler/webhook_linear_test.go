package handler

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/db/repo"
)

func newLinearTestHandler(t *testing.T, sink webhookSink, secret string, rdb *goredis.Client) http.HandlerFunc {
	t.Helper()
	return linearWebhookHandler(silentLogger(), rdb, secret, sink, func() time.Time {
		return time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC)
	})
}

func doLinearRequest(t *testing.T, h http.HandlerFunc, delivery, sig string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/webhooks/linear", bytes.NewReader(body))
	if delivery != "" {
		req.Header.Set("Linear-Delivery", delivery)
	}
	if sig != "" {
		req.Header.Set("Linear-Signature", sig)
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func signLinearBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func linearFixture(t *testing.T, name string, sessionID uuid.UUID) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/webhooks/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return []byte(strings.ReplaceAll(string(body), "{{SESSION_ID}}", sessionID.String()))
}

func TestLinearWebhook_HMAC_BadSignatureRejected(t *testing.T) {
	sink := &fakeSink{}
	body := []byte(`{"type":"Issue","action":"create","data":{}}`)
	h := newLinearTestHandler(t, sink, "linear-secret", nil)
	rec := doLinearRequest(t, h, "linear-delivery-1", "deadbeef", body)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 || len(sink.pending) != 0 || len(sink.audits) != 0 {
		t.Fatalf("bad signature should not write; outcomes=%d pending=%d audits=%d",
			len(sink.outcomes), len(sink.pending), len(sink.audits))
	}
}

func TestLinearWebhook_MissingDeliveryRejected(t *testing.T) {
	sink := &fakeSink{}
	secret := "linear-secret"
	body := []byte(`{"type":"Issue","action":"create","data":{}}`)
	h := newLinearTestHandler(t, sink, secret, nil)
	rec := doLinearRequest(t, h, "", signLinearBody(secret, body), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestLinearWebhook_Idempotency_ReplayReturnsOK(t *testing.T) {
	sessionID := uuid.New()
	tenantID := uuid.New()
	body := linearFixture(t, "linear_issue_created.json", sessionID)
	sink := &fakeSink{
		sessionsByID: map[uuid.UUID]repo.Session{
			sessionID: {ID: sessionID, TenantID: tenantID},
		},
	}
	secret := "linear-secret"
	rdb := newMiniRedis(t)
	h := newLinearTestHandler(t, sink, secret, rdb)
	sig := signLinearBody(secret, body)

	rec1 := doLinearRequest(t, h, "linear-delivery-2", sig, body)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: want 200 got %d body=%q", rec1.Code, rec1.Body.String())
	}
	rec2 := doLinearRequest(t, h, "linear-delivery-2", sig, body)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: want 200 got %d", rec2.Code)
	}
	if rec2.Header().Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("replay missing X-Idempotent-Replay header")
	}
	if len(sink.outcomes) != 1 || len(sink.audits) != 1 {
		t.Fatalf("replay should not write again; outcomes=%d audits=%d", len(sink.outcomes), len(sink.audits))
	}
}

func TestLinearWebhook_IssueCreatedIncidentMarker_InsertsOutcomeAndAudit(t *testing.T) {
	sessionID := uuid.New()
	tenantID := uuid.New()
	body := linearFixture(t, "linear_issue_created.json", sessionID)
	sink := &fakeSink{
		sessionsByID: map[uuid.UUID]repo.Session{
			sessionID: {ID: sessionID, TenantID: tenantID},
		},
	}
	secret := "linear-secret"
	h := newLinearTestHandler(t, sink, secret, nil)
	rec := doLinearRequest(t, h, "linear-delivery-3", signLinearBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%q", rec.Code, rec.Body.String())
	}
	if len(sink.outcomes) != 1 {
		t.Fatalf("want 1 outcome got %d", len(sink.outcomes))
	}
	got := sink.outcomes[0]
	if got.TenantID != tenantID || got.Outcome.SessionID != sessionID {
		t.Fatalf("wrong outcome tenant/session: %+v", got)
	}
	if got.Outcome.OutcomeType != repo.OutcomeIncidentCaused {
		t.Fatalf("want incident_caused got %s", got.Outcome.OutcomeType)
	}
	if got.Outcome.ExternalRef == nil || !strings.Contains(*got.Outcome.ExternalRef, "/OPS-42/") {
		t.Fatalf("wrong external_ref: %v", got.Outcome.ExternalRef)
	}
	if len(sink.audits) != 1 || sink.audits[0].Entry.EventType != auditEventIncidentLinked {
		t.Fatalf("want incident_linked audit, got %+v", sink.audits)
	}
}

func TestLinearWebhook_IssueCreatedSessionMiss_BuffersPending(t *testing.T) {
	sessionID := uuid.New()
	body := linearFixture(t, "linear_issue_created.json", sessionID)
	sink := &fakeSink{}
	secret := "linear-secret"
	h := newLinearTestHandler(t, sink, secret, nil)
	rec := doLinearRequest(t, h, "linear-delivery-4", signLinearBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 || len(sink.audits) != 0 {
		t.Fatalf("miss should not write outcome/audit; outcomes=%d audits=%d", len(sink.outcomes), len(sink.audits))
	}
	if len(sink.pending) != 1 {
		t.Fatalf("want 1 pending row got %d", len(sink.pending))
	}
	if sink.pending[0].Source != repo.PendingSourceLinear || sink.pending[0].EventType != "issue_created" {
		t.Fatalf("wrong pending row: %+v", sink.pending[0])
	}
}

func TestLinearWebhook_CommentIgnored(t *testing.T) {
	body := linearFixture(t, "linear_comment_created.json", uuid.New())
	sink := &fakeSink{}
	secret := "linear-secret"
	h := newLinearTestHandler(t, sink, secret, nil)
	rec := doLinearRequest(t, h, "linear-delivery-5", signLinearBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if len(sink.outcomes) != 0 || len(sink.pending) != 0 || len(sink.audits) != 0 {
		t.Fatalf("comment should be a no-op; outcomes=%d pending=%d audits=%d",
			len(sink.outcomes), len(sink.pending), len(sink.audits))
	}
}

func TestLinearWebhook_IssueDoneAfterIncident_WritesResolvedAudit(t *testing.T) {
	sessionID := uuid.New()
	tenantID := uuid.New()
	body := linearFixture(t, "linear_issue_done.json", sessionID)
	externalRef := "https://linear.app/acme/issue/OPS-42/production-incident-from-agent-run"
	sink := &fakeSink{
		outcomesByRef: map[string]repo.Outcome{
			repo.OutcomeIncidentCaused + "|" + externalRef: {
				SessionID:   sessionID,
				TenantID:    tenantID,
				OutcomeType: repo.OutcomeIncidentCaused,
				ExternalRef: &externalRef,
			},
		},
	}
	secret := "linear-secret"
	h := newLinearTestHandler(t, sink, secret, nil)
	rec := doLinearRequest(t, h, "linear-delivery-6", signLinearBody(secret, body), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%q", rec.Code, rec.Body.String())
	}
	if len(sink.outcomes) != 0 {
		t.Fatalf("Done transition should not mutate outcomes; got %d inserts", len(sink.outcomes))
	}
	if len(sink.audits) != 1 {
		t.Fatalf("want 1 audit got %d", len(sink.audits))
	}
	if sink.audits[0].TenantID != tenantID || sink.audits[0].Entry.EventType != auditEventIncidentResolved {
		t.Fatalf("wrong resolved audit: %+v", sink.audits[0])
	}
}

func TestLinearWebhook_MalformedJSON_Returns400(t *testing.T) {
	sink := &fakeSink{}
	secret := "linear-secret"
	body := []byte(`not-json`)
	h := newLinearTestHandler(t, sink, secret, nil)
	rec := doLinearRequest(t, h, "linear-delivery-7", signLinearBody(secret, body), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}
