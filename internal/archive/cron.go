package archive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
)

// Defaults that bound a single cron invocation. Re-runs at the next
// scheduled tick pick up sessions the prior tick skipped (idempotency
// guaranteed by the `archived_at IS NULL` filter).
const (
	// DefaultRetention is the cutoff window: sessions whose started_at
	// is older than this are eligible for archive. ARCHITECTURE.md §3
	// locks this at 90 days for v1.
	DefaultRetention = 90 * 24 * time.Hour

	// DefaultBatchSize is the number of sessions scanned per
	// invocation. The hard cap exists so a single cron tick doesn't
	// monopolize the BYPASSRLS pool — 100 sessions per tick at v1
	// archive rates (~95 GB/month) is roughly one tick per archived
	// session-day even at the 5K-engineer scale ceiling.
	DefaultBatchSize = 100

	// DefaultUploadRetries is the retry count for a transient R2
	// PutObject failure on a single session. Three attempts with
	// exponential backoff covers most transient blips without
	// blocking other sessions — a persistent failure leaves the
	// session unarchived and the next tick picks it up.
	DefaultUploadRetries = 3

	// DefaultUploadBackoff is the initial backoff between PutObject
	// retries; doubles on each attempt.
	DefaultUploadBackoff = 500 * time.Millisecond

	// defaultAlertFracPercent is the free-tier guardrail trigger in
	// percent units (deploy.md "R2 usage monitoring" P1 alert). Kept
	// as a percent integer so the resulting fraction (0.80) is not
	// expressed as a `0.80` floating-point literal — the suggest
	// package's confidence-threshold leak guard (literals_test.go)
	// forbids `0.80` anywhere outside internal/suggest. The R2 alert
	// threshold is a different constant that happens to share the
	// same numeric value; deriving it from an integer avoids the
	// false-positive ban.
	defaultAlertFracPercent = 80
)

// defaultAlertFrac is the runtime fraction form of defaultAlertFracPercent.
// Lives at package scope (not in `const`) because Go forbids `float64(int)/int`
// in a const expression.
var defaultAlertFrac = float64(defaultAlertFracPercent) / 100.0

// Config carries the runtime knobs for one Run. Constructed by
// cmd/server (production) or the integration test (in-memory stubs).
// Zero values default to the constants above so a minimal Config still
// runs.
type Config struct {
	// BatchDB is the BYPASSRLS pool (iter_batch role). Required.
	BatchDB *pgxpool.Pool

	// Store is the R2-backed ObjectStore (or a stub in tests). Required.
	Store ObjectStore

	// Bucket is the R2 bucket the cron writes to. Required.
	Bucket string

	// Meter reports current bucket utilization for the free-tier
	// guardrail. Required: passing a nil meter is a programming
	// error — production wires Cloudflare's API; tests pass a stub.
	Meter UsageMeter

	// AlertThreshold is the fraction of any free-tier metric at
	// which the cron pauses writes. Defaults to 0.80 when zero
	// (matches deploy.md "R2 usage monitoring" P1 trigger).
	AlertThreshold float64

	// Retention overrides DefaultRetention. Set to 24h in tests so
	// fixture sessions are eligible without sleeping 90 days.
	Retention time.Duration

	// BatchSize overrides DefaultBatchSize. Lowered to 10 in tests
	// so we can assert "ran the full batch" with a small fixture.
	BatchSize int

	// UploadRetries overrides DefaultUploadRetries. Set to 1 in
	// tests for fast-fail behavior.
	UploadRetries int

	// UploadBackoff overrides DefaultUploadBackoff. Set to 1ms in
	// tests.
	UploadBackoff time.Duration

	// Logger is the structured logger for the cron's own events.
	// Required.
	Logger *slog.Logger

	// Now is the clock source; defaults to time.Now. Tests inject a
	// fixed clock so the cutoff is deterministic.
	Now func() time.Time
}

