package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/iter-dev/iter/internal/api/authz"
	"github.com/iter-dev/iter/internal/api/respond"
	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/pkg/contracts"
)

// requireAdmin gates tenant dashboard endpoints to owner/admin memberships.
func requireAdmin(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := contracts.RequireAuth(r.Context()); err != nil {
				respond.JSON(w, http.StatusUnauthorized, respond.Error{Error: "unauthenticated"})
				return
			}

			admin, err := authz.IsAdmin(r.Context())
			if err != nil {
				if errors.Is(err, db.ErrNoTx) {
					logger.ErrorContext(r.Context(), "admin_gate_missing_tenant_tx", "path", r.URL.Path)
				} else {
					logger.ErrorContext(r.Context(), "admin_gate_membership_lookup_failed", "path", r.URL.Path, "err", err)
				}
				respond.JSON(w, http.StatusInternalServerError, respond.Error{Error: "internal_error"})
				return
			}
			if !admin {
				respond.JSON(w, http.StatusForbidden, respond.Error{Error: "forbidden", RequiredRole: "admin"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
