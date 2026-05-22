package middleware

import "net/http"

// Mw is a single HTTP middleware: it wraps an http.Handler with extra
// behavior and returns the wrapped handler. The signature matches
// chi.Router.Use and net/http convention.
type Mw = func(http.Handler) http.Handler

// Chain composes middlewares left-to-right so that the leftmost runs
// outermost. With Chain(A, B, C)(h), a request flows A → B → C → h, and the
// response unwinds C → B → A. router.go uses Chain to declare the stack
// order in one place per ARCHITECTURE.md §9 Step 4.
//
// Chain with no middlewares returns an identity wrapper. This keeps callers
// from special-casing empty stacks.
func Chain(mws ...Mw) Mw {
	return func(next http.Handler) http.Handler {
		// Wrap from the end so the first middleware in mws becomes the
		// outermost layer.
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
