// Package webhook holds the shared receiver skeleton for public inbound
// webhooks. Source-specific handlers own payload mapping; this package owns
// raw-body reads, HMAC verification, delivery-id idempotency, and event routing.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

const (
	// MaxBody caps webhook payloads read into memory. GitHub and Linear
	// deliveries are small; 1 MiB leaves headroom without allowing OOMs.
	MaxBody = 1 << 20

	// IdempotencyTTL is the Redis SETNX window for source delivery IDs.
	IdempotencyTTL = 24 * time.Hour

	ErrInvalidSignature = `{"error":"invalid_signature"}`
	ErrMalformedBody    = `{"error":"malformed_body"}`
	ErrMissingDelivery  = `{"error":"missing_delivery_id"}`
)

// Delivery is the source-neutral envelope passed to event handlers after a
// delivery has passed HMAC verification and idempotency admission.
type Delivery struct {
	Source     string
	Event      string
	DeliveryID string
	Body       []byte
	Request    *http.Request
}

// Response is the small JSON response contract used by webhook handlers.
type Response struct {
	Status  int
	Body    string
	Headers map[string]string
}

// JSON returns a JSON response with the provided status and body.
func JSON(status int, body string) Response {
	return Response{Status: status, Body: body}
}

// EventHandler maps one verified delivery to a response.
type EventHandler func(context.Context, Delivery) Response

// EventNameFunc extracts the event name used for route dispatch.
type EventNameFunc func([]byte, *http.Request) (string, error)

// Config configures a source-specific webhook receiver.
type Config struct {
	Source          string
	Secret          string
	SignatureHeader string
	SignaturePrefix string
	DeliveryHeader  string
	EventName       EventNameFunc
	Routes          map[string]EventHandler
	Logger          *slog.Logger
	Redis           *goredis.Client
	Now             func() time.Time
}

// Handler builds the shared receive/verify/dedup/route pipeline.
func Handler(cfg Config) http.HandlerFunc {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	eventName := cfg.EventName
	if eventName == nil {
		eventName = func(_ []byte, _ *http.Request) (string, error) { return "", nil }
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
		if err != nil {
			logger.WarnContext(ctx, "webhook_read_failed", "source", cfg.Source, "err", err)
			writeJSON(w, JSON(http.StatusBadRequest, ErrMalformedBody))
			return
		}
		if len(body) > MaxBody {
			logger.WarnContext(ctx, "webhook_body_too_large", "source", cfg.Source, "bytes", len(body))
			writeJSON(w, JSON(http.StatusRequestEntityTooLarge, `{"error":"body_too_large"}`))
			return
		}

		signature := r.Header.Get(cfg.SignatureHeader)
		if !VerifyHMACSHA256(cfg.Secret, signature, body, cfg.SignaturePrefix) {
			logger.WarnContext(ctx, "webhook_signature_failed",
				"source", cfg.Source,
				"remote_addr", r.RemoteAddr,
				"delivery", r.Header.Get(cfg.DeliveryHeader))
			writeJSON(w, JSON(http.StatusUnauthorized, ErrInvalidSignature))
			return
		}

		deliveryID := r.Header.Get(cfg.DeliveryHeader)
		if deliveryID == "" {
			writeJSON(w, JSON(http.StatusBadRequest, ErrMissingDelivery))
			return
		}

		if cfg.Redis != nil {
			key := "webhook:" + cfg.Source + ":delivery:" + deliveryID
			set, err := cfg.Redis.SetNX(ctx, key, now().UTC().Format(time.RFC3339Nano), IdempotencyTTL).Result()
			if err != nil {
				logger.WarnContext(ctx, "webhook_idempotency_redis_failed",
					"source", cfg.Source,
					"delivery", deliveryID,
					"err", err)
			} else if !set {
				writeJSON(w, Response{
					Status:  http.StatusOK,
					Body:    `{"status":"ok","replay":true}`,
					Headers: map[string]string{"X-Idempotent-Replay": "true"},
				})
				return
			}
		}

		event, err := eventName(body, r)
		if err != nil {
			writeJSON(w, JSON(http.StatusBadRequest, ErrMalformedBody))
			return
		}
		route, ok := cfg.Routes[event]
		if !ok {
			logger.InfoContext(ctx, "webhook_event_ignored", "source", cfg.Source, "event", event)
			writeJSON(w, JSON(http.StatusOK, `{"status":"ignored"}`))
			return
		}

		writeJSON(w, route(ctx, Delivery{
			Source:     cfg.Source,
			Event:      event,
			DeliveryID: deliveryID,
			Body:       body,
			Request:    r,
		}))
	}
}

// VerifyHMACSHA256 verifies a hex HMAC-SHA256 signature in constant time.
// signaturePrefix is required when non-empty (GitHub uses "sha256="; Linear
// sends bare hex).
func VerifyHMACSHA256(secret, signature string, body []byte, signaturePrefix string) bool {
	if secret == "" || signature == "" {
		return false
	}
	if signaturePrefix != "" {
		if len(signature) < len(signaturePrefix) || signature[:len(signaturePrefix)] != signaturePrefix {
			return false
		}
		signature = signature[len(signaturePrefix):]
	}
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(got, expected)
}

// HeaderEvent returns an EventNameFunc that dispatches from an HTTP header.
func HeaderEvent(header string) EventNameFunc {
	return func(_ []byte, r *http.Request) (string, error) {
		return r.Header.Get(header), nil
	}
}

// JSONFieldEvent returns an EventNameFunc that dispatches from a top-level
// string JSON field, such as Linear's `type`.
func JSONFieldEvent(field string) EventNameFunc {
	return func(body []byte, _ *http.Request) (string, error) {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(body, &envelope); err != nil {
			return "", err
		}
		var event string
		if raw := envelope[field]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &event); err != nil {
				return "", err
			}
		}
		return event, nil
	}
}

func writeJSON(w http.ResponseWriter, resp Response) {
	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.Status)
	_, _ = io.WriteString(w, resp.Body)
}
