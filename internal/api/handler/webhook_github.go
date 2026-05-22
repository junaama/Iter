package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/api/webhook"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
)

// GitHub webhook handler — issue 041.
//
// Pipeline:
//   1. Shared webhook receiver reads the raw body, verifies
//      X-Hub-Signature-256, and dedups X-GitHub-Delivery.
//   2. Dispatch on X-GitHub-Event. Each branch maps to one of:
//        - outcomes.InsertOutcome (matched session)
//        - pending_outcomes.InsertPending (unmatched, buffered for the
//          late-match sweeper)
//   3. Always 200 on success. 5xx is reserved for genuine internal
//      failures GitHub should retry; soft-misses (no matching session)
//      are 200 + buffered.

// commitMarkerRE matches `Closes session: <uuid>` (case-insensitive) in
// a commit message. The marker convention is documented in
// DECISIONS.md "Commit-message session marker (issue 041)".
var commitMarkerRE = regexp.MustCompile(`(?i)closes\s+session:\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)

// revertTitleRE matches "Revert " at the start of a PR title.
var revertTitleRE = regexp.MustCompile(`(?i)^revert\b`)

// webhookSink abstracts the storage side-effects of the webhook handler.
// Production wires this to db.WithTenant + repo.InsertOutcome /
// repo.InsertPending; unit tests substitute an in-memory fake.
//
// We bundle the four touch-points (outcome insert, pending insert,
// session lookup by id, session lookup by repo+sha) behind a single
// interface so a test can construct one struct and pass it in instead
// of stubbing each closure individually.
type webhookSink interface {
	// InsertOutcome attempts to write an outcomes row for the given
	// tenant. Wrapped in db.WithTenant in production; the fake just
	// records the call. ErrAlreadyExists is the only non-fatal error;
	// the handler treats it as a no-op.
	InsertOutcome(ctx context.Context, tenantID uuid.UUID, o repo.Outcome) error

	// InsertPending buffers an unmatched event for the late-match
	// sweeper. Not tenant-scoped — pending_outcomes has no tenant_id.
	InsertPending(ctx context.Context, p repo.PendingOutcome) error

	// LookupBySessionID resolves a session_id parsed from a commit
	// message marker. Returns pgx.ErrNoRows when no match (or RLS
	// hides it).
	LookupBySessionID(ctx context.Context, id uuid.UUID) (repo.Session, error)

	// LookupByRepoCommit resolves a session by (repo_hash, commit_sha).
	// Returns pgx.ErrNoRows on miss.
	LookupByRepoCommit(ctx context.Context, repoHash, commitSHA string) (repo.Session, error)

	// FindOutcomeByTypeRef resolves an existing outcome by source URL.
	// Linear uses this to turn an issue's Done transition into an
	// incident_resolved audit event without mutating the historical
	// incident_caused outcome.
	FindOutcomeByTypeRef(ctx context.Context, outcomeType, externalRef string) (repo.Outcome, error)

	// InsertAudit writes a tenant-scoped audit_log row after the
	// webhook handler has resolved the tenant from a session/outcome.
	InsertAudit(ctx context.Context, tenantID uuid.UUID, entry webhookAuditEntry) error
}

type webhookAuditEntry struct {
	EventType  string
	TargetKind *string
	TargetID   *string
	Details    json.RawMessage
}

// liveSink is the production implementation of webhookSink. It binds
// to the request-path *pgxpool.Pool and uses db.WithTenant for tenant-
// scoped writes (outcomes) and a plain pool-level transaction for
// untenanted reads/writes (pending_outcomes and session lookups —
// the latter under SET LOCAL when the session's tenant is unknown).
//
// Note: session lookups in the webhook path happen BEFORE we know the
// tenant, so we can't use WithTenant. The request path intentionally
// does not use deps.BatchDB; production lookup attempts therefore run
// through the app pool with no SET LOCAL. RLS will hide every row in
// that state until the lookup moves to a deferred background job. The
// webhook still does the right thing — it buffers into
// pending_outcomes — so v1 doesn't lose webhook events; they just
// don't match in real time.
type liveSink struct {
	pool *pgxpool.Pool
}

func (s *liveSink) InsertOutcome(ctx context.Context, tenantID uuid.UUID, o repo.Outcome) error {
	if s.pool == nil {
		return errors.New("webhook: db pool not configured")
	}
	return db.WithTenant(ctx, s.pool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.InsertOutcome(ctx, tx, o)
		if err != nil && errors.Is(err, repo.ErrAlreadyExists) {
			return nil
		}
		return err
	})
}

func (s *liveSink) InsertPending(ctx context.Context, p repo.PendingOutcome) error {
	if s.pool == nil {
		return errors.New("webhook: db pool not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("webhook.pending begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := repo.InsertPending(ctx, tx, p); err != nil {
		if errors.Is(err, repo.ErrAlreadyExists) {
			return nil
		}
		return err
	}
	return tx.Commit(ctx)
}

func (s *liveSink) LookupBySessionID(ctx context.Context, id uuid.UUID) (repo.Session, error) {
	if s.pool == nil {
		return repo.Session{}, errors.New("webhook: db pool not configured")
	}
	// Best-effort tenant-agnostic lookup. RLS will hide the row from
	// the iter_app role; production callers can wire BatchDB later
	// when the late-match sweeper lands.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return repo.Session{}, fmt.Errorf("webhook.lookup begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return repo.FindByID(ctx, tx, id)
}

func (s *liveSink) LookupByRepoCommit(ctx context.Context, repoHash, commitSHA string) (repo.Session, error) {
	if s.pool == nil {
		return repo.Session{}, errors.New("webhook: db pool not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return repo.Session{}, fmt.Errorf("webhook.lookup begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return repo.FindByRepoCommit(ctx, tx, repoHash, commitSHA)
}

func (s *liveSink) FindOutcomeByTypeRef(ctx context.Context, outcomeType, externalRef string) (repo.Outcome, error) {
	if s.pool == nil {
		return repo.Outcome{}, errors.New("webhook: db pool not configured")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return repo.Outcome{}, fmt.Errorf("webhook.outcome_lookup begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out repo.Outcome
	err = tx.QueryRow(ctx, `
		SELECT id, session_id, tenant_id, outcome_type, external_ref, details, observed_at
		  FROM outcomes
		 WHERE outcome_type = $1
		   AND external_ref = $2
		 ORDER BY observed_at DESC
		 LIMIT 1
	`, outcomeType, externalRef).Scan(
		&out.ID, &out.SessionID, &out.TenantID, &out.OutcomeType,
		&out.ExternalRef, &out.Details, &out.ObservedAt,
	)
	if err != nil {
		return repo.Outcome{}, fmt.Errorf("webhook.outcome_lookup: %w", err)
	}
	return out, nil
}

func (s *liveSink) InsertAudit(ctx context.Context, tenantID uuid.UUID, entry webhookAuditEntry) error {
	if s.pool == nil {
		return errors.New("webhook: db pool not configured")
	}
	if len(entry.Details) == 0 {
		entry.Details = json.RawMessage(`{}`)
	}
	return db.WithTenant(ctx, s.pool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_log (
			  tenant_id, actor_user_id, actor_kind, event_type,
			  target_kind, target_id, details
			) VALUES ($1, NULL, 'system', $2, $3, $4, $5)
		`, tenantID, entry.EventType, entry.TargetKind, entry.TargetID, []byte(entry.Details))
		if err != nil {
			return fmt.Errorf("webhook.audit insert: %w", err)
		}
		return nil
	})
}

