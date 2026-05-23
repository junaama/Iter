package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/iter-dev/iter/internal/daemon"
)

var version = "0.1.0"

func main() {
	socketPath := flag.String("socket", daemon.DefaultSocketPath(), "Unix socket path for local app IPC")
	appVersion := flag.String("app-version", os.Getenv("ITER_APP_VERSION"), "expected Mac app version for major-version guard")
	apiBaseURL := flag.String("api-base-url", getenv("ITER_API_BASE_URL", "http://127.0.0.1:8080"), "Iter API base URL used to derive /v1/ws")
	wsURL := flag.String("ws-url", os.Getenv("ITER_WS_URL"), "Iter WebSocket URL; overrides api-base-url")
	apiToken := flag.String("api-token", os.Getenv("ITER_API_TOKEN"), "Iter session JWT for daemon ingest")
	captureDirs := flag.String("capture-dirs", os.Getenv("ITER_CAPTURE_DIRS"), "capture roots as harness=path entries separated by PATHLISTSEP")
	captureWAL := flag.String("capture-wal", getenv("ITER_CAPTURE_WAL_PATH", daemon.DefaultCaptureWALPath()), "SQLite WAL path for durable local capture replay")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	dirs, err := daemon.ParseHarnessDirs(*captureDirs)
	if err != nil {
		logger.Error("invalid capture dirs", "error", err)
		os.Exit(1)
	}
	server, err := daemon.NewServer(daemon.Config{
		SocketPath: *socketPath,
		Version:    version,
		AppVersion: *appVersion,
		Capture: daemon.CaptureConfig{
			APIBaseURL: *apiBaseURL,
			WSEndpoint: *wsURL,
			APIToken:   *apiToken,
			TokenFunc:  keychainAPIToken,
			WALPath:    *captureWAL,
			Dirs:       dirs,
		},
		Logger: logger,
	})
	if err != nil {
		logger.Error("daemon refused to start", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("iter daemon starting", "socket", *socketPath, "version", version)
	if err := server.Serve(ctx); err != nil {
		logger.Error("iter daemon stopped", "error", err)
		os.Exit(1)
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func keychainAPIToken() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	out, err := exec.Command(
		"/usr/bin/security",
		"find-generic-password",
		"-s", "dev.iter.IterApp",
		"-a", "access_token",
		"-w",
	).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
