// Package http builds the application's HTTP transport layer.
//
// Because the package name clashes with net/http, callers import it under the
// corehttp alias. The router, the middleware stack, the response and error
// helpers and the server with graceful shutdown all live here.
package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// defaultReadinessTimeout is the per-check budget for the gating readiness
// checks; it exists so a single stuck check does not leave the request hanging.
const defaultReadinessTimeout = 5 * time.Second

// defaultDegradedTimeout is the per-check budget for the degrading readiness
// checks and it is DELIBERATELY much tighter than
// [defaultReadinessTimeout].
//
// A degrading check answers a question nobody is waiting on — the instance
// keeps serving either way — so the only thing its slowness can still do is
// break the probe. Measured against an unreachable Redis, one Ping costs 1.7s
// because the client dials five times before giving up; a kubelet readinessProbe
// leaves timeoutSeconds at 1 by default, so an unbounded degrading check would
// time the probe OUT and mark the pod NotReady — reintroducing, through the
// back door, the total outage this split exists to prevent. With this budget
// the same probe answers in ~250ms.
//
// This is a CAP and it is not silent: the budget is reported on the WARN line
// [readyHandler] writes for every failing degrading check, and it is
// overridable through [RouterOptions.DegradedCheckTimeout].
const defaultDegradedTimeout = 250 * time.Millisecond

// HealthCheck probes a dependency's reachability.
// Returning nil means the dependency counts as healthy.
type HealthCheck func(ctx context.Context) error

// GatingChecks are the probes whose failure takes the instance OUT of traffic.
//
// DegradingChecks has the identical underlying type on purpose but is a
// SEPARATE named type, so the two cannot be passed to each other's parameter or
// assigned to each other's field: Go's assignability rules let a plain map
// literal fill either one, while a variable of one named type will not compile
// where the other is expected.
//
// The reason for spending a type on this is the failure it prevents. Moving one
// dependency from the degrading side to the gating side is a one-word edit that
// reads harmless in review and produces a full storefront outage the first time
// that dependency blinks — see [RouterOptions.DegradedChecks]. A test cannot
// stand guard over every future call site; the compiler can.
type GatingChecks map[string]HealthCheck

// DegradingChecks are the probes whose failure is reported but leaves the
// instance IN traffic. Why the type is separate from [GatingChecks] is on that
// type; what qualifies a dependency for this side is on
// [RouterOptions.DegradedChecks].
type DegradingChecks map[string]HealthCheck

// probe is one readiness check together with what its failure MEANS and how
// long it is allowed to take.
//
// The two live in one struct because they are one decision: a dependency whose
// loss takes the instance out of traffic is worth waiting seconds for, and a
// dependency whose loss only degrades the instance is not worth waiting for at
// all (see [defaultDegradedTimeout]).
type probe struct {
	name    string
	check   HealthCheck
	gating  bool
	timeout time.Duration
}

