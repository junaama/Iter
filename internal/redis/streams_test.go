//go:build integration

// Integration tests for the Streams + DLQ helpers, run against a real
// redis:7-alpine container via testcontainers-go. The unit-test surface
// is empty on purpose: every public helper is a thin wrapper over a
// Redis command, and mocking the command layer would just re-test
// go-redis. The cheaper, higher-confidence path is a real container.
//
// Gated by the `integration` build tag so `go test ./...` skips it.
// Run with `make test-redis`.
package redis_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	iredis "github.com/iter-dev/iter/internal/redis"
)

// startRedis spins up a redis:7-alpine container and returns a ready
// client + cleanup func. Failure aborts the test (no point continuing
// without Redis).
func startRedis(ctx context.Context, t *testing.T) (*goredis.Client, func()) {
	t.Helper()
	container, err := tcredis.Run(ctx, "redis:7-alpine")
	if err != nil {
		t.Fatalf("start redis container: %v", err)
	}
	uri, err := container.ConnectionString(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("redis ConnectionString: %v", err)
	}
	cfg, err := iredis.ConfigFromURL(uri)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("ConfigFromURL: %v", err)
	}
	client, err := iredis.NewClient(ctx, cfg)
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		t.Fatalf("NewClient: %v", err)
	}
	cleanup := func() {
		_ = client.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate redis container: %v", err)
		}
	}
	return client, cleanup
}

func TestEnsureStreamAndGroupIdempotent(t *testing.T) {
	ctx := context.Background()
	client, cleanup := startRedis(ctx, t)
	defer cleanup()

	const stream, group = "test:stream:ensure", "g1"
	if err := iredis.EnsureStreamAndGroup(ctx, client, stream, group); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	// Second call MUST be a no-op, not a BUSYGROUP error.
	if err := iredis.EnsureStreamAndGroup(ctx, client, stream, group); err != nil {
		t.Fatalf("second ensure (should be idempotent): %v", err)
	}
}

func TestReadGroupAndAck(t *testing.T) {
	ctx := context.Background()
	client, cleanup := startRedis(ctx, t)
	defer cleanup()

	const stream, group, consumer = "test:stream:read", "g1", "c1"
	if err := iredis.EnsureStreamAndGroup(ctx, client, stream, group); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	id, err := client.XAdd(ctx, &goredis.XAddArgs{Stream: stream, Values: map[string]any{"k": "v"}}).Result()
	if err != nil {
		t.Fatalf("XADD: %v", err)
	}

	msgs, err := iredis.ReadGroup(ctx, client, stream, group, consumer, 10, 500*time.Millisecond)
	if err != nil {
		t.Fatalf("ReadGroup: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ID != id {
		t.Fatalf("expected 1 msg with id=%s, got %+v", id, msgs)
	}
	if got := msgs[0].Values["k"]; got != "v" {
		t.Fatalf("expected k=v, got %v", got)
	}

	// Before ACK, the message is in the PEL: XPENDING reports count=1.
	pendingBefore, err := client.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("XPENDING before ACK: %v", err)
	}
	if pendingBefore.Count != 1 {
		t.Fatalf("expected pending=1 before ACK, got %d", pendingBefore.Count)
	}

	if err := iredis.Ack(ctx, client, stream, group, id); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	pendingAfter, err := client.XPending(ctx, stream, group).Result()
	if err != nil {
		t.Fatalf("XPENDING after ACK: %v", err)
	}
	if pendingAfter.Count != 0 {
		t.Fatalf("expected pending=0 after ACK, got %d", pendingAfter.Count)
	}
}

func TestClaimStuckMessage(t *testing.T) {
	ctx := context.Background()
	client, cleanup := startRedis(ctx, t)
	defer cleanup()

	const stream, group = "test:stream:claim", "g1"
	if err := iredis.EnsureStreamAndGroup(ctx, client, stream, group); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	id, err := client.XAdd(ctx, &goredis.XAddArgs{Stream: stream, Values: map[string]any{"k": "v"}}).Result()
	if err != nil {
		t.Fatalf("XADD: %v", err)
	}
	// consumer A reads → message lands in PEL under A.
	if _, err := iredis.ReadGroup(ctx, client, stream, group, "A", 10, 100*time.Millisecond); err != nil {
		t.Fatalf("ReadGroup A: %v", err)
	}
	// Wait past the min-idle window before B claims.
	time.Sleep(150 * time.Millisecond)

	claimed, err := iredis.Claim(ctx, client, stream, group, "B", 100*time.Millisecond, []string{id})
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != id {
		t.Fatalf("expected to claim id=%s, got %+v", id, claimed)
	}
}

func TestDLQPushAndList(t *testing.T) {
	ctx := context.Background()
	client, cleanup := startRedis(ctx, t)
	defer cleanup()

	const stream = "test:stream:dlq"
	if got, want := iredis.DLQName(stream), "dlq:"+stream; got != want {
		t.Fatalf("DLQName: got %s want %s", got, want)
	}

	const origID = "1700000000000-0"
	payload := map[string]any{"k": "v", "n": "42"}
	pushedID, err := iredis.PushDLQ(ctx, client, stream, origID, "consumer-A", payload, "boom")
	if err != nil {
		t.Fatalf("PushDLQ: %v", err)
	}
	if pushedID == "" {
		t.Fatal("PushDLQ returned empty id")
	}

	entries, err := iredis.ListDLQ(ctx, client, stream, 10)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(entries))
	}
	e := entries[0]
	if e.OriginalID != origID {
		t.Fatalf("OriginalID: got %s want %s", e.OriginalID, origID)
	}
	if e.OriginalStream != stream {
		t.Fatalf("OriginalStream: got %s want %s", e.OriginalStream, stream)
	}
	if e.Consumer != "consumer-A" {
		t.Fatalf("Consumer: got %s want consumer-A", e.Consumer)
	}
	if e.Error != "boom" {
		t.Fatalf("Error: got %s want boom", e.Error)
	}
	if e.FailedAt.IsZero() {
		t.Fatal("FailedAt should be set")
	}
	if got, want := e.Payload["k"], "v"; got != want {
		t.Fatalf("payload.k: got %s want %s", got, want)
	}
	if got, want := e.Payload["n"], "42"; got != want {
		t.Fatalf("payload.n: got %s want %s", got, want)
	}
}

// Failure-mode smoke: NewClient against a bogus addr fails fast.
func TestNewClientFailsFastOnUnreachable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := iredis.NewClient(ctx, iredis.Config{Addr: "127.0.0.1:1", DialTimeout: 250 * time.Millisecond})
	if err == nil {
		t.Fatal("expected dial error against unreachable addr")
	}
	if got := fmt.Sprintf("%v", err); got == "" {
		t.Fatal("expected non-empty error")
	}
}
