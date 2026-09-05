package http

import (
	"context"
	"net/http"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// Auth codes. Clients branch on these; the messages may change.
const (
	// CodeUnauthenticated means no identity was presented, or it was invalid.
	CodeUnauthenticated = "unauthenticated"
	// CodeForbidden means the identity is valid but the privileges are not
	// enough.
	CodeForbidden = "forbidden"
)

// SchemeBearer is the scheme an [Authenticator] receives for a bearer
// credential.
//
// It is LOWER CASE, and that is not a style choice: [bearerCredential]
// normalizes the scheme read from the header, so an authenticator never sees
// "Bearer" from an HTTP request even when the client wrote it that way. Any
// caller that reaches an authenticator WITHOUT going through the header — the
// admin panel, which carries its token in a cookie — has to spell the same
// value, and this constant is what keeps the two spellings from drifting
// apart.
const SchemeBearer = "bearer"

// Principal is the verified caller's identity.
//
// The core does not know HOW the identity was verified: it may be a JWT, a
// secret API key or something else. It only carries the fields an
// authorization decision needs.
type Principal struct {
	// ID is the caller's unique identity (a user or an API key).
	ID string
	// Kind is the identity's type: "user" | "api_key".
	Kind string
	// Scopes are the caller's privileges (e.g. "admin", "orders:read").
	Scopes []string
	// SalesChannelIDs are the sales channels a publishable key is bound to;
	// catalog filtering on the store surface rests on them.
	SalesChannelIDs []string
}

// HasScope reports whether the caller holds the given privilege.
func (p Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope || s == ScopeAdmin {
			return true
		}
	}
	return false
}

// ScopeAdmin is the superior privilege covering all the others.
const ScopeAdmin = "admin"

// Authenticator resolves an incoming request's identity.
//
// The interface is declared on the CONSUMER side (in the core) and the auth
// module satisfies it structurally (ADR 0001). Because the core does not know
// the modules (Principle 2.4), the concrete implementation is resolved from the
// container by name and handed in here.
type Authenticator interface {
	// AuthenticateAdmin resolves the admin surface's identity: a Bearer JWT or
	// a secret API key. It returns errors.Unauthorized when the identity is
	// invalid.
	//
	// scheme arrives NORMALIZED to lower case; see [SchemeBearer]. An
	// implementation should still compare case-insensitively, because it can
	// also be called from outside the HTTP path.
	AuthenticateAdmin(ctx context.Context, scheme, credential string) (Principal, error)

	// AuthenticateStore resolves the store surface's identity: a publishable
	// API key. It returns errors.Unauthorized when the key is invalid.
	//
	// A publishable key is NOT A SECRET (it is visible in the browser); its
	// only job is to bind the request to a sales channel.
	AuthenticateStore(ctx context.Context, key string) (Principal, error)
}

// principalKey is the context key carrying the verified identity.
type principalKey struct{}

// principalSinkKey carries the slot an outer middleware left for the identity.
type principalSinkKey struct{}

// withPrincipalSink leaves a slot that [WithPrincipal] also fills in.
//
// # Why the identity has to travel back UP
//
// A guard passes the identity DOWN by deriving a new request, so a middleware
// running OUTSIDE the guard never sees it: the derived request reaches the
// inner handler and dies there, while the outer middleware is still holding the
// original. The audit log has to run outside — a write REFUSED for want of an
// identity is exactly the write worth recording — so the identity comes back up
// through this slot instead.
//
// # Why it is safe to read
//
// The guard fills the slot before calling the handler, and the outer middleware
// reads it only after that call has returned; the call and its return order the
// two accesses. Nothing reads the slot while the handler is running.
//
// # The other answer to the same hazard
//
// Idempotency needs the identity too, and it is installed AFTER authentication
// for exactly this reason (see [Idempotency]). Moving inward is the cheaper
// answer and the right one wherever it is available; the slot exists for the
// one middleware whose job requires it to stay outside.
func withPrincipalSink(ctx context.Context, slot *Principal) context.Context {
	return context.WithValue(ctx, principalSinkKey{}, slot)
}

