//go:build integration

package handler_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/api"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/auth"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const stackTestIssuer = "https://api.workos.com"

type stackTestAuth struct {
	privateKey *rsa.PrivateKey
	keyID      string
	verifier   *auth.Verifier
}

func newStackTestAuth(t *testing.T) stackTestAuth {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	publicJWK, err := jwk.FromRaw(privateKey.Public())
	if err != nil {
		t.Fatalf("public jwk: %v", err)
	}
	keyID := "stack-test-key"
	if err := publicJWK.Set(jwk.KeyIDKey, keyID); err != nil {
		t.Fatalf("set public kid: %v", err)
	}
	if err := publicJWK.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("set public alg: %v", err)
	}
	set := jwk.NewSet()
	if err := set.AddKey(publicJWK); err != nil {
		t.Fatalf("add public key: %v", err)
	}

	verifier, err := auth.NewVerifier(auth.VerifierConfig{
		JWKSURL: "https://example.test/jwks.json",
		Issuer:  stackTestIssuer,
		Fetch: func(context.Context, string) (jwk.Set, error) {
			return set, nil
		},
	})
	if err != nil {
		t.Fatalf("auth.NewVerifier: %v", err)
	}

	return stackTestAuth{privateKey: privateKey, keyID: keyID, verifier: verifier}
}

func (a stackTestAuth) token(t *testing.T, tenantID, userID uuid.UUID) string {
	t.Helper()
	now := time.Now().UTC()
	tok, err := jwt.NewBuilder().
		Issuer(stackTestIssuer).
		Subject(userID.String()).
		Claim("tenant_id", tenantID.String()).
		Claim("token_type", "cli").
		JwtID("jti-" + uuid.NewString()).
		IssuedAt(now.Add(-1 * time.Minute)).
		NotBefore(now.Add(-1 * time.Minute)).
		Expiration(now.Add(1 * time.Hour)).
		Build()
	if err != nil {
		t.Fatalf("token build: %v", err)
	}

	signKey, err := jwk.FromRaw(a.privateKey)
	if err != nil {
		t.Fatalf("private jwk: %v", err)
	}
	if err := signKey.Set(jwk.KeyIDKey, a.keyID); err != nil {
		t.Fatalf("set private kid: %v", err)
	}
	if err := signKey.Set(jwk.AlgorithmKey, jwa.RS256); err != nil {
		t.Fatalf("set private alg: %v", err)
	}
	raw, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, signKey))
	if err != nil {
		t.Fatalf("jwt.Sign: %v", err)
	}
	return string(raw)
}

type stackAPIFixture struct {
	server *httptest.Server
	tdb    *dbtest.TestDB
	redis  *goredis.Client
	auth   stackTestAuth
}

func newStackAPIFixture(t *testing.T) *stackAPIFixture {
	t.Helper()
	installCleanTrufflehogStub(t)

	tdb := dbtest.Setup(t, "../../../migrations")
	t.Cleanup(tdb.Cleanup)

	mr := miniredis.RunT(t)
	redis := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = redis.Close() })

	testAuth := newStackTestAuth(t)
	router := api.NewRouter(app.Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:     tdb.AppPool,
		Redis:  redis,
		Auth:   testAuth.verifier,
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	return &stackAPIFixture{server: server, tdb: tdb, redis: redis, auth: testAuth}
}

func installCleanTrufflehogStub(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trufflehog-clean")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write trufflehog stub: %v", err)
	}
	t.Setenv("ITER_TRUFFLEHOG_BIN", path)
}

func seedTenantUser(t *testing.T, f *stackAPIFixture, ctx context.Context, label string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	tenantID := uuid.MustParse(f.tdb.SeedTenant(ctx, t, "tenant-"+label))
	userID := uuid.MustParse(f.tdb.SeedUser(ctx, t, label+"@example.com", "User "+label))
	f.tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleMember)
	return tenantID, userID
}

func seedUserInTenant(t *testing.T, f *stackAPIFixture, ctx context.Context, tenantID uuid.UUID, label string) uuid.UUID {
	t.Helper()
	userID := uuid.MustParse(f.tdb.SeedUser(ctx, t, label+"@example.com", "User "+label))
	f.tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), repo.RoleMember)
	return userID
}

func doStackRequest(
	t *testing.T,
	f *stackAPIFixture,
	method string,
	path string,
	token string,
	idempotencyKey string,
	body any,
) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, f.server.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return resp, respBody
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", string(body), err)
	}
	return out
}