// RouterOptions decides how the root router is built.
type RouterOptions struct {
	// Version is the build version reported in the /health and /ready
	// responses.
	Version string
	// Logger is the logger the middleware stack uses; nil means slog.Default.
	Logger *slog.Logger
	// ReadinessChecks are the GATING dependency checks run at the /ready
	// endpoint: a failure here answers "this instance cannot serve traffic"
	// and /ready returns 503, so the orchestrator pulls the instance out of
	// the load balancer. Postgres belongs here — without it not one endpoint
	// answers correctly. When both check maps are empty, /ready always returns
	// 200.
	//
	// Put a dependency here only if its loss DISABLES the instance. A
	// dependency whose loss merely degrades it belongs in [DegradedChecks];
	// the reason the distinction is not cosmetic is on that field.
	ReadinessChecks GatingChecks
	// DegradedChecks are the dependency checks whose failure is REPORTED but
	// does not take the instance out of traffic: they appear in the /ready
	// body next to the gating ones and turn the overall status into
	// "degraded", while the status code stays 200.
	//
	// # Why a failing dependency may keep serving traffic
	//
	// Readiness is a routing decision, not a health opinion, and for a SHARED
	// dependency the routing decision is worthless: every replica talks to the
	// same Redis, so every replica fails its probe in the same second and
	// Kubernetes empties the Service of endpoints. There is no healthier pod
	// left to shift traffic to — the probe converts a partial degradation into
	// a total outage. That is the "fail-closed for everything" option ADR 0007
	// examined and REJECTED for the guard middlewares ("a brief Redis outage
	// would close the whole store; the protection component itself would
	// become the biggest source of outage"), and a readiness gate on the same
	// dependency reintroduces it one layer up.
	//
	// Measured, not deduced (TestRedisOutageMeasurement in cmd/server, guard
	// backend on redis, Redis unreachable): a storefront catalog read returns
	// 200, a write without an Idempotency-Key returns 200, and a write WITH
	// one returns a per-request 503 idempotency_store_unavailable the client
	// can retry. Nothing is served incorrectly — the one class of request that
	// cannot be protected is the one refused, exactly as ADR 0007 decided.
	// Gating on that dependency would have taken the 200s down with it.
	//
	// A name present in BOTH maps is treated as gating: the safe reading of an
	// ambiguous configuration is the one that pulls traffic.
	DegradedChecks DegradingChecks
	// ReadinessTimeout is the per-check budget for [ReadinessChecks]; zero
	// means 5s.
	//
	// It is PER CHECK and it used to be the budget for all of them together.
	// The endpoint is still bounded, because the probes run concurrently: one
	// /ready request costs at most the largest of the two budgets, not their
	// sum. The change is what a slow check can no longer do — eat the budget a
	// gating check still needs to answer with.
	ReadinessTimeout time.Duration
	// DegradedCheckTimeout is the per-check budget for [DegradedChecks]; zero
	// means 250ms. Why it is so much tighter is in [defaultDegradedTimeout].
	DegradedCheckTimeout time.Duration
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
	warnAboutDuplicateChecks(opts, log)
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

// warnAboutDuplicateChecks reports, ONCE at build time, every name registered on
// both readiness sides.
//
// [readinessProbes] resolves such a name to the gating side and drops the
// degrading copy, which is the safe reading — but silently. The condition is a
// configuration mistake, not a runtime event, so the line belongs here rather
// than on the request path: an operator who meant to degrade on a dependency
// and typed it into both maps would otherwise find out only from a full outage
// the first time that dependency blinks.
func warnAboutDuplicateChecks(opts RouterOptions, log *slog.Logger) {
	for name := range opts.DegradedChecks {
		if _, alsoGating := opts.ReadinessChecks[name]; !alsoGating {
			continue
		}

		log.Warn("a readiness check is registered on BOTH sides; the gating one wins and the degrading copy is dropped",
			"check", name)
	}
}

// readyHandler is the readiness endpoint that probes the dependencies.
//
// The status code answers ONE question — can this instance serve traffic —
// and only [RouterOptions.ReadinessChecks] gets a vote on it: a failure there
// returns 503 and the orchestrator pulls this process out of traffic without
// killing it. A failing [RouterOptions.DegradedChecks] entry keeps the 200,
// because pulling every replica out at once over a shared dependency is an
// outage, not a mitigation (the full reasoning is on that field).
//
// The three body statuses are DISTINCT on purpose, so an operator reading a
// body or a log line can tell which of the two happened:
//
//	ok           200  every check passed
//	degraded     200  a degrading dependency is down; this instance still serves
//	unavailable  503  a gating dependency is down; this instance is out of traffic
//
// Both cases also write a WARN line, and the degraded line says the instance is
// STILL SERVING — a log that reported only "a readiness check failed" would
// leave the reader unable to tell an outage from a degradation, which is
// exactly the distinction this endpoint now makes.
func readyHandler(opts RouterOptions, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		probes := readinessProbes(opts)
		results := runChecks(ctx, probes)

		status := http.StatusOK
		body := readyResponse{Status: "ok", Version: opts.Version, Checks: results}
		for _, p := range probes {
			if results[p.name].Status == "ok" {
				continue
			}

			if p.gating {
				status = http.StatusServiceUnavailable
				body.Status = "unavailable"
				log.WarnContext(ctx, "a gating readiness check failed; this instance is leaving traffic",
					"check", p.name, "error", results[p.name].Error, "budget", p.timeout)

				continue
			}

			if body.Status == "ok" {
				body.Status = "degraded"
			}
			log.WarnContext(ctx, "a degrading dependency is down; this instance KEEPS SERVING",
				"check", p.name, "error", results[p.name].Error, "budget", p.timeout)
		}

		WriteJSON(ctx, w, status, body)
	}
}