// GitHubWebhookHandler returns the HTTP handler mounted at
// POST /v1/webhooks/github. The handler is constructed with the full
// app.Deps and reads only the fields it needs at request time so a
// future deps refactor doesn't churn the signature.
func GitHubWebhookHandler(deps app.Deps) http.HandlerFunc {
	sink := &liveSink{pool: deps.DB}
	return githubWebhookHandler(deps.Logger, deps.Redis, deps.WebhookSecrets.GitHub, sink, time.Now)
}

// githubWebhookHandler is the testable core. Unit tests construct
// their own webhookSink fake + a fixed-time clock.
func githubWebhookHandler(
	logger *slog.Logger,
	rdb *goredis.Client,
	secret string,
	sink webhookSink,
	now func() time.Time,
) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}

	return webhook.Handler(webhook.Config{
		Source:          repo.PendingSourceGitHub,
		Secret:          secret,
		SignatureHeader: "X-Hub-Signature-256",
		SignaturePrefix: "sha256=",
		DeliveryHeader:  "X-GitHub-Delivery",
		EventName:       webhook.HeaderEvent("X-GitHub-Event"),
		Logger:          logger,
		Redis:           rdb,
		Now:             now,
		Routes: map[string]webhook.EventHandler{
			"pull_request": func(ctx context.Context, d webhook.Delivery) webhook.Response {
				return handlePullRequest(ctx, logger, sink, d.Body, d.DeliveryID)
			},
			"push": func(ctx context.Context, d webhook.Delivery) webhook.Response {
				return handlePush(ctx, logger, sink, d.Body, d.DeliveryID)
			},
			"check_run": func(ctx context.Context, d webhook.Delivery) webhook.Response {
				return handleCheckRun(ctx, logger, sink, d.Body, d.DeliveryID)
			},
			"ping": func(context.Context, webhook.Delivery) webhook.Response {
				return webhook.JSON(http.StatusOK, `{"status":"pong"}`)
			},
		},
	})
}

