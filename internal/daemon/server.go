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
	mu            sync.RWMutex
	paused        bool
	lastSessionAt *time.Time
	capturedToday int
}

type Status struct {
	Running       bool       `json:"running"`
	LastSessionAt *time.Time `json:"last_session_at"`
	CapturedToday int        `json:"captured_today"`
	Paused        bool       `json:"paused"`
}

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
}

type response struct {
	ID     string         `json:"id"`
	Result map[string]any `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func NewServer(cfg Config) (*Server, error) {
	version := strings.TrimSpace(cfg.Version)
	if version == "" {
		version = "0.1.0"
	}
	if cfg.AppVersion != "" && major(version) != major(cfg.AppVersion) {
		return nil, fmt.Errorf("daemon/app major version mismatch: daemon=%s app=%s", version, cfg.AppVersion)
	}
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
		state:      &State{},
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
	default:
		return response{ID: req.ID, Error: "unknown_method"}
	}
}

func (s *State) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Status{
		Running:       true,
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
