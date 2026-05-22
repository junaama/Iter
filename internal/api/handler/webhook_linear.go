package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/iter-dev/iter/internal/api/webhook"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db/repo"
)

const (
	auditEventIncidentLinked   = "incident_linked"
	auditEventIncidentResolved = "incident_resolved"
)

// LinearWebhookHandler returns the HTTP handler mounted at
// POST /v1/webhooks/linear.
func LinearWebhookHandler(deps app.Deps) http.HandlerFunc {
	sink := &liveSink{pool: deps.DB}
	return linearWebhookHandler(deps.Logger, deps.Redis, deps.WebhookSecrets.Linear, sink, time.Now)
}

func linearWebhookHandler(
	logger *slog.Logger,
	rdb *goredis.Client,
	secret string,
	sink webhookSink,
	now func() time.Time,
) http.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}

	return webhook.Handler(webhook.Config{
		Source:          repo.PendingSourceLinear,
		Secret:          secret,
		SignatureHeader: "Linear-Signature",
		DeliveryHeader:  "Linear-Delivery",
		EventName:       webhook.JSONFieldEvent("type"),
		Logger:          logger,
		Redis:           rdb,
		Now:             now,
		Routes: map[string]webhook.EventHandler{
			"Issue": func(ctx context.Context, d webhook.Delivery) webhook.Response {
				return handleLinearIssue(ctx, logger, sink, d.Body, d.DeliveryID)
			},
			"Comment": func(ctx context.Context, d webhook.Delivery) webhook.Response {
				logger.DebugContext(ctx, "linear_comment_ignored", "delivery", d.DeliveryID)
				return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
			},
		},
	})
}

type linearEvent struct {
	Action string      `json:"action"`
	Type   string      `json:"type"`
	Data   linearIssue `json:"data"`
}

type linearIssue struct {
	ID          string       `json:"id"`
	Identifier  string       `json:"identifier"`
	URL         string       `json:"url"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Labels      linearLabels `json:"labels"`
	State       linearState  `json:"state"`
}

type linearState struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type linearLabel struct {
	Name string `json:"name"`
}

type linearLabels []linearLabel

func (l *linearLabels) UnmarshalJSON(body []byte) error {
	if string(body) == "null" {
		*l = nil
		return nil
	}
	var arr []linearLabel
	if err := json.Unmarshal(body, &arr); err == nil {
		*l = arr
		return nil
	}
	var conn struct {
		Nodes []linearLabel `json:"nodes"`
	}
	if err := json.Unmarshal(body, &conn); err != nil {
		return err
	}
	*l = conn.Nodes
	return nil
}

func handleLinearIssue(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	body []byte,
	deliveryID string,
) webhook.Response {
	var ev linearEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return webhook.JSON(http.StatusBadRequest, webhook.ErrMalformedBody)
	}

	switch {
	case isLinearCreate(ev.Action):
		return handleLinearIssueCreated(ctx, logger, sink, ev.Data, body, deliveryID)
	case isLinearUpdate(ev.Action) && ev.Data.isDone():
		return handleLinearIssueDone(ctx, logger, sink, ev.Data, body, deliveryID)
	default:
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}
}

func handleLinearIssueCreated(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	issue linearIssue,
	body []byte,
	deliveryID string,
) webhook.Response {
	if !issue.hasIncidentLabel() {
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}

	sessionID, ok := parseSessionMarker(issue.Description)
	if !ok {
		logger.DebugContext(ctx, "linear_incident_missing_session_marker", "delivery", deliveryID, "issue_id", issue.ID)
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}

	session, err := sink.LookupBySessionID(ctx, sessionID)
	if err != nil {
		bufferPending(ctx, logger, sink, repo.PendingOutcome{
			Source:     repo.PendingSourceLinear,
			DeliveryID: deliveryID,
			EventType:  "issue_created",
			Payload:    json.RawMessage(body),
		})
		return webhook.JSON(http.StatusOK, `{"status":"ok"}`)
	}

	externalRef := issue.externalRef()
	writeOutcome(ctx, logger, sink, session.TenantID, repo.Outcome{
		SessionID:   session.ID,
		TenantID:    session.TenantID,
		OutcomeType: repo.OutcomeIncidentCaused,
		ExternalRef: &externalRef,
		Details:     issue.details(deliveryID),
	})
	writeAudit(ctx, logger, sink, session.TenantID, webhookAuditEntry{
		EventType:  auditEventIncidentLinked,
		TargetKind: stringPtr("session"),
		TargetID:   stringPtr(session.ID.String()),
		Details:    issue.details(deliveryID),
	})

	return webhook.JSON(http.StatusOK, `{"status":"ok"}`)
}

func handleLinearIssueDone(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	issue linearIssue,
	body []byte,
	deliveryID string,
) webhook.Response {
	externalRef := issue.externalRef()
	if externalRef == "" {
		return webhook.JSON(http.StatusOK, `{"status":"ignored"}`)
	}

	outcome, err := sink.FindOutcomeByTypeRef(ctx, repo.OutcomeIncidentCaused, externalRef)
	if err != nil {
		logger.DebugContext(ctx, "linear_done_without_incident_outcome",
			"delivery", deliveryID,
			"issue_id", issue.ID,
			"err", err)
		bufferPending(ctx, logger, sink, repo.PendingOutcome{
			Source:     repo.PendingSourceLinear,
			DeliveryID: deliveryID,
			EventType:  "issue_done",
			Payload:    json.RawMessage(body),
		})
		return webhook.JSON(http.StatusOK, `{"status":"ok"}`)
	}

	writeAudit(ctx, logger, sink, outcome.TenantID, webhookAuditEntry{
		EventType:  auditEventIncidentResolved,
		TargetKind: stringPtr("session"),
		TargetID:   stringPtr(outcome.SessionID.String()),
		Details:    issue.details(deliveryID),
	})

	return webhook.JSON(http.StatusOK, `{"status":"ok"}`)
}

func (i linearIssue) hasIncidentLabel() bool {
	for _, label := range i.Labels {
		if strings.Contains(strings.ToLower(label.Name), "incident") {
			return true
		}
	}
	return false
}

func (i linearIssue) isDone() bool {
	return strings.EqualFold(i.State.Name, "done") || strings.EqualFold(i.State.Type, "completed")
}

func (i linearIssue) externalRef() string {
	if i.URL != "" {
		return i.URL
	}
	return i.ID
}

func (i linearIssue) details(deliveryID string) json.RawMessage {
	body, _ := json.Marshal(map[string]string{
		"delivery_id": deliveryID,
		"issue_id":    i.ID,
		"identifier":  i.Identifier,
		"url":         i.URL,
		"title":       i.Title,
	})
	return json.RawMessage(body)
}

func isLinearCreate(action string) bool {
	action = strings.ToLower(action)
	return action == "create" || action == "created"
}

func isLinearUpdate(action string) bool {
	action = strings.ToLower(action)
	return action == "update" || action == "updated"
}

func writeAudit(
	ctx context.Context,
	logger *slog.Logger,
	sink webhookSink,
	tenantID uuid.UUID,
	entry webhookAuditEntry,
) {
	if err := sink.InsertAudit(ctx, tenantID, entry); err != nil {
		logger.WarnContext(ctx, "webhook_audit_insert_failed",
			"event_type", entry.EventType,
			"tenant_id", tenantID.String(),
			"err", err)
	}
}

func stringPtr(s string) *string {
	return &s
}
