package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	defaultCaptureInterval = 10 * time.Second
	defaultCaptureMaxFiles = 200
	defaultCaptureMaxBytes = 512 * 1024
	captureCompleteAfter   = 5 * time.Second
)

var supportedCaptureExts = map[string]struct{}{
	".json":   {},
	".jsonl":  {},
	".ndjson": {},
}

type CaptureConfig struct {
	APIBaseURL string
	WSEndpoint string
	APIToken   string
	WALPath    string
	Dirs       []HarnessDir
	Interval   time.Duration
	MaxFiles   int
	MaxBytes   int64
	Now        func() time.Time
}

type HarnessDir struct {
	Harness string
	Path    string
}

type CapturePublisher interface {
	Publish(ctx context.Context, events []CaptureEvent) error
}

type CaptureRunner struct {
	cfg       CaptureConfig
	state     *State
	logger    *slog.Logger
	publisher CapturePublisher
	seen      map[string]fileSeen
}

type fileSeen struct {
	modTime time.Time
	size    int64
}

type CaptureEvent struct {
	SessionID  uuid.UUID
	EventType  contracts.EventType
	OccurredAt time.Time
	Payload    map[string]any
}

type capturedSession struct {
	SessionID uuid.UUID
	Harness   string
	Model     string
	Prompt    string
	StartedAt time.Time
	EndedAt   *time.Time
	Tools     []string
	SourceKey string
}

func NewCaptureRunner(cfg CaptureConfig, state *State, logger *slog.Logger) *CaptureRunner {
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.withDefaults()
	return &CaptureRunner{
		cfg:       cfg,
		state:     state,
		logger:    logger,
		publisher: NewWSPublisher(cfg.WSEndpoint, cfg.APIToken, logger, cfg.Now),
		seen:      map[string]fileSeen{},
	}
}

func (c CaptureConfig) Enabled() bool {
	return strings.TrimSpace(c.APIToken) != "" && strings.TrimSpace(c.wsEndpoint()) != ""
}

func (c CaptureConfig) withDefaults() CaptureConfig {
	if c.Interval <= 0 {
		c.Interval = defaultCaptureInterval
	}
	if c.MaxFiles <= 0 {
		c.MaxFiles = defaultCaptureMaxFiles
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = defaultCaptureMaxBytes
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.WSEndpoint == "" {
		c.WSEndpoint = c.wsEndpoint()
	}
	if len(c.Dirs) == 0 {
		c.Dirs = DefaultHarnessDirs()
	}
	if c.WALPath == "" {
		c.WALPath = DefaultCaptureWALPath()
	}
	return c
}

func (c CaptureConfig) wsEndpoint() string {
	if c.WSEndpoint != "" {
		return c.WSEndpoint
	}
	base := strings.TrimRight(strings.TrimSpace(c.APIBaseURL), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	switch {
	case strings.HasPrefix(base, "https://"):
		return "wss://" + strings.TrimPrefix(base, "https://") + "/v1/ws"
	case strings.HasPrefix(base, "http://"):
		return "ws://" + strings.TrimPrefix(base, "http://") + "/v1/ws"
	case strings.HasPrefix(base, "ws://"), strings.HasPrefix(base, "wss://"):
		return base
	default:
		return "ws://" + base + "/v1/ws"
	}
}

func DefaultHarnessDirs() []HarnessDir {
	return []HarnessDir{
		{Harness: "claude_code", Path: "~/.claude"},
		{Harness: "codex", Path: "~/.codex"},
		{Harness: "gemini_cli", Path: "~/.gemini"},
		{Harness: "opencode", Path: "~/.config/opencode"},
		{Harness: "pi", Path: "~/Library/Application Support/Pi"},
	}
}

func DefaultCaptureWALPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "Iter", "capture.sqlite")
	}
	return filepath.Join(home, "Library", "Application Support", "Iter", "capture.sqlite")
}

