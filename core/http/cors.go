package http

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORS headers, spelled once.
const (
	headerOrigin         = "Origin"
	headerAllowOrigin    = "Access-Control-Allow-Origin"
	headerAllowMethods   = "Access-Control-Allow-Methods"
	headerAllowHeaders   = "Access-Control-Allow-Headers"
	headerExposeHeaders  = "Access-Control-Expose-Headers"
	headerMaxAge         = "Access-Control-Max-Age"
	headerRequestMethod  = "Access-Control-Request-Method"
	headerRequestHeaders = "Access-Control-Request-Headers"
	headerVary           = "Vary"
)

// AnyOrigin is the wildcard an installation can configure to open the store
// surface to every site.
//
// It is spelled out rather than assumed: a default-open CORS policy is a
// security decision, and one nobody made is one nobody can be asked about.
const AnyOrigin = "*"

// corsMaxAge is how long a browser may cache a preflight answer.
//
// Ten minutes rather than a day: the allow-list lives in configuration, and a
// long cache would keep a removed origin working in browsers that had already
// asked. Short enough to change a policy meaningfully, long enough that a
// storefront does not preflight on every request.
const corsMaxAge = 10 * time.Minute

// corsAllowedHeaders are the request headers a browser storefront needs.
//
// The list is CLOSED rather than reflected back from the request: echoing
// whatever a browser asks for turns the allow-list into a formality, since the
// asking side is the one being checked.
var corsAllowedHeaders = []string{
	"Content-Type",
	"Accept",
	PublishableKeyHeader,
	IdempotencyKeyHeader,
	requestIDHeader,
}

// corsExposedHeaders are the response headers a browser may read.
var corsExposedHeaders = []string{requestIDHeader}

// corsAllowedMethods are the methods the store surface answers.
var corsAllowedMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions,
}

// CORS answers browser preflights and stamps allowed responses.
//
// # Why this exists at all, and why only for the STORE surface
//
// The store surface is meant to be called from a browser: its identity is a
// publishable key, and that key's own documentation says it is NOT A SECRET and
// is expected to be visible. Yet a browser could not call it at all — the
// preflight died before the key was ever read — so the one topology that
// otherwise works end to end, a guest storefront in a browser, was unreachable.
//
// ADR 0011 rejected CORS, and this does not overturn that rejection: it refused
// CORS as a way to ship the ADMIN PANEL as a separate application, "because a
// token would have to be kept in the browser". The admin surface still gets no
// CORS here, for exactly that reason. What is opened is the surface whose key
// was designed to live in a browser.
//
// # Credentials are NOT allowed, and that is the CSRF decision
//
// Access-Control-Allow-Credentials is never sent. The store surface
// authenticates from a HEADER, and a header is not attached automatically by a
// browser — which is precisely where this API's CSRF immunity comes from
// (ADR 0011, decision 3). Allowing credentials would let a cross-site page ride
// an ambient cookie, and would destroy that immunity for a convenience nothing
// asked for.
//
// # An empty allow-list disables it
//
// With no origins configured, no CORS header is written and a browser is turned
// away exactly as before. That is the safe default: an installation opts IN to
// being callable from other sites.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := normalizeOrigins(allowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get(headerOrigin)

			// Vary is set whenever a policy EXISTS, on every response and not
			// only on the allowed ones: a cache that stored an answer without
			// it would serve one origin's response to another.
			if len(allowed) > 0 {
				w.Header().Add(headerVary, headerOrigin)
			}

			if origin == "" || !originAllowed(allowed, origin) {
				// A preflight from a disallowed origin still ends HERE with a
				// 204 and no CORS headers. Passing it down would run a real
				// handler for a request the browser is going to discard.
				if isPreflight(r) {
					WriteJSON(r.Context(), w, http.StatusNoContent, nil)

					return
				}

				next.ServeHTTP(w, r)

				return
			}

			writeAllowHeaders(w, origin)

			if isPreflight(r) {
				w.Header().Set(headerAllowMethods, strings.Join(corsAllowedMethods, ", "))
				w.Header().Set(headerAllowHeaders, strings.Join(corsAllowedHeaders, ", "))
				w.Header().Set(headerMaxAge, strconv.Itoa(int(corsMaxAge.Seconds())))
				WriteJSON(r.Context(), w, http.StatusNoContent, nil)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeAllowHeaders stamps the response as allowed for the origin.
//
// The ORIGIN is echoed rather than the wildcard even when the wildcard is
// configured. Both are valid without credentials, but echoing keeps the answer
// specific — a cache keyed by Vary then stores one response per origin instead
// of one shared "*" that a later, stricter policy would not invalidate.
func writeAllowHeaders(w http.ResponseWriter, origin string) {
	w.Header().Set(headerAllowOrigin, origin)
	w.Header().Set(headerExposeHeaders, strings.Join(corsExposedHeaders, ", "))
}

// isPreflight reports whether the request is a CORS preflight.
//
// A bare OPTIONS is NOT one: the method has other uses, and answering every
// OPTIONS as a preflight would swallow them. The request-method header is what
// makes it a preflight.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get(headerRequestMethod) != ""
}

// originAllowed reports whether the origin is on the list.
func originAllowed(allowed []string, origin string) bool {
	if slices.Contains(allowed, AnyOrigin) {
		return true
	}

	return slices.Contains(allowed, strings.ToLower(origin))
}

// normalizeOrigins trims and lowercases the configured origins.
//
// An origin is compared as an exact string, and browsers send it lowercased
// without a trailing slash. Normalizing here means a configuration written with
// a capital letter or a stray slash does not silently fail to match.
func normalizeOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "" {
			continue
		}
		if trimmed == AnyOrigin {
			out = append(out, AnyOrigin)

			continue
		}
		out = append(out, strings.ToLower(strings.TrimRight(trimmed, "/")))
	}

	return out
}
