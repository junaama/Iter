package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/internal/redact"
	"github.com/iter-dev/iter/pkg/contracts"
)

const maxStackBodyBytes = 1 << 20

var envAssignmentRE = regexp.MustCompile(`[A-Z][A-Z0-9_]{4,}=`)

// StackMeHandler handles GET /v1/stack/me.
func StackMeHandler(deps app.Deps) http.HandlerFunc {
	return newStackHandler(deps).me
}

// StackUserHandler handles GET /v1/stack/{user_id}.
func StackUserHandler(deps app.Deps) http.HandlerFunc {
	return newStackHandler(deps).user
}

// StackCreateHandler handles POST /v1/stack.
func StackCreateHandler(deps app.Deps) http.HandlerFunc {
	return newStackHandler(deps).create
}

// StackShareHandler handles POST /v1/stack/{id}/share.
func StackShareHandler(deps app.Deps) http.HandlerFunc {
	return newStackHandler(deps).share
}

// StackUnshareHandler handles DELETE /v1/stack/{id}/share/{user_id}.
func StackUnshareHandler(deps app.Deps) http.HandlerFunc {
	return newStackHandler(deps).unshare
}

type stackHandler struct {
	logger *slog.Logger
}

func newStackHandler(deps app.Deps) stackHandler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return stackHandler{logger: logger}
}

func (h stackHandler) me(w http.ResponseWriter, r *http.Request) {
	principal, tx, ok := principalAndTx(w, r)
	if !ok {
		return
	}

	stacks, err := repo.ListByUser(r.Context(), tx, principal.UserID)
	if err != nil {
		h.serverError(w, r, "stack_list_me_failed", err)
		return
	}
	writeStackJSON(w, http.StatusOK, stackResponses(stacks))
}

func (h stackHandler) user(w http.ResponseWriter, r *http.Request) {
	principal, tx, ok := principalAndTx(w, r)
	if !ok {
		return
	}

	ownerID, err := parseUUIDParam(r, "user_id")
	if err != nil {
		writeStackError(w, http.StatusNotFound, "not_found")
		return
	}
	if !h.userInTenant(w, r, tx, principal.TenantID, ownerID, http.StatusNotFound, "not_found") {
		return
	}

	shared, err := repo.ListSharedWithUser(r.Context(), tx, principal.UserID)
	if err != nil {
		h.serverError(w, r, "stack_list_shared_failed", err)
		return
	}
	out := make([]repo.Stack, 0, len(shared))
	for _, s := range shared {
		if s.UserID == ownerID {
			out = append(out, s)
		}
	}
	writeStackJSON(w, http.StatusOK, stackResponses(out))
}

func (h stackHandler) create(w http.ResponseWriter, r *http.Request) {
	principal, tx, ok := principalAndTx(w, r)
	if !ok {
		return
	}

	raw, err := readStackBody(w, r)
	if err != nil {
		writeStackError(w, http.StatusRequestEntityTooLarge, "body_too_large")
		return
	}

	tier, safePayload, classifyErr := redact.Classify(raw)
	if tier == redact.Dirty {
		if classifyErr != nil {
			h.logger.WarnContext(r.Context(), "stack_classification_failed", "err", classifyErr)
		}
		writeClassificationFailed(w)
		return
	}

	var original contracts.StackUpsertRequest
	if err := decodeStrict(raw, &original); err != nil {
		writeStackError(w, http.StatusBadRequest, "malformed_body")
		return
	}
	if err := validateStackPayload(stackPayload(original)); err != nil {
		writeStackError(w, http.StatusUnprocessableEntity, "invalid_stack")
		return
	}
	if containsEnvAssignment(stackPayload(original)) {
		writeStackError(w, http.StatusUnprocessableEntity, "raw_config_forbidden")
		return
	}

	var req contracts.StackUpsertRequest
	if err := decodeStrict(safePayload, &req); err != nil {
		h.serverError(w, r, "stack_redacted_payload_invalid", err)
		return
	}
	payload := stackPayload(req)
	if err := validateStackPayload(payload); err != nil {
		writeStackError(w, http.StatusUnprocessableEntity, "invalid_stack")
		return
	}

	created, err := repo.CreateStack(r.Context(), tx, repo.Stack{
		TenantID:       principal.TenantID,
		UserID:         principal.UserID,
		Name:           payload.Name,
		Harnesses:      payload.Harnesses,
		Skills:         payload.Skills,
		Docs:           payload.Docs,
		Notes:          payload.Notes,
		Classification: tier.String(),
	})
	if err != nil {
		h.serverError(w, r, "stack_create_failed", err)
		return
	}
	writeStackJSON(w, http.StatusCreated, stackResponse(created))
}