// readinessProbes flattens the two check maps into one list, attaching to each
// entry what its failure means and how long it may take.
//
// It runs PER REQUEST rather than once when the router is built, and the
// difference is a silent failure, not a style preference: a check registered
// into either map after NewRouter returned would be missing from a snapshot
// taken at build time, and a dependency that is simply never probed reports
// nothing at all — no 503, no degraded body, no log line. A kubelet polls
// /ready every ten seconds or so, which is not a rate at which two map walks
// are worth that risk.
//
// A name appearing in both maps is kept as GATING and the degrading copy is
// dropped. Silently preferring the degrading one would mean a typo quietly
// switching a dependency the operator meant to gate on into one that only gets
// mentioned in a body.
//
// CONTRACT, and it is a hard one because the runtime enforces it with a process
// kill: the two maps belong to the caller and are read here on every request,
// so they must be fully populated BEFORE the server starts serving. Writing to
// one while a request is in flight is a concurrent map write, which Go turns
// into "fatal error: concurrent map read and map write" — the whole process,
// not one request. Registering after [NewRouter] returns but before Start is
// fine and is what the composition root does.
func readinessProbes(opts RouterOptions) []probe {
	gating := opts.ReadinessTimeout
	if gating <= 0 {
		gating = defaultReadinessTimeout
	}

	degraded := opts.DegradedCheckTimeout
	if degraded <= 0 {
		degraded = defaultDegradedTimeout
	}

	probes := make([]probe, 0, len(opts.ReadinessChecks)+len(opts.DegradedChecks))
	for name, check := range opts.ReadinessChecks {
		probes = append(probes, probe{name: name, check: check, gating: true, timeout: gating})
	}

	for name, check := range opts.DegradedChecks {
		if _, alsoGating := opts.ReadinessChecks[name]; alsoGating {
			continue
		}

		probes = append(probes, probe{name: name, check: check, timeout: degraded})
	}

	return probes
}

// probeError turns a failed check into the sentence an operator reads in the
// /ready body.
//
// It exists because the budget makes the honest error DISAPPEAR. An unreachable
// Redis is refused instantly on each dial, but the client dials five times with
// backoff, so a 250ms budget expires during a backoff sleep and the caller gets
// a bare "context deadline exceeded" — a message that reads like a bug in gobit
// and says nothing about which dependency did what. Naming the budget turns it
// back into a fact about the dependency.
//
// The question it answers is whether the PROBE's own deadline fired, and it
// asks the probe context rather than the returned error. The two are not the
// same thing: a dependency with its own timeout reports context.DeadlineExceeded
// of its own making — pgx normalizes a dial timeout into exactly that, and the
// pool sets ConnectTimeout to 5s by default — so an installation running
// connect_timeout=2 against an unreachable Postgres would be told "no answer
// within the 5s probe budget" about a check that gave up at 2s and a budget
// that never expired. The request context is consulted too: when the caller
// hung up (the kubelet's own timeout, a shutdown) the budget is again not what
// expired and naming it would be a lie.
//
// KNOWN COST, stated because it is invisible from the body: the budget destroys
// the root cause. An unreachable Redis is refused instantly per dial but the
// client retries with backoff, so the budget expires during a sleep and the
// only error left is the deadline — "connection refused", a DNS failure and an
// AUTH failure all render as the same sentence. Recovering the dial error is not
// possible from this side; the check returns what the client gave it.
func probeError(reqCtx, probeCtx context.Context, p probe, err error) string {
	if errors.Is(probeCtx.Err(), context.DeadlineExceeded) && reqCtx.Err() == nil {
		return fmt.Sprintf("no answer within the %s probe budget", p.timeout)
	}

	return err.Error()
}

// runChecks runs every probe concurrently and collects the results.
//
// The budget is applied PER PROBE rather than to the request as a whole: a
// shared deadline would let a slow degrading check eat the budget a gating
// check still needs, and the gating check is the one whose answer decides
// whether this instance keeps receiving requests.
func runChecks(ctx context.Context, probes []probe) map[string]checkResult {
	if len(probes) == 0 {
		return nil
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]checkResult, len(probes))
	)

	for _, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()

			probeCtx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()

			res := checkResult{Status: "ok"}
			if err := p.check(probeCtx); err != nil {
				res = checkResult{Status: "error", Error: probeError(ctx, probeCtx, p, err)}
			}

			mu.Lock()
			results[p.name] = res
			mu.Unlock()
		}()
	}

	wg.Wait()
	return results
}
