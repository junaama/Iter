// Pool construction for the request-path (iter_app) and batch-path
// (iter_batch) Postgres connections.
//
// Why a thin wrapper rather than calling pgxpool.New directly:
//
//   - PgBouncer in transaction mode (DECISIONS.md Phase 2) hands a server
//     connection to a client only for the duration of a single transaction.
//     Server-side prepared statements created on one backend are invisible
//     to the next, so pgx's default QueryExecModeCacheStatement is unsafe.
//     We force QueryExecModeCacheDescribe at the connection layer so that
//     pgx only caches column-type descriptions (client-side, per pgx.Conn)
//     and uses unnamed extended-protocol queries that PgBouncer can route
//     transparently. This is the only mode that gives us both PgBouncer
//     compatibility and the speed of typed result decoding.
//
//   - Pool sizing is fixed to a small budget per process. The cluster
//     ceiling lives in PgBouncer (~50 server-side conns per DECISIONS.md
//     Phase 2); the per-process pool only needs enough conns to absorb
//     burst concurrency from chi handlers. Defaults below come from
//     issue 049's acceptance criteria.
//
//   - Slow-acquire logging is attached here so every call site (handlers,
//     WithTenant, WithBatch) inherits the same visibility into pool
//     starvation without rewriting plumbing. Metrics integration is
//     deferred to ARCHITECTURE.md §9 Step 7.

package db

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig carries every knob NewPool needs. Zero values fall through to
// the documented defaults; callers should set DSN explicitly (it has no
// sane default) but can leave the rest at zero for production behavior.
type PoolConfig struct {
	// DSN is the Postgres connection string. Required. For the request
	// path this is $DATABASE_URL (iter_app); for the batch path this is
	// $DATABASE_URL_BATCH (iter_batch). See deploy.md "Environment
	// variables" for the exact format.
	DSN string

	// MaxConns caps the per-process pool. Default: 25. Total cluster
	// connections = MaxConns * <process count>; keep this well below
	// the PgBouncer pool size (DECISIONS.md Phase 2: ~50 server-side).
	MaxConns int32

	// MinConns is the floor the health check tries to maintain. Default:
	// 2. Keeps a couple of warm connections to avoid first-request
	// latency after idle periods.
	MinConns int32

	// MaxConnLifetime is the age at which a connection is recycled even
	// if healthy. Default: 1h. Bounds the blast radius of upstream
	// changes (PgBouncer restarts, Postgres failover, role-grant
	// changes) so a stale conn is never kept indefinitely.
	MaxConnLifetime time.Duration

	// MaxConnIdleTime is the idle window before a connection is closed
	// by the health check. Default: 30m. Smaller than MaxConnLifetime
	// so an idle pool shrinks toward MinConns between traffic spikes.
	MaxConnIdleTime time.Duration

	// HealthCheckPeriod is how often the pool wakes to enforce the
	// lifetime / idle / min-conns invariants. Default: 1m.
	HealthCheckPeriod time.Duration

	// SlowAcquireThreshold logs (via Logger) every Acquire that takes
	// longer than this. Default: 50ms — long enough to ignore lock
	// jitter, short enough to surface real pool starvation. Set to
	// zero to disable.
	SlowAcquireThreshold time.Duration

	// Logger receives slow-acquire warnings. If nil, slog.Default() is
	// used. Always non-nil after NewPool returns.
	Logger *slog.Logger
}

// Default knob values; mirrored in PoolConfig field docs.
const (
	defaultMaxConns             = int32(25)
	defaultMinConns             = int32(2)
	defaultMaxConnLifetime      = time.Hour
	defaultMaxConnIdleTime      = 30 * time.Minute
	defaultHealthCheckPeriod    = time.Minute
	defaultSlowAcquireThreshold = 50 * time.Millisecond
)