// ---------------------------------------------------------------------------
// Event handlers
// ---------------------------------------------------------------------------

type githubRepository struct {
	HTMLURL  string `json:"html_url"`
	FullName string `json:"full_name"`
}

type githubPullRequestEvent struct {
	Action      string            `json:"action"`
	PullRequest githubPullRequest `json:"pull_request"`
	Repository  githubRepository  `json:"repository"`
}

type githubPullRequest struct {
	HTMLURL        string        `json:"html_url"`
	Title          string        `json:"title"`
	Merged         bool          `json:"merged"`
	MergeCommitSHA string        `json:"merge_commit_sha"`
	Head           githubPRRef   `json:"head"`
	Labels         []githubLabel `json:"labels"`
}

type githubPRRef struct {
	SHA string `json:"sha"`
}

type githubLabel struct {
	Name string `json:"name"`
}

func handlePullRequest(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	body []byte,
	deliveryID string,
) webhook.Response {
	var ev githubPullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return webhook.JSON(http.StatusBadRequest, webhook.ErrMalformedBody)
	}

	// Only `closed` is interesting — open/edited/reviewed don't map to
	// outcomes at v1.
	if ev.Action != "closed" {
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}

	outcomeType := ""
	switch {
	case ev.PullRequest.Merged:
		outcomeType = repo.OutcomePRMerged
	case isRevertPR(ev.PullRequest):
		outcomeType = repo.OutcomePRReverted
	default:
		// PR closed without merge and not a revert: nothing to record.
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}

	// Match commit SHA. For merged PRs prefer merge_commit_sha (always
	// set on merge); fall back to head SHA otherwise.
	commitSHA := ev.PullRequest.MergeCommitSHA
	if commitSHA == "" {
		commitSHA = ev.PullRequest.Head.SHA
	}
	repoHash := hashRepoURL(ev.Repository.HTMLURL)
	externalRef := ev.PullRequest.HTMLURL

	insertOrBuffer(ctx, logger, sink, insertOrBufferParams{
		Source:      repo.PendingSourceGitHub,
		DeliveryID:  deliveryID,
		EventType:   "pull_request",
		OutcomeType: outcomeType,
		ExternalRef: &externalRef,
		RepoHash:    repoHash,
		CommitSHA:   commitSHA,
		RawBody:     body,
	})

	return webhook.JSON(http.StatusOK, `{"status":"ok"}`)
}

type githubPushEvent struct {
	Repository githubRepository `json:"repository"`
	Commits    []githubCommit   `json:"commits"`
}

type githubCommit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	URL     string `json:"url"`
}

func handlePush(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	body []byte,
	deliveryID string,
) webhook.Response {
	var ev githubPushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return webhook.JSON(http.StatusBadRequest, webhook.ErrMalformedBody)
	}

	processed := 0
	for _, c := range ev.Commits {
		sid, ok := parseSessionMarker(c.Message)
		if !ok {
			continue
		}
		ref := c.URL
		s, err := sink.LookupBySessionID(ctx, sid)
		if err != nil {
			// No matching session (RLS, never existed, or marker from
			// a different cloud). Buffer for the late-match sweeper.
			bufferPending(ctx, logger, sink, repo.PendingOutcome{
				Source:     repo.PendingSourceGitHub,
				DeliveryID: deliveryID + ":" + c.ID,
				EventType:  "push",
				Payload:    json.RawMessage(body),
			})
			continue
		}
		writeOutcome(ctx, logger, sink, s.TenantID, repo.Outcome{
			SessionID:   s.ID,
			TenantID:    s.TenantID,
			OutcomeType: repo.OutcomeCommitLanded,
			ExternalRef: &ref,
			Details:     json.RawMessage(jsonObjectOf("commit_sha", c.ID)),
		})
		processed++
	}

	return webhook.JSON(http.StatusOK, fmt.Sprintf(`{"status":"ok","processed":%d}`, processed))
}

