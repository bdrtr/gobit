package adminui

import "net/http"

// contentSecurityPolicy is what every panel page and asset carries (ADR 0155).
//
// # Why the panel needs a policy of its own
//
// The panel renders HTML inside an administrator's session, and since ADR 0030
// its newer screens are CLIENTS of /admin/v1: a shell rendered on the server, a
// script that fills it. That shape is what makes a policy load-bearing rather
// than decorative — a script the panel serves has the operator's session, so the
// question "which scripts may run here" is the question "who may act as the
// administrator".
//
// Before this the tree carried NO Content-Security-Policy at all, on any
// surface. Measured 2026-09-12: zero occurrences.
//
// # Why the policy can be this strict
//
// Because the panel earned it by construction. Its templates carry no inline
// script, no inline style, no event attribute and no image; the stylesheet and
// every script are LINKED from the panel's own origin; no template uses
// template.HTML, template.JS or template.URL (its own layout says so). So
// 'self' covers everything the panel does today without a nonce, and a nonce
// that nothing needs is a mechanism that rots.
//
// `default-src 'none'` is the part worth keeping. Anything the panel gains later
// — a font, an image, a web worker — fails LOUDLY in the browser's console
// rather than quietly working, which is the only way a policy stays true to the
// page it protects.
const contentSecurityPolicy = "default-src 'none'; " +
	// Scripts come from the panel's own origin and nowhere else. A plugin's
	// screen is served BY THE PANEL from bytes it registered (ADR 0155), which
	// is exactly why this can stay 'self': no third-party origin is ever named.
	"script-src 'self'; " +
	"style-src 'self'; " +
	// The API the panel's own screens read, on the same origin.
	"connect-src 'self'; " +
	// The login and the sign-out post to the panel; nothing else may.
	"form-action 'self'; " +
	// A <base> tag would let injected markup re-point every relative URL.
	"base-uri 'none'; " +
	// The panel may not be framed at all. It is the header's replacement for
	// X-Frame-Options and it is stricter: SAMEORIGIN would still allow a page
	// on this host to frame the panel.
	"frame-ancestors 'none'"

// The header names and the values that pair with the policy.
const (
	headerContentSecurityPolicy = "Content-Security-Policy"
	// headerReferrerPolicy stops the panel's own URLs from leaving with a
	// click. A panel path carries identifiers — which order, which customer —
	// and a Referer sent to another site is that identifier handed over.
	headerReferrerPolicy = "Referrer-Policy"
	referrerPolicy       = "no-referrer"
	// headerFrameOptions is sent BESIDE frame-ancestors rather than instead of
	// it: the directive is the standard and the header is what a browser too old
	// to read it obeys. They say the same thing, so they cannot disagree.
	headerFrameOptions = "X-Frame-Options"
	frameOptions       = "DENY"
)

// SecurityHeaders puts the policy on every response of the routes it wraps.
//
// # Why a middleware and not a call per handler
//
// Because a call per handler is a rule that holds until somebody adds a handler.
// The panel has twenty routes today; the twenty-first would be written by
// copying a neighbor, and a neighbor that had forgotten the call would
// propagate the omission. Wrapped once, a new route is covered by existing.
//
// # Why the composition root installs it on the PREFIX
//
// ADR 0155 put it on the chi group [UI.Routes] opens, which covers every route
// the panel binds — and that is a narrower thing than every response under the
// panel's address. A route bound on the same router AFTER the panel's group sits
// outside it: it still passes the identity and origin rings, which the guard
// stack scopes to the PREFIX, and it answered an operator's browser with no
// policy at all. Measured, not argued, and recorded as D97.
//
// So it moved to where those two rings already are (ADR 0157). The stack is
// shared with /admin/v1 and /store/v1 — surfaces that serve JSON to programs and
// have no use for a policy about scripts — which is why it is SCOPED there, to
// the panel's prefix, exactly as the origin and identity rings are.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set(headerContentSecurityPolicy, contentSecurityPolicy)
		header.Set(headerReferrerPolicy, referrerPolicy)
		header.Set(headerFrameOptions, frameOptions)

		next.ServeHTTP(w, r)
	})
}
