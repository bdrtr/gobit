package http

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/bdrtr/gobit/core/audit"
)

// AuditWriter is the surface the middleware needs to record a request.
//
// It is declared HERE rather than taken as the concrete store, so this package
// depends on the one method it calls and a test can supply it without a
// database.
type AuditWriter interface {
	// Write records one audited request under the given id.
	Write(ctx context.Context, id string, e audit.Entry) error
}

// auditedMethods are the methods that CHANGE something.
//
// Reads are left out: knowing that somebody listed the orders answers no
// question, and recording every read would bury the writes in volume.
var auditedMethods = []string{
	http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// Audit records who called which write and what came back.
//
// # Why it wraps writes only, and only where an identity exists
//
// A row is worth writing when it names somebody. The storefront is
// unauthenticated by decision (ADR 0008), so a row there would say "somebody"
// and mean nothing; the caller scopes this middleware to the admin surface for
// that reason.
//
// # Why the row is written AFTER the handler
//
// The status is part of the record. Writing before the work would mean guessing
// it, or recording an attempt that was then refused — a different fact, and a
// noisier log.
//
// # Why a failed audit does not fail the request
//
// The change is already committed. Refusing the response would undo nothing and
// would turn a logging fault into a customer-visible outage. It is reported at
// ERROR instead, and the residual is real: a change whose row was lost is a
// change with no trail. Closing that window would mean the audit row joining
// every module's transaction — the coupling ADR 0023 accepted for events,
// which this record does not earn.
func Audit(writer AuditWriter, newID func() string, log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !slices.Contains(auditedMethods, r.Method) {
				next.ServeHTTP(w, r)

				return
			}

			recorder, ok := w.(*responseWriter)
			if !ok {
				// The request logger wraps every response, so this cannot
				// happen in the real chain. If it ever does, the request is
				// served rather than failed: an audit gap is not worth an
				// outage, and the missing wrapper is a wiring fault that shows
				// up in the log below.
				log.ErrorContext(r.Context(), "the response is not wrapped, so the write cannot "+
					"be audited", "method", r.Method, "path", r.URL.Path)
				next.ServeHTTP(w, r)

				return
			}

			// The identity is NOT on this request yet, and will not be: the
			// guard that establishes it runs inside and derives its own
			// request. The slot is how it comes back — see
			// [withPrincipalSink] for why the audit has to sit out here.
			var actor Principal
			sunk := r.WithContext(withPrincipalSink(r.Context(), &actor))

			next.ServeHTTP(recorder, sunk)

			// A request that arrived already carrying an identity — a guard
			// placed outside this one — is read the ordinary way.
			if actor.ID == "" {
				actor, _ = PrincipalFromContext(r.Context())
			}

			entry := audit.Entry{
				ActorID:   actor.ID,
				ActorKind: actor.Kind,
				Method:    r.Method,
				// The ROUTE's path is not used and the request's is: a route
				// pattern would record "/admin/v1/orders/{id}" for every order,
				// and the question this table answers is which ONE was touched.
				Path:      r.URL.Path,
				Status:    recorder.status,
				RequestID: RequestIDFromContext(r.Context()),
			}

			// The write uses a context that OUTLIVES the request. The client
			// disconnecting is not a reason to lose the record of a change that
			// already happened — and a canceled request is exactly when
			// somebody will later want to know what ran.
			if err := writer.Write(context.WithoutCancel(r.Context()), newID(), entry); err != nil {
				log.ErrorContext(r.Context(), "an admin write could not be audited; the change "+
					"HAPPENED and there is no trail of it",
					"actor_id", entry.ActorID, "method", entry.Method,
					"path", entry.Path, "status", entry.Status, "error", err)
			}
		})
	}
}