func TestStackEndpoints_HappyPathShareUnshareAndAudit(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantID, userA := seedTenantUser(t, f, ctx, "stack-a")
	userB := seedUserInTenant(t, f, ctx, tenantID, "stack-b")
	tokenA := f.auth.token(t, tenantID, userA)
	tokenB := f.auth.token(t, tenantID, userB)

	createBody := map[string]any{
		"name":      "Go API stack",
		"harnesses": []string{"codex"},
		"skills":    []string{"golang-pro"},
		"docs":      []string{"https://go.dev/doc/"},
		"notes":     "Use table-driven tests.",
	}
	resp, body := doStackRequest(t, f, http.MethodPost, "/v1/stack", tokenA, "create-stack", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, body)
	}
	created := decodeJSON[contracts.StackResponse](t, body)
	if created.UserID != userA || created.Payload.Name != "Go API stack" {
		t.Fatalf("created stack mismatch: %+v", created)
	}

	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack", tokenA, "create-stack", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create replay status = %d body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("create replay missing X-Idempotent-Replay")
	}
	if countStacks(t, f.tdb.Super, tenantID) != 1 {
		t.Fatalf("idempotent create inserted duplicate stack")
	}

	resp, body = doStackRequest(t, f, http.MethodGet, "/v1/stack/me", tokenA, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me status = %d body=%s", resp.StatusCode, body)
	}
	own := decodeJSON[[]contracts.StackResponse](t, body)
	if len(own) != 1 || own[0].ID != created.ID {
		t.Fatalf("GET /me = %+v", own)
	}

	resp, body = doStackRequest(t, f, http.MethodGet, "/v1/stack/"+userA.String(), tokenB, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET shared before share status = %d body=%s", resp.StatusCode, body)
	}
	beforeShare := decodeJSON[[]contracts.StackResponse](t, body)
	if len(beforeShare) != 0 {
		t.Fatalf("share visible before grant: %+v", beforeShare)
	}

	shareBody := map[string]string{"shared_with_user_id": userB.String()}
	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack/"+created.ID.String()+"/share", tokenA, "share-stack", shareBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share status = %d body=%s", resp.StatusCode, body)
	}
	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack/"+created.ID.String()+"/share", tokenA, "share-stack", shareBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share replay status = %d body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("X-Idempotent-Replay") != "true" {
		t.Fatalf("share replay missing X-Idempotent-Replay")
	}

	resp, body = doStackRequest(t, f, http.MethodGet, "/v1/stack/me", tokenA, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me after share status = %d body=%s", resp.StatusCode, body)
	}
	own = decodeJSON[[]contracts.StackResponse](t, body)
	if len(own) != 1 || len(own[0].Shares) != 1 || own[0].Shares[0].SharedWithUserID != userB {
		t.Fatalf("GET /me share grants = %+v", own)
	}

	resp, body = doStackRequest(t, f, http.MethodGet, "/v1/stack/"+userA.String(), tokenB, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET shared after share status = %d body=%s", resp.StatusCode, body)
	}
	shared := decodeJSON[[]contracts.StackResponse](t, body)
	if len(shared) != 1 || shared[0].ID != created.ID {
		t.Fatalf("shared stacks = %+v", shared)
	}

	resp, body = doStackRequest(t, f, http.MethodDelete, "/v1/stack/"+created.ID.String()+"/share/"+userB.String(), tokenA, "", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unshare status = %d body=%s", resp.StatusCode, body)
	}

	resp, body = doStackRequest(t, f, http.MethodGet, "/v1/stack/"+userA.String(), tokenB, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET shared after unshare status = %d body=%s", resp.StatusCode, body)
	}
	afterUnshare := decodeJSON[[]contracts.StackResponse](t, body)
	if len(afterUnshare) != 0 {
		t.Fatalf("share still visible after revoke: %+v", afterUnshare)
	}

	events := auditEvents(t, f.tdb.Super, tenantID)
	if len(events) != 2 {
		t.Fatalf("audit events len = %d want 2: %+v", len(events), events)
	}
	if events[0].EventType != repo.AuditEventStackShared || events[1].EventType != repo.AuditEventStackUnshared {
		t.Fatalf("audit event order/type mismatch: %+v", events)
	}
	for _, ev := range events {
		if ev.ActorUserID != userA.String() || ev.TargetID != created.ID.String() || ev.SharedWithUserID != userB.String() {
			t.Fatalf("bad audit payload: %+v", ev)
		}
	}
}