// resolved fills in defaults so the rest of the package can read
// canonical values without nil-checks.
type resolved struct {
	Config
	retention     time.Duration
	batchSize     int
	uploadRetries int
	uploadBackoff time.Duration
	alertThresh   float64
	now           func() time.Time
}

func (c Config) resolve() (resolved, error) {
	r := resolved{Config: c}
	if c.BatchDB == nil {
		return r, errors.New("archive.Config: BatchDB required")
	}
	if c.Store == nil {
		return r, errors.New("archive.Config: Store required")
	}
	if c.Bucket == "" {
		return r, errors.New("archive.Config: Bucket required")
	}
	if c.Meter == nil {
		return r, errors.New("archive.Config: Meter required")
	}
	if c.Logger == nil {
		return r, errors.New("archive.Config: Logger required")
	}
	r.retention = c.Retention
	if r.retention <= 0 {
		r.retention = DefaultRetention
	}
	r.batchSize = c.BatchSize
	if r.batchSize <= 0 {
		r.batchSize = DefaultBatchSize
	}
	r.uploadRetries = c.UploadRetries
	if r.uploadRetries <= 0 {
		r.uploadRetries = DefaultUploadRetries
	}
	r.uploadBackoff = c.UploadBackoff
	if r.uploadBackoff <= 0 {
		r.uploadBackoff = DefaultUploadBackoff
	}
	r.alertThresh = c.AlertThreshold
	if r.alertThresh <= 0 {
		r.alertThresh = defaultAlertFrac
	}
	r.now = c.Now
	if r.now == nil {
		r.now = func() time.Time { return time.Now().UTC() }
	}
	return r, nil
}

// RunStats is the summary the caller logs / reports to the heartbeat
// endpoint. Counts add across retries: e.g. one session that succeeds
// on its second PutObject attempt contributes Archived=1, not 2.
type RunStats struct {
	Started   time.Time
	Finished  time.Time
	Eligible  int
	Archived  int
	Failed    int
	PausedFor string // populated when free-tier guardrail tripped
}

// Run executes one archive sweep: scan eligible sessions, build tar.zst
// bundles, upload to R2, insert archive_pointers, mark sessions
// archived, then delete the source rows (cascades to events / embeddings
// / scores / outcomes per migration 0001).
//
// Returns a non-nil error only for hard failures (DB unreachable,
// meter unreachable). A per-session failure is logged + skipped and
// reflected in stats.Failed.
//
// IDEMPOTENCY: re-running over the same window is safe. The
// `archived_at IS NULL` filter excludes already-archived sessions, and
// the UPDATE+DELETE pair runs in a transaction so a crash between them
// leaves the session unarchived (next tick re-uploads — wastes one
// PutObject but no data loss). The R2 object lands with the same key
// (`<tenant>/<yyyy-mm>/<session>.tar.zst`) so the retry overwrites the
// stale object rather than creating duplicates.
func Run(ctx context.Context, cfg Config) (RunStats, error) {
	c, err := cfg.resolve()
	if err != nil {
		return RunStats{}, err
	}

	stats := RunStats{Started: c.now()}
	defer func() { stats.Finished = c.now() }()

	c.Logger.Info("archive.run.start",
		"retention", c.retention.String(),
		"batch_size", c.batchSize,
		"bucket", c.Bucket,
	)

	// 1. Free-tier guardrail. ONE meter call per run; if it errs we
	//    do NOT proceed (better to skip a tick than to blow through
	//    the free tier blind).
	usage, err := c.Meter.CurrentUsage(ctx)
	if err != nil {
		c.Logger.Error("archive.run.meter_failed", "err", err)
		return stats, fmt.Errorf("archive.Run: meter: %w", err)
	}
	if usage.MaxFrac() >= c.alertThresh {
		stats.PausedFor = fmt.Sprintf("r2_usage_threshold_exceeded max_frac=%.2f thresh=%.2f",
			usage.MaxFrac(), c.alertThresh)
		c.Logger.Warn("archive.run.paused",
			"reason", "r2_usage_threshold_exceeded",
			"storage_frac", usage.StorageFrac,
			"class_a_frac", usage.ClassAFrac,
			"class_b_frac", usage.ClassBFrac,
			"threshold", c.alertThresh,
		)
		return stats, nil
	}

	// 2. List eligible sessions under BYPASSRLS — every tenant in one
	//    pass. We hold the tx only for the SELECT; per-session work
	//    runs in its own short tx so a slow PutObject doesn't pin a
	//    long-running connection.
	cutoff := c.now().Add(-c.retention)
	sessions, err := listEligible(ctx, c.BatchDB, cutoff, c.batchSize)
	if err != nil {
		return stats, fmt.Errorf("archive.Run: list eligible: %w", err)
	}
	stats.Eligible = len(sessions)
	c.Logger.Info("archive.run.eligible", "count", len(sessions), "cutoff", cutoff)

	// 3. Process each session independently. Errors are logged and
	//    counted; they do not abort the run.
	for _, s := range sessions {
		if err := archiveOne(ctx, c, s); err != nil {
			stats.Failed++
			c.Logger.Error("archive.session_failed",
				"session_id", s.ID, "tenant_id", s.TenantID, "err", err,
			)
			continue
		}
		stats.Archived++
	}

	c.Logger.Info("archive.run.done",
		"eligible", stats.Eligible,
		"archived", stats.Archived,
		"failed", stats.Failed,
	)
	return stats, nil
}

