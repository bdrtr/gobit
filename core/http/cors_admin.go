package http

import (
	"net/http"
	"slices"
)

// adminCORSAllowedHeaders are the request headers a panel on an allowed origin
// may send to the admin surface (ADR 0451): its bearer token, the idempotency
// key a retried write carries, the request id and the read validator. The
// list is closed for [corsAllowedHeaders]'s reason; the store surface's keys
// are not on it, since no admin route reads them.
var adminCORSAllowedHeaders = []string{
	"Content-Type",
	"Accept",
	"Authorization",
	IdempotencyKeyHeader,
	requestIDHeader,
	"If-None-Match",
}

// adminCORSExposedHeaders are the admin responses' headers a panel's script
// may read: the store surface's three, whether a write was replayed, where a
// created record lives, and an export's file name.
var adminCORSExposedHeaders = []string{
	requestIDHeader, "ETag", retryAfterHeader, IdempotencyReplayedHeader, "Location", "Content-Disposition",
}

// AdminCORS answers browser preflights on the admin surface for the listed
// origins and stamps their responses (ADR 0451).
//
// # What it opens, and what it does not
//
// A page on a listed origin may read the answer to a request it sent with its
// own bearer token, which is what a panel served from another host needs: the
// token was already in that page, and same-origin through a proxy it is in the
// page just the same. Credentials are never allowed, as on the store surface,
// so the panel's session cookie is never ridden: a browser does not send it on
// a preflighted request without Access-Control-Allow-Credentials, and a simple
// request that carries it is answered to nobody's script. A cookie-bearing write
// is still refused by the panel's own origin check, which does not read this
// list.
//
// # The wildcard is not honored
//
// "*" in the list is dropped: a site that may read admin answers is a site an
// operator chose, and a wildcard is a choice nobody made. Configuration refuses
// it before it gets here (ADMIN_CORS_ALLOWED_ORIGINS); the drop is for an
// embedder building the guards itself.
//
// An empty list, or one holding only the wildcard, writes no CORS header at all.
func AdminCORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := slices.DeleteFunc(normalizeOrigins(allowedOrigins), func(o string) bool { return o == AnyOrigin })
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	return corsPolicy(allowed, adminCORSAllowedHeaders, adminCORSExposedHeaders)
}
