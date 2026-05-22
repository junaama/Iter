package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerIPCMethods(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	server, err := NewServer(Config{SocketPath: socketPath, Version: "1.2.3", AppVersion: "1.0.0"})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ctx) }()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	defer conn.Close()

	reader := bufio.NewReader(conn)
	writeRequest(t, conn, "1", "ping")
	requireResponse(t, reader, "1", "ok", true)

	writeRequest(t, conn, "2", "version")
	requireResponse(t, reader, "2", "version", "1.2.3")

	writeRequest(t, conn, "3", "status")
	status := readResponse(t, reader, "3")
	requireResult(t, status, "running", true)
	requireResult(t, status, "paused", false)

	writeRequest(t, conn, "4", "pause")
	requireResponse(t, reader, "4", "paused", true)

	writeRequest(t, conn, "5", "status")
	requireResponse(t, reader, "5", "paused", true)

	writeRequest(t, conn, "6", "resume")
	requireResponse(t, reader, "6", "paused", false)

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket was not removed on shutdown: %v", err)
	}
}

func TestNewServerRejectsMajorVersionMismatch(t *testing.T) {
	_, err := NewServer(Config{SocketPath: filepath.Join(t.TempDir(), "daemon.sock"), Version: "2.0.0", AppVersion: "1.9.0"})
	if err == nil {
		t.Fatal("NewServer() error = nil, want major version mismatch")
	}
}

func TestServeRefusesNonSocketAtSocketPath(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "daemon.sock")
	if err := os.WriteFile(socketPath, []byte("not a socket"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	server, err := NewServer(Config{SocketPath: socketPath})
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("Serve() error = nil, want refusal to remove non-socket")
	}
}

func writeRequest(t *testing.T, conn net.Conn, id string, method string) {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"id": id, "method": method})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	payload = append(payload, '\n')
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
}

func requireResponse(t *testing.T, reader *bufio.Reader, id string, key string, want any) {
	t.Helper()
	res := readResponse(t, reader, id)
	requireResult(t, res, key, want)
}

func readResponse(t *testing.T, reader *bufio.Reader, id string) map[string]any {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("ReadBytes() error = %v", err)
	}
	var res struct {
		ID     string         `json:"id"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.Unmarshal(line, &res); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", string(line), err)
	}
	if res.ID != id {
		t.Fatalf("response id = %q, want %q", res.ID, id)
	}
	if res.Error != "" {
		t.Fatalf("response error = %q", res.Error)
	}
	return res.Result
}

func requireResult(t *testing.T, result map[string]any, key string, want any) {
	t.Helper()
	got, ok := result[key]
	if !ok {
		t.Fatalf("result[%q] missing in %#v", key, result)
	}
	if got != want {
		t.Fatalf("result[%q] = %#v, want %#v", key, got, want)
	}
}

func waitForSocket(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSocket != 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s was not created", path)
}
