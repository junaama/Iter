package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	defaultSessionsLimit = 25
	maxSessionsLimit     = 100
)

var errSessionsDBUnavailable = errors.New("sessions: db transaction unavailable")

type sessionSummaryLister interface {
	ListSessionSummaries(
		r *http.Request,
		filter repo.SessionSummaryFilter,
		limit int,
		cursorStartedAt time.Time,
		cursorID uuid.UUID,
	) ([]repo.SessionListRow, error)
}

type liveSessionSummaryLister struct{}

func (liveSessionSummaryLister) ListSessionSummaries(
	r *http.Request,
	filter repo.SessionSummaryFilter,
	limit int,
	cursorStartedAt time.Time,
	cursorID uuid.UUID,
) ([]repo.SessionListRow, error) {
	tx := db.FromContext(r.Context())
	if tx == nil {
		return nil, errSessionsDBUnavailable
	}
	return repo.ListSessionSummaries(r.Context(), tx, filter, limit, cursorStartedAt, cursorID)
}

// ListSessionsHandler returns the GET /v1/sessions handler.
func ListSessionsHandler(deps app.Deps) http.HandlerFunc {
	return listSessionsHandler(deps.Logger, liveSessionSummaryLister{})
}

func listSessionsHandler(logger *slog.Logger, lister sessionSummaryLister) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	if lister == nil {
		lister = liveSessionSummaryLister{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			writeAPIJSON(w, http.StatusUnauthorized, apiError{Error: "invalid_token"})
			return
		}

		filter, limit, cursorStartedAt, cursorID, qerr := parseListSessionsQuery(r.URL.Query(), principal)
		if qerr != nil {
			writeAPIJSON(w, qerr.status, qerr.body)
			return
		}

		rows, err := lister.ListSessionSummaries(r, filter, limit+1, cursorStartedAt, cursorID)
		if err != nil {
			status := http.StatusInternalServerError
			body := apiError{Error: "internal_error"}
			if errors.Is(err, errSessionsDBUnavailable) {
				status = http.StatusServiceUnavailable
				body = apiError{Error: "db_unavailable"}
			}
			logger.ErrorContext(r.Context(), "sessions_list_failed", "err", err)
			writeAPIJSON(w, status, body)
			return
		}

		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}

		sessions, err := mapSessionSummaries(rows)
		if err != nil {
			logger.ErrorContext(r.Context(), "sessions_list_map_failed", "err", err)
			writeAPIJSON(w, http.StatusInternalServerError, apiError{Error: "internal_error"})
			return
		}

		resp := contracts.ListSessionsResponse{Sessions: sessions}
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1].Session
			next, err := encodeSessionsCursor(last.StartedAt, last.ID)
			if err != nil {
				logger.ErrorContext(r.Context(), "sessions_cursor_encode_failed", "err", err)
				writeAPIJSON(w, http.StatusInternalServerError, apiError{Error: "internal_error"})
				return
			}
			resp.NextCursor = &next
		}
		writeAPIJSON(w, http.StatusOK, resp)
	}
}

type apiError struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
	See     string   `json:"see,omitempty"`
}

type queryError struct {
	status int
	body   apiError
}

