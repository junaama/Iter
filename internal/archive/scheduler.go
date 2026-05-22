package archive

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"
)

// timeUTCLocation centralizes the "use UTC" decision so cron entries
// can never accidentally pick the server's local timezone. Wrapped in a
// function so the package init order is irrelevant.
func timeUTCLocation() *time.Location { return time.UTC }

// SchedulerConfig wires a robfig/cron scheduler that fires Run on a
// fixed crontab. The default crontab is `0 3 * * *` (03:00 UTC daily)
// per ARCHITECTURE.md §4. Tests inject `@every 1s` to assert the
// scheduler actually invokes Run.
//
// TIMEZONE: the scheduler is constructed with `cron.New(cron.WithLocation(time.UTC))`
// so the crontab is interpreted in UTC. This matters: Modal's nightly
// scorer is also UTC (02:00 UTC, per `modal/scoring.py`), and the
// archive runs an hour later so the day's last scored sessions get one
// pass through the new score table before they're eligible for archive.
type SchedulerConfig struct {
	// CronConfig is the per-run knob set. Required.
	Cron Config

	// Spec is the crontab. Required. The default at cmd/server is
	// "0 3 * * *". @every-style strings work for tests.
	Spec string

	// Logger is the structured logger for scheduler-level events
	// (job-scheduled, job-completed, job-errored). Independent of
	// Cron.Logger so the scheduler can prefix its own keys.
	Logger *slog.Logger
}

// Scheduler is the lightweight wrapper around robfig/cron. Start() spins
// up the goroutine; Stop() drains in-flight jobs. The zero value is
// invalid — use NewScheduler.
type Scheduler struct {
	cron   *cron.Cron
	cfg    SchedulerConfig
	logger *slog.Logger
}

// NewScheduler builds a Scheduler from cfg without starting it. Register
// is implicit (one job per scheduler — the archive cron); future
// in-process cron jobs would either get their own Scheduler or this
// would grow an Add(spec, job) method.
//
// CHOICE: github.com/robfig/cron/v3 over a hand-rolled time.Ticker:
// robfig parses real crontab syntax (`0 3 * * *`), supports DST handling
// via WithLocation, and ships a structured EntryID for runtime
// inspection. A hand-rolled ticker would either reimplement crontab
// parsing (a non-trivial subset of POSIX cron's spec) or require the
// caller to compute "next 03:00 UTC" themselves on every tick — too
// much surface for a single-job v1.
func NewScheduler(cfg SchedulerConfig) (*Scheduler, error) {
	if cfg.Spec == "" {
		return nil, errors.New("archive.Scheduler: Spec required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("archive.Scheduler: Logger required")
	}
	if _, err := cfg.Cron.resolve(); err != nil {
		return nil, fmt.Errorf("archive.Scheduler: invalid Cron config: %w", err)
	}

	// UTC location keeps the crontab interpretation aligned with the
	// project's "every timestamp is UTC" invariant.
	c := cron.New(cron.WithLocation(timeUTCLocation()))

	s := &Scheduler{cron: c, cfg: cfg, logger: cfg.Logger}

	if _, err := c.AddFunc(cfg.Spec, s.runOnce); err != nil {
		return nil, fmt.Errorf("archive.Scheduler: AddFunc: %w", err)
	}
	return s, nil
}

// Start spins up the cron goroutine. Non-blocking; callers should call
// Stop on shutdown to drain in-flight jobs.
func (s *Scheduler) Start() {
	s.logger.Info("archive.scheduler.start", "spec", s.cfg.Spec)
	s.cron.Start()
}

// Stop signals the cron to stop scheduling new ticks and returns once
// any currently-running tick completes. Bounded by the context the
// caller wires in via the cron job (Run respects ctx.Done()).
func (s *Scheduler) Stop() {
	stopCtx := s.cron.Stop()
	<-stopCtx.Done()
	s.logger.Info("archive.scheduler.stopped")
}

// runOnce is what the cron entry calls. We construct a fresh context
// per tick because cron entries are detached from any HTTP request —
// production wires a process-lifetime context; tests pass a deadline'd
// context to bound the run.
func (s *Scheduler) runOnce() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stats, err := Run(ctx, s.cfg.Cron)
	if err != nil {
		s.logger.Error("archive.scheduler.run_failed", "err", err)
		return
	}
	s.logger.Info("archive.scheduler.run_completed",
		"eligible", stats.Eligible,
		"archived", stats.Archived,
		"failed", stats.Failed,
		"paused_for", stats.PausedFor,
		"started", stats.Started,
		"finished", stats.Finished,
	)
}
