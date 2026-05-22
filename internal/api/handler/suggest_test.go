package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
	"github.com/iter-dev/iter/pkg/contracts"
)

type fakeEmbedProvider struct {
	name  string
	vec   []float32
	delay time.Duration
	err   error

	calls atomic.Int32
}

func (p *fakeEmbedProvider) Name() string {
	if p.name == "" {
		return "fake"
	}
	return p.name
}

func (p *fakeEmbedProvider) Embed(ctx context.Context, req embed.EmbedRequest) (embed.EmbedResponse, error) {
	p.calls.Add(1)
	if p.delay > 0 {
		select {
		case <-ctx.Done():
			return embed.EmbedResponse{}, ctx.Err()
		case <-time.After(p.delay):
		}
	}
	if p.err != nil {
		return embed.EmbedResponse{}, p.err
	}
	vec := p.vec
	if vec == nil {
		vec = testVector()
	}
	out := make([][]float32, len(req.Inputs))
	for i := range out {
		out[i] = append([]float32(nil), vec...)
	}
	return embed.EmbedResponse{Vectors: out}, nil
}

type fakeLLM struct {
	text string
	err  error

	calls atomic.Int32
}

func (f *fakeLLM) Complete(context.Context, contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error) {
	f.calls.Add(1)
	if f.err != nil {
		return contracts.LLMCompletionResponse{}, f.err
	}
	return contracts.LLMCompletionResponse{Provider: "fake", Model: "fake", Text: f.text}, nil
}

type fakeSuggestStore struct {
	search    suggestCandidateSearch
	searchErr error

	mu             sync.Mutex
	persisted      []persistedSuggestion
	persistErr     error
	persistBlock   chan struct{}
	persistStarted chan struct{}
	startOnce      sync.Once
}

func (s *fakeSuggestStore) SearchCandidates(context.Context, []float32, int) (suggestCandidateSearch, error) {
	if s.searchErr != nil {
		return suggestCandidateSearch{}, s.searchErr
	}
	return s.search, nil
}

func (s *fakeSuggestStore) PersistSuggestion(context.Context, persistedSuggestion) error {
	if s.persistStarted != nil {
		s.startOnce.Do(func() { close(s.persistStarted) })
	}
	if s.persistBlock != nil {
		<-s.persistBlock
	}
	if s.persistErr != nil {
		return s.persistErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persisted = append(s.persisted, persistedSuggestion{})
	return nil
}

func (s *fakeSuggestStore) persistCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.persisted)
}

type fakeEmbedRedis struct {
	mu       sync.Mutex
	store    map[string][]byte
	setCalls int
}

func newFakeEmbedRedis() *fakeEmbedRedis {
	return &fakeEmbedRedis{store: make(map[string][]byte)}
}

func (f *fakeEmbedRedis) Get(_ context.Context, key string) *goredis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := goredis.NewStringCmd(context.Background(), "get", key)
	v, ok := f.store[key]
	if !ok {
		cmd.SetErr(goredis.Nil)
		return cmd
	}
	cmd.SetVal(string(v))
	return cmd
}

func (f *fakeEmbedRedis) Set(_ context.Context, key string, value interface{}, _ time.Duration) *goredis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	cmd := goredis.NewStatusCmd(context.Background(), "set", key, value)
	switch v := value.(type) {
	case []byte:
		f.store[key] = append([]byte(nil), v...)
	case string:
		f.store[key] = []byte(v)
	}
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeEmbedRedis) setCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setCalls
}

func waitForEmbedCacheSet(f *fakeEmbedRedis, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f.setCallCount() >= want {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return f.setCallCount() >= want
}

func TestSuggestHandler_ValidationRejectsMalformedBodies(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, &fakeLLM{}, &fakeSuggestStore{})

	cases := []struct {
		name       string
		body       string
		wantDetail string
	}{
		{
			name: "empty raw prompt",
			body: `{
				"user_id":"` + userID.String() + `",
				"tenant_id":"` + tenantID.String() + `",
				"session_context":{"harness":"codex","model":"gpt-5","raw_prompt":""}
			}`,
			wantDetail: "session_context.raw_prompt",
		},
		{
			name: "unknown field",
			body: `{
				"user_id":"` + userID.String() + `",
				"tenant_id":"` + tenantID.String() + `",
				"session_context":{"harness":"codex","model":"gpt-5","raw_prompt":"ship it"},
				"surprise": true
			}`,
			wantDetail: "unknown field",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveSuggest(t, handler, tenantID, userID, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status: got %d want 400 body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["error"] != "validation" {
				t.Fatalf("error: got %v want validation", body["error"])
			}
			if !strings.Contains(rec.Body.String(), tc.wantDetail) {
				t.Fatalf("body %q missing detail %q", rec.Body.String(), tc.wantDetail)
			}
		})
	}
}

