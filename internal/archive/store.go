package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// ObjectStore is the narrow surface the archive cron needs from an
// S3-compatible store. Defining it here (rather than reaching for
// `*s3.Client` directly) lets the integration test pass an in-memory
// stub without spinning up MinIO or a real R2 bucket.
//
// The three methods are deliberately blob-only: the cron does not list,
// HEAD, or multipart-upload anything at v1. If a future caller needs
// those, add them here so the stub stays mechanically aligned with the
// real client.
type ObjectStore interface {
	// PutObject writes body to bucket/key. Returns an error if the
	// underlying client cannot reach the store after its own retries.
	PutObject(ctx context.Context, bucket, key string, body []byte) error

	// GetObject reads bucket/key into memory. Used by verify-r2 and any
	// future cross-bucket parity check; the cron itself does not GET
	// during the archive path (write-only).
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)

	// DeleteObject removes bucket/key. Used by verify-r2 only at v1;
	// the cron does not delete objects (Cloudflare lifecycle handles
	// IA transitions, and explicit retention purges are operator-run).
	DeleteObject(ctx context.Context, bucket, key string) error
}

// R2Config carries the connection parameters for the live Cloudflare R2
// client. Built by cmd/server from R2_* env vars (deploy.md "Cloudflare
// R2"). Zero-valued Region falls through to "auto" which is what R2
// expects (the service is region-less but the AWS SDK requires a string).
type R2Config struct {
	Endpoint        string // https://<account>.r2.cloudflarestorage.com
	AccessKeyID     string
	SecretAccessKey string
	Region          string // "auto" for R2; the SDK still requires it
}

// Validate fails closed on missing fields. Called by NewR2Store before
// any network roundtrip so a misconfigured boot exits immediately
// rather than after the first PutObject.
func (c R2Config) Validate() error {
	switch {
	case c.Endpoint == "":
		return errors.New("archive.R2Config: Endpoint required")
	case c.AccessKeyID == "":
		return errors.New("archive.R2Config: AccessKeyID required")
	case c.SecretAccessKey == "":
		return errors.New("archive.R2Config: SecretAccessKey required")
	}
	return nil
}

// r2Store is the production ObjectStore — aws-sdk-go-v2 talking to R2
// via the S3-compatible endpoint. Exported as an interface so tests
// never accidentally instantiate the real client.
type r2Store struct {
	client *s3.Client
}

// NewR2Store builds an ObjectStore backed by aws-sdk-go-v2's S3 client
// pointed at R2. UsePathStyle is forced ON because R2 does not support
// virtual-hosted-style addressing for buckets containing dots, and
// virtual-host is the default in aws-sdk-go-v2 v1.x. Path-style is the
// safe choice for every R2 bucket name we'll ever pick.
//
// The AWS SDK insists on a region string; we pass cfg.Region verbatim.
// R2's documented value is "auto".
func NewR2Store(ctx context.Context, cfg R2Config) (ObjectStore, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("archive.NewR2Store: load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		// Path-style addressing: required for R2. The default
		// virtual-hosted style fails on bucket names that contain
		// dots (e.g. iter-archive.staging) — picking path-style up
		// front means we don't have to remember that constraint at
		// bucket-naming time.
		o.UsePathStyle = true
	})

	return &r2Store{client: client}, nil
}

// PutObject uploads the body as a single non-multipart request. The
// archive cron's per-session payloads are bounded by `session_events`
// + embedding + scores + outcomes for one session — well under the
// 5 MiB multipart threshold even for a long session, so a single PUT
// is the right call shape.
func (s *r2Store) PutObject(ctx context.Context, bucket, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		return fmt.Errorf("archive.r2Store.PutObject %s/%s: %w", bucket, key, err)
	}
	return nil
}

func (s *r2Store) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("archive.r2Store.GetObject %s/%s: %w", bucket, key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("archive.r2Store.GetObject %s/%s: read body: %w", bucket, key, err)
	}
	return body, nil
}

func (s *r2Store) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("archive.r2Store.DeleteObject %s/%s: %w", bucket, key, err)
	}
	return nil
}
