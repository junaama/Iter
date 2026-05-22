package embed

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// DefaultCacheTTL is the cache lifetime applied when CacheConfig.TTL is
// zero. 7 days is a deliberate trade-off documented in DECISIONS.md:
//   - Long enough to absorb repeat embeddings of the same prompt across a
//     normal engineering work-rhythm (a developer iterates on the same
//     task for days, the suggest path hits the same input embeddings on
//     each retry).
//   - Short enough that a Voyage model upgrade flushes naturally within
//     a week (we'd also bump the cache key prefix, but TTL is the safety
//     net).
const DefaultCacheTTL = 7 * 24 * time.Hour

// cacheKeyPrefix isolates embedding cache keys from any other use of the
// shared Redis database. Bump this string when the vector encoding
// changes — that's a backward-incompatible cache wire change.
const cacheKeyPrefix = "embed:cache:"

// RedisLike is the slim surface this package consumes from
// *goredis.Client. Exported so cmd/server can pass either a real
// *goredis.Client (which satisfies it directly) or a fake; defining
// the seam keeps the cache testable with a fake (cache-write-failure
// test) without forcing callers to import go-redis just to construct a
// CacheConfig.
type RedisLike interface {
	Get(ctx context.Context, key string) *goredis.StringCmd
	Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *goredis.StatusCmd
}

// Cache is the SHA256-keyed embedding cache. It is provider-agnostic:
// the cache key is sha256(model + "\x00" + input), so two providers
// returning vectors for the same model+input share a cache slot. (In
// practice each provider has its own native model id, so cross-provider
// collisions don't happen — but keying on model rather than provider name
// keeps the cache useful if a provider exposes the same model under a
// different name.)
//
// Cache failures are intentionally non-fatal:
//   - Get errors return (nil, nil, false) — treat as miss.
//   - Set is fire-and-forget on a goroutine, so a wedged Redis never
//     blocks the embed response. The DECISIONS.md entry calls this out
//     because the suggest path has a ≤1s P99 budget and a slow Redis
//     SET on the critical path is exactly the kind of latency surprise
//     that would violate it.
type Cache struct {
	rdb RedisLike
	ttl time.Duration
}

// CacheConfig configures the cache. A nil Redis client returns a nil
// *Cache from NewCache so the router can run cache-disabled in dev
// without nil-checks scattered through the call path.
type CacheConfig struct {
	Redis RedisLike
	TTL   time.Duration
}

// NewCache builds a cache. Returns nil if cfg.Redis is nil — callers
// invoke cache.Get / cache.Set on a nil receiver and get cache-miss /
// no-op behavior, which is the right default for cache-disabled.
func NewCache(cfg CacheConfig) *Cache {
	if cfg.Redis == nil {
		return nil
	}
	if cfg.TTL <= 0 {
		cfg.TTL = DefaultCacheTTL
	}
	return &Cache{rdb: cfg.Redis, ttl: cfg.TTL}
}

// cacheKey computes the deterministic cache key for a (model, input) pair.
// Lowercase hex SHA256 over the literal bytes — collision-resistant and
// stable across processes / Redis restarts.
func cacheKey(model, input string) string {
	h := sha256.New()
	h.Write([]byte(model))
	h.Write([]byte{0}) // separator so "ab"+"cd" ≠ "abc"+"d"
	h.Write([]byte(input))
	return cacheKeyPrefix + hex.EncodeToString(h.Sum(nil))
}

// Get returns the cached vector for (model, input). Returns (nil, false)
// on miss, decode error, or any Redis error — the caller treats both as
// "compute it." We deliberately do NOT propagate Redis errors here: a
// degraded cache must never fail the embedding path.
func (c *Cache) Get(ctx context.Context, model, input string) ([]float32, bool) {
	if c == nil {
		return nil, false
	}
	raw, err := c.rdb.Get(ctx, cacheKey(model, input)).Bytes()
	if err != nil {
		// goredis.Nil = miss; any other error is also treated as miss.
		return nil, false
	}
	vec, err := decodeVector(raw)
	if err != nil {
		return nil, false
	}
	return vec, true
}

// Set writes the vector to the cache. Fire-and-forget on a goroutine so
// the caller is never blocked by a slow Redis SET; the goroutine's ctx
// is detached from the request ctx (with the request's deadline still
// honored, capped at TTL) so a request cancellation doesn't drop the
// cache write that the next request might benefit from.
func (c *Cache) Set(parent context.Context, model, input string, vector []float32) {
	if c == nil {
		return
	}
	// Encode synchronously so a malformed vector surfaces in the caller's
	// stack trace if it ever happens. Encoding is cheap (6 KiB for 1536
	// floats) and runs after the response is already returned to the
	// user via the goroutine launch below.
	buf, err := encodeVector(vector)
	if err != nil {
		return
	}
	key := cacheKey(model, input)
	go func() {
		// Detach from the request context; bound by a small absolute
		// timeout so a Redis hang can't leak goroutines.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 2*time.Second)
		defer cancel()
		_ = c.rdb.Set(ctx, key, buf, c.ttl).Err()
	}()
}

// encodeVector serializes a []float32 as little-endian binary. 1536
// floats × 4 bytes = 6 KiB on the wire — about 10× more compact than
// JSON and 2× more compact than gob, with zero allocations on decode
// beyond the result slice.
//
// Format: a 4-byte little-endian uint32 length prefix (vector dim)
// followed by len*4 bytes of little-endian float32. The prefix lets the
// decoder reject corrupted entries (truncated payloads) without trusting
// the byte slice length alone, and it's a forward-compatibility hook if
// we ever want to embed multi-vector entries.
func encodeVector(vec []float32) ([]byte, error) {
	if len(vec) > int(^uint32(0)) {
		return nil, errors.New("embed: vector too large")
	}
	buf := bytes.NewBuffer(make([]byte, 0, 4+len(vec)*4))
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(vec))); err != nil {
		return nil, fmt.Errorf("embed: encode len: %w", err)
	}
	if err := binary.Write(buf, binary.LittleEndian, vec); err != nil {
		return nil, fmt.Errorf("embed: encode vec: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeVector is the inverse of encodeVector. Strict on length so a
// corrupt cache entry never produces a vector of wrong dimension.
func decodeVector(raw []byte) ([]float32, error) {
	if len(raw) < 4 {
		return nil, errors.New("embed: cache entry too short")
	}
	r := bytes.NewReader(raw)
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, fmt.Errorf("embed: decode len: %w", err)
	}
	expected := int(n)*4 + 4
	if len(raw) != expected {
		return nil, fmt.Errorf("embed: cache entry length mismatch: got %d, want %d", len(raw), expected)
	}
	out := make([]float32, int(n))
	if err := binary.Read(r, binary.LittleEndian, &out); err != nil {
		return nil, fmt.Errorf("embed: decode vec: %w", err)
	}
	return out, nil
}