func TestSuggestHandler_NoEvidenceSuppresses(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	store := &fakeSuggestStore{search: suggestCandidateSearch{NeighborCount: 0}}
	llmFake := &fakeLLM{text: goodLLMJSON("better prompt", 0.9)}
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, llmFake, store)

	rec := serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeSuggestResponse(t, rec)
	if resp.Action != contracts.ActionSuppress {
		t.Fatalf("action: got %q", resp.Action)
	}
	if resp.NoSuggestionReason == nil || *resp.NoSuggestionReason != contracts.NoSuggestionNoEvidence {
		t.Fatalf("reason: got %#v", resp.NoSuggestionReason)
	}
	if llmFake.calls.Load() != 0 {
		t.Fatalf("LLM should not be called with no evidence")
	}
}

func TestSuggestHandler_LowConfidenceSuppressesBeforeLLM(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	store := &fakeSuggestStore{search: suggestCandidateSearch{
		NeighborCount: 1,
		Candidates: []suggestCandidate{{
			SessionID:      uuid.New(),
			Similarity:     1.0,
			CompositeScore: 0.4,
		}},
	}}
	llmFake := &fakeLLM{text: goodLLMJSON("better prompt", 0.9)}
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, llmFake, store)

	rec := serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	resp := decodeSuggestResponse(t, rec)
	if resp.NoSuggestionReason == nil || *resp.NoSuggestionReason != contracts.NoSuggestionLowConfidence {
		t.Fatalf("reason: got %#v", resp.NoSuggestionReason)
	}
	if llmFake.calls.Load() != 0 {
		t.Fatalf("LLM should not be called for low-confidence evidence")
	}
}

func TestSuggestHandler_LLMUnavailableSuppresses(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	store := highConfidenceStore()
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, &fakeLLM{err: llm.ErrAllProvidersUnavailable}, store)

	rec := serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	resp := decodeSuggestResponse(t, rec)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d", rec.Code)
	}
	if resp.NoSuggestionReason == nil || *resp.NoSuggestionReason != contracts.NoSuggestionLLMUnavailable {
		t.Fatalf("reason: got %#v", resp.NoSuggestionReason)
	}
}

func TestSuggestHandler_LLMUnparseableSuppresses(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, &fakeLLM{text: "not json"}, highConfidenceStore())

	rec := serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	resp := decodeSuggestResponse(t, rec)
	if resp.NoSuggestionReason == nil || *resp.NoSuggestionReason != contracts.NoSuggestionLLMUnparseable {
		t.Fatalf("reason: got %#v", resp.NoSuggestionReason)
	}
}

func TestSuggestHandler_DenylistSuppressesBeforePersistence(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	store := highConfidenceStore()
	logBuf := &strings.Builder{}
	logger := slog.New(slog.NewTextHandler(writerFunc(func(p []byte) (int, error) {
		return logBuf.Write(p)
	}), nil))
	handler := newSuggestHandler(logger, &fakeEmbedProvider{}, &fakeLLM{
		text: goodLLMJSON("run tests\nrm -rf /", 0.9),
	}, store)

	rec := serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	resp := decodeSuggestResponse(t, rec)
	if resp.Action != contracts.ActionSuppress {
		t.Fatalf("action: got %q", resp.Action)
	}
	if resp.RefinedPrompt != nil || resp.Rationale != nil {
		t.Fatalf("denylist response leaked prompt/rationale: %#v", resp)
	}
	if strings.Contains(rec.Body.String(), "rm -rf") {
		t.Fatalf("response leaked denied prompt: %s", rec.Body.String())
	}
	if store.persistCount() != 0 {
		t.Fatalf("denylist hit should not persist, got %d persists", store.persistCount())
	}
	if !strings.Contains(logBuf.String(), "denylist_hit") || !strings.Contains(logBuf.String(), "pattern_id") {
		t.Fatalf("denylist security event missing opaque pattern id: %s", logBuf.String())
	}
}

func TestSuggestHandler_PersistenceIsFireAndForget(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	store := highConfidenceStore()
	store.persistStarted = make(chan struct{})
	store.persistBlock = make(chan struct{})
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, &fakeLLM{
		text: goodLLMJSON("better prompt", 0.9),
	}, store)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	}()

	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("handler returned status %d", rec.Code)
		}
	case <-store.persistStarted:
		select {
		case rec := <-done:
			if rec.Code != http.StatusOK {
				t.Fatalf("handler returned status %d", rec.Code)
			}
		case <-time.After(50 * time.Millisecond):
			close(store.persistBlock)
			t.Fatal("handler waited for async persistence")
		}
	case <-time.After(100 * time.Millisecond):
		close(store.persistBlock)
		t.Fatal("handler did not return or start persistence")
	}
	close(store.persistBlock)
}

