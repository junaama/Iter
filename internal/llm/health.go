package llm

// ProviderStatus is the JSON-friendly health label per provider, mirroring
// the shape sketched in deploy.md "Healthcheck" and ARCHITECTURE.md §4
// (`/health` returns `... + llm_routes`).
//
//	"ok"       — breaker closed; provider serving traffic.
//	"degraded" — breaker half-open; next call is a probe.
//	"down"     — breaker open; calls are short-circuited until recovery.
type ProviderStatus string

const (
	// StatusOK indicates the provider is healthy (breaker closed).
	StatusOK ProviderStatus = "ok"
	// StatusDegraded indicates the breaker is half-open and the next
	// request is a probe.
	StatusDegraded ProviderStatus = "degraded"
	// StatusDown indicates the breaker is open and traffic is short-circuited.
	StatusDown ProviderStatus = "down"
)

// HealthSnapshot reports the current breaker state of every registered
// provider. Intended for the `/health` handler (issue 030); the issue 055
// brief permits this to back the `?deep=1` probe path but the default
// snapshot is breaker-state only — it does NOT call out to providers, so
// invoking it on every healthcheck is free.
func (r *Router) HealthSnapshot() map[string]ProviderStatus {
	out := make(map[string]ProviderStatus, len(r.providers))
	for _, p := range r.providers {
		b := r.breakers[p.Name()]
		switch b.snapshot() {
		case BreakerClosed:
			out[p.Name()] = StatusOK
		case BreakerHalfOpen:
			out[p.Name()] = StatusDegraded
		case BreakerOpen:
			out[p.Name()] = StatusDown
		default:
			out[p.Name()] = StatusDown
		}
	}
	return out
}
