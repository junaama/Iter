// verify-r2 is a single-shot smoke test for the Cloudflare R2 bucket
// provisioned by scripts/provision-r2.sh (issue 047).
//
// It uses the EXACT R2Config + ObjectStore construction the archive
// cron uses at runtime, so a passing run proves the SDK + endpoint +
// credentials work as a quartet before the cron starts writing.
//
// Invoked from scripts/verify-r2.sh; not a long-running process.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"time"

	"github.com/iter-dev/iter/internal/archive"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "verify-r2: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg := archive.R2Config{
		Endpoint:        os.Getenv("R2_ENDPOINT"),
		AccessKeyID:     os.Getenv("R2_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		Region:          os.Getenv("R2_REGION"),
	}
	bucket := os.Getenv("R2_ARCHIVE_BUCKET")
	if bucket == "" {
		return fmt.Errorf("R2_ARCHIVE_BUCKET is required")
	}

	store, err := archive.NewR2Store(ctx, cfg)
	if err != nil {
		return fmt.Errorf("construct r2 store: %w", err)
	}

	// 1 KiB random payload — large enough to exercise the full read
	// roundtrip, small enough to leave no measurable footprint on the
	// bucket's storage budget.
	body := make([]byte, 1024)
	if _, err := rand.Read(body); err != nil {
		return fmt.Errorf("generate payload: %w", err)
	}
	key := fmt.Sprintf("_verify/%d.bin", time.Now().UnixNano())

	fmt.Printf("PUT  %s/%s (%d bytes)\n", bucket, key, len(body))
	if err := store.PutObject(ctx, bucket, key, body); err != nil {
		return fmt.Errorf("put: %w", err)
	}

	fmt.Printf("GET  %s/%s\n", bucket, key)
	got, err := store.GetObject(ctx, bucket, key)
	if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	if !bytes.Equal(body, got) {
		return fmt.Errorf("round-trip mismatch: wrote %d bytes, read %d bytes",
			len(body), len(got))
	}

	fmt.Printf("DEL  %s/%s\n", bucket, key)
	if err := store.DeleteObject(ctx, bucket, key); err != nil {
		return fmt.Errorf("delete: %w", err)
	}

	return nil
}
