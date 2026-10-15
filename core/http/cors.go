package http

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORS headers, spelled once.
const (
	headerOrigin        = "Origin"
	headerAllowOrigin   = "Access-Control-Allow-Origin"
	headerAllowMethods  = "Access-Control-Allow-Methods"
	headerAllowHeaders  = "Access-Control-Allow-Headers"
	headerExposeHeaders = "Access-Control-Expose-Headers"
	headerMaxAge        = "Access-Control-Max-Age"
	headerRequestMethod = "Access-Control-Request-Method"
	headerVary          = "Vary"
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
//
// It carries If-None-Match, the request half of the catalog's validator pair
// (ADR 0391), so a script on an allowed origin can revalidate a read itself,
// and [OrderTokenHeader], so one can open the order its shopper just placed
// (ADR 0439). The headers a plugin's storefront routes decide access on join
// it from [GuardOptions.CORSHeaders], declared by the plugin rather than asked
// for by the browser (ADR 0444).
var corsAllowedHeaders = []string{
	"Content-Type",
	"Accept",
	PublishableKeyHeader,
	IdempotencyKeyHeader,
	requestIDHeader,
	"If-None-Match",
	OrderTokenHeader,
}

// OrderTokenHeader is the header a storefront request presents an order's
// access token in (ADR 0439).
//
// The order module's storefront routes read it; the core names it because the
// browser allow-list above has to, and a header a script cannot send is a
// route a browser storefront cannot call. It is a header rather than a query
// parameter because the request log writes the path, and a token in the path
// would be a token in every log line.
const OrderTokenHeader = "X-Order-Token"

// corsExposedHeaders are the response headers a browser may read: the request
// id, the ETag that is the response half of the validator pair (ADR 0391), and
// the Retry-After a checkout refused because its payment is not reported yet
// says when to send the completion again with (ADR 0438).
var corsExposedHeaders = []string{requestIDHeader, "ETag", retryAfterHeader}

// corsAllowedMethods are the methods the store surface answers. A method a
// store route binds and this list leaves out is a route a browser storefront
// cannot call, since its preflight is answered without it: PATCH on a cart's
// line item was one (D287). internal/app's
// TestEveryStoreMethodPassesABrowsersPreflight walks every installation the
// tree ships for such a method.
var corsAllowedMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions,
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
	return cors(allowedOrigins, nil)
}

// cors is [CORS] allowing the extra request headers beside the core's own:
// the headers the plugins' storefront routes decide access on (ADR 0444).
func cors(allowedOrigins, extraHeaders []string) func(http.Handler) http.Handler {
	allowed := normalizeOrigins(allowedOrigins)
	headers := strings.Join(allowedHeaders(extraHeaders), ", ")

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
				w.Header().Set(headerAllowHeaders, headers)
				w.Header().Set(headerMaxAge, strconv.Itoa(int(corsMaxAge.Seconds())))
				WriteJSON(r.Context(), w, http.StatusNoContent, nil)

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// allowedHeaders is the core's list followed by the extra headers it does not
// already carry, each once, compared as HTTP compares them. An extra name
// [CheckStoreProofHeader] refuses is left out rather than written into the
// preflight answer: "X-A, Authorization" would allow a second header.
func allowedHeaders(extra []string) []string {
	out := slices.Clone(corsAllowedHeaders)
	seen := make(map[string]bool, len(out)+len(extra))
	for _, h := range out {
		seen[http.CanonicalHeaderKey(h)] = true
	}
	for _, h := range extra {
		key := http.CanonicalHeaderKey(strings.TrimSpace(h))
		if seen[key] || CheckStoreProofHeader(key) != nil {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}

	return out
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

// reservedProofHeaders are names a plugin may not declare as a storefront
// proof header beside the core's own list: the trace context, which a client
// may send fresh on every attempt, and the credentials the store ring does not
// decide on.
var reservedProofHeaders = []string{"Traceparent", "Tracestate", "Authorization", "Cookie"}

// CheckStoreProofHeader says why a name cannot be a header a plugin's
// storefront routes decide access on (ADR 0444), or nil when it can.
//
// The name must be an HTTP field name (RFC 9110, section 5.1), so it cannot
// smuggle a second name into the preflight answer, and it must not be one the
// core already reads: a declared header keys every store write off a cart, so
// a request id sent fresh per attempt would turn idempotency off for all of
// them, and an order's token or the publishable key would move records the
// core already keys.
func CheckStoreProofHeader(name string) error {
	if !fieldName(name) {
		return fmt.Errorf("%q is not a header field name", name)
	}
	key := http.CanonicalHeaderKey(name)
	for _, own := range slices.Concat(corsAllowedHeaders, reservedProofHeaders) {
		if http.CanonicalHeaderKey(own) == key {
			return fmt.Errorf("%s is a header the core reads", key)
		}
	}

	return nil
}

// fieldName reports whether name is one or more token characters.
func fieldName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}

	return true
}
