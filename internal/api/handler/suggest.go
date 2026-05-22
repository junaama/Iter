package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/internal/denylist"
	"github.com/iter-dev/iter/internal/embed"
	"github.com/iter-dev/iter/internal/llm"
	"github.com/iter-dev/iter/internal/suggest"
	"github.com/iter-dev/iter/pkg/contracts"
	prompttemplates "github.com/iter-dev/iter/templates"
)

const (
	suggestMaxBodyBytes = 1 << 20
	suggestKNN          = 20
	suggestContextLimit = 5
	suggestLLMMaxTokens = 512
	suggestPersistBound = 2 * time.Second
	postgresRetryAfter  = "5"
)

var (
	errSuggestPostgresUnavailable = errors.New("suggest: postgres unavailable")
	suggestPromptTemplate         = template.Must(template.New("suggest").Parse(prompttemplates.Suggest))
)

type suggestEmbedder interface {
	Embed(context.Context, embed.EmbedRequest) (embed.EmbedResponse, error)
}

type suggestCompleter interface {
	Complete(context.Context, contracts.LLMCompletionRequest) (contracts.LLMCompletionResponse, error)
}

type suggestStore interface {
	SearchCandidates(context.Context, []float32, int) (suggestCandidateSearch, error)
	PersistSuggestion(context.Context, persistedSuggestion) error
}

type suggestCandidateSearch struct {
	NeighborCount int
	Candidates    []suggestCandidate
}

type suggestCandidate struct {
	SessionID          uuid.UUID
	Similarity         float64
	CompositeScore     float64
	CombinedConfidence float64
	ScoreRationale     string
}

type persistedSuggestion struct {
	TenantID           uuid.UUID
	SourcePrompt       string
	SourceEmbedding    []float32
	RefinedPrompt      string
	Rationale          *string
	EvidenceSessionIDs []uuid.UUID
}

type liveSuggestStore struct {
	pool *pgxpool.Pool
}

func (s liveSuggestStore) SearchCandidates(ctx context.Context, queryVec []float32, k int) (suggestCandidateSearch, error) {
	tx := db.FromContext(ctx)
	if tx == nil {
		return suggestCandidateSearch{}, errSuggestPostgresUnavailable
	}

	hits, err := repo.SearchEmbeddingsKNN(ctx, tx, queryVec, k)
	if err != nil {
		return suggestCandidateSearch{}, fmt.Errorf("%w: %v", errSuggestPostgresUnavailable, err)
	}
	result := suggestCandidateSearch{NeighborCount: len(hits)}
	for _, hit := range hits {
		score, err := repo.LatestScoreForSession(ctx, tx, hit.SessionID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return suggestCandidateSearch{}, fmt.Errorf("%w: %v", errSuggestPostgresUnavailable, err)
		}
		rationale := ""
		if score.Rationale != nil {
			rationale = *score.Rationale
		}
		result.Candidates = append(result.Candidates, suggestCandidate{
			SessionID:      hit.SessionID,
			Similarity:     hit.Similarity,
			CompositeScore: score.CompositeScore,
			ScoreRationale: rationale,
		})
	}
	return result, nil
}

func (s liveSuggestStore) PersistSuggestion(ctx context.Context, p persistedSuggestion) error {
	if s.pool == nil {
		return errSuggestPostgresUnavailable
	}
	return db.WithTenant(ctx, s.pool, p.TenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		_, err := repo.UpsertSuggestion(ctx, tx, repo.Suggestion{
			TenantID:           p.TenantID,
			SourcePrompt:       p.SourcePrompt,
			SourceEmbedding:    pgvector.NewVector(p.SourceEmbedding),
			RefinedPrompt:      p.RefinedPrompt,
			Rationale:          p.Rationale,
			EvidenceSessionIDs: p.EvidenceSessionIDs,
		})
		return err
	})
}

type suggestHandler struct {
	logger *slog.Logger
	embed  suggestEmbedder
	llm    suggestCompleter
	store  suggestStore
}

// SuggestHandler returns the POST /v1/suggest handler.
func SuggestHandler(deps app.Deps) http.HandlerFunc {
	var emb suggestEmbedder
	if deps.Embed != nil {
		emb = deps.Embed
	}
	var comp suggestCompleter
	if deps.LLM != nil {
		comp = deps.LLM
	}
	return newSuggestHandler(deps.Logger, emb, comp, liveSuggestStore{pool: deps.DB}).ServeHTTP
}

