package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	maxSessionEventsPerResponse = 10000
	maxSubagentDepth            = 5
)

// SessionDetailHandler returns GET /v1/sessions/:id. It expects the auth
// middleware to have installed a Principal and the tenant middleware to have
// installed the RLS-scoped pgx.Tx in context.
func SessionDetailHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := contracts.RequireAuth(r.Context()); err != nil {
			writeSessionError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		tx := db.FromContext(r.Context())
		if tx == nil {
			writeSessionError(w, http.StatusServiceUnavailable, "db_unavailable")
			return
		}

		sessionID, ok := parseUUIDPathParam(w, r, "id")
		if !ok {
			return
		}
		cursor, ok := parseEventsCursor(w, r)
		if !ok {
			return
		}

		session, err := repo.GetSession(r.Context(), tx, sessionID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeSessionError(w, http.StatusNotFound, "not_found")
				return
			}
			logger.ErrorContext(r.Context(), "session_detail_get_session_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}

		events, err := repo.ListSessionEventsPage(r.Context(), tx, sessionID, maxSessionEventsPerResponse+1, cursor)
		if err != nil {
			logger.ErrorContext(r.Context(), "session_detail_events_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}
		eventsPage, err := mapEventsPage(events)
		if err != nil {
			logger.ErrorContext(r.Context(), "session_detail_event_cursor_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}

		subagentRows, subagentsTruncated, err := repo.ListSubagentTree(r.Context(), tx, sessionID, maxSubagentDepth)
		if err != nil {
			logger.ErrorContext(r.Context(), "session_detail_subagents_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}

		outcomes, err := repo.ListOutcomesForSession(r.Context(), tx, sessionID)
		if err != nil {
			logger.ErrorContext(r.Context(), "session_detail_outcomes_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}

		resp := contracts.SessionDetailResponse{
			Session: mapSessionDetail(session),
			Events:  eventsPage,
			Subagents: contracts.SubagentTree{
				Items:     mapSubagentTree(subagentRows),
				Truncated: subagentsTruncated,
			},
			Outcomes: mapOutcomeDetails(outcomes),
		}
		writeSessionJSON(w, http.StatusOK, resp, logger, r)
	}
}

// SessionScoresHandler returns GET /v1/scores/:session_id. It first fetches
// the session under RLS so "missing" and "belongs to another tenant" both
// resolve to the same 404.
func SessionScoresHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := contracts.RequireAuth(r.Context()); err != nil {
			writeSessionError(w, http.StatusUnauthorized, "unauthenticated")
			return
		}
		tx := db.FromContext(r.Context())
		if tx == nil {
			writeSessionError(w, http.StatusServiceUnavailable, "db_unavailable")
			return
		}

		sessionID, ok := parseUUIDPathParam(w, r, "session_id")
		if !ok {
			return
		}
		if _, err := repo.GetSession(r.Context(), tx, sessionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeSessionError(w, http.StatusNotFound, "not_found")
				return
			}
			logger.ErrorContext(r.Context(), "session_scores_get_session_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}

		scores, err := repo.ListScoresForSession(r.Context(), tx, sessionID)
		if err != nil {
			logger.ErrorContext(r.Context(), "session_scores_list_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}
		scoreDetails, err := mapScoreDetails(scores)
		if err != nil {
			logger.ErrorContext(r.Context(), "session_scores_map_failed", "err", err)
			writeSessionError(w, http.StatusInternalServerError, "internal")
			return
		}

		writeSessionJSON(w, http.StatusOK, contracts.SessionScoresResponse{
			SessionID: sessionID,
			Scores:    scoreDetails,
		}, logger, r)
	}
}

func parseUUIDPathParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, "invalid_id")
		return uuid.Nil, false
	}
	return id, true
}

type eventCursorWire struct {
	OccurredAt time.Time `json:"occurred_at"`
	ID         int64     `json:"id"`
}

func parseEventsCursor(w http.ResponseWriter, r *http.Request) (*repo.SessionEventCursor, bool) {
	raw := r.URL.Query().Get("events_cursor")
	if raw == "" {
		return nil, true
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		writeSessionError(w, http.StatusBadRequest, "invalid_events_cursor")
		return nil, false
	}
	var wire eventCursorWire
	if err := json.Unmarshal(data, &wire); err != nil || wire.ID <= 0 || wire.OccurredAt.IsZero() {
		writeSessionError(w, http.StatusBadRequest, "invalid_events_cursor")
		return nil, false
	}
	return &repo.SessionEventCursor{OccurredAt: wire.OccurredAt, ID: wire.ID}, true
}

func encodeEventsCursor(ev repo.SessionEventRow) (string, error) {
	data, err := json.Marshal(eventCursorWire{OccurredAt: ev.OccurredAt, ID: ev.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func mapEventsPage(rows []repo.SessionEventRow) (contracts.SessionEventsPage, error) {
	page := contracts.SessionEventsPage{
		Items: []contracts.SessionEventDetail{},
	}
	if len(rows) == 0 {
		return page, nil
	}
	if len(rows) > maxSessionEventsPerResponse {
		cursor, err := encodeEventsCursor(rows[maxSessionEventsPerResponse-1])
		if err != nil {
			return page, err
		}
		page.NextCursor = &cursor
		rows = rows[:maxSessionEventsPerResponse]
	}
	page.Items = make([]contracts.SessionEventDetail, 0, len(rows))
	for _, ev := range rows {
		payload := ev.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		page.Items = append(page.Items, contracts.SessionEventDetail{
			ID:         ev.ID,
			SessionID:  ev.SessionID,
			TenantID:   ev.TenantID,
			Type:       ev.EventType,
			Payload:    payload,
			OccurredAt: ev.OccurredAt,
		})
	}
	return page, nil
}

type mutableSubagentNode struct {
	session  contracts.SessionDetail
	depth    int
	children []*mutableSubagentNode
}

func mapSubagentTree(rows []repo.SubagentTreeRow) []contracts.SubagentSessionNode {
	if len(rows) == 0 {
		return []contracts.SubagentSessionNode{}
	}
	nodes := make(map[uuid.UUID]*mutableSubagentNode, len(rows))
	roots := []*mutableSubagentNode{}
	for _, row := range rows {
		node := &mutableSubagentNode{
			session: mapSessionDetail(row.Session),
			depth:   row.Depth,
		}
		nodes[row.Session.ID] = node
		if row.Session.ParentSessionID == nil {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[*row.Session.ParentSessionID]
		if !ok {
			roots = append(roots, node)
			continue
		}
		parent.children = append(parent.children, node)
	}
	return freezeSubagentNodes(roots)
}

func freezeSubagentNodes(nodes []*mutableSubagentNode) []contracts.SubagentSessionNode {
	out := make([]contracts.SubagentSessionNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, contracts.SubagentSessionNode{
			Session:  node.session,
			Depth:    node.depth,
			Children: freezeSubagentNodes(node.children),
		})
	}
	return out
}

func mapSessionDetail(s repo.Session) contracts.SessionDetail {
	var effort *contracts.Effort
	if s.Effort != nil {
		v := contracts.Effort(*s.Effort)
		effort = &v
	}
	return contracts.SessionDetail{
		ID:              s.ID,
		TenantID:        s.TenantID,
		UserID:          s.UserID,
		ParentSessionID: s.ParentSessionID,
		Harness:         contracts.Harness(s.Harness),
		Model:           s.Model,
		Effort:          effort,
		Tools:           append([]string{}, s.Tools...),
		RepoHash:        s.RepoHash,
		GitBranch:       s.GitBranch,
		StartedAt:       s.StartedAt,
		EndedAt:         s.EndedAt,
		WallTimeMs:      s.WallTimeMs,
		TurnCount:       s.TurnCount,
		TotalTokensIn:   s.TotalTokensIn,
		TotalTokensOut:  s.TotalTokensOut,
		RedactedPrompt:  s.RedactedPrompt,
		RedactedSystem:  s.RedactedSystem,
		Classification:  contracts.Classification(s.Classification),
		IngestedAt:      s.IngestedAt,
		ArchivedAt:      s.ArchivedAt,
	}
}

func mapOutcomeDetails(rows []repo.Outcome) []contracts.SessionOutcomeDetail {
	out := make([]contracts.SessionOutcomeDetail, 0, len(rows))
	for _, row := range rows {
		details := row.Details
		if len(details) == 0 {
			details = json.RawMessage(`{}`)
		}
		out = append(out, contracts.SessionOutcomeDetail{
			ID:          row.ID,
			SessionID:   row.SessionID,
			TenantID:    row.TenantID,
			OutcomeType: contracts.OutcomeType(row.OutcomeType),
			ExternalRef: row.ExternalRef,
			Details:     append(json.RawMessage(nil), details...),
			ObservedAt:  row.ObservedAt,
		})
	}
	return out
}

func mapScoreDetails(rows []repo.Score) ([]contracts.SessionScoreDetail, error) {
	out := make([]contracts.SessionScoreDetail, 0, len(rows))
	for _, row := range rows {
		rawSignals := row.Signals
		if len(rawSignals) == 0 {
			rawSignals = json.RawMessage(`{}`)
		}
		var signals contracts.ScoreSignals
		if err := json.Unmarshal(rawSignals, &signals); err != nil {
			return nil, err
		}
		out = append(out, contracts.SessionScoreDetail{
			ID:                row.ID,
			SessionID:         row.SessionID,
			TenantID:          row.TenantID,
			ScorerVersion:     row.ScorerVersion,
			CompositeScore:    row.CompositeScore,
			Signals:           signals,
			Rationale:         row.Rationale,
			ContributorWeight: row.ContributorWeight,
			ScoredAt:          row.ScoredAt,
		})
	}
	return out, nil
}

func writeSessionJSON(w http.ResponseWriter, status int, body any, logger *slog.Logger, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil && logger != nil {
		logger.WarnContext(r.Context(), "session_detail_encode_failed", "err", err)
	}
}

func writeSessionError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
