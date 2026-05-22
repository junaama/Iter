//go:build integration

package archive_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/archive"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
)

// stubStore is an in-memory ObjectStore. The PutObject result can be
// overridden per-key to simulate transient failures, and writes are
// recorded so tests can assert "uploaded N objects."
type stubStore struct {
	mu      sync.Mutex
	objects map[string][]byte // key => body
	fails   map[string]int    // key => remaining failures before success
}

func newStubStore() *stubStore {
	return &stubStore{
		objects: make(map[string][]byte),
		fails:   make(map[string]int),
	}
}

func (s *stubStore) PutObject(_ context.Context, bucket, key string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.fails[key]; ok && n > 0 {
		s.fails[key] = n - 1
		return fmt.Errorf("stub: transient failure for %s/%s", bucket, key)
	}
	cpy := make([]byte, len(body))
	copy(cpy, body)
	s.objects[bucket+"/"+key] = cpy
	return nil
}

func (s *stubStore) GetObject(_ context.Context, bucket, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.objects[bucket+"/"+key]; ok {
		return b, nil
	}
	return nil, errors.New("stub: not found")
}

func (s *stubStore) DeleteObject(_ context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, bucket+"/"+key)
	return nil
}

// stubMeter returns a fixed Usage. The test mutates the Usage field
// directly to push the cron above the threshold.
type stubMeter struct {
	mu sync.Mutex
	u  archive.Usage
}

func (m *stubMeter) CurrentUsage(_ context.Context) (archive.Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.u, nil
}

func (m *stubMeter) set(u archive.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.u = u
}

// seedTenancy inserts a tenant + user + membership directly via the
// superuser handle. Mirrors the helper in repo_test.go.
func seedTenancy(ctx context.Context, t *testing.T, tdb *dbtest.TestDB, label string) (string, string) {
	t.Helper()
	tenantID := tdb.SeedTenant(ctx, t, "arch-"+label)
	userID := tdb.SeedUser(ctx, t, "arch-"+label+"@example.com", "Arch-"+label)
	tdb.SeedMembership(ctx, t, tenantID, userID, "owner")
	return tenantID, userID
}

// silentLogger discards every log line so test output stays readable.
// The cron's structured logs are exhaustively asserted elsewhere — the
// integration tests care about visible state changes (DB rows, R2
// keys), not log shape.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fixedNow returns a clock that always reports the same instant. Lets
// us assert "cutoff is exactly t - retention" without flakiness.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestArchive_HappyPath(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	batch := tdb.NewBatchPool(ctx, t)
	tenantID, userID := seedTenancy(ctx, t, tdb, "happy")

	// Three sessions, all >1 day old. We use a 24h retention so the
	// test doesn't have to backdate by 90d in fixtures.
	old := time.Now().UTC().Add(-48 * time.Hour)
	wantIDs := []string{
		tdb.SeedSession(ctx, t, tenantID, userID, old),
		tdb.SeedSession(ctx, t, tenantID, userID, old.Add(time.Minute)),
		tdb.SeedSession(ctx, t, tenantID, userID, old.Add(2*time.Minute)),
	}
	// One session NEWER than the cutoff — must NOT be archived.
	fresh := tdb.SeedSession(ctx, t, tenantID, userID, time.Now().UTC())

	store := newStubStore()
	meter := &stubMeter{u: archive.Usage{StorageFrac: 0.1}}

	stats, err := archive.Run(ctx, archive.Config{
		BatchDB:       batch,
		Store:         store,
		Bucket:        "test-bucket",
		Meter:         meter,
		Retention:     24 * time.Hour,
		BatchSize:     100,
		UploadRetries: 1,
		UploadBackoff: time.Millisecond,
		Logger:        silentLogger(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Eligible != 3 || stats.Archived != 3 || stats.Failed != 0 {
		t.Fatalf("stats = %+v, want eligible=3 archived=3 failed=0", stats)
	}
	if len(store.objects) != 3 {
		t.Fatalf("uploaded %d objects, want 3", len(store.objects))
	}

	// Sessions row gone (cascade-deleted); pointer present.
	for _, id := range wantIDs {
		var exists bool
		if err := tdb.Super.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)", id,
		).Scan(&exists); err != nil {
			t.Fatalf("check sessions: %v", err)
		}
		if exists {
			t.Errorf("session %s still in sessions table after archive", id)
		}
		var pointerCount int
		if err := tdb.Super.QueryRowContext(ctx,
			"SELECT count(*) FROM archive_pointers WHERE session_id = $1", id,
		).Scan(&pointerCount); err != nil {
			t.Fatalf("check archive_pointers: %v", err)
		}
		if pointerCount != 1 {
			t.Errorf("archive_pointers for %s: count=%d, want 1", id, pointerCount)
		}
	}

	// Fresh session untouched.
	var freshExists bool
	if err := tdb.Super.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)", fresh,
	).Scan(&freshExists); err != nil {
		t.Fatalf("check fresh: %v", err)
	}
	if !freshExists {
		t.Fatal("fresh session was archived (cutoff violation)")
	}
}