// NewPool builds a *pgxpool.Pool ready for transaction-mode PgBouncer.
//
// Caller owns lifecycle: defer pool.Close() at the call site. Returns an
// error if the DSN is unparseable or the initial connection probe fails.
//
// The returned pool is configured with QueryExecModeCacheDescribe — see the
// package-level comment for why. Callers MUST NOT downgrade individual
// queries to QueryExecModeCacheStatement; doing so will break in
// transaction-mode PgBouncer.
func NewPool(ctx context.Context, cfg PoolConfig) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, errors.New("db.NewPool: DSN is required")
	}

	cfg = applyPoolDefaults(cfg)

	pgxCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("db.NewPool: parse DSN: %w", err)
	}

	// Transaction-mode PgBouncer compatibility: never use server-side
	// named prepared statements. cache_describe keeps the pgx-side
	// type-description cache (fast) while sending each query as an
	// unnamed extended-protocol round-trip (PgBouncer-safe).
	pgxCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

	pgxCfg.MaxConns = cfg.MaxConns
	pgxCfg.MinConns = cfg.MinConns
	pgxCfg.MaxConnLifetime = cfg.MaxConnLifetime
	pgxCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pgxCfg.HealthCheckPeriod = cfg.HealthCheckPeriod

	pool, err := pgxpool.NewWithConfig(ctx, pgxCfg)
	if err != nil {
		return nil, fmt.Errorf("db.NewPool: open pool: %w", err)
	}

	// Probe the connection synchronously so a bad DSN or unreachable
	// Postgres fails the boot, not the first request.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db.NewPool: ping: %w", err)
	}

	return pool, nil
}

// applyPoolDefaults fills in zero-valued fields without mutating the
// caller's PoolConfig — coding-style.md "immutability".
func applyPoolDefaults(cfg PoolConfig) PoolConfig {
	out := cfg
	if out.MaxConns == 0 {
		out.MaxConns = defaultMaxConns
	}
	if out.MinConns == 0 {
		out.MinConns = defaultMinConns
	}
	if out.MaxConnLifetime == 0 {
		out.MaxConnLifetime = defaultMaxConnLifetime
	}
	if out.MaxConnIdleTime == 0 {
		out.MaxConnIdleTime = defaultMaxConnIdleTime
	}
	if out.HealthCheckPeriod == 0 {
		out.HealthCheckPeriod = defaultHealthCheckPeriod
	}
	if out.SlowAcquireThreshold == 0 {
		out.SlowAcquireThreshold = defaultSlowAcquireThreshold
	}
	if out.Logger == nil {
		out.Logger = slog.Default()
	}
	return out
}

// Healthcheck runs `SELECT 1` against the pool and returns nil on success
// or the underlying pgx error otherwise. Intended for /health (issue 028's
// HealthHandler) so the endpoint can report "db: ok" / 200 vs "db: down"
// / 503 without duplicating the probe shape across handlers.
//
// Uses a short-circuit context: if the caller passes a request context,
// we honor it; otherwise the probe is bounded only by the pool's own
// acquire/ping deadlines. The handler is responsible for supplying a
// deadline appropriate to the /health latency budget.
func Healthcheck(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("db.Healthcheck: pool is nil")
	}
	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("db.Healthcheck: %w", err)
	}
	if one != 1 {
		return fmt.Errorf("db.Healthcheck: unexpected result %d", one)
	}
	return nil
}

// acquire is the central wrapper that times every pool checkout and emits
// a slow-acquire warning when the budget is blown. WithTenant and
// WithBatch funnel through this helper so the visibility hook is in one
// place. Callers MUST call conn.Release() (the conn embeds the release
// hook); this helper does not own its lifecycle.
func acquire(
	ctx context.Context,
	pool *pgxpool.Pool,
	logger *slog.Logger,
	slowThreshold time.Duration,
	purpose string,
) (*pgxpool.Conn, error) {
	start := time.Now()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if slowThreshold > 0 {
		if elapsed := time.Since(start); elapsed > slowThreshold {
			logger.Warn("slow pgx pool acquire",
				"purpose", purpose,
				"elapsed_ms", elapsed.Milliseconds(),
				"threshold_ms", slowThreshold.Milliseconds(),
				"pool_total", pool.Stat().TotalConns(),
				"pool_idle", pool.Stat().IdleConns(),
				"pool_acquired", pool.Stat().AcquiredConns(),
			)
		}
	}
	return conn, nil
}
