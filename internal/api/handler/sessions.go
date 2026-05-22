package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/api/authz"
	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	defaultSessionsLimit = 25
	maxSessionsLimit     = 100
)

// ListSessionsHandler returns the GET /v1/sessions handler.
func ListSessionsHandler(deps app.Deps) http.HandlerFunc {
	return listSessionsHandler(deps.Logger)
}

func listSessionsHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "invalid_token"})
			return
		}

		admin, err := authz.IsAdmin(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "sessions_admin_lookup_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}

		filter, limit, cursorStartedAt, cursorID, qerr := parseListSessionsQuery(r.URL.Query(), principal, admin)
		if qerr != nil {
			respond.JSON(w, qerr.status, qerr.body)
			return
		}

		tx, err := db.RequireTx(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "sessions_missing_tenant_tx", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}

		rows, err := repo.ListSessionSummaries(r.Context(), tx, filter, limit+1, cursorStartedAt, cursorID)
		if err != nil {
			logger.ErrorContext(r.Context(), "sessions_list_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}

		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}

		sessions, err := mapSessionSummaries(rows)
		if err != nil {
			logger.ErrorContext(r.Context(), "sessions_list_map_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
			return
		}

		resp := contracts.ListSessionsResponse{Sessions: sessions}
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1].Session
			next, err := encodeSessionsCursor(last.StartedAt, last.ID)
			if err != nil {
				logger.ErrorContext(r.Context(), "sessions_cursor_encode_failed", "err", err)
				respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
				return
			}
			resp.NextCursor = &next
		}
		respond.JSON(w, http.StatusOK, resp)
	}
}

type queryError struct {
	status int
	body   respond.Error
}

func parseListSessionsQuery(
	values url.Values,
	principal contracts.Principal,
	admin bool,
) (repo.SessionSummaryFilter, int, time.Time, uuid.UUID, *queryError) {
	rawParams, details, qerr := collectListSessionsParams(values)
	if qerr != nil {
		return repo.SessionSummaryFilter{}, 0, time.Time{}, uuid.Nil, qerr
	}

	state := listSessionsQueryState{
		principal: principal,
		admin:     admin,
		limit:     defaultSessionsLimit,
		details:   details,
	}
	for _, spec := range listSessionsParamSpecs {
		raw, ok := rawParams[spec.name]
		if !ok {
			continue
		}
		spec.parse(&state, raw)
		if state.qerr != nil {
			return state.filter, 0, time.Time{}, uuid.Nil, state.qerr
		}
	}
	state.validateRanges()
	state.applyRoleDefaults()

	if len(state.details) > 0 {
		return state.filter, 0, time.Time{}, uuid.Nil, &queryError{
			status: http.StatusBadRequest,
			body: respond.Error{
				Error:   "invalid_query",
				Details: state.details,
			},
		}
	}

	return state.filter, state.limit, state.cursorStartedAt, state.cursorID, nil
}

type listSessionsQueryState struct {
	principal       contracts.Principal
	admin           bool
	filter          repo.SessionSummaryFilter
	limit           int
	cursorStartedAt time.Time
	cursorID        uuid.UUID
	details         []string
	qerr            *queryError
}

func (s *listSessionsQueryState) validateRanges() {
	if s.filter.StartedAfter != nil && s.filter.StartedBefore != nil &&
		!s.filter.StartedAfter.Before(*s.filter.StartedBefore) {
		s.details = append(s.details, "started_after must be before started_before")
	}
	if s.filter.MinScore != nil && s.filter.MaxScore != nil && *s.filter.MinScore > *s.filter.MaxScore {
		s.details = append(s.details, "min_score must be less than or equal to max_score")
	}
}

func (s *listSessionsQueryState) applyRoleDefaults() {
	if s.admin {
		return
	}
	userID := s.principal.UserID
	s.filter.UserID = &userID
	if s.filter.Classification == nil {
		s.filter.ExcludeDirty = true
	}
}

type listSessionsParamSpec struct {
	name  string
	parse func(*listSessionsQueryState, string)
}

