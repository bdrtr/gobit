package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The error codes this surface can refuse a REGISTRATION with. A callback that
// fails to register stops startup; none of these can reach a provider.
const (
	// CodeCallbackInvalid is a registration this package cannot honor.
	CodeCallbackInvalid = "callback_invalid"
	// CodeCallbackConflict is a second registration for one path.
	CodeCallbackConflict = "callback_conflict"
	// CodeCallbackFrozen is a registration arriving after the routes were bound.
	CodeCallbackFrozen = "callback_frozen"
)

// DefaultCallbackBody is the body limit a route gets when it names none.
//
// Providers post small form or JSON payloads; the one measured example is a
// PayTR form of a few hundred bytes. The limit exists because the alternative
// is net/http's own 10 MB urlencoded default, which is a standard library
// decision rather than one this repository made.
const DefaultCallbackBody = 64 << 10

// DefaultCallbackTimeout bounds one callback's work.
//
// Without it the only ceiling is the process-wide write timeout, so a slow
// database holds the connection for the whole write budget while the provider
// is already counting the call as unacknowledged.
const DefaultCallbackTimeout = 10 * time.Second

// CallbackVerify proves that a request came from the provider it claims to be.
//
// It receives the BUFFERED body because every measured provider signs the body;
// the request is passed as well for the ones that sign into a header. The
// comparison must be constant time — the signature is the only credential this
// surface has.
//
// An error means the request is refused with [CallbackAck.Rejected] and nothing
// is recorded: a request that failed verification is not evidence of anything.
type CallbackVerify func(ctx context.Context, r *http.Request, body []byte) error

// CallbackKey derives, from the VERIFIED payload, the two tuples the replay ring
// keys on.
//
// identity answers "which event is this" and becomes the store key. content
// answers "what does it assert" and becomes the fingerprint, so that the same
// event arriving with a different outcome is distinguishable from a plain
// retry.
//
// Both tuples must be drawn ONLY from fields the signature covers. A field the
// provider does not sign is a field an attacker can perturb, and perturbing the
// key mints a fresh one — which defeats the replay window entirely while
// looking like it works.
//
// Returning an empty identity DECLINES: the request goes to the handler with no
// replay record. That is the right answer for a payload this route cannot key,
// and it is deliberately not an error — refusing it would tell a provider its
// own message is malformed on this repository's authority.
type CallbackKey func(r *http.Request, body []byte) (identity, content []string, err error)

// CallbackResponse is one answer in a provider's own protocol.
//
// The core's JSON error envelope never reaches this surface. The measured
// example is exact about why: PayTR reads the BODY, not the status, and
// anything but its token means "not acknowledged" — so an error envelope on a
// success produces an endless retry loop while every payment looks fine from
// the inside.
type CallbackResponse struct {
	// Status is the HTTP status code.
	Status int
	// ContentType is written when it is not empty.
	ContentType string
	// Body is written verbatim.
	Body string
}

// CallbackAck is the provider's protocol for each outcome this ring can produce.
//
// Every field is required: a zero answer would send a 0 status, and a provider
// reading it cannot tell "accepted" from "refused". [CallbackRegistry.Register] refuses
// an incomplete one at startup rather than at the first callback.
type CallbackAck struct {
	// Accepted is the answer after the handler succeeded.
	Accepted CallbackResponse
	// Duplicate answers an event already recorded, INCLUDING one that arrives
	// asserting something different. Answering a contradiction with a refusal
	// would make a PayTR-shaped provider retry it forever; it is acknowledged
	// and reported instead.
	Duplicate CallbackResponse
	// Rejected answers a failed signature.
	Rejected CallbackResponse
	// Malformed answers a body that could not be read or keyed.
	Malformed CallbackResponse
	// Unavailable answers a fault on this side. It must be the answer that makes
	// the provider RETRY: the work did not happen and only a retry can save it.
	Unavailable CallbackResponse
}

