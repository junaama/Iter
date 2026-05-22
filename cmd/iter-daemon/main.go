package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/iter-dev/iter/internal/daemon"
)

var version = "0.1.0"

func main() {
	socketPath := flag.String("socket", daemon.DefaultSocketPath(), "Unix socket path for local app IPC")
	appVersion := flag.String("app-version", os.Getenv("ITER_APP_VERSION"), "expected Mac app version for major-version guard")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server, err := daemon.NewServer(daemon.Config{
		SocketPath: *socketPath,
		Version:    version,
		AppVersion: *appVersion,
		Logger:     logger,
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