func TestStackUpsertUpdatesExistingCallerStack(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantID, userID := seedTenantUser(t, f, ctx, "upsert")
	token := f.auth.token(t, tenantID, userID)

	initialBody := map[string]any{
		"name":      "Initial stack",
		"harnesses": []string{"codex"},
		"skills":    []string{"golang-pro"},
		"docs":      []string{"https://go.dev/doc/"},
	}
	resp, body := doStackRequest(t, f, http.MethodPost, "/v1/stack", token, "upsert-create", initialBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, body)
	}
	created := decodeJSON[contracts.StackResponse](t, body)

	updatedBody := map[string]any{
		"name":      "Updated stack",
		"harnesses": []string{"codex", "opencode"},
		"skills":    []string{"golang-pro", "swiftui"},
		"docs":      []string{"https://go.dev/doc/", "docs/stack.md"},
		"notes":     "Prefer focused feedback loops.",
	}
	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack", token, "upsert-update", updatedBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert status = %d body=%s", resp.StatusCode, body)
	}
	updated := decodeJSON[contracts.StackResponse](t, body)
	if updated.ID != created.ID {
		t.Fatalf("upsert changed stack id: %s -> %s", created.ID, updated.ID)
	}
	if updated.Payload.Name != "Updated stack" || len(updated.Payload.Harnesses) != 2 {
		t.Fatalf("upsert payload mismatch: %+v", updated.Payload)
	}
	if updated.Payload.Notes == nil || *updated.Payload.Notes != "Prefer focused feedback loops." {
		t.Fatalf("upsert notes mismatch: %+v", updated.Payload.Notes)
	}
	if countStacks(t, f.tdb.Super, tenantID) != 1 {
		t.Fatalf("upsert inserted duplicate stack")
	}

	resp, body = doStackRequest(t, f, http.MethodGet, "/v1/stack/me", token, "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /me status = %d body=%s", resp.StatusCode, body)
	}
	own := decodeJSON[[]contracts.StackResponse](t, body)
	if len(own) != 1 || own[0].Payload.Name != "Updated stack" {
		t.Fatalf("GET /me after upsert = %+v", own)
	}
}

func TestStackCreate_DirtyPayloadReturns422(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantID, userID := seedTenantUser(t, f, ctx, "dirty")
	token := f.auth.token(t, tenantID, userID)

	body := map[string]any{
		"name":      "Dirty stack",
		"harnesses": []string{"codex"},
		"notes":     "Customer was at 123 Main Street during testing.",
	}
	resp, respBody := doStackRequest(t, f, http.MethodPost, "/v1/stack", token, "dirty-stack", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", resp.StatusCode, respBody)
	}
	got := decodeJSON[map[string]string](t, respBody)
	if got["error"] != "classification_failed" || got["tier"] != "dirty" {
		t.Fatalf("body = %v", got)
	}
	if countStacks(t, f.tdb.Super, tenantID) != 0 {
		t.Fatalf("dirty stack was persisted")
	}
}

func TestStackCreate_EnvVarHeuristicReturns422(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantID, userID := seedTenantUser(t, f, ctx, "env")
	token := f.auth.token(t, tenantID, userID)

	body := map[string]any{
		"name":      "Env stack",
		"harnesses": []string{"codex"},
		"notes":     "Run with OPENAI_API_KEY=sk-test-value in the shell.",
	}
	resp, respBody := doStackRequest(t, f, http.MethodPost, "/v1/stack", token, "env-stack", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", resp.StatusCode, respBody)
	}
	got := decodeJSON[map[string]string](t, respBody)
	if got["error"] != "raw_config_forbidden" {
		t.Fatalf("body = %v", got)
	}
	if countStacks(t, f.tdb.Super, tenantID) != 0 {
		t.Fatalf("env-shaped stack was persisted")
	}
}

func TestStackCreate_SecretShapedDocReturns422(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantID, userID := seedTenantUser(t, f, ctx, "secret-doc")
	token := f.auth.token(t, tenantID, userID)

	body := map[string]any{
		"name":      "Secret doc stack",
		"harnesses": []string{"codex"},
		"docs":      []string{"./.env.local"},
	}
	resp, respBody := doStackRequest(t, f, http.MethodPost, "/v1/stack", token, "secret-doc-stack", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", resp.StatusCode, respBody)
	}
	got := decodeJSON[map[string]string](t, respBody)
	if got["error"] != "raw_config_forbidden" {
		t.Fatalf("body = %v", got)
	}
	if countStacks(t, f.tdb.Super, tenantID) != 0 {
		t.Fatalf("secret-shaped doc stack was persisted")
	}
}