func parseListSessionsQuery(values url.Values, principal contracts.Principal) (repo.SessionSummaryFilter, int, time.Time, uuid.UUID, *queryError) {
	admin := principalHasAdminRole(principal)
	filter := repo.SessionSummaryFilter{}
	limit := defaultSessionsLimit
	var details []string

	for name := range values {
		lower := strings.ToLower(name)
		if _, ok := nlSearchParams[lower]; ok {
			return filter, 0, time.Time{}, uuid.Nil, &queryError{
				status: http.StatusBadRequest,
				body: apiError{
					Error: "nl_search_not_supported",
					See:   "ARCHITECTURE.md#anti-screens",
				},
			}
		}
		if _, ok := allowedSessionsParams[lower]; !ok {
			details = append(details, fmt.Sprintf("unsupported query parameter %q", name))
		}
	}

	getOne := func(name string) (string, bool) {
		vals, ok := values[name]
		if !ok {
			return "", false
		}
		if len(vals) != 1 {
			details = append(details, fmt.Sprintf("%s must appear at most once", name))
			return "", false
		}
		if vals[0] == "" {
			details = append(details, fmt.Sprintf("%s must not be empty", name))
			return "", false
		}
		return vals[0], true
	}

	if raw, ok := getOne("limit"); ok {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			details = append(details, "limit must be a positive integer")
		} else if n > maxSessionsLimit {
			limit = maxSessionsLimit
		} else {
			limit = n
		}
	}

	if admin {
		if raw, ok := getOne("user_id"); ok {
			id, err := uuid.Parse(raw)
			if err != nil {
				details = append(details, "user_id must be a UUID")
			} else {
				filter.UserID = &id
			}
		}
	} else {
		userID := principal.UserID
		filter.UserID = &userID
	}

	if raw, ok := getOne("harness"); ok {
		if !contracts.ValidHarness(raw) {
			details = append(details, "harness must be one of claude_code, codex, pi, opencode, gemini_cli")
		} else {
			filter.Harness = &raw
		}
	}

	if raw, ok := getOne("started_after"); ok {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			details = append(details, "started_after must be an RFC3339 timestamp")
		} else {
			filter.StartedAfter = &t
		}
	}
	if raw, ok := getOne("started_before"); ok {
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			details = append(details, "started_before must be an RFC3339 timestamp")
		} else {
			filter.StartedBefore = &t
		}
	}
	if filter.StartedAfter != nil && filter.StartedBefore != nil &&
		!filter.StartedAfter.Before(*filter.StartedBefore) {
		details = append(details, "started_after must be before started_before")
	}

	if raw, ok := getOne("min_score"); ok {
		score, err := parseScoreParam("min_score", raw)
		if err != nil {
			details = append(details, err.Error())
		} else {
			filter.MinScore = &score
		}
	}
	if raw, ok := getOne("max_score"); ok {
		score, err := parseScoreParam("max_score", raw)
		if err != nil {
			details = append(details, err.Error())
		} else {
			filter.MaxScore = &score
		}
	}
	if filter.MinScore != nil && filter.MaxScore != nil && *filter.MinScore > *filter.MaxScore {
		details = append(details, "min_score must be less than or equal to max_score")
	}

	if raw, ok := getOne("has_outcome"); ok {
		if !contracts.ValidOutcomeType(raw) {
			details = append(details, "has_outcome must be a supported outcome type")
		} else {
			filter.HasOutcome = &raw
		}
	}

	if raw, ok := getOne("classification"); ok {
		if !contracts.ValidClassification(raw) {
			details = append(details, "classification must be one of clean, strippable, dirty")
		} else if raw == string(contracts.ClassificationDirty) && !admin {
			return filter, 0, time.Time{}, uuid.Nil, &queryError{
				status: http.StatusForbidden,
				body:   apiError{Error: "dirty_sessions_admin_only"},
			}
		} else {
			filter.Classification = &raw
		}
	} else if !admin {
		filter.ExcludeDirty = true
	}

	var cursorStartedAt time.Time
	var cursorID uuid.UUID
	if raw, ok := getOne("cursor"); ok {
		startedAt, id, err := decodeSessionsCursor(raw)
		if err != nil {
			details = append(details, "cursor is invalid")
		} else {
			cursorStartedAt = startedAt
			cursorID = id
		}
	}

	if len(details) > 0 {
		return filter, 0, time.Time{}, uuid.Nil, &queryError{
			status: http.StatusBadRequest,
			body: apiError{
				Error:   "invalid_query",
				Details: details,
			},
		}
	}

	return filter, limit, cursorStartedAt, cursorID, nil
}

var allowedSessionsParams = map[string]struct{}{
	"user_id":        {},
	"harness":        {},
	"started_after":  {},
	"started_before": {},
	"min_score":      {},
	"max_score":      {},
	"has_outcome":    {},
	"classification": {},
	"cursor":         {},
	"limit":          {},
}

var nlSearchParams = map[string]struct{}{
	"q":      {},
	"query":  {},
	"search": {},
}

func parseScoreParam(name, raw string) (float64, error) {
	score, err := strconv.ParseFloat(raw, 64)
	if err != nil || score < 0 || score > 1 {
		return 0, fmt.Errorf("%s must be a number between 0 and 1", name)
	}
	return score, nil
}

func principalHasAdminRole(principal contracts.Principal) bool {
	for _, role := range principal.Roles {
		switch strings.ToLower(role) {
		case repo.RoleOwner, repo.RoleAdmin, "tenant_owner", "tenant_admin":
			return true
		}
	}
	return false
}

func mapSessionSummaries(rows []repo.SessionListRow) ([]contracts.SessionSummary, error) {
	out := make([]contracts.SessionSummary, 0, len(rows))
	for _, row := range rows {
		s := row.Session
		tools := s.Tools
		if tools == nil {
			tools = []string{}
		}

		var effort *contracts.Effort
		if s.Effort != nil {
			v := contracts.Effort(*s.Effort)
			effort = &v
		}

		summary := contracts.SessionSummary{
			ID:              s.ID,
			UserID:          s.UserID,
			ParentSessionID: s.ParentSessionID,
			Harness:         contracts.Harness(s.Harness),
			Model:           s.Model,
			Effort:          effort,
			Tools:           tools,
			StartedAt:       s.StartedAt,
			EndedAt:         s.EndedAt,
			WallTimeMs:      s.WallTimeMs,
			TurnCount:       s.TurnCount,
			RedactedPrompt:  s.RedactedPrompt,
		}
		if row.LatestScore != nil {
			score, err := mapSessionScoreView(*row.LatestScore)
			if err != nil {
				return nil, err
			}
			summary.LatestScore = &score
		}
		out = append(out, summary)
	}
	return out, nil
}

func mapSessionScoreView(score repo.Score) (contracts.SessionScoreView, error) {
	rawSignals := score.Signals
	if len(rawSignals) == 0 {
		rawSignals = []byte(`{}`)
	}
	var signals contracts.ScoreSignals
	if err := json.Unmarshal(rawSignals, &signals); err != nil {
		return contracts.SessionScoreView{}, fmt.Errorf("unmarshal score signals: %w", err)
	}
	return contracts.SessionScoreView{
		SessionID:         score.SessionID,
		CompositeScore:    score.CompositeScore,
		Signals:           signals,
		ContributorWeight: score.ContributorWeight,
		ScoredAt:          score.ScoredAt,
		Rationale:         score.Rationale,
	}, nil
}

func writeAPIJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