type githubCheckRunEvent struct {
	Action     string           `json:"action"`
	CheckRun   githubCheckRun   `json:"check_run"`
	Repository githubRepository `json:"repository"`
}

type githubCheckRun struct {
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
	HTMLURL    string `json:"html_url"`
}

func handleCheckRun(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	body []byte,
	deliveryID string,
) webhook.Response {
	var ev githubCheckRunEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return webhook.JSON(http.StatusBadRequest, webhook.ErrMalformedBody)
	}

	outcomeType := ""
	switch ev.CheckRun.Conclusion {
	case "success":
		outcomeType = repo.OutcomeTestsPassed
	case "failure":
		outcomeType = repo.OutcomeTestsFailed
	default:
		// Other conclusions (neutral, cancelled, timed_out, action_required,
		// stale, skipped) don't map to outcomes at v1.
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}

	repoHash := hashRepoURL(ev.Repository.HTMLURL)
	externalRef := ev.CheckRun.HTMLURL

	insertOrBuffer(ctx, logger, sink, insertOrBufferParams{
		Source:      repo.PendingSourceGitHub,
		DeliveryID:  deliveryID,
		EventType:   "check_run",
		OutcomeType: outcomeType,
		ExternalRef: &externalRef,
		RepoHash:    repoHash,
		CommitSHA:   ev.CheckRun.HeadSHA,
		RawBody:     body,
	})

	return webhook.JSON(http.StatusOK, `{"status":"ok"}`)
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

type insertOrBufferParams struct {
	Source      string
	DeliveryID  string
	EventType   string
	OutcomeType string
	ExternalRef *string
	RepoHash    string
	CommitSHA   string
	RawBody     []byte
}

// insertOrBuffer attempts a (repo_hash, commit_sha) lookup; on hit
// inserts an outcome under the matched tenant; on miss buffers the
// raw event into pending_outcomes.
func insertOrBuffer(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	p insertOrBufferParams,
) {
	s, err := sink.LookupByRepoCommit(ctx, p.RepoHash, p.CommitSHA)
	if err != nil {
		bufferPending(ctx, logger, sink, repo.PendingOutcome{
			Source:     p.Source,
			DeliveryID: p.DeliveryID,
			EventType:  p.EventType,
			Payload:    json.RawMessage(p.RawBody),
		})
		return
	}
	writeOutcome(ctx, logger, sink, s.TenantID, repo.Outcome{
		SessionID:   s.ID,
		TenantID:    s.TenantID,
		OutcomeType: p.OutcomeType,
		ExternalRef: p.ExternalRef,
	})
}

func writeOutcome(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	tenantID uuid.UUID,
	o repo.Outcome,
) {
	if err := sink.InsertOutcome(ctx, tenantID, o); err != nil {
		logger.WarnContext(ctx, "webhook_outcome_insert_failed",
			"outcome_type", o.OutcomeType,
			"tenant_id", tenantID.String(),
			"err", err)
	}
}

func bufferPending(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	p repo.PendingOutcome,
) {
	if err := sink.InsertPending(ctx, p); err != nil {
		logger.WarnContext(ctx, "webhook_pending_insert_failed",
			"delivery_id", p.DeliveryID,
			"event_type", p.EventType,
			"err", err)
	}
}

// hashRepoURL returns the canonical sha256 of a repo URL, lower-cased
// and stripped of a trailing `.git` so users who configure either form
// land on the same hash. Formula in DECISIONS.md "repo_hash formula
// (issue 041)".
func hashRepoURL(url string) string {
	canonical := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(url)), ".git")
	h := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(h[:])
}

// parseSessionMarker extracts the session_id from a commit message
// matching the documented `Closes session: <uuid>` marker. Case-
// insensitive, allows arbitrary whitespace between tokens.
func parseSessionMarker(message string) (uuid.UUID, bool) {
	m := commitMarkerRE.FindStringSubmatch(message)
	if len(m) < 2 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(m[1])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// isRevertPR returns true if the PR has a label containing "revert"
// (case-insensitive) OR a title starting with "Revert ".
func isRevertPR(pr githubPullRequest) bool {
	if revertTitleRE.MatchString(pr.Title) {
		return true
	}
	for _, lbl := range pr.Labels {
		if strings.Contains(strings.ToLower(lbl.Name), "revert") {
			return true
		}
	}
	return false
}

// jsonObjectOf builds a one-key JSON object as a string. Tiny utility
// for trivial single-field details payloads.
func jsonObjectOf(key, value string) string {
	b, _ := json.Marshal(map[string]string{key: value})
	return string(b)
}
