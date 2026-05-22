package embed

import (
	"context"
	"errors"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := [][]float32{
		{},
		{0},
		{1, -1, 3.14, -2.71, 0.0001, 1e9},
		make([]float32, 1536), // realistic 1536-dim vector (all zeros)
	}
	for i, vec := range cases {
		buf, err := encodeVector(vec)
		if err != nil {
			t.Fatalf("case %d: encode: %v", i, err)
		}
		// Length check: 4-byte prefix + 4 bytes per float.
		if want := 4 + 4*len(vec); len(buf) != want {
			t.Errorf("case %d: encoded len %d, want %d", i, len(buf), want)
		}
		got, err := decodeVector(buf)
		if err != nil {
			t.Fatalf("case %d: decode: %v", i, err)
		}
		if len(got) != len(vec) {
			t.Fatalf("case %d: decoded len %d, want %d", i, len(got), len(vec))
		}
		for j := range got {
			if got[j] != vec[j] {
				t.Errorf("case %d: element %d: got %v, want %v", i, j, got[j], vec[j])
			}
		}
	}
}

func TestDecodeRejectsTruncated(t *testing.T) {
	vec := []float32{1, 2, 3}
	buf, err := encodeVector(vec)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := decodeVector(buf[:len(buf)-1]); err == nil {
		t.Error("truncated buffer should fail to decode")
	}
	if _, err := decodeVector(buf[:2]); err == nil {
		t.Error("too-short buffer should fail to decode")
	}
}

func TestCacheKeyStability(t *testing.T) {
	k1 := cacheKey("voyage-code-3", "hello")
	k2 := cacheKey("voyage-code-3", "hello")
	if k1 != k2 {
		t.Errorf("same (model,input) must produce same key; got %q vs %q", k1, k2)
	}
	if cacheKey("voyage-code-3", "hello") == cacheKey("openai-3", "hello") {
		t.Error("different models should produce different keys")
	}
	// Separator: ensures "ab"+"cd" ≠ "abc"+"d".
	if cacheKey("ab", "cd") == cacheKey("abc", "d") {
		t.Error("model+input boundary collision; separator missing")
	}
}

func TestCacheGetSetRoundTrip(t *testing.T) {
	rdb := newFakeRedis()
	c := NewCache(CacheConfig{Redis: rdb})
	if c == nil {
		t.Fatal("NewCache returned nil with non-nil redis")
	}

	if _, ok := c.Get(context.Background(), "m", "in"); ok {
		t.Error("empty cache should miss")
	}

	c.Set(context.Background(), "m", "in", []float32{1, 2, 3})
	if !waitForSetCalls(rdb, 1, 500*time.Millisecond) {
		t.Fatal("cache Set goroutine did not fire")
	}

	vec, ok := c.Get(context.Background(), "m", "in")
	if !ok {
		t.Fatal("cache should hit after set")
	}
	if len(vec) != 3 || vec[0] != 1 || vec[1] != 2 || vec[2] != 3 {
		t.Errorf("got vector %v, want [1 2 3]", vec)
	}
}

func TestCacheNilSafe(t *testing.T) {
	// NewCache returns nil when Redis is nil; nil-receiver calls are
	// safe and behave as cache-disabled.
	c := NewCache(CacheConfig{Redis: nil})
	if c != nil {
		t.Fatal("NewCache with nil redis must return nil *Cache")
	}
	if _, ok := c.Get(context.Background(), "m", "in"); ok {
		t.Error("nil cache must miss")
	}
	// Set on nil receiver must not panic.
	c.Set(context.Background(), "m", "in", []float32{1})
}

func TestCacheGetErrorIsTreatedAsMiss(t *testing.T) {
	rdb := newFakeRedis()
	rdb.getErr = errors.New("redis exploded")
	c := NewCache(CacheConfig{Redis: rdb})
	if _, ok := c.Get(context.Background(), "m", "in"); ok {
		t.Error("redis error should be reported as cache miss, not propagated")
	}
}

func TestCacheTTLDefault(t *testing.T) {
	rdb := newFakeRedis()
	c := NewCache(CacheConfig{Redis: rdb}) // TTL zero → default
	if c.ttl != DefaultCacheTTL {
		t.Errorf("ttl = %v, want %v", c.ttl, DefaultCacheTTL)
	}
}

// Ensure the fakeRedis seam matches the interface the real client
// satisfies — a compile-time check that the seam stays narrow.
var _ RedisLike = (*fakeRedis)(nil)
var _ RedisLike = (*goredis.Client)(nil)