func TestSuggestHandler_EmbeddingCacheHitAndMissLatency(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	rdb := newFakeEmbedRedis()
	provider := &fakeEmbedProvider{name: "cachefake", vec: testVector(), delay: 25 * time.Millisecond}
	router := embed.NewRouter(embed.RouterConfig{
		Providers: []embed.Provider{provider},
		Priority:  []string{"cachefake"},
		Cache:     embed.NewCache(embed.CacheConfig{Redis: rdb}),
	})
	handler := newSuggestHandler(silentLogger(), router, nil, &fakeSuggestStore{
		search: suggestCandidateSearch{NeighborCount: 0},
	})
	body := validSuggestBody(tenantID, userID, "same prompt")

	start := time.Now()
	rec := serveSuggest(t, handler, tenantID, userID, body)
	miss := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("miss status: got %d", rec.Code)
	}
	if miss >= 200*time.Millisecond {
		t.Fatalf("cache miss path too slow: %s", miss)
	}
	if !waitForEmbedCacheSet(rdb, 1, 100*time.Millisecond) {
		t.Fatal("cache write did not complete")
	}

	start = time.Now()
	rec = serveSuggest(t, handler, tenantID, userID, body)
	hit := time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("hit status: got %d", rec.Code)
	}
	if hit >= 5*time.Millisecond {
		t.Fatalf("cache hit path too slow: %s", hit)
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("provider should only be called on miss, got %d", provider.calls.Load())
	}
}

func TestSuggestHandler_OrchestrationP99Under50ms(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, &fakeLLM{
		text: goodLLMJSON("better prompt", 0.9),
	}, highConfidenceStore())
	body := validSuggestBody(tenantID, userID, "original")

	durations := make([]time.Duration, 0, 100)
	for i := 0; i < 100; i++ {
		start := time.Now()
		rec := serveSuggest(t, handler, tenantID, userID, body)
		durations = append(durations, time.Since(start))
		if rec.Code != http.StatusOK {
			t.Fatalf("iteration %d status: got %d body=%s", i, rec.Code, rec.Body.String())
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	p99 := durations[98]
	if p99 >= 50*time.Millisecond {
		t.Fatalf("p99 orchestration latency too slow: %s (all=%v)", p99, durations)
	}
}

func TestSuggestHandler_PostgresDown503WithRetryAfter(t *testing.T) {
	tenantID := uuid.New()
	userID := uuid.New()
	handler := newSuggestHandler(silentLogger(), &fakeEmbedProvider{}, nil, &fakeSuggestStore{
		searchErr: errors.New("db down"),
	})

	rec := serveSuggest(t, handler, tenantID, userID, validSuggestBody(tenantID, userID, "original"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "5" {
		t.Fatalf("Retry-After: got %q want 5", got)
	}
}

func highConfidenceStore() *fakeSuggestStore {
	return &fakeSuggestStore{search: suggestCandidateSearch{
		NeighborCount: 1,
		Candidates: []suggestCandidate{{
			SessionID:      uuid.New(),
			Similarity:     1.0,
			CompositeScore: 0.9,
			ScoreRationale: "worked well",
		}},
	}}
}

func serveSuggest(t *testing.T, h http.Handler, tenantID, userID uuid.UUID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/suggest", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	principal := contracts.Principal{TenantID: tenantID, UserID: userID, TokenID: "jti-test", TokenType: "cli"}
	req = req.WithContext(contracts.WithPrincipal(req.Context(), principal))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func validSuggestBody(tenantID, userID uuid.UUID, rawPrompt string) string {
	return `{
		"user_id":"` + userID.String() + `",
		"tenant_id":"` + tenantID.String() + `",
		"session_context":{
			"harness":"codex",
			"model":"gpt-5",
			"effort":"high",
			"tools":["browser_use"],
			"repo_hash":"repo123",
			"git_branch":"main",
			"cwd_files":["go.mod"],
			"raw_prompt":` + mustJSON(rawPrompt) + `
		}
	}`
}

func goodLLMJSON(prompt string, confidence float64) string {
	return `{"refined_prompt":` + mustJSON(prompt) + `,"confidence":` + jsonFloat(confidence) + `,"rationale":"because evidence supports it"}`
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func jsonFloat(f float64) string {
	b, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func decodeSuggestResponse(t *testing.T, rec *httptest.ResponseRecorder) contracts.SuggestResponse {
	t.Helper()
	var resp contracts.SuggestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func testVector() []float32 {
	vec := make([]float32, repo.EmbeddingDim)
	vec[0] = 1
	return vec
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
