package authz

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/iter-dev/iter/internal/db"
	"github.com/iter-dev/iter/internal/db/repo"
	"github.com/iter-dev/iter/pkg/contracts"
)

type adminCacheKey struct{}

type adminStatus struct {
	once  sync.Once
	admin bool
	err   error
}

// AdminCache installs the per-request cache used by IsAdmin.
func AdminCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(WithAdminCache(r.Context())))
	})
}

// WithAdminCache returns a context that memoizes the tenant_users role lookup
// for the duration of one request.
func WithAdminCache(ctx context.Context) context.Context {
	if _, ok := ctx.Value(adminCacheKey{}).(*adminStatus); ok {
		return ctx
	}
	return context.WithValue(ctx, adminCacheKey{}, &adminStatus{})
}

// IsAdmin returns whether the authenticated principal's current tenant_users
// membership is owner/admin. It intentionally ignores JWT role claims so role
// changes take effect on the next request.
func IsAdmin(ctx context.Context) (bool, error) {
	if cached, ok := ctx.Value(adminCacheKey{}).(*adminStatus); ok {
		cached.once.Do(func() {
			cached.admin, cached.err = loadAdmin(ctx)
		})
		return cached.admin, cached.err
	}
	return loadAdmin(ctx)
}

func loadAdmin(ctx context.Context) (bool, error) {
	principal, err := contracts.RequireAuth(ctx)
	if err != nil {
		return false, err
	}
	tx, err := db.RequireTx(ctx)
	if err != nil {
		return false, err
	}
	membership, err := repo.GetTenantUser(ctx, tx, principal.TenantID, principal.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return IsAdminRole(membership.Role), nil
}

// IsAdminRole is the closed owner/admin membership check used by request authz.
func IsAdminRole(role string) bool {
	return role == repo.RoleOwner || role == repo.RoleAdmin
}