func ParseHarnessDirs(raw string) ([]HarnessDir, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, string(os.PathListSeparator))
	out := make([]HarnessDir, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		harness, path, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("capture dir %q must be harness=path", part)
		}
		harness = strings.TrimSpace(harness)
		path = strings.TrimSpace(path)
		if !validCaptureHarness(harness) || path == "" {
			return nil, fmt.Errorf("invalid capture dir %q", part)
		}
		out = append(out, HarnessDir{Harness: harness, Path: expandHome(path)})
	}
	return out, nil
}

func (r *CaptureRunner) Run(ctx context.Context) {
	wal, err := OpenCaptureWAL(ctx, r.cfg.WALPath)
	if err != nil {
		r.logger.Warn("daemon_capture_wal_unavailable", "err", err)
	} else {
		defer wal.Close()
	}
	r.scanAndPublishUsing(ctx, wal)
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.scanAndPublishUsing(ctx, wal)
		}
	}
}

func (r *CaptureRunner) scanAndPublishUsing(ctx context.Context, wal *CaptureWAL) {
	if wal != nil {
		r.scanAndPublishWithWAL(ctx, wal)
		return
	}
	r.scanAndPublish(ctx)
}

func (r *CaptureRunner) scanAndPublish(ctx context.Context) {
	if r.state != nil && r.state.Paused() {
		return
	}
	events, err := r.Scan(ctx)
	if err != nil {
		r.logger.Warn("daemon_capture_scan_failed", "err", err)
		return
	}
	if len(events) == 0 {
		return
	}
	if r.state != nil {
		r.state.SetCurrentTask("syncing local sessions", r.cfg.Now().UTC())
	}
	if err := r.publisher.Publish(ctx, events); err != nil {
		r.logger.Warn("daemon_capture_publish_failed", "events", len(events), "err", err)
		return
	}
	if r.state != nil {
		r.state.RecordSessionCaptured(r.cfg.Now().UTC())
	}
	r.logger.Info("daemon_capture_published", "events", len(events))
}

func (r *CaptureRunner) scanAndPublishWithWAL(ctx context.Context, wal *CaptureWAL) {
	if r.state != nil && r.state.Paused() {
		return
	}
	events, err := r.Scan(ctx)
	if err != nil {
		r.logger.Warn("daemon_capture_scan_failed", "err", err)
		return
	}
	if len(events) > 0 {
		if _, err := wal.AppendBatch(ctx, captureWALEvents(events)); err != nil {
			r.logger.Warn("daemon_capture_wal_append_failed", "events", len(events), "err", err)
			return
		}
	}
	entries, err := wal.Unsent(ctx, r.cfg.MaxFiles*2)
	if err != nil {
		r.logger.Warn("daemon_capture_wal_unsent_failed", "err", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	if r.state != nil {
		r.state.SetCurrentTask("syncing local sessions", r.cfg.Now().UTC())
	}
	var sent int
	for _, entry := range entries {
		event := captureEvent(entry.Event)
		if err := r.publisher.Publish(ctx, []CaptureEvent{event}); err != nil {
			r.logger.Warn("daemon_capture_publish_failed", "wal_id", entry.ID, "err", err)
			break
		}
		if err := wal.MarkSent(ctx, entry.ID); err != nil {
			r.logger.Warn("daemon_capture_wal_mark_sent_failed", "wal_id", entry.ID, "err", err)
			break
		}
		sent++
	}
	if sent > 0 {
		if r.state != nil {
			r.state.RecordSessionCaptured(r.cfg.Now().UTC())
		}
		r.logger.Info("daemon_capture_published", "events", sent)
	}
}

func (r *CaptureRunner) Scan(ctx context.Context) ([]CaptureEvent, error) {
	var sessions []capturedSession
	for _, dir := range r.cfg.Dirs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if r.state != nil && !r.state.CaptureEnabled(dir.Harness) {
			continue
		}
		found, err := r.scanDir(ctx, dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			r.logger.Debug("daemon_capture_dir_skipped", "harness", dir.Harness, "path", dir.Path, "err", err)
			continue
		}
		sessions = append(sessions, found...)
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].StartedAt.Before(sessions[j].StartedAt)
	})
	var events []CaptureEvent
	for _, session := range sessions {
		events = append(events, session.toEvents(r.cfg.Now())...)
	}
	return events, nil
}

