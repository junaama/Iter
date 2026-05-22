package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// Msg is the projection of a single Redis Streams entry returned by
// ReadGroup. The Values map mirrors XADD field/value pairs verbatim; the
// caller decides the schema (JSON-in-a-single-field, fielded, etc.). This
// package does not interpret payloads.
type Msg struct {
	Stream string
	ID     string
	Values map[string]any
}

// EnsureStreamAndGroup is the idempotent XGROUP CREATE ... MKSTREAM call.
// The stream is created if missing, the group is created at the tail
// ("$") if missing, and BUSYGROUP (group already exists) is swallowed.
//
// Idempotency matters because every consumer process calls this on boot;
// returning an error when the group already exists would force every
// caller to inspect strings.
func EnsureStreamAndGroup(ctx context.Context, client *goredis.Client, stream, group string) error {
	if client == nil {
		return errors.New("redis: nil client")
	}
	if stream == "" || group == "" {
		return errors.New("redis: stream and group are required")
	}
	// "$" means "deliver only messages added after this group was created"
	// — exactly what new consumer groups want; replay of historical
	// entries is an explicit ops action via XGROUP SETID, not the default.
	err := client.XGroupCreateMkStream(ctx, stream, group, "$").Err()
	if err == nil {
		return nil
	}
	// BUSYGROUP is the Redis-defined error for "group already exists".
	// Any go-redis version reports it as a plain error string; matching
	// the substring is the documented pattern (no typed error).
	if strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("redis: XGROUP CREATE MKSTREAM %s %s: %w", stream, group, err)
}

// ReadGroup wraps XREADGROUP with one stream and one consumer. count is
// the max messages to return per call; block is the long-poll budget
// (zero means non-blocking). Returns an empty slice (no error) when the
// block timeout expires without new messages — that's the steady-state
// idle case, not a failure.
func ReadGroup(
	ctx context.Context,
	client *goredis.Client,
	stream, group, consumer string,
	count int64,
	block time.Duration,
) ([]Msg, error) {
	if client == nil {
		return nil, errors.New("redis: nil client")
	}
	if stream == "" || group == "" || consumer == "" {
		return nil, errors.New("redis: stream, group, consumer are required")
	}
	res, err := client.XReadGroup(ctx, &goredis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err != nil {
		// go-redis returns redis.Nil when the BLOCK timeout elapses with
		// nothing new. That's the idle case, not a failure: return empty.
		if errors.Is(err, goredis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("redis: XREADGROUP %s %s: %w", stream, group, err)
	}
	// One stream requested => one result entry expected. Defensive copy
	// into our own Msg shape so callers don't reach into go-redis types.
	out := make([]Msg, 0)
	for _, s := range res {
		for _, m := range s.Messages {
			values := make(map[string]any, len(m.Values))
			for k, v := range m.Values {
				values[k] = v
			}
			out = append(out, Msg{Stream: s.Stream, ID: m.ID, Values: values})
		}
	}
	return out, nil
}

// Ack is XACK for a single message id. Multiple ids per call is a
// micro-optimization not needed at v1 throughput; the call site loops if
// it has a batch.
func Ack(ctx context.Context, client *goredis.Client, stream, group, id string) error {
	if client == nil {
		return errors.New("redis: nil client")
	}
	if stream == "" || group == "" || id == "" {
		return errors.New("redis: stream, group, id are required")
	}
	if _, err := client.XAck(ctx, stream, group, id).Result(); err != nil {
		return fmt.Errorf("redis: XACK %s %s %s: %w", stream, group, id, err)
	}
	return nil
}

// Claim wraps XCLAIM for taking over stuck (pending-too-long) messages.
// minIdle is the minimum idle duration a message must have spent in the
// PEL before this consumer is allowed to steal it. Returns the messages
// successfully claimed (which may be a subset of the requested ids if
// some were already ACKed or re-delivered to another consumer).
func Claim(
	ctx context.Context,
	client *goredis.Client,
	stream, group, consumer string,
	minIdle time.Duration,
	ids []string,
) ([]Msg, error) {
	if client == nil {
		return nil, errors.New("redis: nil client")
	}
	if stream == "" || group == "" || consumer == "" {
		return nil, errors.New("redis: stream, group, consumer are required")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	res, err := client.XClaim(ctx, &goredis.XClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Messages: ids,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: XCLAIM %s %s: %w", stream, group, err)
	}
	out := make([]Msg, 0, len(res))
	for _, m := range res {
		values := make(map[string]any, len(m.Values))
		for k, v := range m.Values {
			values[k] = v
		}
		out = append(out, Msg{Stream: stream, ID: m.ID, Values: values})
	}
	return out, nil
}
