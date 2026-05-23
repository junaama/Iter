package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/app"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/pkg/contracts"
)

const onboardingMaxBodyBytes = 8 * 1024

type onboardingTenantMatch struct {
	TenantID    uuid.UUID `json:"tenant_id"`
	Name        string    `json:"name"`
	MemberCount int       `json:"member_count"`
}

type onboardingTenantDomainResponse struct {
	Domain string                 `json:"domain"`
	Match  *onboardingTenantMatch `json:"match,omitempty"`
}

type onboardingWorkspaceRequest struct {
	Name string `json:"name"`
}

type onboardingWorkspaceResponse struct {
	TenantID uuid.UUID `json:"tenant_id"`
	Name     string    `json:"name"`
	Status   string    `json:"status"`
}

type onboardingJoinRequestRequest struct {
	TenantID uuid.UUID `json:"tenant_id"`
}

type onboardingJoinRequestResponse struct {
	RequestID  uuid.UUID `json:"request_id"`
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantName string    `json:"tenant_name"`
	Status     string    `json:"status"`
}

// OnboardingTenantDomainHandler looks for an existing active tenant with at
// least one member whose email domain matches the supplied domain. The current
// user's own tenant is excluded so first-run users see "create workspace"
// unless a teammate already exists elsewhere.
func OnboardingTenantDomainHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}
		tx, err := db.RequireTx(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "onboarding_domain_missing_tenant_tx")
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			return
		}
		domain := normalizeEmailDomain(r.URL.Query().Get("domain"))
		if domain == "" {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_domain"})
			return
		}

		match, err := findTenantByDomain(r.Context(), tx, domain, principal.TenantID)
		if err != nil {
			logger.ErrorContext(r.Context(), "onboarding_domain_lookup_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			return
		}
		respond.JSON(w, http.StatusOK, onboardingTenantDomainResponse{Domain: domain, Match: match})
	}
}

// OnboardingWorkspaceHandler renames the personal tenant minted during
// auth-session exchange into the user's chosen workspace.
func OnboardingWorkspaceHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}
		tx, err := db.RequireTx(r.Context())
		if err != nil {
			logger.ErrorContext(r.Context(), "onboarding_workspace_missing_tenant_tx")
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			return
		}
		var req onboardingWorkspaceRequest
		if err := parseOnboardingJSON(r, &req); err != nil {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_request"})
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" || len(name) > 120 {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_workspace_name"})
			return
		}

		var resp onboardingWorkspaceResponse
		err = tx.QueryRow(r.Context(), `
			UPDATE tenants
			   SET name = $2
			 WHERE id = $1
			   AND deleted_at IS NULL
			RETURNING id, name
		`, principal.TenantID, name).Scan(&resp.TenantID, &resp.Name)
		if err != nil {
			logger.ErrorContext(r.Context(), "onboarding_workspace_update_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			return
		}
		resp.Status = "ready"
		respond.JSON(w, http.StatusOK, resp)
	}
}

// OnboardingTenantJoinRequestHandler records the first-run join intent as an
// audit event in the target tenant. Persistent request queues and admin
// approval actions are tracked as the follow-up from issue 067.
func OnboardingTenantJoinRequestHandler(deps app.Deps) http.HandlerFunc {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(w http.ResponseWriter, r *http.Request) {
		principal, err := contracts.RequireAuth(r.Context())
		if err != nil {
			respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
			return
		}
		var req onboardingJoinRequestRequest
		if err := parseOnboardingJSON(r, &req); err != nil || req.TenantID == uuid.Nil {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "invalid_request"})
			return
		}
		if req.TenantID == principal.TenantID {
			respond.JSON(w, http.StatusBadRequest, respond.Error{Error: "already_in_tenant"})
			return
		}
		if deps.DB == nil {
			respond.JSON(w, http.StatusServiceUnavailable, respond.Error{Error: "db_unavailable"})
			return
		}

		requestID := uuid.New()
		var tenantName string
		err = db.WithTenant(r.Context(), deps.DB, req.TenantID.String(), func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx, `
				SELECT name
				  FROM tenants
				 WHERE id = $1
				   AND deleted_at IS NULL
			`, req.TenantID).Scan(&tenantName); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO audit_log (
				  tenant_id, actor_user_id, actor_kind, event_type,
				  target_kind, target_id, details
				)
				VALUES (
				  $1, $2, 'user', 'admin_action',
				  'tenant_join_request', $3,
				  jsonb_build_object('status', 'pending', 'source', 'mac_onboarding')
				)
			`, req.TenantID, principal.UserID, requestID.String())
			return err
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				respond.JSON(w, http.StatusNotFound, respond.Error{Error: "tenant_not_found"})
				return
			}
			logger.ErrorContext(r.Context(), "onboarding_join_request_failed", "err", err)
			respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal"})
			return
		}

		respond.JSON(w, http.StatusAccepted, onboardingJoinRequestResponse{
			RequestID:  requestID,
			TenantID:   req.TenantID,
			TenantName: tenantName,
			Status:     "pending_admin_approval",
		})
	}
}

func findTenantByDomain(ctx context.Context, tx pgx.Tx, domain string, currentTenant uuid.UUID) (*onboardingTenantMatch, error) {
	var match onboardingTenantMatch
	err := tx.QueryRow(ctx, `
		SELECT t.id, t.name, count(DISTINCT tu_all.user_id)::int AS member_count
		  FROM tenants t
		  JOIN tenant_users tu_match ON tu_match.tenant_id = t.id
		  JOIN users u_match ON u_match.id = tu_match.user_id
		  LEFT JOIN tenant_users tu_all ON tu_all.tenant_id = t.id
		 WHERE t.deleted_at IS NULL
		   AND t.id <> $2
		   AND lower(split_part(u_match.email::text, '@', 2)) = $1
		 GROUP BY t.id, t.name, t.created_at
		 ORDER BY member_count DESC, t.created_at ASC
		 LIMIT 1
	`, domain, currentTenant).Scan(&match.TenantID, &match.Name, &match.MemberCount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &match, nil
}

func parseOnboardingJSON(r *http.Request, out any) error {
	limited := http.MaxBytesReader(nil, r.Body, onboardingMaxBodyBytes)
	defer limited.Close()
	return json.NewDecoder(limited).Decode(out)
}

func normalizeEmailDomain(raw string) string {
	domain := strings.ToLower(strings.TrimSpace(raw))
	domain = strings.TrimPrefix(domain, "@")
	if strings.Contains(domain, "@") {
		parts := strings.Split(domain, "@")
		domain = parts[len(parts)-1]
	}
	if domain == "" || strings.ContainsAny(domain, "/:\\") || !strings.Contains(domain, ".") {
		return ""
	}
	return domain
}