// CallbackRoute is one provider's inbound endpoint.
type CallbackRoute struct {
	// Source names the sender ("paytr", "yurtici"). It namespaces the replay
	// keys, so two providers cannot collide on a derived key, and it is what the
	// logs identify the caller as.
	Source string
	// Path is the FULL path the provider posts to. Patterns are refused: the
	// guard looks the route up by exact path, and a wildcard would make "is this
	// path guarded" unanswerable by reading it.
	Path string
	// Method defaults to POST.
	Method string
	// Verify is required. A callback with no signature check is the defect this
	// whole class exists to remove, so there is no way to express one.
	Verify CallbackVerify
	// Key is required. A route that genuinely cannot key its payload says so per
	// request by returning an empty identity, which is a decision made with the
	// payload in hand rather than at registration.
	Key CallbackKey
	// Handler runs after every guard has passed.
	Handler http.HandlerFunc
	// MaxBodyBytes defaults to [DefaultCallbackBody].
	MaxBodyBytes int64
	// Timeout defaults to [DefaultCallbackTimeout].
	Timeout time.Duration
	// Ack is the provider's response protocol.
	Ack CallbackAck
}

// CallbackOptions are the shared services the ring guards with.
//
// Every one of them is optional and the ring degrades in a stated direction
// when one is absent, because an installation without Redis must still be able
// to take a payment callback. What is NOT optional is the per-route signature
// check: that one lives on the route.
type CallbackOptions struct {
	// Limiter bounds how often a callback path can be called. Nil means no
	// quota — the state this repository is in today.
	Limiter RateLimiter
	// LimitKey defaults to [ClientIPKey]. The route's path is prepended to
	// whatever it returns, so one provider flooding cannot exhaust another's
	// budget.
	LimitKey KeyFunc
	// Store is the replay window. Nil means a verified callback is processed
	// every time it arrives, which is what happens today.
	Store IdempotencyStore
	// Logger is required in practice; nil falls back to the default logger,
	// because a callback ring that cannot report a forged signature is worse
	// than a noisy one.
	Logger *slog.Logger
}

// CallbackRegistry holds the callback routes and guards them.
//
// # Why this is a registry and not a middleware over a prefix
//
// The middleware stack is frozen before any plugin exists: the composition root
// builds the router, and only then installs the plugins. The plugin that knows
// the path, the secret and the signature formula does not exist when the guards
// are decided, so a plugin can never contribute a ring — it can only FILL IN an
// object created before it. That is the same shape as [DeferredAuthenticator],
// for the same reason.
//
// The alternative — one middleware over a reserved prefix — cannot carry
// per-route policy either: the body limit, the timeout, the verifier and the
// answer protocol differ per provider, so a single ring would have to apply the
// strictest to all, or re-implement route lookup, which is this registry with
// worse ergonomics.
//
// # It is bound in two phases and the second one freezes it
//
// [CallbackRegistry.Register] collects; [CallbackRegistry.Mount] binds the
// routes to the router and closes the registry. A Register after Mount is a
// loud error rather than a silent no-op:
// silently ignoring it would produce a route the provider can reach and nothing
// guards, which is the defect being removed.
type CallbackRegistry struct {
	opts CallbackOptions

	mu     sync.RWMutex
	routes map[string]*CallbackRoute
	frozen bool
}

// NewCallbackRegistry builds an empty registry.
func NewCallbackRegistry(opts CallbackOptions) *CallbackRegistry {
	if opts.LimitKey == nil {
		opts.LimitKey = ClientIPKey
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &CallbackRegistry{opts: opts, routes: map[string]*CallbackRoute{}}
}

// Register adds one provider's callback.
//
// Every refusal here stops startup, and that is the intent: a callback route
// that is half-configured is one a provider will reach and this ring will not
// guard.
func (g *CallbackRegistry) Register(rt CallbackRoute) error {
	if err := validateCallbackRoute(&rt); err != nil {
		return err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.frozen {
		return coreerrors.Conflict(CodeCallbackFrozen,
			"the callback %q arrived after the routes were bound; a route registered now "+
				"would never be mounted and never be guarded", rt.Path)
	}
	if existing, taken := g.routes[rt.Path]; taken {
		return coreerrors.Conflict(CodeCallbackConflict,
			"%q is already the callback of %q; two providers cannot share one path, "+
				"because the second one's signature check would refuse the first one's traffic",
			rt.Path, existing.Source)
	}

	g.routes[rt.Path] = &rt

	return nil
}

// Routes reports the registered paths, in no particular order.
func (g *CallbackRegistry) Routes() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	paths := make([]string, 0, len(g.routes))
	for path := range g.routes {
		paths = append(paths, path)
	}

	return paths
}

// Mount binds every registered route to the router and freezes the registry.
//
// Binding here rather than letting each plugin bind its own is what makes the
// guard binding instead of advisory: a route the registry does not know is a
// route it does not wrap, and a plugin that forgot to register would otherwise
// get an unguarded endpoint that looks exactly like a guarded one.
func (g *CallbackRegistry) Mount(r chi.Router) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.frozen {
		return coreerrors.Conflict(CodeCallbackFrozen, "the callback routes are already mounted")
	}
	if r == nil {
		return coreerrors.Invalid(CodeCallbackInvalid, "a router is required to mount the callbacks")
	}

	for _, rt := range g.routes {
		r.Method(rt.Method, rt.Path, rt.Handler)
	}
	g.frozen = true

	return nil
}

