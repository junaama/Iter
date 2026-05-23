package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/iter-dev/iter/internal/denylist"
	"github.com/iter-dev/iter/internal/suggest"
	"github.com/iter-dev/iter/pkg/contracts"
)

const DefaultSocketName = "daemon.sock"

type Config struct {
	SocketPath string
	Version    string
	AppVersion string
	Logger     *slog.Logger
}

type Server struct {
	socketPath string
	version    string
	logger     *slog.Logger
	state      *State
}

type State struct {
	mu                    sync.RWMutex
	paused                bool
	currentTask           string
	idleSince             *time.Time
	lastSessionAt         *time.Time
	capturedToday         int
	pendingSuggestions    []localSuggestion
	suppressedSuggestions map[string]struct{}
}

type Status struct {
	Running       bool       `json:"running"`
	CurrentTask   *string    `json:"current_task,omitempty"`
	IdleSince     *time.Time `json:"idle_since,omitempty"`
	LastSessionAt *time.Time `json:"last_session_at"`
	CapturedToday int        `json:"captured_today"`
	Paused        bool       `json:"paused"`
}

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type response struct {
	ID     string         `json:"id"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type localSuggestion struct {
	ID            string
	SessionID     string
	Action        contracts.Action
	SuggestionID  *string
	RefinedPrompt string
	Rationale     *string
	Confidence    float64
	Evidence      []contracts.SuggestEvidence
	CreatedAt     time.Time
}

type suppressPatternParams struct {
	RefinedPrompt string `json:"refined_prompt"`
	SuggestionID  string `json:"suggestion_id,omitempty"`
}

func NewServer(cfg Config) (*Server, error) {
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = "0.1.0"
	}
	if cfg.AppVersion != "" && major(version) != major(cfg.AppVersion) {
		return nil, fmt.Errorf("daemon/app major version mismatch: daemon=%s app=%s", version, cfg.AppVersion)
	}
	idleSince := time.Now().UTC()
	socketPath := cfg.SocketPath
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return &Server{
		socketPath: socketPath,
		version:    version,
		logger:     logger,
		state:      &State{idleSince: &idleSince},
	}, nil
}

func DefaultSocketPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "Iter", DefaultSocketName)
	}
	return filepath.Join(home, "Library", "Application Support", "Iter", DefaultSocketName)
}

func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return err
	}
	if err := removeStaleSocket(s.socketPath); err != nil {
		return err
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	defer listener.Close()
	defer os.Remove(s.socketPath)

	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			s.logger.Warn("daemon ipc accept failed", "error", err)
			continue
		}
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = encoder.Encode(response{Error: "invalid_json"})
			continue
		}
		_ = encoder.Encode(s.dispatch(req))
	}
	if err := scanner.Err(); err != nil {
		s.logger.Debug("daemon ipc read ended", "error", err)
	}
}

func (s *Server) dispatch(req request) response {
	if req.ID == "" {
		return response{Error: "missing_id"}
	}
	switch req.Method {
	case "ping":
		return response{ID: req.ID, Result: map[string]any{"ok": true}}
	case "version":
		return response{ID: req.ID, Result: map[string]any{"version": s.version}}
	case "status":
		status := s.state.Status()
		return response{ID: req.ID, Result: map[string]any{
			"running":         status.Running,
			"current_task":    status.CurrentTask,
			"idle_since":      status.IdleSince,
			"last_session_at": status.LastSessionAt,
			"captured_today":  status.CapturedToday,
			"paused":          status.Paused,
		}}
	case "pause":
		s.state.SetPaused(true)
		return response{ID: req.ID, Result: map[string]any{"paused": true}}
	case "resume":
		s.state.SetPaused(false)
		return response{ID: req.ID, Result: map[string]any{"paused": false}}
	case "suggestion.available":
		return response{ID: req.ID, Result: s.nextSuggestionResult()}
	case "suggestion.suppress_pattern":
		return s.suppressPattern(req)
	default:
		return response{ID: req.ID, Error: "unknown_method"}
	}
}

func (s *Server) HandleSuggestionAvailable(sessionID uuid.UUID, resp contracts.SuggestResponse) {
	if resp.NoSuggestionReason != nil {
		s.logger.Info("suggestion_noop",
			"session_id", sessionID.String(),
			"reason", string(*resp.NoSuggestionReason))
		return
	}
	if resp.RefinedPrompt == nil {
		s.logger.Info("suggestion_noop",
			"session_id", sessionID.String(),
			"reason", "missing_refined_prompt")
		return
	}

	action, refined := suggest.SuggestionAction(resp.Confidence, *resp.RefinedPrompt)
	if action == contracts.ActionSuppress || strings.TrimSpace(refined) == "" {
		s.logger.Info("suggestion_noop",
			"session_id", sessionID.String(),
			"reason", "suppressed_by_decision_function")
		return
	}
	if s.state.IsSuggestionSuppressed(refined) {
		s.logger.Info("suggestion_suppressed_by_pattern", "session_id", sessionID.String())
		return
	}
	if hit, patternID := denylist.Contains(refined); hit {
		s.logger.Warn("denylist_hit",
			"pattern_id", patternID,
			"session_id", sessionID.String())
		return
	}

	var suggestionID *string
	if resp.SuggestionID != nil && *resp.SuggestionID != uuid.Nil {
		id := resp.SuggestionID.String()
		suggestionID = &id
	}
	s.state.EnqueueSuggestion(localSuggestion{
		ID:            uuid.NewString(),
		SessionID:     sessionID.String(),
		Action:        action,
		SuggestionID:  suggestionID,
		RefinedPrompt: refined,
		Rationale:     resp.Rationale,
		Confidence:    resp.Confidence,
		Evidence:      resp.Evidence,
		CreatedAt:     time.Now().UTC(),
	})
}

func (s *Server) nextSuggestionResult() map[string]any {
	suggestion, ok := s.state.PopSuggestion()
	if !ok {
		return map[string]any{"available": false}
	}
	evidence := suggestion.Evidence
	if evidence == nil {
		evidence = []contracts.SuggestEvidence{}
	}
	var suggestionID any
	if suggestion.SuggestionID != nil {
		suggestionID = *suggestion.SuggestionID
	}
	var rationale any
	if suggestion.Rationale != nil {
		rationale = *suggestion.Rationale
	}
	return map[string]any{
		"available":      true,
		"id":             suggestion.ID,
		"session_id":     suggestion.SessionID,
		"action":         string(suggestion.Action),
		"suggestion_id":  suggestionID,
		"refined_prompt": suggestion.RefinedPrompt,
		"rationale":      rationale,
		"confidence":     suggestion.Confidence,
		"evidence":       evidence,
		"created_at":     suggestion.CreatedAt,
	}
}

func (s *Server) suppressPattern(req request) response {
	var params suppressPatternParams
	if len(req.Params) == 0 || json.Unmarshal(req.Params, &params) != nil {
		return response{ID: req.ID, Error: "invalid_params"}
	}
	if !s.state.SuppressSuggestion(params.RefinedPrompt) {
		return response{ID: req.ID, Error: "invalid_params"}
	}
	s.logger.Info("suggestion_suppress_endpoint_missing",
		"suggestion_id", params.SuggestionID,
		"backend_endpoint", "not_implemented")
	return response{ID: req.ID, Result: map[string]any{
		"suppressed":        true,
		"backend_endpoint":  "not_implemented",
		"persisted_locally": true,
	}}
}

func (s *Server) SetCurrentTask(task string) {
	s.state.SetCurrentTask(task, time.Now().UTC())
}

func (s *Server) RecordSessionCaptured(at time.Time) {
	s.state.RecordSessionCaptured(at)
}

func (s *State) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var currentTask *string
	if s.currentTask != "" {
		task := s.currentTask
		currentTask = &task
	}
	return Status{
		Running:       true,
		CurrentTask:   currentTask,
		IdleSince:     s.idleSince,
		LastSessionAt: s.lastSessionAt,
		CapturedToday: s.capturedToday,
		Paused:        s.paused,
	}
}

func (s *State) SetPaused(paused bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.paused = paused
}

func (s *State) SetCurrentTask(task string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	task = strings.TrimSpace(task)
	s.currentTask = task
	if task == "" {
		idleSince := now.UTC()
		s.idleSince = &idleSince
		return
	}
	s.idleSince = nil
}

func (s *State) RecordSessionCaptured(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	capturedAt := at.UTC()
	s.currentTask = ""
	s.idleSince = &capturedAt
	s.lastSessionAt = &capturedAt
	s.capturedToday++
}

func (s *State) EnqueueSuggestion(suggestion localSuggestion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isSuggestionSuppressedLocked(suggestion.RefinedPrompt) {
		return
	}
	s.pendingSuggestions = append(s.pendingSuggestions, suggestion)
}

func (s *State) PopSuggestion() (localSuggestion, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pendingSuggestions) == 0 {
		return localSuggestion{}, false
	}
	suggestion := s.pendingSuggestions[0]
	copy(s.pendingSuggestions, s.pendingSuggestions[1:])
	s.pendingSuggestions = s.pendingSuggestions[:len(s.pendingSuggestions)-1]
	return suggestion, true
}

func (s *State) SuppressSuggestion(refinedPrompt string) bool {
	key := normalizeSuggestionPrompt(refinedPrompt)
	if key == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suppressedSuggestions == nil {
		s.suppressedSuggestions = make(map[string]struct{})
	}
	s.suppressedSuggestions[key] = struct{}{}
	filtered := s.pendingSuggestions[:0]
	for _, suggestion := range s.pendingSuggestions {
		if normalizeSuggestionPrompt(suggestion.RefinedPrompt) == key {
			continue
		}
		filtered = append(filtered, suggestion)
	}
	s.pendingSuggestions = filtered
	return true
}

func (s *State) IsSuggestionSuppressed(refinedPrompt string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isSuggestionSuppressedLocked(refinedPrompt)
}

func (s *State) isSuggestionSuppressedLocked(refinedPrompt string) bool {
	key := normalizeSuggestionPrompt(refinedPrompt)
	if key == "" || s.suppressedSuggestions == nil {
		return false
	}
	_, ok := s.suppressedSuggestions[key]
	return ok
}

func normalizeSuggestionPrompt(refinedPrompt string) string {
	return strings.ToLower(strings.Join(strings.Fields(refinedPrompt), " "))
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to remove non-socket at %s", path)
		}
		return os.Remove(path)
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func major(version string) int {
	version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
	if version == "" || version == "dev" {
		return 0
	}
	head := strings.SplitN(version, ".", 2)[0]
	value, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return value
}
