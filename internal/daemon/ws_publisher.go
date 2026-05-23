package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/iter-dev/iter/internal/ws"
)

const publishTimeout = 10 * time.Second

type WSPublisher struct {
	endpoint string
	token    string
	tokenFn  func() string
	logger   *slog.Logger
	now      func() time.Time
}

func NewWSPublisher(endpoint, token string, tokenFn func() string, logger *slog.Logger, now func() time.Time) *WSPublisher {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &WSPublisher{
		endpoint: strings.TrimSpace(endpoint),
		token:    strings.TrimSpace(token),
		tokenFn:  tokenFn,
		logger:   logger,
		now:      now,
	}
}

func (p *WSPublisher) Publish(ctx context.Context, events []CaptureEvent) error {
	if len(events) == 0 {
		return nil
	}
	if p.endpoint == "" {
		return errors.New("websocket endpoint is required")
	}
	token := p.tokenValue()
	if token == "" {
		return errors.New("websocket token is required")
	}
	ctx, cancel := context.WithTimeout(ctx, publishTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, p.endpoint, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + token},
		},
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close(websocket.StatusNormalClosure, "capture batch complete")
	}()
	for _, event := range events {
		msgID := uuid.New()
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return err
		}
		msg := ws.Ingest{
			Envelope: ws.Envelope{
				Type:   ws.MessageTypeIngest,
				MsgID:  msgID,
				SentAt: p.now().UTC(),
			},
			SessionID:  event.SessionID,
			EventType:  string(event.EventType),
			OccurredAt: event.OccurredAt.UTC(),
			Payload:    payload,
		}
		body, err := json.Marshal(msg)
		if err != nil {
			return err
		}
		if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
			return err
		}
		if err := p.waitAck(ctx, conn, msgID); err != nil {
			return err
		}
	}
	return nil
}

func (p *WSPublisher) tokenValue() string {
	if p.token != "" {
		return p.token
	}
	if p.tokenFn == nil {
		return ""
	}
	return strings.TrimSpace(p.tokenFn())
}

func (p *WSPublisher) waitAck(ctx context.Context, conn *websocket.Conn, msgID uuid.UUID) error {
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		if typ != websocket.MessageText {
			return errors.New("unexpected binary websocket frame")
		}
		var env ws.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			return err
		}
		switch env.Type {
		case ws.MessageTypePing:
			pong := ws.NewPong(env.MsgID, p.now().UTC())
			body, err := json.Marshal(pong)
			if err != nil {
				return err
			}
			if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
				return err
			}
		case ws.MessageTypeAck:
			var ack ws.Ack
			if err := json.Unmarshal(data, &ack); err != nil {
				return err
			}
			if ack.AckMsgID != msgID {
				continue
			}
			if ack.Status != "ok" {
				if ack.Code == "" {
					return errors.New("server rejected capture event")
				}
				return errors.New("server rejected capture event: " + ack.Code)
			}
			return nil
		default:
			p.logger.Debug("daemon_capture_ws_ignored", "type", string(env.Type))
		}
	}
}