// WithPrincipal puts the verified identity into the context.
//
// It also fills the slot left by [withPrincipalSink] when there is one, which
// is how every guard feeds the audit log without knowing that it exists.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if slot, ok := ctx.Value(principalSinkKey{}).(*Principal); ok {
		*slot = p
	}

	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext returns the verified identity from the context.
// A false second result means the request was not authenticated.
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// RequireAdmin protects the admin surface.
//
// It reads the Authorization header and supports two shapes:
//
//	Authorization: Bearer <jwt>
//	Authorization: Bearer <secret api key>
//
// Telling the two apart is [Authenticator]'s job; the core only parses the
// header. When auth is nil the middleware rejects EVERY request: an unprotected
// admin surface is the most expensive form of misconfiguration and must not
// stay quietly open.
func RequireAdmin(auth Authenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if auth == nil {
				unauthorized(ctx, w, "authentication is not configured")
				return
			}

			scheme, credential, ok := bearerCredential(r)
			if !ok {
				unauthorized(ctx, w, "authentication is required")
				return
			}

			principal, err := auth.AuthenticateAdmin(ctx, scheme, credential)
			if err != nil {
				// The reason is LOGGED, never LEAKED to the client: the
				// difference between "no such user" and "wrong password" is
				// what user enumeration is made of.
				LoggerFromContext(ctx).WarnContext(ctx, "admin authentication failed",
					"error", err, "request_id", RequestIDFromContext(ctx))
				unauthorized(ctx, w, "authentication is required")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, principal)))
		})
	}
}

// RequireStore protects the store surface.
//
// The publishable key is read from the "x-publishable-api-key" header. The key
// is NOT A SECRET; its purpose is to bind the request to a sales channel, not
// to keep anything confidential.
func RequireStore(auth Authenticator, header string) func(http.Handler) http.Handler {
	if header == "" {
		header = PublishableKeyHeader
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if auth == nil {
				unauthorized(ctx, w, "authentication is not configured")
				return
			}

			key := strings.TrimSpace(r.Header.Get(header))
			if key == "" {
				unauthorized(ctx, w, "a publishable api key is required")
				return
			}

			principal, err := auth.AuthenticateStore(ctx, key)
			if err != nil {
				LoggerFromContext(ctx).WarnContext(ctx, "store authentication failed",
					"error", err, "request_id", RequestIDFromContext(ctx))
				unauthorized(ctx, w, "the publishable api key is invalid")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, principal)))
		})
	}
}

// PublishableKeyHeader is the default header the publishable key is read from.
const PublishableKeyHeader = "x-publishable-api-key"

// RequireScope protects routes that demand a particular privilege.
//
// It is used AFTER [RequireAdmin]; with no identity it returns 401, with
// insufficient privileges 403. Separating the two is deliberate: 401 means
// "tell me who you are", 403 means "I know who you are but you are not
// allowed".
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			principal, ok := PrincipalFromContext(ctx)
			if !ok {
				unauthorized(ctx, w, "authentication is required")
				return
			}
			if !principal.HasScope(scope) {
				WriteError(ctx, w, coreerrors.Forbidden(CodeForbidden,
					"this operation requires the %q privilege", scope))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// bearerCredential splits the Authorization header into scheme and credential.
//
// The scheme is lower-cased on the way out; see [SchemeBearer] for what that
// commits the authenticators to.
func bearerCredential(r *http.Request) (scheme, credential string, ok bool) {
	raw := strings.TrimSpace(r.Header.Get("Authorization"))
	if raw == "" {
		return "", "", false
	}

	scheme, credential, found := strings.Cut(raw, " ")
	if !found {
		return "", "", false
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", "", false
	}
	return strings.ToLower(scheme), credential, true
}

// unauthorized writes a 401 response conforming to RFC 9110.
//
// The WWW-Authenticate header is MANDATORY: without knowing which scheme is
// expected, a client cannot retry with the right credentials.
func unauthorized(ctx context.Context, w http.ResponseWriter, message string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	WriteError(ctx, w, coreerrors.Unauthorized(CodeUnauthenticated, "%s", message))
}
