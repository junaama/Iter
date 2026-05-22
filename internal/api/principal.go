package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

const (
	forbiddenAdminBody = `{"error":"forbidden","required_role":"admin"}`
	internalErrorBody  = `{"error":"internal_error"}`
)

// requireAdmin gates tenant dashboard endpoints to owner/admin memberships.
// It reads the current membership from tenant_users instead of trusting the
// optional JWT roles claim so role changes take effect on the next request.
func requireAdmin(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := contracts.RequireAuth(r.Context())
			if err != nil {
				writeAPIJSON(w, http.StatusUnauthorized, `{"error":"unauthenticated"}`)
				return
			}

			tx := db.FromContext(r.Context())
			if tx == nil {
				logger.ErrorContext(r.Context(), "admin_gate_missing_tenant_tx", "path", r.URL.Path)
				writeAPIJSON(w, http.StatusInternalServerError, internalErrorBody)
				return
			}

			membership, err := repo.GetTenantUser(r.Context(), tx, principal.TenantID, principal.UserID)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeAPIJSON(w, http.StatusForbidden, forbiddenAdminBody)
					return
				}
				logger.ErrorContext(r.Context(), "admin_gate_membership_lookup_failed", "path", r.URL.Path, "err", err)
				writeAPIJSON(w, http.StatusInternalServerError, internalErrorBody)
				return
			}
			if !isAdminRole(membership.Role) {
				writeAPIJSON(w, http.StatusForbidden, forbiddenAdminBody)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isAdminRole(role string) bool {
	return role == repo.RoleOwner || role == repo.RoleAdmin
}

func writeAPIJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