func (r *CaptureRunner) scanDir(ctx context.Context, dir HarnessDir) ([]capturedSession, error) {
	root := expandHome(dir.Path)
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}
	var candidates []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, ".git") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := supportedCaptureExts[strings.ToLower(filepath.Ext(path))]; !ok {
			return nil
		}
		candidates = append(candidates, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		ai, _ := os.Stat(candidates[i])
		aj, _ := os.Stat(candidates[j])
		if ai == nil || aj == nil {
			return candidates[i] < candidates[j]
		}
		return ai.ModTime().After(aj.ModTime())
	})
	if len(candidates) > r.cfg.MaxFiles {
		candidates = candidates[:r.cfg.MaxFiles]
	}
	var sessions []capturedSession
	for _, path := range candidates {
		session, ok, err := r.captureFile(dir.Harness, path)
		if err != nil {
			r.logger.Debug("daemon_capture_file_skipped", "path", path, "err", err)
			continue
		}
		if ok {
			sessions = append(sessions, session)
		}
	}
	return sessions, nil
}

func (r *CaptureRunner) captureFile(harness, path string) (capturedSession, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return capturedSession{}, false, err
	}
	if info.Size() <= 0 || info.Size() > r.cfg.MaxBytes {
		return capturedSession{}, false, nil
	}
	seen := r.seen[path]
	if seen.size == info.Size() && seen.modTime.Equal(info.ModTime()) {
		return capturedSession{}, false, nil
	}
	if r.cfg.Now().Sub(info.ModTime()) < captureCompleteAfter {
		return capturedSession{}, false, nil
	}
	session, err := parseCaptureFile(harness, path, info, r.cfg.MaxBytes)
	if err != nil {
		return capturedSession{}, false, err
	}
	if strings.TrimSpace(session.Prompt) == "" {
		return capturedSession{}, false, nil
	}
	r.seen[path] = fileSeen{modTime: info.ModTime(), size: info.Size()}
	return session, true, nil
}

func parseCaptureFile(harness, path string, info os.FileInfo, maxBytes int64) (capturedSession, error) {
	f, err := os.Open(path)
	if err != nil {
		return capturedSession{}, err
	}
	defer f.Close()
	limited := io.LimitReader(f, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return capturedSession{}, err
	}
	if int64(len(body)) > maxBytes {
		return capturedSession{}, errors.New("capture file too large")
	}
	records, err := decodeCaptureRecords(body)
	if err != nil {
		return capturedSession{}, err
	}
	prompt, model, started, ended, tools := extractSessionFields(records)
	if started.IsZero() {
		started = info.ModTime().UTC()
	}
	if ended == nil {
		mod := info.ModTime().UTC()
		ended = &mod
	}
	key := stableSourceKey(path)
	return capturedSession{
		SessionID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("iter-capture:"+key)),
		Harness:   harness,
		Model:     fallback(model, "unknown"),
		Prompt:    sanitizePrompt(prompt),
		StartedAt: started.UTC(),
		EndedAt:   ended,
		Tools:     tools,
		SourceKey: key,
	}, nil
}

func decodeCaptureRecords(body []byte) ([]map[string]any, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, errors.New("empty capture file")
	}
	var records []map[string]any
	var single any
	if json.Unmarshal([]byte(trimmed), &single) == nil {
		flattenJSON(single, &records)
		return records, nil
	}
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), int(defaultCaptureMaxBytes))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item any
		if json.Unmarshal([]byte(line), &item) == nil {
			flattenJSON(item, &records)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, errors.New("no JSON records")
	}
	return records, nil
}

func flattenJSON(v any, out *[]map[string]any) {
	switch x := v.(type) {
	case map[string]any:
		*out = append(*out, x)
		for _, child := range x {
			flattenJSON(child, out)
		}
	case []any:
		for _, child := range x {
			flattenJSON(child, out)
		}
	}
}