// Middleware is the ring the composition root installs on the router.
//
// It runs on every request and does nothing to a path it does not know, so it
// carries no prefix policy of its own. That is deliberate: a reserved prefix
// would force every existing provider's URL to move, and the URL is configured
// on the PROVIDER's side, where changing it is an operational break rather than
// a deploy.
func (g *CallbackRegistry) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rt := g.lookup(r)
			if rt == nil {
				next.ServeHTTP(w, r)

				return
			}

			g.guard(w, r, rt, next)
		})
	}
}

// lookup finds the route for a request, and only when the registry is mounted.
//
// Before Mount the ring refuses to recognize anything: the routes are not bound
// yet, so a request arriving now cannot reach a handler in any case, and
// pretending to guard it would report a protection that is not running.
func (g *CallbackRegistry) lookup(r *http.Request) *CallbackRoute {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.frozen {
		return nil
	}

	rt, found := g.routes[r.URL.Path]
	if !found || !strings.EqualFold(rt.Method, r.Method) {
		return nil
	}

	return rt
}

// validateCallbackRoute fills the defaults in and refuses what cannot be guarded.
func validateCallbackRoute(rt *CallbackRoute) error {
	switch {
	case strings.TrimSpace(rt.Source) == "":
		return coreerrors.Invalid(CodeCallbackInvalid,
			"a callback needs a source: it namespaces the replay keys, and two providers "+
				"sharing a namespace can silence each other's events")
	case !strings.HasPrefix(rt.Path, "/"):
		return coreerrors.Invalid(CodeCallbackInvalid,
			"the callback path of %q must be absolute, got %q", rt.Source, rt.Path)
	case strings.ContainsAny(rt.Path, "{*"):
		return coreerrors.Invalid(CodeCallbackInvalid,
			"the callback path of %q is a pattern (%q); the guard looks a route up by exact "+
				"path, and a pattern would make \"is this path guarded\" unanswerable by "+
				"reading it", rt.Source, rt.Path)
	case rt.Verify == nil:
		return coreerrors.Invalid(CodeCallbackInvalid,
			"the callback of %q has no signature check; a body is not a credential", rt.Source)
	case rt.Key == nil:
		return coreerrors.Invalid(CodeCallbackInvalid,
			"the callback of %q has no key derivation; a route that cannot key a payload "+
				"says so per request, not by leaving this out", rt.Source)
	case rt.Handler == nil:
		return coreerrors.Invalid(CodeCallbackInvalid,
			"the callback of %q has no handler", rt.Source)
	}

	if rt.Method == "" {
		rt.Method = http.MethodPost
	}
	rt.Method = strings.ToUpper(rt.Method)
	if rt.MaxBodyBytes <= 0 {
		rt.MaxBodyBytes = DefaultCallbackBody
	}
	if rt.Timeout <= 0 {
		rt.Timeout = DefaultCallbackTimeout
	}

	return validateCallbackAck(rt)
}

// validateCallbackAck refuses an answer vocabulary with a hole in it.
func validateCallbackAck(rt *CallbackRoute) error {
	answers := map[string]CallbackResponse{
		"accepted":    rt.Ack.Accepted,
		"duplicate":   rt.Ack.Duplicate,
		"rejected":    rt.Ack.Rejected,
		"malformed":   rt.Ack.Malformed,
		"unavailable": rt.Ack.Unavailable,
	}
	for name, answer := range answers {
		if answer.Status < 100 || answer.Status > 599 {
			return coreerrors.Invalid(CodeCallbackInvalid,
				"the callback of %q has no %s answer (status %d).\n"+
					"Every outcome needs one: the provider reads what this surface writes, and "+
					"an empty answer tells it nothing about whether to retry",
				rt.Source, name, answer.Status)
		}
	}

	return nil
}
