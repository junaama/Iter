package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/iter-dev/iter/internal/app"
)

// NewRouter constructs the chi-backed HTTP handler tree.
//
// Subsequent slices (029–047) register routes by calling Route / Mount /
// Method on the returned chi.Router. The returned value also satisfies
// http.Handler so cmd/server can pass it straight to *http.Server. The
// concrete return type is intentionally chi.Router (not http.Handler) so
// per-route registration in later issues stays type-safe without an
// upcast at every call site.
//
// Keep this signature stable: 029–047 issue bodies promise route
// registration will not churn `NewRouter` itself.
func NewRouter(deps app.Deps) chi.Router {
	r := chi.NewRouter()

	// Placeholder for any unrouted request until handlers land in 029+.
	// 503 (not 404) so misconfigured callers reaching this binary today
	// can distinguish "wrong URL" from "skeleton, no handlers yet".
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "iter api skeleton — handlers land in issues 029+", http.StatusServiceUnavailable)
	})

	return r
}
