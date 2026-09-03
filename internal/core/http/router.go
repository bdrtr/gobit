// Package http builds the application's HTTP transport layer.
//
// Because the package name clashes with net/http, callers import it under the
// corehttp alias. The router, the middleware stack, the response and error
// helpers and the server with graceful shutdown all live here.
package http

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// defaultReadinessTimeout is the total budget for the readiness checks; it
// exists so a single stuck check does not leave the request hanging.
const defaultReadinessTimeout = 5 * time.Second

// HealthCheck probes a dependency's reachability.
// Returning nil means the dependency counts as healthy.
type HealthCheck func(ctx context.Context) error

// RouterOptions decides how the root router is built.
type RouterOptions struct {
	// Version is the build version reported in the /health and /ready
	// responses.
	Version string
	// Logger is the logger the middleware stack uses; nil means slog.Default.
	Logger *slog.Logger
	// ReadinessChecks are the dependency checks run at the /ready endpoint
	// (e.g. "postgres", "redis"). When empty, /ready always returns 200.
	ReadinessChecks map[string]HealthCheck
	// ReadinessTimeout is the total budget for all the checks; zero means 5s.
	ReadinessTimeout time.Duration
	// TelemetryService is the service name written as the service.name
	// attribute onto the trace spans and the HTTP metrics; it is repeated here
	// even though the name is also reported in the OTel Resource (for the
	// rationale see [Telemetry]).
	//
	// When empty the telemetry middleware is NOT attached at all. The name
	// lives here because of the ORDER: [Telemetry] must run ABOVE Recoverer so
	// a panic in a handler reaches the span as the 500 Recoverer writes. Below
	// it, a panic would cut the span short and that request would appear in
	// tracing with "no status code" — that is, the request most worth looking
	// at would be the one recorded worst.
	TelemetryService string
	// Middlewares are the middlewares added AFTER the core stack and BEFORE
	// the routes (the guards, the rate limit, idempotency).
	//
	// They live here because of chi's rule: middlewares must be added BEFORE
	// the routes are registered, and /health and /ready are registered in this
	// function. Whoever builds the application cannot call r.Use after NewRouter
	// has returned — chi panics.
	//
	// They sit at the BOTTOM of the stack: Recoverer wraps them too, so a panic
	// in a guard middleware becomes a 500 instead of cutting the connection.
	Middlewares []func(http.Handler) http.Handler
}

// healthResponse is the body of the /health endpoint.
type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// checkResult is the outcome of a single dependency check.
type checkResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// readyResponse is the body of the /ready endpoint.
type readyResponse struct {
	Status  string                 `json:"status"`
	Version string                 `json:"version"`
	Checks  map[string]checkResult `json:"checks,omitempty"`
}

// NewRouter builds the application's root router together with the middleware
// stack.
//
// The middleware order is deliberate: RequestID runs first so the logger and
// the recoverer can report the request by its id; RequestLogger wraps Recoverer
// so the 500 written after a panic is logged too. Telemetry comes second (see
// [RouterOptions.TelemetryService]), and [RouterOptions.Middlewares] sits below
// Recoverer.
//
// /health and /ready DO pass through the [RouterOptions.Middlewares] stack: a
// guard middleware is expected to narrow its own scope with [Scoped]. Taking
// the health endpoints outside the stack would separate the path the
// orchestrator sees from the one the application sees — and that separation can
// make a real failure look healthy.
func NewRouter(opts RouterOptions) chi.Router {
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(RequestID)
	if opts.TelemetryService != "" {
		r.Use(Telemetry(opts.TelemetryService))
	}
	r.Use(RequestLogger(log))
	r.Use(Recoverer(log))
	for _, mw := range opts.Middlewares {
		if mw == nil {
			continue
		}
		r.Use(mw)
	}

	r.Get("/health", healthHandler(opts.Version))
	r.Get("/ready", readyHandler(opts, log))

	return r
}

// healthHandler is the liveness endpoint reporting that the process is up.
// It does NOT probe the dependencies; the orchestrator uses it for process
// liveness, and a transient database outage must not get this process killed.
func healthHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(r.Context(), w, http.StatusOK, healthResponse{
			Status:  "ok",
			Version: version,
		})
	}
}

// readyHandler is the readiness endpoint that probes the dependencies.
// When any check fails it returns 503; the orchestrator pulls this process out
// of traffic but does not kill it.
func readyHandler(opts RouterOptions, log *slog.Logger) http.HandlerFunc {
	timeout := opts.ReadinessTimeout
	if timeout <= 0 {
		timeout = defaultReadinessTimeout
	}

	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		results := runChecks(ctx, opts.ReadinessChecks)

		status := http.StatusOK
		body := readyResponse{Status: "ok", Version: opts.Version, Checks: results}
		for name, res := range results {
			if res.Status != "ok" {
				status = http.StatusServiceUnavailable
				body.Status = "degraded"
				log.WarnContext(ctx, "a readiness check failed", "check", name, "error", res.Error)
			}
		}

		WriteJSON(ctx, w, status, body)
	}
}

// runChecks runs every check concurrently and collects the results.
func runChecks(ctx context.Context, checks map[string]HealthCheck) map[string]checkResult {
	if len(checks) == 0 {
		return nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]checkResult, len(checks))
	)

	for name, check := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()

			res := checkResult{Status: "ok"}
			if err := check(ctx); err != nil {
				res = checkResult{Status: "error", Error: err.Error()}
			}

			mu.Lock()
			results[name] = res
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}