func (h stackHandler) share(w http.ResponseWriter, r *http.Request) {
	principal, tx, ok := principalAndTx(w, r)
	if !ok {
		return
	}

	stackID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeStackError(w, http.StatusNotFound, "not_found")
		return
	}

	var req contracts.StackShareRequest
	if err := decodeJSONBody(w, r, &req); err != nil {
		writeStackError(w, http.StatusBadRequest, "malformed_body")
		return
	}
	if req.SharedWithUserID == uuid.Nil {
		writeStackError(w, http.StatusUnprocessableEntity, "invalid_stack_share")
		return
	}
	if !h.userInTenant(w, r, tx, principal.TenantID, req.SharedWithUserID, http.StatusUnprocessableEntity, "cross_tenant_share_forbidden") {
		return
	}

	stack, err := repo.GetStack(r.Context(), tx, stackID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeStackError(w, http.StatusNotFound, "not_found")
			return
		}
		h.serverError(w, r, "stack_get_for_share_failed", err)
		return
	}
	if stack.UserID != principal.UserID {
		writeStackError(w, http.StatusNotFound, "not_found")
		return
	}

	if err := repo.AddShare(r.Context(), tx, stackID, req.SharedWithUserID); err != nil {
		h.serverError(w, r, "stack_share_failed", err)
		return
	}
	if err := h.logStackShare(r, tx, principal, stackID, req.SharedWithUserID, repo.AuditEventStackShared); err != nil {
		h.serverError(w, r, "stack_share_audit_failed", err)
		return
	}
	writeStackJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h stackHandler) unshare(w http.ResponseWriter, r *http.Request) {
	principal, tx, ok := principalAndTx(w, r)
	if !ok {
		return
	}

	stackID, err := parseUUIDParam(r, "id")
	if err != nil {
		writeStackError(w, http.StatusNotFound, "not_found")
		return
	}
	targetUserID, err := parseUUIDParam(r, "user_id")
	if err != nil {
		writeStackError(w, http.StatusNotFound, "not_found")
		return
	}
	if !h.userInTenant(w, r, tx, principal.TenantID, targetUserID, http.StatusUnprocessableEntity, "cross_tenant_share_forbidden") {
		return
	}

	stack, err := repo.GetStack(r.Context(), tx, stackID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeStackError(w, http.StatusNotFound, "not_found")
			return
		}
		h.serverError(w, r, "stack_get_for_unshare_failed", err)
		return
	}
	if stack.UserID != principal.UserID {
		writeStackError(w, http.StatusNotFound, "not_found")
		return
	}

	if err := repo.RemoveShare(r.Context(), tx, stackID, targetUserID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.serverError(w, r, "stack_unshare_failed", err)
		return
	}
	if err := h.logStackShare(r, tx, principal, stackID, targetUserID, repo.AuditEventStackUnshared); err != nil {
		h.serverError(w, r, "stack_unshare_audit_failed", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h stackHandler) userInTenant(
	w http.ResponseWriter,
	r *http.Request,
	tx pgx.Tx,
	tenantID uuid.UUID,
	userID uuid.UUID,
	status int,
	code string,
) bool {
	if _, err := repo.GetTenantUser(r.Context(), tx, tenantID, userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeStackError(w, status, code)
			return false
		}
		h.serverError(w, r, "tenant_membership_lookup_failed", err)
		return false
	}
	return true
}

