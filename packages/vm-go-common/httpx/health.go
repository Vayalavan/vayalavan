package httpx

import (
	"github.com/go-chi/chi/v5"
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"time"
)

// The two probe endpoints every service exposes (CLAUDE.md rule 8).
const (
	// HealthzPath is liveness: is the process running at all?
	HealthzPath = "/healthz"
	// ReadyzPath is readiness: can it actually serve traffic?
	ReadyzPath = "/readyz"
)

// readinessTimeout bounds the total time spent on dependency checks. A probe
// that hangs is indistinguishable from a probe that fails, and a hung
// readiness handler will pin an orchestrator's probe worker.
const readinessTimeout = 3 * time.Second

// Check is a named readiness probe for one dependency.
type Check struct {
	Name string
	// Probe returns nil when the dependency is usable. It must respect the
	// context deadline.
	Probe func(ctx context.Context) error
}

type healthResponse struct {
	Status  string            `json:"status"`
	Service string            `json:"service"`
	Checks  map[string]string `json:"checks,omitempty"`
}

// Healthz reports process liveness.
//
// Deliberately checks nothing. Liveness answers "should this process be
// restarted?", and a database outage is not fixed by restarting every API —
// wiring dependency checks in here turns a Postgres blip into a cascading
// restart loop across the whole platform. Dependencies belong in Readyz.
func Healthz(serviceName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(r.Context(), w, slog.Default(), http.StatusOK, healthResponse{
			Status:  "ok",
			Service: serviceName,
		})
	}
}

// Readyz reports whether the service can serve traffic, running every
// dependency check and returning 503 if any fails.
//
// Checks run sequentially under one shared deadline: there are only ever a
// couple of them, and sequential execution keeps the failure attribution in
// the response body unambiguous.
func Readyz(serviceName string, logger *slog.Logger, checks ...Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
		defer cancel()

		results := make(map[string]string, len(checks))
		ready := true

		for _, c := range checks {
			if err := c.Probe(ctx); err != nil {
				ready = false
				results[c.Name] = "failed"
				logger.WarnContext(ctx, "readiness check failed",
					slog.String("check", c.Name),
					slog.Any("error", err),
				)
				continue
			}
			results[c.Name] = "ok"
		}

		status, label := http.StatusOK, "ok"
		if !ready {
			status, label = http.StatusServiceUnavailable, "unavailable"
		}

		// Check names and pass/fail only. The underlying error text can name
		// internal hosts and is logged instead.
		WriteJSON(ctx, w, logger, status, healthResponse{
			Status:  label,
			Service: serviceName,
			Checks:  results,
		})
	}
}

// secureCompare compares two secrets in constant time, so an attacker cannot
// recover a token byte by byte from response timing.
func secureCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RoutePattern returns the chi route pattern for a request.
//
// The PATTERN, not the path: "/orders/{id}" rather than
// "/orders/9f3c…", so one label covers every order instead of one label per
// order. Unbounded label cardinality is the classic way a metrics endpoint
// takes down the system it was added to observe.
func RoutePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if pattern := rctx.RoutePattern(); pattern != "" {
			return pattern
		}
	}
	return ""
}