func TestStackShare_CrossTenantTargetReturns422(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantA, userA := seedTenantUser(t, f, ctx, "tenant-a")
	_, userB := seedTenantUser(t, f, ctx, "tenant-b")
	tokenA := f.auth.token(t, tenantA, userA)
	stackID := createStack(t, f, tokenA)

	body := map[string]string{"shared_with_user_id": userB.String()}
	resp, respBody := doStackRequest(t, f, http.MethodPost, "/v1/stack/"+stackID.String()+"/share", tokenA, "cross-share", body)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d body=%s", resp.StatusCode, respBody)
	}
	got := decodeJSON[map[string]string](t, respBody)
	if got["error"] != "cross_tenant_share_forbidden" {
		t.Fatalf("body = %v", got)
	}
}

func TestStackShare_IncludedDocsMustBeSafeSubset(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantID, userA := seedTenantUser(t, f, ctx, "share-docs-a")
	userB := seedUserInTenant(t, f, ctx, tenantID, "share-docs-b")
	tokenA := f.auth.token(t, tenantID, userA)

	createBody := map[string]any{
		"name":      "Share docs stack",
		"harnesses": []string{"codex"},
		"docs":      []string{"docs/stack.md", "https://go.dev/doc/"},
	}
	resp, body := doStackRequest(t, f, http.MethodPost, "/v1/stack", tokenA, "share-docs-create", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", resp.StatusCode, body)
	}
	stackID := decodeJSON[contracts.StackResponse](t, body).ID

	shareBody := map[string]any{
		"shared_with_user_id": userB.String(),
		"included_docs":       []string{"docs/stack.md"},
	}
	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack/"+stackID.String()+"/share", tokenA, "share-docs-ok", shareBody)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("share status = %d body=%s", resp.StatusCode, body)
	}

	badBody := map[string]any{
		"shared_with_user_id": userB.String(),
		"included_docs":       []string{"./.env.local"},
	}
	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack/"+stackID.String()+"/share", tokenA, "share-docs-secret", badBody)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("secret doc share status = %d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]string](t, body)
	if got["error"] != "raw_config_forbidden" {
		t.Fatalf("secret doc share body = %v", got)
	}

	outOfStackBody := map[string]any{
		"shared_with_user_id": userB.String(),
		"included_docs":       []string{"docs/not-in-stack.md"},
	}
	resp, body = doStackRequest(t, f, http.MethodPost, "/v1/stack/"+stackID.String()+"/share", tokenA, "share-docs-subset", outOfStackBody)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("out-of-stack doc share status = %d body=%s", resp.StatusCode, body)
	}
	got = decodeJSON[map[string]string](t, body)
	if got["error"] != "invalid_stack_share" {
		t.Fatalf("out-of-stack doc share body = %v", got)
	}
}

func TestStackUser_CrossTenantOwnerReturns404(t *testing.T) {
	f := newStackAPIFixture(t)
	ctx := context.Background()
	tenantA, userA := seedTenantUser(t, f, ctx, "viewer")
	_, userB := seedTenantUser(t, f, ctx, "other-tenant-owner")
	tokenA := f.auth.token(t, tenantA, userA)

	resp, body := doStackRequest(t, f, http.MethodGet, "/v1/stack/"+userB.String(), tokenA, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	got := decodeJSON[map[string]string](t, body)
	if got["error"] != "not_found" {
		t.Fatalf("body = %v", got)
	}
}

func createStack(t *testing.T, f *stackAPIFixture, token string) uuid.UUID {
	t.Helper()
	body := map[string]any{
		"name":      "Share source",
		"harnesses": []string{"codex"},
	}
	resp, respBody := doStackRequest(t, f, http.MethodPost, "/v1/stack", token, "create-"+uuid.NewString(), body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create stack status = %d body=%s", resp.StatusCode, respBody)
	}
	return decodeJSON[contracts.StackResponse](t, respBody).ID
}

func countStacks(t *testing.T, db *sql.DB, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM stacks WHERE tenant_id = $1`, tenantID,
	).Scan(&n); err != nil {
		t.Fatalf("count stacks: %v", err)
	}
	return n
}

type auditEvent struct {
	EventType        string
	ActorUserID      string
	TargetID         string
	SharedWithUserID string
}

func auditEvents(t *testing.T, db *sql.DB, tenantID uuid.UUID) []auditEvent {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), `
		SELECT event_type, actor_user_id::text, target_id, details->>'shared_with_user_id'
		  FROM audit_log
		 WHERE tenant_id = $1
		 ORDER BY id ASC
	`, tenantID)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []auditEvent
	for rows.Next() {
		var ev auditEvent
		if err := rows.Scan(&ev.EventType, &ev.ActorUserID, &ev.TargetID, &ev.SharedWithUserID); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit events: %v", err)
	}
	return out
}