func newSuggestHandler(
	logger *slog.Logger,
	emb suggestEmbedder,
	comp suggestCompleter,
	store suggestStore,
) *suggestHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &suggestHandler{logger: logger, embed: emb, llm: comp, store: store}
}

func (h *suggestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, details, ok := decodeSuggestRequest(w, r)
	if !ok {
		writeSuggestJSON(w, http.StatusBadRequest, validationBody(details))
		return
	}

	principal, err := contracts.RequireAuth(r.Context())
	if err != nil {
		writeSuggestJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
		return
	}
	if principal.UserID != req.UserID || principal.TenantID != req.TenantID {
		writeSuggestJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(req.Options.MaxLatencyMS)*time.Millisecond)
	defer cancel()

	queryVec, err := h.embedPrompt(ctx, req.SessionContext.RawPrompt)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLatencyBudgetExceeded, 0))
			return
		}
		writeSuggestJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "embedding_unavailable"})
		return
	}

	search, err := h.store.SearchCandidates(ctx, queryVec, suggestKNN)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLatencyBudgetExceeded, 0))
			return
		}
		h.logger.WarnContext(ctx, "suggest_postgres_unavailable", "err", err)
		w.Header().Set("Retry-After", postgresRetryAfter)
		writeSuggestJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "postgres_unavailable"})
		return
	}
	if search.NeighborCount == 0 {
		writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionNoEvidence, 0))
		return
	}

	ranked, bestConfidence := rankSuggestCandidates(search.Candidates)
	if len(ranked) == 0 {
		writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLowConfidence, bestConfidence))
		return
	}
	if len(ranked) > suggestContextLimit {
		ranked = ranked[:suggestContextLimit]
	}

	if h.llm == nil {
		writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLLMUnavailable, bestConfidence))
		return
	}

	prompt, err := buildSuggestPrompt(req, ranked)
	if err != nil {
		h.logger.ErrorContext(ctx, "suggest_template_failed", "err", err)
		writeSuggestJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		return
	}

	llmResp, err := h.llm.Complete(ctx, contracts.LLMCompletionRequest{
		Tier: contracts.LLMTierCheapHot,
		Messages: []contracts.LLMMessage{
			{Role: contracts.LLMRoleSystem, Content: "Return only the requested JSON object."},
			{Role: contracts.LLMRoleUser, Content: prompt},
		},
		Temperature: 0.2,
		MaxTokens:   suggestLLMMaxTokens,
	})
	if err != nil {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLatencyBudgetExceeded, bestConfidence))
		case errors.Is(err, llm.ErrAllProvidersUnavailable):
			writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLLMUnavailable, bestConfidence))
		default:
			h.logger.WarnContext(ctx, "suggest_llm_failed", "err", err)
			writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLLMUnavailable, bestConfidence))
		}
		return
	}

	parsed, ok := parseSuggestLLMResponse(llmResp.Text)
	if !ok {
		writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLLMUnparseable, bestConfidence))
		return
	}

	action, refined := suggest.SuggestionAction(parsed.Confidence, parsed.RefinedPrompt)
	if action == contracts.ActionSuppress {
		writeSuggestJSON(w, http.StatusOK, suppressResponse(contracts.NoSuggestionLowConfidence, parsed.Confidence))
		return
	}

	if hit, patternID := denylist.Contains(refined); hit {
		h.logger.WarnContext(ctx, "denylist_hit",
			"pattern_id", patternID,
			"tenant_id", req.TenantID.String(),
			"user_id", req.UserID.String())
		writeSuggestJSON(w, http.StatusOK, contracts.SuggestResponse{
			Action:             contracts.ActionSuppress,
			Confidence:         parsed.Confidence,
			Evidence:           []contracts.SuggestEvidence{},
			NoSuggestionReason: nil,
		})
		return
	}

	rationale := optionalString(parsed.Rationale)
	evidenceIDs := candidateSessionIDs(ranked)
	h.persistAsync(r.Context(), persistedSuggestion{
		TenantID:           req.TenantID,
		SourcePrompt:       req.SessionContext.RawPrompt,
		SourceEmbedding:    queryVec,
		RefinedPrompt:      refined,
		Rationale:          rationale,
		EvidenceSessionIDs: evidenceIDs,
	})

	resp := contracts.SuggestResponse{
		Action:             action,
		RefinedPrompt:      &refined,
		Rationale:          rationale,
		Confidence:         parsed.Confidence,
		Evidence:           []contracts.SuggestEvidence{},
		NoSuggestionReason: nil,
	}
	writeSuggestJSON(w, http.StatusOK, resp)
}