var listSessionsParamSpecs = []listSessionsParamSpec{
	{
		name: "limit",
		parse: func(s *listSessionsQueryState, raw string) {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				s.details = append(s.details, "limit must be a positive integer")
			} else if n > maxSessionsLimit {
				s.limit = maxSessionsLimit
			} else {
				s.limit = n
			}
		},
	},
	{
		name: "user_id",
		parse: func(s *listSessionsQueryState, raw string) {
			if !s.admin {
				return
			}
			id, err := uuid.Parse(raw)
			if err != nil {
				s.details = append(s.details, "user_id must be a UUID")
				return
			}
			s.filter.UserID = &id
		},
	},
	{
		name: "harness",
		parse: func(s *listSessionsQueryState, raw string) {
			if !contracts.ValidHarness(raw) {
				s.details = append(s.details, "harness must be one of claude_code, codex, pi, opencode, gemini_cli")
				return
			}
			s.filter.Harness = &raw
		},
	},
	{
		name: "started_after",
		parse: func(s *listSessionsQueryState, raw string) {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				s.details = append(s.details, "started_after must be an RFC3339 timestamp")
				return
			}
			s.filter.StartedAfter = &t
		},
	},
	{
		name: "started_before",
		parse: func(s *listSessionsQueryState, raw string) {
			t, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				s.details = append(s.details, "started_before must be an RFC3339 timestamp")
				return
			}
			s.filter.StartedBefore = &t
		},
	},
	{
		name: "min_score",
		parse: func(s *listSessionsQueryState, raw string) {
			score, err := parseScoreParam("min_score", raw)
			if err != nil {
				s.details = append(s.details, err.Error())
				return
			}
			s.filter.MinScore = &score
		},
	},
	{
		name: "max_score",
		parse: func(s *listSessionsQueryState, raw string) {
			score, err := parseScoreParam("max_score", raw)
			if err != nil {
				s.details = append(s.details, err.Error())
				return
			}
			s.filter.MaxScore = &score
		},
	},
	{
		name: "has_outcome",
		parse: func(s *listSessionsQueryState, raw string) {
			if !contracts.ValidOutcomeType(raw) {
				s.details = append(s.details, "has_outcome must be a supported outcome type")
				return
			}
			s.filter.HasOutcome = &raw
		},
	},
	{
		name: "classification",
		parse: func(s *listSessionsQueryState, raw string) {
			if !contracts.ValidClassification(raw) {
				s.details = append(s.details, "classification must be one of clean, strippable, dirty")
				return
			}
			if raw == string(contracts.ClassificationDirty) && !s.admin {
				s.qerr = &queryError{
					status: http.StatusForbidden,
					body:   respond.Error{Error: "dirty_sessions_admin_only"},
				}
				return
			}
			s.filter.Classification = &raw
		},
	},
	{
		name: "cursor",
		parse: func(s *listSessionsQueryState, raw string) {
			startedAt, id, err := decodeSessionsCursor(raw)
			if err != nil {
				s.details = append(s.details, "cursor is invalid")
				return
			}
			s.cursorStartedAt = startedAt
			s.cursorID = id
		},
	},
}

func collectListSessionsParams(values url.Values) (map[string]string, []string, *queryError) {
	rawParams := make(map[string]string, len(values))
	var details []string
	for name, vals := range values {
		lower := strings.ToLower(name)
		if _, ok := nlSearchParams[lower]; ok {
			return nil, nil, &queryError{
				status: http.StatusBadRequest,
				body: respond.Error{
					Error: "nl_search_not_supported",
					See:   "ARCHITECTURE.md#anti-screens",
				},
			}
		}
		if _, ok := allowedSessionsParams[lower]; !ok {
			details = append(details, fmt.Sprintf("unsupported query parameter %q", name))
			continue
		}
		if len(vals) != 1 {
			details = append(details, fmt.Sprintf("%s must appear at most once", lower))
			continue
		}
		if vals[0] == "" {
			details = append(details, fmt.Sprintf("%s must not be empty", lower))
			continue
		}
		if _, exists := rawParams[lower]; exists {
			details = append(details, fmt.Sprintf("%s must appear at most once", lower))
			continue
		}
		rawParams[lower] = vals[0]
	}
	return rawParams, details, nil
}

var allowedSessionsParams = func() map[string]struct{} {
	out := make(map[string]struct{}, len(listSessionsParamSpecs))
	for _, spec := range listSessionsParamSpecs {
		out[spec.name] = struct{}{}
	}
	return out
}()

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
