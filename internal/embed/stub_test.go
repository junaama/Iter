package embed

import (
	"context"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// stubProvider is a deterministic in-process Provider for tests. NOT
// exported because real call sites have no reason to use it; the
// embedding worker and suggest path always go through Router.
type stubProvider struct {
	name     string
	respond  func(EmbedRequest) (EmbedResponse, error)
	failWith error

	mu    sync.Mutex
	calls int
}

func (s *stubProvider) Name() string { return s.name }

func (s *stubProvider) Embed(_ context.Context, req EmbedRequest) (EmbedResponse, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.failWith != nil {
		return EmbedResponse{}, s.failWith
	}
	if s.respond != nil {
		return s.respond(req)
	}
	// Default: one zero vector per input.
	out := make([][]float32, len(req.Inputs))
	for i := range out {
		out[i] = []float32{1, 2, 3}
	}
	return EmbedResponse{Vectors: out}, nil
}

func (s *stubProvider) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// fakeRedis is a minimal in-process implementation of the redisClient
// surface. Used by cache tests; supports an injected error for the
// "cache write failure does not block response" test.
type fakeRedis struct {
	mu       sync.Mutex
	store    map[string][]byte
	getErr   error
	setErr   error
	setCalls int
}

func newFakeRedis() *fakeRedis {
	return &fakeRedis{store: make(map[string][]byte)}
}

func (f *fakeRedis) Get(_ context.Context, key string) *goredis.StringCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	cmd := goredis.NewStringCmd(context.Background(), "get", key)
	if f.getErr != nil {
		cmd.SetErr(f.getErr)
		return cmd
	}
	v, ok := f.store[key]
	if !ok {
		cmd.SetErr(goredis.Nil)
		return cmd
	}
	cmd.SetVal(string(v))
	return cmd
}

func (f *fakeRedis) Set(_ context.Context, key string, value interface{}, _ time.Duration) *goredis.StatusCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setCalls++
	cmd := goredis.NewStatusCmd(context.Background(), "set", key, value)
	if f.setErr != nil {
		cmd.SetErr(f.setErr)
		return cmd
	}
	switch v := value.(type) {
	case []byte:
		f.store[key] = append([]byte(nil), v...)
	case string:
		f.store[key] = []byte(v)
	}
	cmd.SetVal("OK")
	return cmd
}

func (f *fakeRedis) setCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.setCalls
}

// waitForSetCalls polls the fake until it has observed at least n Set
// invocations, or the deadline elapses. Cache writes are fire-and-forget
// in a goroutine; tests use this rather than sleep.
func waitForSetCalls(f *fakeRedis, n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if f.setCallCount() >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return f.setCallCount() >= n
}