func TestArchive_CascadeAfterDelete(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	batch := tdb.NewBatchPool(ctx, t)
	tenantID, userID := seedTenancy(ctx, t, tdb, "cascade")

	old := time.Now().UTC().Add(-48 * time.Hour)
	sessionID := tdb.SeedSession(ctx, t, tenantID, userID, old)

	// Seed one event under the session so cascade has something to
	// delete. session_events row count is the cascade signal.
	if _, err := tdb.Super.ExecContext(ctx, `
		INSERT INTO session_events (tenant_id, session_id, event_type, payload, occurred_at)
		VALUES ($1, $2, 'prompt_sent', '{}'::jsonb, now())
	`, tenantID, sessionID); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	tdb.SeedScore(ctx, t, tenantID, sessionID, "v1-test", 0.42, old)

	store := newStubStore()
	meter := &stubMeter{u: archive.Usage{StorageFrac: 0.1}}

	if _, err := archive.Run(ctx, archive.Config{
		BatchDB:   batch,
		Store:     store,
		Bucket:    "test-bucket",
		Meter:     meter,
		Retention: 24 * time.Hour,
		Logger:    silentLogger(),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Events and scores must be cascade-deleted by the sessions FK.
	for _, tbl := range []string{"session_events", "session_scores"} {
		var n int
		if err := tdb.Super.QueryRowContext(ctx,
			fmt.Sprintf("SELECT count(*) FROM %s WHERE session_id = $1", tbl), sessionID,
		).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		if n != 0 {
			t.Errorf("table %s still has %d rows for session %s (cascade failed)", tbl, n, sessionID)
		}
	}
}

func TestArchive_Idempotency(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	batch := tdb.NewBatchPool(ctx, t)
	tenantID, userID := seedTenancy(ctx, t, tdb, "idem")

	old := time.Now().UTC().Add(-48 * time.Hour)
	for i := 0; i < 3; i++ {
		tdb.SeedSession(ctx, t, tenantID, userID, old.Add(time.Duration(i)*time.Minute))
	}

	store := newStubStore()
	meter := &stubMeter{u: archive.Usage{StorageFrac: 0.1}}

	cfg := archive.Config{
		BatchDB:   batch,
		Store:     store,
		Bucket:    "test-bucket",
		Meter:     meter,
		Retention: 24 * time.Hour,
		Logger:    silentLogger(),
	}

	// First run archives all three.
	stats1, err := archive.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if stats1.Archived != 3 {
		t.Fatalf("first run Archived=%d, want 3", stats1.Archived)
	}

	// Second run finds nothing eligible.
	stats2, err := archive.Run(ctx, cfg)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if stats2.Eligible != 0 || stats2.Archived != 0 {
		t.Fatalf("second run stats = %+v, want eligible=0 archived=0", stats2)
	}
}

func TestArchive_PerSessionFailureSkips(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	batch := tdb.NewBatchPool(ctx, t)
	tenantID, userID := seedTenancy(ctx, t, tdb, "fail")

	old := time.Now().UTC().Add(-48 * time.Hour)
	idA := tdb.SeedSession(ctx, t, tenantID, userID, old.Add(0*time.Minute))
	idB := tdb.SeedSession(ctx, t, tenantID, userID, old.Add(1*time.Minute))
	idC := tdb.SeedSession(ctx, t, tenantID, userID, old.Add(2*time.Minute))

	// Pre-compute the failing object key for session B; bundle keys
	// are deterministic given (tenant, started_at month, session id).
	// We instead use a stub that fails every attempt for a specific
	// substring of the session id.
	badKey := fmt.Sprintf("%s/%s/%s.tar.zst",
		tenantID, old.Add(1*time.Minute).UTC().Format("2006-01"), idB)

	store := newStubStore()
	// Schedule 99 failures so all retries (max 3) are exhausted.
	store.fails[badKey] = 99
	meter := &stubMeter{u: archive.Usage{StorageFrac: 0.1}}

	stats, err := archive.Run(ctx, archive.Config{
		BatchDB:       batch,
		Store:         store,
		Bucket:        "test-bucket",
		Meter:         meter,
		Retention:     24 * time.Hour,
		UploadRetries: 3,
		UploadBackoff: time.Millisecond,
		Logger:        silentLogger(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.Eligible != 3 || stats.Archived != 2 || stats.Failed != 1 {
		t.Fatalf("stats = %+v, want eligible=3 archived=2 failed=1", stats)
	}

	// A + C archived (sessions row gone); B retained.
	expectGone := []string{idA, idC}
	expectKept := []string{idB}
	for _, id := range expectGone {
		var exists bool
		_ = tdb.Super.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)", id,
		).Scan(&exists)
		if exists {
			t.Errorf("expected session %s archived, still present", id)
		}
	}
	for _, id := range expectKept {
		var exists bool
		_ = tdb.Super.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)", id,
		).Scan(&exists)
		if !exists {
			t.Errorf("expected session %s retained (upload failed), got deleted", id)
		}
	}
}

func TestArchive_FreeTierGuardrail(t *testing.T) {
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	batch := tdb.NewBatchPool(ctx, t)
	tenantID, userID := seedTenancy(ctx, t, tdb, "guard")

	old := time.Now().UTC().Add(-48 * time.Hour)
	idA := tdb.SeedSession(ctx, t, tenantID, userID, old)

	store := newStubStore()
	// Push above 80% threshold.
	meter := &stubMeter{u: archive.Usage{StorageFrac: 0.95}}

	stats, err := archive.Run(ctx, archive.Config{
		BatchDB:        batch,
		Store:          store,
		Bucket:         "test-bucket",
		Meter:          meter,
		AlertThreshold: 0.80,
		Retention:      24 * time.Hour,
		Logger:         silentLogger(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stats.PausedFor == "" {
		t.Fatal("expected PausedFor set, was empty")
	}
	if stats.Archived != 0 {
		t.Errorf("Archived=%d, want 0 when paused", stats.Archived)
	}
	if len(store.objects) != 0 {
		t.Errorf("uploaded %d objects, want 0 when paused", len(store.objects))
	}
	// Session must remain in the DB.
	var exists bool
	_ = tdb.Super.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM sessions WHERE id = $1)", idA,
	).Scan(&exists)
	if !exists {
		t.Fatal("session deleted despite guardrail pause")
	}
}

func TestArchive_RLSStillEnforcedAfterArchive(t *testing.T) {
	// Verifies that even after archive_pointers are inserted, RLS
	// continues to scope them to the owning tenant. Belt-and-braces
	// because archive_pointers.tenant_id has no FK to tenants (per
	// DECISIONS.md Phase 3) but DOES have an RLS policy.
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	ctx := context.Background()

	batch := tdb.NewBatchPool(ctx, t)
	tenantA, userA := seedTenancy(ctx, t, tdb, "a")
	tenantB, _ := seedTenancy(ctx, t, tdb, "b")

	old := time.Now().UTC().Add(-48 * time.Hour)
	idA := tdb.SeedSession(ctx, t, tenantA, userA, old)

	store := newStubStore()
	meter := &stubMeter{u: archive.Usage{StorageFrac: 0.1}}
	if _, err := archive.Run(ctx, archive.Config{
		BatchDB:   batch,
		Store:     store,
		Bucket:    "test-bucket",
		Meter:     meter,
		Retention: 24 * time.Hour,
		Logger:    silentLogger(),
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Under tenantB's RLS context, the archive_pointers row for A
	// must be invisible.
	if err := db.WithTenant(ctx, tdb.AppPool, tenantB, func(ctx context.Context, tx pgx.Tx) error {
		var n int
		if err := tx.QueryRow(ctx,
			"SELECT count(*) FROM archive_pointers WHERE session_id = $1",
			uuid.MustParse(idA),
		).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			return fmt.Errorf("tenantB sees %d pointers for tenantA's session", n)
		}
		return nil
	}); err != nil {
		t.Fatalf("RLS check under tenantB: %v", err)
	}
}
