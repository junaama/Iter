//go:build integration

package embed

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/dbtest"
	"github.com/iter-dev/iter/internal/db/repo"
	iredis "github.com/iter-dev/iter/internal/redis"
)

func startEmbedRedis(ctx context.Context, t *testing.T) (*goredis.Client, func()) {
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
	return client, func() {
		_ = client.Close()
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate redis container: %v", err)
		}
	}
}

func TestWorkerIntegrationPersistsAndReplacesEmbedding(t *testing.T) {
	ctx := context.Background()
	tdb := dbtest.Setup(t, "../../migrations")
	defer tdb.Cleanup()
	redisClient, cleanup := startEmbedRedis(ctx, t)
	defer cleanup()

	tenantID := uuid.MustParse(tdb.SeedTenant(ctx, t, "embed-worker"))
	userID := uuid.MustParse(tdb.SeedUser(ctx, t, "worker@example.com", "Worker"))
	tdb.SeedMembership(ctx, t, tenantID.String(), userID.String(), "member")
	sessionID := uuid.MustParse(tdb.SeedSession(ctx, t, tenantID.String(), userID.String(), time.Now().UTC()))

	provider := &workerStubProvider{}
	router := NewRouter(RouterConfig{
		Providers: []Provider{provider},
		Priority:  []string{"stub"},
	})
	worker, err := NewWorker(WorkerConfig{
		DB:       tdb.AppPool,
		Redis:    redisClient,
		Embedder: router,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	first := Job{TenantID: tenantID, SessionID: sessionID, SourceText: "first"}
	second := Job{TenantID: tenantID, SessionID: sessionID, SourceText: "second"}
	for _, job := range []Job{first, second} {
		body, _ := json.Marshal(job)
		if err := redisClient.RPush(ctx, QueueName, string(body)).Err(); err != nil {
			t.Fatalf("RPush: %v", err)
		}
		if err := worker.ProcessOneBatch(ctx); err != nil {
			t.Fatalf("ProcessOneBatch: %v", err)
		}
	}

	if provider.callCount() != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.callCount())
	}
	if err := db.WithTenant(ctx, tdb.AppPool, tenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM session_embeddings WHERE session_id = $1`, sessionID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("embedding row count = %d, want 1", count)
		}
		embedding, err := repo.GetEmbeddingForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		if embedding.EmbeddingModel != DefaultModel {
			t.Fatalf("embedding model = %q, want %q", embedding.EmbeddingModel, DefaultModel)
		}
		if len(embedding.Vec) != repo.EmbeddingDim {
			t.Fatalf("embedding dim = %d, want %d", len(embedding.Vec), repo.EmbeddingDim)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
}
