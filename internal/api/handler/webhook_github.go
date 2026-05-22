package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
)

// GitHub webhook handler — issue 041.
//
// Pipeline:
//   1. Read raw body (MUST be done before HMAC; we keep up to maxBody).
//   2. Verify X-Hub-Signature-256 against deps.WebhookSecrets.GitHub
//      with crypto/hmac.Equal (constant-time). Reject 401 on any
//      mismatch or missing header; the response body is generic
//      ("invalid_signature") so an attacker can't tell which check
//      tripped (ARCHITECTURE.md §7).
//   3. Idempotency by X-GitHub-Delivery in Redis (24h TTL). Replay
//      returns 200 + X-Idempotent-Replay: true.
//   4. Dispatch on X-GitHub-Event. Each branch maps to one of:
//        - outcomes.InsertOutcome (matched session)
//        - pending_outcomes.InsertPending (unmatched, buffered for the
//          late-match sweeper)
//   5. Always 200 on success. 5xx is reserved for genuine internal
//      failures GitHub should retry; soft-misses (no matching session)
//      are 200 + buffered.

// maxWebhookBody caps the body we'll read into memory. GitHub deliveries
// run ~5-15 KiB; 1 MiB is generous and matches the idempotency
// middleware's body cap.
const maxWebhookBody = 1 << 20

// webhookIdempotencyTTL is the window over which we dedup
// X-GitHub-Delivery values. GitHub redelivers within minutes; 24h gives
// us a comfortable margin without bloating Redis.
const webhookIdempotencyTTL = 24 * time.Hour

// errInvalidSignature is the canonical 401 response body. Generic on
// purpose — never leak which check failed.
const errInvalidSignature = `{"error":"invalid_signature"}`

// errMalformedBody is returned when the body isn't valid JSON for the
// event we expect. Only 400 we emit; everything else degrades to 200.
const errMalformedBody = `{"error":"malformed_body"}`

// errMissingDelivery is the 400 for a request without X-GitHub-Delivery.
// GitHub always sets it; absence means a misconfigured or hostile sender.
const errMissingDelivery = `{"error":"missing_delivery_id"}`

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
}

// liveSink is the production implementation of webhookSink. It binds
// to the request-path *pgxpool.Pool and uses db.WithTenant for tenant-
// scoped writes (outcomes) and a plain pool-level transaction for
// untenanted reads/writes (pending_outcomes and session lookups —
// the latter under SET LOCAL when the session's tenant is unknown).
//
// Note: session lookups in the webhook path happen BEFORE we know the
// tenant, so we can't use WithTenant. v1 cmd/server doesn't wire
// deps.BatchDB so the production binding tries the request pool with
// no SET LOCAL. RLS will hide every row in that state — so production
// session lookups in the webhook path always miss until either
// BatchDB is wired or the lookup is moved to a deferred background
// job (issue 042+). The webhook still does the right thing — it
// buffers into pending_outcomes — so v1 doesn't lose webhook events;
// they just don't match in real time.
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

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Step 1: read body. Hard-cap to maxWebhookBody so a hostile
		// sender can't OOM us. Read BEFORE auth so we can HMAC the
		// exact bytes.
		body, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody+1))
		if err != nil {
			logger.WarnContext(ctx, "webhook_read_failed", "err", err)
			writeWebhookJSON(w, http.StatusBadRequest, errMalformedBody)
			return
		}
		if len(body) > maxWebhookBody {
			logger.WarnContext(ctx, "webhook_body_too_large", "bytes", len(body))
			writeWebhookJSON(w, http.StatusRequestEntityTooLarge, `{"error":"body_too_large"}`)
			return
		}

		// Step 2: HMAC verify. A missing or malformed secret means the
		// handler is misconfigured — fail every delivery rather than
		// accept untrusted input. The check is constant-time.
		sigHeader := r.Header.Get("X-Hub-Signature-256")
		if !verifyGitHubSignature(secret, sigHeader, body) {
			logger.WarnContext(ctx, "webhook_signature_failed",
				"remote_addr", r.RemoteAddr,
				"event", r.Header.Get("X-GitHub-Event"),
				"delivery", r.Header.Get("X-GitHub-Delivery"))
			writeWebhookJSON(w, http.StatusUnauthorized, errInvalidSignature)
			return
		}

		// Step 3: delivery id is required after signature is verified.
		// (Order matters: signature failure is the first rejection so
		// the response shape gives nothing away.)
		deliveryID := r.Header.Get("X-GitHub-Delivery")
		if deliveryID == "" {
			writeWebhookJSON(w, http.StatusBadRequest, errMissingDelivery)
			return
		}

		// Step 4: idempotency via Redis SETNX. nil Redis fails open —
		// duplicates flow through but InsertOutcome's dedup keeps the
		// data clean. Same posture as the idempotency middleware.
		if rdb != nil {
			key := "webhook:github:delivery:" + deliveryID
			set, err := rdb.SetNX(ctx, key, now().UTC().Format(time.RFC3339Nano), webhookIdempotencyTTL).Result()
			if err != nil {
				logger.WarnContext(ctx, "webhook_idempotency_redis_failed", "err", err)
				// fail-open: continue
			} else if !set {
				// Hit: same delivery already processed. Respond 200
				// with a replay marker. We don't cache the body —
				// GitHub doesn't inspect it; status code + header is
				// what matters.
				w.Header().Set("X-Idempotent-Replay", "true")
				writeWebhookJSON(w, http.StatusOK, `{"status":"ok","replay":true}`)
				return
			}
		}

		// Step 5: dispatch.
		event := r.Header.Get("X-GitHub-Event")
		switch event {
		case "pull_request":
			handlePullRequest(ctx, logger, sink, body, deliveryID, w)
		case "push":
			handlePush(ctx, logger, sink, body, deliveryID, w)
		case "check_run":
			handleCheckRun(ctx, logger, sink, body, deliveryID, w)
		case "ping":
			// GitHub sends `ping` on webhook creation. Acknowledge.
			writeWebhookJSON(w, http.StatusOK, `{"status":"pong"}`)
		default:
			// Unsupported event: 200 so GitHub doesn't retry, but log.
			logger.InfoContext(ctx, "webhook_event_ignored", "event", event)
			writeWebhookJSON(w, http.StatusOK, `{"status":"ignored"}`)
		}
	}
}

