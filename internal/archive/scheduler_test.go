package archive

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestNewScheduler_AcceptsValidCron exercises only the construction +
// validation path. We do not let the cron fire in this test (it'd hit
// the real BatchDB / R2) — that lives in the integration test under
// the `integration` build tag.
func TestNewScheduler_AcceptsValidCron(t *testing.T) {
	cfg := SchedulerConfig{
		Spec:   "0 3 * * *",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cron: Config{
			BatchDB: &pgxpool.Pool{}, // non-nil; resolve() checks nil only
			Store:   &nopStore{},
			Bucket:  "b",
			Meter:   &nopMeter{},
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}
	s, err := NewScheduler(cfg)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	if s == nil {
		t.Fatal("NewScheduler returned nil scheduler")
	}
}

// TestNewScheduler_RejectsEmptySpec confirms construction is fail-closed
// — an empty spec must NOT default to a once-a-second tick or similar.
func TestNewScheduler_RejectsEmptySpec(t *testing.T) {
	_, err := NewScheduler(SchedulerConfig{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cron: Config{
			BatchDB: &pgxpool.Pool{},
			Store:   &nopStore{},
			Bucket:  "b",
			Meter:   &nopMeter{},
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	})
	if err == nil {
		t.Fatal("expected error for empty Spec, got nil")
	}
}

// TestNewScheduler_RejectsBadCronSpec — robfig parses crontab strings
// at construction time, so a typo never reaches runtime.
func TestNewScheduler_RejectsBadCronSpec(t *testing.T) {
	_, err := NewScheduler(SchedulerConfig{
		Spec:   "0 25 * * *", // hour 25 is invalid
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cron: Config{
			BatchDB: &pgxpool.Pool{},
			Store:   &nopStore{},
			Bucket:  "b",
			Meter:   &nopMeter{},
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	})
	if err == nil {
		t.Fatal("expected error for invalid crontab, got nil")
	}
}

// Minimal stubs for the construction-time tests above. They never run
// because the test does not call Start().
type nopStore struct{}

func (nopStore) PutObject(_ context.Context, _, _ string, _ []byte) error { return nil }
func (nopStore) GetObject(_ context.Context, _, _ string) ([]byte, error) {
	return nil, errors.New("nop")
}
func (nopStore) DeleteObject(_ context.Context, _, _ string) error { return nil }

type nopMeter struct{}

func (nopMeter) CurrentUsage(_ context.Context) (Usage, error) {
	return Usage{MeasuredAt: time.Now()}, nil
}