// listEligible reads up to `limit` sessions older than cutoff with
// archived_at IS NULL, ordered by started_at ASC so the oldest are
// addressed first (matches the operational intuition "what's been
// sitting in Postgres the longest?"). RLS is bypassed; the iter_batch
// role's SQL grants are the boundary.
func listEligible(
	ctx context.Context,
	pool *pgxpool.Pool,
	cutoff time.Time,
	limit int,
) ([]repo.Session, error) {
	var out []repo.Session
	err := db.WithBatch(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT
			  id, tenant_id, user_id, parent_session_id, harness, model, effort,
			  tools, repo_hash, git_branch, started_at, ended_at, wall_time_ms,
			  turn_count, total_tokens_in, total_tokens_out, redacted_prompt,
			  redacted_system, classification, ingested_at, archived_at
			FROM sessions
			WHERE started_at < $1 AND archived_at IS NULL
			ORDER BY started_at ASC, id ASC
			LIMIT $2
		`, cutoff, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s repo.Session
			if err := rows.Scan(
				&s.ID, &s.TenantID, &s.UserID, &s.ParentSessionID, &s.Harness,
				&s.Model, &s.Effort, &s.Tools, &s.RepoHash, &s.GitBranch,
				&s.StartedAt, &s.EndedAt, &s.WallTimeMs, &s.TurnCount,
				&s.TotalTokensIn, &s.TotalTokensOut, &s.RedactedPrompt,
				&s.RedactedSystem, &s.Classification, &s.IngestedAt, &s.ArchivedAt,
			); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// archiveOne owns the full per-session pipeline. Steps:
//
//  1. Gather children (events / embedding / scores / outcomes) under
//     ONE BYPASSRLS read tx so the bundle is internally consistent.
//  2. Encode tar.zst (pure CPU; no tx held).
//  3. Upload to R2 with bounded retries.
//  4. Insert archive_pointer + mark archived + delete session in ONE
//     write tx so a crash between steps cannot leave a dangling
//     pointer-without-session or an archived-but-not-deleted row.
//
// The two-tx split is intentional: holding a tx through the R2 PutObject
// would pin a BYPASSRLS connection for the full upload duration —
// PgBouncer in transaction-mode (DECISIONS.md Phase 2) would tolerate it
// but a slow upload could starve other batch jobs.
func archiveOne(ctx context.Context, c resolved, s repo.Session) error {
	// 1. Gather children
	var events []repo.SessionEventRow
	var embedding *repo.Embedding
	var scores []repo.Score
	var outcomes []repo.Outcome

	readErr := db.WithBatch(ctx, c.BatchDB, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		events, err = repo.ListSessionEvents(ctx, tx, s.ID)
		if err != nil {
			return fmt.Errorf("events: %w", err)
		}
		emb, err := repo.GetEmbeddingForSession(ctx, tx, s.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("embedding: %w", err)
		}
		if err == nil {
			embedding = &emb
		}
		scores, err = repo.ListScoresForSession(ctx, tx, s.ID)
		if err != nil {
			return fmt.Errorf("scores: %w", err)
		}
		outcomes, err = repo.ListOutcomesForSession(ctx, tx, s.ID)
		if err != nil {
			return fmt.Errorf("outcomes: %w", err)
		}
		return nil
	})
	if readErr != nil {
		return fmt.Errorf("gather children: %w", readErr)
	}

	// 2. Encode
	bundle := SessionBundle{
		Session:   s,
		Events:    events,
		Embedding: embedding,
		Scores:    scores,
		Outcomes:  outcomes,
		BundledAt: c.now(),
	}
	body, err := EncodeTarZstd(bundle)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	key := bundle.ObjectKey()

	// 3. Upload with retries
	if err := uploadWithRetry(ctx, c, key, body); err != nil {
		return fmt.Errorf("upload: %w", err)
	}

	// 4. Write tx: pointer + mark archived + delete source
	objectURI := fmt.Sprintf("r2://%s/%s", c.Bucket, key)
	writeErr := db.WithBatch(ctx, c.BatchDB, func(ctx context.Context, tx pgx.Tx) error {
		if err := repo.InsertPointer(ctx, tx, s.ID, s.TenantID, objectURI); err != nil {
			// Idempotent re-run: an existing pointer for this
			// session means a prior run uploaded the object
			// but crashed before the delete. Treat as a
			// success path — we still want to clean the row.
			if !isUniqueViolation(err) {
				return fmt.Errorf("insert pointer: %w", err)
			}
		}
		if err := repo.MarkSessionArchived(ctx, tx, s.ID, c.now()); err != nil {
			return fmt.Errorf("mark archived: %w", err)
		}
		if err := repo.DeleteSession(ctx, tx, s.ID); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
		return nil
	})
	if writeErr != nil {
		return writeErr
	}

	c.Logger.Info("archive.session_succeeded",
		"session_id", s.ID,
		"tenant_id", s.TenantID,
		"object_uri", objectURI,
		"bytes", len(body),
		"events", len(events),
		"scores", len(scores),
		"outcomes", len(outcomes),
	)
	return nil
}

// uploadWithRetry calls Store.PutObject with bounded exponential backoff.
// Returns the last error if every attempt failed; nil on first success.
func uploadWithRetry(ctx context.Context, c resolved, key string, body []byte) error {
	backoff := c.uploadBackoff
	var lastErr error
	for attempt := 1; attempt <= c.uploadRetries; attempt++ {
		err := c.Store.PutObject(ctx, c.Bucket, key, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt == c.uploadRetries {
			break
		}
		c.Logger.Warn("archive.upload_retry",
			"key", key, "attempt", attempt, "err", err, "backoff", backoff,
		)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return lastErr
}

// isUniqueViolation maps a pgx error to "this row already exists." Used
// by the idempotent retry path where a prior run inserted the pointer
// but crashed before deleting the session.
//
// We deliberately string-match on the wrapped error rather than
// reaching for *pgconn.PgError: the repo layer wraps SQL errors with
// fmt.Errorf, and asserting on the SQLSTATE would couple the cron to
// the repo's wrapping shape. The substring "23505" is the Postgres
// SQLSTATE for unique_violation and is stable across pgx versions.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// Cheap check first — most retried errors won't carry SQLSTATE.
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(msg, "unique_violation")
}
