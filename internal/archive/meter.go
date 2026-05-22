package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Usage carries the current R2 bucket utilization, as fractions of each
// free-tier ceiling. A value > 1.0 means the bucket has already crossed
// that ceiling; the cron compares against R2_USAGE_ALERT_THRESHOLD
// (default 0.80) and pauses when ANY field is at or past the threshold.
//
// StorageFrac is the dominant signal for the archive cron specifically:
// the cron writes ~95 GB/month at v1 production scale (DECISIONS.md
// Phase 2), so storage saturates long before Class A ops do. Class A/B
// frac fields are still checked because a misbehaving sweep (e.g. a
// retry storm) could blow through ops budget before storage notices.
type Usage struct {
	StorageFrac float64
	ClassAFrac  float64
	ClassBFrac  float64
	MeasuredAt  time.Time
}

// MaxFrac returns the highest utilization across all metrics — the value
// the guardrail actually thresholds against.
func (u Usage) MaxFrac() float64 {
	m := u.StorageFrac
	if u.ClassAFrac > m {
		m = u.ClassAFrac
	}
	if u.ClassBFrac > m {
		m = u.ClassBFrac
	}
	return m
}

// UsageMeter reports current R2 bucket utilization. Implemented by the
// Cloudflare Analytics API in production; stubbed in tests so a single
// run can return 0.5, 0.9, 1.0 in sequence without juggling httptest
// servers.
type UsageMeter interface {
	// CurrentUsage returns the latest known utilization. Implementations
	// SHOULD cache (15-min sample cadence per deploy.md "Metric
	// collection") so the cron's single call per run does not become
	// a per-bucket API stampede.
	CurrentUsage(ctx context.Context) (Usage, error)
}

// MeterConfig describes the Cloudflare Analytics endpoint + bucket info
// needed to compute Usage. The free-tier ceilings live in env vars so
// staging can run with smaller caps for testing.
type MeterConfig struct {
	AccountID     string
	APIToken      string
	BucketName    string
	FreeStorageGB float64
	FreeClassAOps int64
	FreeClassBOps int64
	HTTPClient    *http.Client // optional; defaults to http.DefaultClient
}

// Validate fails closed on missing fields. We tolerate zero free-tier
// numbers (with a warning at call sites) so a paid-tier deploy can
// disable that particular check by setting the corresponding ceiling
// to 0 — divide-by-zero is handled by treating the metric as "unknown
// utilization" (Frac=0).
func (c MeterConfig) Validate() error {
	switch {
	case c.AccountID == "":
		return errors.New("archive.MeterConfig: AccountID required")
	case c.APIToken == "":
		return errors.New("archive.MeterConfig: APIToken required")
	case c.BucketName == "":
		return errors.New("archive.MeterConfig: BucketName required")
	}
	return nil
}

// cloudflareMeter is the production UsageMeter — one HTTP call per
// CurrentUsage to the Cloudflare REST API's r2/buckets/{name} endpoint
// (storage) plus one to the Analytics GraphQL gateway (class A/B ops).
//
// At v1 we only consume the storage call; Class A/B operations require
// the GraphQL endpoint which is not yet documented to the level needed
// for a robust integration. The Frac fields for ops are left at 0,
// which the MaxFrac() comparison treats as "not pressuring," matching
// the storage-dominant cost profile.
type cloudflareMeter struct {
	cfg    MeterConfig
	client *http.Client
}

// NewCloudflareMeter builds the production UsageMeter. The HTTPClient
// defaults to http.DefaultClient — fine here because a 5s timeout is
// applied per-request below.
func NewCloudflareMeter(cfg MeterConfig) (UsageMeter, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	c := cfg.HTTPClient
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	return &cloudflareMeter{cfg: cfg, client: c}, nil
}

// cfBucketResponse is the slice of Cloudflare's bucket GET payload that
// we actually parse. The full payload includes location, creation_date,
// storage_class etc.; we project just payload_size.
type cfBucketResponse struct {
	Result struct {
		PayloadSize int64 `json:"payload_size"`
	} `json:"result"`
	Success bool `json:"success"`
}

// CurrentUsage issues ONE HTTP call to the Cloudflare REST API and
// returns the storage fraction (ops fractions are 0 at v1 — see
// cloudflareMeter doc). One API call per cron run is well inside any
// reasonable rate limit and counts as a single Class B op against the
// bucket's own metering (deploy.md "R2 usage monitoring" budget).
func (m *cloudflareMeter) CurrentUsage(ctx context.Context) (Usage, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/r2/buckets/%s",
		m.cfg.AccountID, m.cfg.BucketName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Usage{}, fmt.Errorf("archive.cloudflareMeter: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.cfg.APIToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return Usage{}, fmt.Errorf("archive.cloudflareMeter: do: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Usage{}, fmt.Errorf("archive.cloudflareMeter: status %d", resp.StatusCode)
	}

	var body cfBucketResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Usage{}, fmt.Errorf("archive.cloudflareMeter: decode: %w", err)
	}
	if !body.Success {
		return Usage{}, errors.New("archive.cloudflareMeter: Cloudflare reported success=false")
	}

	const bytesPerGB = float64(1 << 30) // 1024^3, matches Cloudflare's GiB-billing
	storageGB := float64(body.Result.PayloadSize) / bytesPerGB
	frac := 0.0
	if m.cfg.FreeStorageGB > 0 {
		frac = storageGB / m.cfg.FreeStorageGB
	}

	return Usage{
		StorageFrac: frac,
		MeasuredAt:  time.Now().UTC(),
	}, nil
}
