package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// DLQ envelope field names. Kept as exported constants so consumers
// (ops tooling, replayers, the future dashboard "stuck messages" view)
// can read them without re-declaring the magic strings.
const (
	DLQFieldOriginalID     = "original_id"
	DLQFieldOriginalStream = "original_stream"
	DLQFieldConsumer       = "consumer"
	DLQFieldError          = "error"
	DLQFieldFailedAt       = "failed_at_unix_ms"
	DLQFieldPayloadPrefix  = "payload."
)

// dlqPrefix is the only place the `dlq:` namespace is spelled. Per
// DECISIONS.md Phase 7 the convention is `dlq:*`; centralising it here
// means a typo can't drift the namespace across producers and the ops
// inspector.
const dlqPrefix = "dlq:"

// DLQName returns the dead-letter stream name for a given source stream
// per the `dlq:<stream>` convention from DECISIONS.md Phase 7.
func DLQName(stream string) string {
	return dlqPrefix + stream
}

// DLQEntry is the parsed projection of one dead-letter record. Payload
// is the original XADD field/value map of the failed message (string
// values, as Redis stores them); the rest is the failure metadata.
type DLQEntry struct {
	// ID is the XADD id assigned to the DLQ entry itself (NOT the
	// original message id — that's OriginalID below).
	ID             string
	OriginalID     string
	OriginalStream string
	Consumer       string
	Error          string
	FailedAt       time.Time
	Payload        map[string]string
}

// PushDLQ writes a failure record to dlq:<originalStream>. The payload
// fields from the original message are flattened with a `payload.` prefix
// so an XRANGE inspection shows them inline rather than nested in JSON.
//
// errMsg is the human-readable failure reason; passing an empty string
// is allowed (some callers only have a panic-recovered value with no
// message), but discouraged.
func PushDLQ(
	ctx context.Context,
	client *goredis.Client,
	originalStream, originalID, consumer string,
	payload map[string]any,
	errMsg string,
) (string, error) {
	if client == nil {
		return "", errors.New("redis: nil client")
	}
	if originalStream == "" || originalID == "" {
		return "", errors.New("redis: originalStream and originalID are required")
	}
	values := make(map[string]any, len(payload)+5)
	values[DLQFieldOriginalID] = originalID
	values[DLQFieldOriginalStream] = originalStream
	values[DLQFieldConsumer] = consumer
	values[DLQFieldError] = errMsg
	values[DLQFieldFailedAt] = strconv.FormatInt(time.Now().UnixMilli(), 10)
	for k, v := range payload {
		values[DLQFieldPayloadPrefix+k] = v
	}
	id, err := client.XAdd(ctx, &goredis.XAddArgs{
		Stream: DLQName(originalStream),
		Values: values,
	}).Result()
	if err != nil {
		return "", fmt.Errorf("redis: XADD %s: %w", DLQName(originalStream), err)
	}
	return id, nil
}

// ListDLQ returns the most recent n DLQ entries for the given source
// stream. Uses XREVRANGE so the newest entries come first — ops looking
// at a backlog almost always wants the freshest failure first.
//
// n<=0 returns nothing (callers should pass an explicit batch).
func ListDLQ(ctx context.Context, client *goredis.Client, originalStream string, n int64) ([]DLQEntry, error) {
	if client == nil {
		return nil, errors.New("redis: nil client")
	}
	if originalStream == "" {
		return nil, errors.New("redis: originalStream is required")
	}
	if n <= 0 {
		return nil, nil
	}
	res, err := client.XRevRangeN(ctx, DLQName(originalStream), "+", "-", n).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: XREVRANGE %s: %w", DLQName(originalStream), err)
	}
	out := make([]DLQEntry, 0, len(res))
	for _, m := range res {
		entry := DLQEntry{ID: m.ID, Payload: make(map[string]string)}
		for k, v := range m.Values {
			s, _ := v.(string)
			switch k {
			case DLQFieldOriginalID:
				entry.OriginalID = s
			case DLQFieldOriginalStream:
				entry.OriginalStream = s
			case DLQFieldConsumer:
				entry.Consumer = s
			case DLQFieldError:
				entry.Error = s
			case DLQFieldFailedAt:
				if ms, perr := strconv.ParseInt(s, 10, 64); perr == nil {
					entry.FailedAt = time.UnixMilli(ms)
				}
			default:
				if len(k) > len(DLQFieldPayloadPrefix) && k[:len(DLQFieldPayloadPrefix)] == DLQFieldPayloadPrefix {
					entry.Payload[k[len(DLQFieldPayloadPrefix):]] = s
				}
			}
		}
		out = append(out, entry)
	}
	return out, nil
}