func (h stackHandler) logStackShare(
	r *http.Request,
	tx pgx.Tx,
	principal contracts.Principal,
	stackID uuid.UUID,
	targetUserID uuid.UUID,
	event string,
) error {
	targetKind := "stack"
	targetID := stackID.String()
	details, err := json.Marshal(map[string]string{
		"shared_with_user_id": targetUserID.String(),
	})
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	actorUserID := principal.UserID
	_, err = repo.InsertAuditLog(r.Context(), tx, repo.AuditLog{
		TenantID:    principal.TenantID,
		ActorUserID: &actorUserID,
		ActorKind:   repo.ActorKindUser,
		EventType:   event,
		TargetKind:  &targetKind,
		TargetID:    &targetID,
		Details:     details,
	})
	return err
}

func (h stackHandler) serverError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	h.logger.ErrorContext(r.Context(), msg, "err", err)
	writeStackError(w, http.StatusInternalServerError, "internal")
}

func principalAndTx(w http.ResponseWriter, r *http.Request) (contracts.Principal, pgx.Tx, bool) {
	principal, err := contracts.RequireAuth(r.Context())
	if err != nil {
		writeStackError(w, http.StatusUnauthorized, "invalid_token")
		return contracts.Principal{}, nil, false
	}
	tx := db.FromContext(r.Context())
	if tx == nil {
		writeStackError(w, http.StatusServiceUnavailable, "db_unavailable")
		return contracts.Principal{}, nil, false
	}
	return principal, tx, true
}

func parseUUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	return uuid.Parse(chi.URLParam(r, name))
}

func readStackBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(http.MaxBytesReader(w, r.Body, maxStackBodyBytes))
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) error {
	raw, err := readStackBody(w, r)
	if err != nil {
		return err
	}
	return decodeStrict(raw, dst)
}

func decodeStrict(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("multiple JSON values")
	}
	return nil
}

func validateStackPayload(p contracts.StackPayload) error {
	if len(p.Name) == 0 || len(p.Name) > 120 {
		return errors.New("name length out of range")
	}
	if len(p.Harnesses) == 0 {
		return errors.New("at least one harness required")
	}
	if p.Notes != nil && len(*p.Notes) > 10_000 {
		return errors.New("notes length out of range")
	}
	return nil
}

func containsEnvAssignment(p contracts.StackPayload) bool {
	for _, s := range stackPayloadStrings(p) {
		if envAssignmentRE.MatchString(s) {
			return true
		}
	}
	return false
}

func stackPayloadStrings(p contracts.StackPayload) []string {
	total := 1 + len(p.Harnesses) + len(p.Skills) + len(p.Docs)
	if p.Notes != nil {
		total++
	}
	out := make([]string, 0, total)
	out = append(out, p.Name)
	out = append(out, p.Harnesses...)
	out = append(out, p.Skills...)
	out = append(out, p.Docs...)
	if p.Notes != nil {
		out = append(out, *p.Notes)
	}
	return out
}

func stackPayload(req contracts.StackUpsertRequest) contracts.StackPayload {
	return contracts.StackPayload(req)
}

func stackResponses(stacks []repo.Stack) []contracts.StackResponse {
	out := make([]contracts.StackResponse, 0, len(stacks))
	for _, s := range stacks {
		out = append(out, stackResponse(s))
	}
	return out
}

func stackResponse(s repo.Stack) contracts.StackResponse {
	return contracts.StackResponse{
		ID:     s.ID,
		UserID: s.UserID,
		Payload: contracts.StackPayload{
			Name:      s.Name,
			Harnesses: append([]string(nil), s.Harnesses...),
			Skills:    append([]string(nil), s.Skills...),
			Docs:      append([]string(nil), s.Docs...),
			Notes:     s.Notes,
		},
		Classification: contracts.Classification(s.Classification),
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}

func writeStackJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeStackError(w http.ResponseWriter, status int, code string) {
	writeStackJSON(w, status, map[string]string{"error": code})
}

func writeClassificationFailed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	_, _ = w.Write([]byte(`{"error":"classification_failed","tier":"dirty"}`))
}