// verifyGitHubSignature returns true iff sigHeader is a well-formed
// `sha256=<hex>` value whose HMAC-SHA256 of body keyed by secret
// matches in constant time.
//
// An empty secret OR an empty header is a fail. We never accept a
// blank signature on the theory that "no signature is also no
// signature" — GitHub always sets one when a secret is configured.
func verifyGitHubSignature(secret, sigHeader string, body []byte) bool {
	if secret == "" || sigHeader == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return false
	}
	sig, err := hex.DecodeString(sigHeader[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(sig, expected)
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
	w http.ResponseWriter,
) {
	var ev githubPullRequestEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeWebhookJSON(w, http.StatusBadRequest, errMalformedBody)
		return
	}

	// Only `closed` is interesting — open/edited/reviewed don't map to
	// outcomes at v1.
	if ev.Action != "closed" {
		writeWebhookJSON(w, http.StatusOK, `{"status":"ignored"}`)
		return
	}

	outcomeType := ""
	switch {
	case ev.PullRequest.Merged:
		outcomeType = repo.OutcomePRMerged
	case isRevertPR(ev.PullRequest):
		outcomeType = repo.OutcomePRReverted
	default:
		// PR closed without merge and not a revert: nothing to record.
		writeWebhookJSON(w, http.StatusOK, `{"status":"ignored"}`)
		return
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

	writeWebhookJSON(w, http.StatusOK, `{"status":"ok"}`)
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
	w http.ResponseWriter,
) {
	var ev githubPushEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeWebhookJSON(w, http.StatusBadRequest, errMalformedBody)
		return
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

	writeWebhookJSON(w, http.StatusOK, fmt.Sprintf(`{"status":"ok","processed":%d}`, processed))
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
	w http.ResponseWriter,
) {
	var ev githubCheckRunEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeWebhookJSON(w, http.StatusBadRequest, errMalformedBody)
		return
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
		writeWebhookJSON(w, http.StatusOK, `{"status":"ignored"}`)
		return
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

	writeWebhookJSON(w, http.StatusOK, `{"status":"ok"}`)
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

// writeWebhookJSON writes a JSON response with a fixed content-type.
// All webhook responses use this so the wire shape stays consistent.
func writeWebhookJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// jsonObjectOf builds a one-key JSON object as a string. Tiny utility
// for trivial single-field details payloads.
func jsonObjectOf(key, value string) string {
	b, _ := json.Marshal(map[string]string{key: value})
	return string(b)
}