func extractSessionFields(records []map[string]any) (prompt, model string, started time.Time, ended *time.Time, tools []string) {
	toolSet := map[string]struct{}{}
	for _, record := range records {
		if prompt == "" {
			prompt = firstCaptureString(record, "redacted_prompt", "prompt", "raw_prompt", "user_prompt", "input", "query")
		}
		if prompt == "" {
			prompt = messageContent(record)
		}
		if model == "" {
			model = firstCaptureString(record, "model", "model_name")
		}
		if started.IsZero() {
			started = firstCaptureTime(record, "started_at", "created_at", "timestamp", "time")
		}
		if ended == nil {
			if t := firstCaptureTime(record, "ended_at", "completed_at", "finished_at"); !t.IsZero() {
				ended = &t
			}
		}
		if tool := firstCaptureString(record, "tool", "tool_name", "name"); tool != "" {
			toolSet[tool] = struct{}{}
		}
	}
	for tool := range toolSet {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return prompt, model, started, ended, tools
}

func (s capturedSession) toEvents(now time.Time) []CaptureEvent {
	payload := map[string]any{
		"harness":         s.Harness,
		"model":           s.Model,
		"redacted_prompt": s.Prompt,
		"source_key":      s.SourceKey,
	}
	if len(s.Tools) > 0 {
		payload["tools"] = s.Tools
	}
	if s.EndedAt != nil {
		payload["ended_at"] = s.EndedAt.Format(time.RFC3339)
	}
	events := []CaptureEvent{{
		SessionID:  s.SessionID,
		EventType:  contracts.EventPromptSent,
		OccurredAt: s.StartedAt,
		Payload:    payload,
	}}
	if s.EndedAt != nil && !s.EndedAt.After(now.Add(captureCompleteAfter)) {
		events = append(events, CaptureEvent{
			SessionID:  s.SessionID,
			EventType:  contracts.EventSessionCompleted,
			OccurredAt: s.EndedAt.UTC(),
			Payload:    payload,
		})
	}
	return events
}

func captureWALEvents(events []CaptureEvent) []CaptureWALEvent {
	out := make([]CaptureWALEvent, 0, len(events))
	for _, event := range events {
		out = append(out, CaptureWALEvent{
			SessionID:  event.SessionID,
			EventType:  event.EventType,
			OccurredAt: event.OccurredAt,
			Payload:    event.Payload,
		})
	}
	return out
}

func captureEvent(event CaptureWALEvent) CaptureEvent {
	return CaptureEvent{
		SessionID:  event.SessionID,
		EventType:  event.EventType,
		OccurredAt: event.OccurredAt,
		Payload:    event.Payload,
	}
}

func firstCaptureString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := record[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func messageContent(record map[string]any) string {
	msg, ok := record["message"].(map[string]any)
	if !ok {
		return ""
	}
	if role, _ := msg["role"].(string); role != "" && role != "user" {
		return ""
	}
	if content, ok := msg["content"].(string); ok {
		return strings.TrimSpace(content)
	}
	return ""
}

func firstCaptureTime(record map[string]any, keys ...string) time.Time {
	for _, key := range keys {
		switch v := record[key].(type) {
		case string:
			if t := parseCaptureTime(v); !t.IsZero() {
				return t
			}
		case float64:
			if v > 0 {
				return time.Unix(int64(v), 0).UTC()
			}
		}
	}
	return time.Time{}
}

func parseCaptureTime(v string) time.Time {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password)\s*[:=]\s*["']?[^"'\s]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{20,}`),
}

func sanitizePrompt(prompt string) string {
	out := strings.TrimSpace(prompt)
	for _, pattern := range secretPatterns {
		out = pattern.ReplaceAllString(out, "[redacted-secret]")
	}
	if len(out) > 16*1024 {
		out = out[:16*1024]
	}
	return out
}

func stableSourceKey(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])
}

func expandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func fallback(value, fb string) string {
	if strings.TrimSpace(value) == "" {
		return fb
	}
	return value
}
