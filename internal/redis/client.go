package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Defaults for Iter's Redis use. Short timeouts because the suggest path
// has a ≤1s P99 budget and Redis is on the critical read path; pool size
// is conservative because v1 sizing targets ~5K engineers (see
// ARCHITECTURE.md §8 capacity math) and PgBouncer-style fan-out happens
// at Postgres, not Redis.
const (
	DefaultDialTimeout  = 2 * time.Second
	DefaultReadTimeout  = 1 * time.Second
	DefaultWriteTimeout = 1 * time.Second
	DefaultPoolSize     = 10
	DefaultMinIdleConns = 2
	DefaultMaxRetries   = 3
)

// Config is the explicit, structured form of a Redis connection. Callers
// either build it by hand (tests) or via ConfigFromURL (production).
//
// Zero-valued duration / int fields fall back to the package Default* on
// NewClient — callers do not need to repeat the defaults.
type Config struct {
	// Addr is host:port. Required.
	Addr string
	// Username + Password are optional. Username is the Redis 6 ACL user
	// name; empty string means the legacy "default" user.
	Username string
	Password string
	// DB is the logical database index. Zero (the default Redis DB) is
	// fine for v1; we don't multi-tenant via DB index, that's RLS's job.
	DB int

	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	PoolSize     int
	MinIdleConns int
	MaxRetries   int
}

// ConfigFromURL parses a redis:// or rediss:// URL into a Config with
// Iter's defaults applied. Returns an error for malformed input or
// unsupported schemes — the cmd/server wiring should treat that as fatal
// at boot, not a runtime soft-fail.
func ConfigFromURL(raw string) (Config, error) {
	if raw == "" {
		return Config{}, errors.New("redis: empty url")
	}
	opts, err := goredis.ParseURL(raw)
	if err != nil {
		return Config{}, fmt.Errorf("redis: parse url: %w", err)
	}
	return Config{
		Addr:     opts.Addr,
		Username: opts.Username,
		Password: opts.Password,
		DB:       opts.DB,
	}, nil
}

// NewClient constructs a go-redis client with Iter defaults filled in and
// verifies reachability with a PING bounded by the dial timeout. Callers
// own Close() (defer client.Close() in main, etc.).
//
// Returning the concrete *goredis.Client (rather than an interface) is a
// deliberate trade-off: the streams + DLQ helpers in this package take
// the concrete type so they can use go-redis's typed XREADGROUP /
// XPENDING return shapes without a hand-maintained interface seam.
// Tests use testcontainers + a real Redis (see streams_test.go).
func NewClient(ctx context.Context, cfg Config) (*goredis.Client, error) {
	if cfg.Addr == "" {
		return nil, errors.New("redis: Addr is required")
	}

	opts := &goredis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  durationOr(cfg.DialTimeout, DefaultDialTimeout),
		ReadTimeout:  durationOr(cfg.ReadTimeout, DefaultReadTimeout),
		WriteTimeout: durationOr(cfg.WriteTimeout, DefaultWriteTimeout),
		PoolSize:     intOr(cfg.PoolSize, DefaultPoolSize),
		MinIdleConns: intOr(cfg.MinIdleConns, DefaultMinIdleConns),
		MaxRetries:   intOr(cfg.MaxRetries, DefaultMaxRetries),
	}

	client := goredis.NewClient(opts)

	// PING bounded by DialTimeout so a wedged Redis at boot fails fast
	// rather than blocking SIGTERM draining. The deadline is independent
	// of the caller's ctx so an already-expired ctx still produces a
	// clear "redis: ping failed" rather than a context.DeadlineExceeded.
	pingCtx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis: ping failed: %w", err)
	}
	return client, nil
}

func durationOr(v, fallback time.Duration) time.Duration {
	if v <= 0 {
		return fallback
	}
	return v
}

func intOr(v, fallback int) int {
	if v <= 0 {
		return fallback
	}
	return v
}