func (h *suggestHandler) embedPrompt(ctx context.Context, prompt string) ([]float32, error) {
	if h.embed == nil {
		return nil, embed.ErrAllProvidersUnavailable
	}
	resp, err := h.embed.Embed(ctx, embed.EmbedRequest{Inputs: []string{prompt}})
	if err != nil {
		return nil, err
	}
	if len(resp.Vectors) != 1 || len(resp.Vectors[0]) == 0 {
		return nil, embed.ErrAllProvidersUnavailable
	}
	return resp.Vectors[0], nil
}

func (h *suggestHandler) persistAsync(parent context.Context, p persistedSuggestion) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), suggestPersistBound)
		defer cancel()
		if err := h.store.PersistSuggestion(ctx, p); err != nil {
			h.logger.WarnContext(parent, "suggest_persist_failed", "err", err)
		}
	}()
}

func decodeSuggestRequest(w http.ResponseWriter, r *http.Request) (contracts.SuggestRequest, map[string]string, bool) {
	req := contracts.DefaultSuggestRequest()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, suggestMaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, map[string]string{"body": err.Error()}, false
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return req, map[string]string{"body": "must contain a single JSON object"}, false
	}
	if details := req.Validate(); len(details) > 0 {
		return req, details, false
	}
	return req, nil, true
}

func rankSuggestCandidates(candidates []suggestCandidate) ([]suggestCandidate, float64) {
	ranked := make([]suggestCandidate, 0, len(candidates))
	best := 0.0
	for _, candidate := range candidates {
		candidate.CombinedConfidence = boundedConfidence(candidate.CompositeScore * candidate.Similarity)
		if candidate.CombinedConfidence > best {
			best = candidate.CombinedConfidence
		}
		action, _ := suggest.SuggestionAction(candidate.CombinedConfidence, "candidate evidence")
		if action == contracts.ActionSuppress {
			continue
		}
		ranked = append(ranked, candidate)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return ranked[i].CombinedConfidence > ranked[j].CombinedConfidence
	})
	return ranked, best
}

func boundedConfidence(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

type suggestPromptData struct {
	Harness    string
	Model      string
	Effort     string
	Tools      string
	RepoHash   string
	GitBranch  string
	CWDFiles   string
	RawPrompt  string
	Candidates []suggestCandidate
}

func buildSuggestPrompt(req contracts.SuggestRequest, candidates []suggestCandidate) (string, error) {
	data := suggestPromptData{
		Harness:    req.SessionContext.Harness,
		Model:      req.SessionContext.Model,
		Effort:     req.SessionContext.Effort,
		Tools:      strings.Join(req.SessionContext.Tools, ", "),
		RepoHash:   stringValue(req.SessionContext.RepoHash),
		GitBranch:  stringValue(req.SessionContext.GitBranch),
		CWDFiles:   strings.Join(req.SessionContext.CWDFiles, ", "),
		RawPrompt:  req.SessionContext.RawPrompt,
		Candidates: candidates,
	}
	var buf bytes.Buffer
	if err := suggestPromptTemplate.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

type suggestLLMJSON struct {
	RefinedPrompt string  `json:"refined_prompt"`
	Confidence    float64 `json:"confidence"`
	Rationale     string  `json:"rationale"`
}

func parseSuggestLLMResponse(text string) (suggestLLMJSON, bool) {
	var out suggestLLMJSON
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return suggestLLMJSON{}, false
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return suggestLLMJSON{}, false
	}
	if strings.TrimSpace(out.RefinedPrompt) == "" {
		return suggestLLMJSON{}, false
	}
	if math.IsNaN(out.Confidence) || out.Confidence < 0 || out.Confidence > 1 {
		return suggestLLMJSON{}, false
	}
	return out, true
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func candidateSessionIDs(candidates []suggestCandidate) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, candidate.SessionID)
	}
	return out
}

func suppressResponse(reason contracts.NoSuggestionReason, confidence float64) contracts.SuggestResponse {
	reasonCopy := reason
	return contracts.SuggestResponse{
		Action:             contracts.ActionSuppress,
		Confidence:         boundedConfidence(confidence),
		Evidence:           []contracts.SuggestEvidence{},
		NoSuggestionReason: &reasonCopy,
	}
}

func validationBody(details map[string]string) map[string]any {
	return map[string]any{
		"error":   "validation",
		"details": details,
	}
}

func writeSuggestJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
