package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	goredis "github.com/redis/go-redis/v9"

	iredis "github.com/iter-dev/iter/internal/redis"
	"github.com/iter-dev/iter/internal/ws"
	"github.com/iter-dev/iter/pkg/contracts"
)

// NewWSHandler builds the trace.event WebSocket handler. It ACKs the daemon
// only after XADD succeeds, preserving the gateway's ack-every-message
// contract without pretending an event is durable before Redis accepts it.
func NewWSHandler(client *goredis.Client, logger *slog.Logger, now func() time.Time) ws.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context, p contracts.Principal, env ws.Envelope, raw json.RawMessage) ws.Ack {
		if client == nil {
			logger.LogAttrs(ctx, slog.LevelError, "ingest_ws_redis_unavailable",
				slog.String("tenant_id", p.TenantID.String()))
			return ws.NewErrorAck(env.MsgID, "ingest_unavailable", now())
		}

		var in ws.Ingest
		if err := json.Unmarshal(raw, &in); err != nil {
			logger.LogAttrs(ctx, slog.LevelWarn, "ingest_ws_decode_failed",
				slog.String("tenant_id", p.TenantID.String()),
				slog.String("err", err.Error()))
			return ws.NewErrorAck(env.MsgID, "malformed_ingest", now())
		}
		if in.SessionID == zeroUUID || in.EventType == "" {
			return ws.NewErrorAck(env.MsgID, "malformed_ingest", now())
		}
		if _, err := parseEventType(in.EventType); err != nil {
			return ws.NewErrorAck(env.MsgID, "invalid_event_type", now())
		}

		q := QueuedEvent{
			TenantID:   p.TenantID,
			UserID:     p.UserID,
			MsgID:      env.MsgID,
			SessionID:  in.SessionID,
			EventID:    env.MsgID,
			EventType:  in.EventType,
			OccurredAt: in.OccurredAt,
			Payload:    in.Payload,
			ReceivedAt: now().UTC(),
		}
		body, err := json.Marshal(q)
		if err != nil {
			logger.LogAttrs(ctx, slog.LevelError, "ingest_ws_marshal_failed",
				slog.String("tenant_id", p.TenantID.String()),
				slog.String("err", err.Error()))
			return ws.NewErrorAck(env.MsgID, "handler_error", now())
		}
		stream := StreamName(p.TenantID)
		if err := iredis.EnsureStreamAndGroup(ctx, client, stream, ConsumerGroup); err != nil {
			logger.LogAttrs(ctx, slog.LevelError, "ingest_ws_group_create_failed",
				slog.String("stream", stream),
				slog.String("tenant_id", p.TenantID.String()),
				slog.String("err", err.Error()))
			return ws.NewErrorAck(env.MsgID, "ingest_enqueue_failed", now())
		}
		if err := client.XAdd(ctx, &goredis.XAddArgs{
			Stream: stream,
			Values: map[string]any{
				MessageField: string(body),
				RetriesField: 0,
			},
		}).Err(); err != nil {
			logger.LogAttrs(ctx, slog.LevelError, "ingest_ws_xadd_failed",
				slog.String("stream", stream),
				slog.String("tenant_id", p.TenantID.String()),
				slog.String("err", err.Error()))
			return ws.NewErrorAck(env.MsgID, "ingest_enqueue_failed", now())
		}
		return ws.NewAck(env.MsgID, now())
	}
}
