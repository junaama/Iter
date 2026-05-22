package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iter-dev/iter/internal/api/middleware"
)

// TestChain_OrderAndUnwind verifies left-to-right composition: with
// Chain(A, B, C), the request flows A → B → C → handler, and the
// response unwinds C → B → A.
func TestChain_OrderAndUnwind(t *testing.T) {
	t.Parallel()

	var order []string

	mw := func(label string) middleware.Mw {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "pre:"+label)
				next.ServeHTTP(w, r)
				order = append(order, "post:"+label)
			})
		}
	}

	chain := middleware.Chain(mw("A"), mw("B"), mw("C"))
	h := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		order = append(order, "handler")
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	want := []string{"pre:A", "pre:B", "pre:C", "handler", "post:C", "post:B", "post:A"}
	if len(order) != len(want) {
		t.Fatalf("trace len: got %v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("trace[%d]: got %q want %q (full %v)", i, order[i], want[i], order)
		}
	}
}

// TestChain_Empty returns an identity wrapper.
func TestChain_Empty(t *testing.T) {
	t.Parallel()

	chain := middleware.Chain()
	called := false
	h := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Fatal("handler not invoked through empty chain")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusTeapot)
	}
}
