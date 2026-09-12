# ADR 0155 — A plugin can put a screen in the panel

**Summary:** A plugin registers a screen as `{label, path, script bytes}`; the
panel renders the shell, serves the script from its own origin and puts the entry
in its menu — under a Content-Security-Policy the panel did not have before. It
costs a published value type and a policy that refuses anything undeclared, and it
puts a plugin's data on a page instead of in a terminal.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

The panel shipped six screens and no way to add a seventh: `sections()` is a
package-private slice and `internal/adminui` cannot be imported from outside. A
plugin could open an admin ENDPOINT — `plugins/analytics` does — and the only way
to read it was curl.

Two things were measured before the shape was decided, and both changed it. The
tree carried NO `Content-Security-Policy` on any surface: zero occurrences. And
the panel's own screens are already clean enough for a strict one — no inline
script, no inline style, no event attribute, no image, nothing using
`template.HTML`.

Measurement: [measurements/0155](../measurements/0155-the-panel-had-no-policy.md)

## Decision

`core/plugin.AdminPage{Label, Path, Script []byte}` is published and
`Host.RegisterAdminPage` collects them; the panel takes them as a constructor
argument, refuses a malformed one at STARTUP, and binds the shell, the script and
the menu entry from one list. Every panel response carries
`default-src 'none'; script-src 'self'; …`.

## Consequences

The script is BYTES, not a URL, and that is what makes the policy possible: the
panel serves it from its own origin, so `script-src 'self'` covers it and no
installation's policy is opened for a third-party host — including the ones that
installed no plugin.

A plugin ships no template, and ADR 0030's rejected alternative stays rejected:
one shell, owned here, renders every registered screen — the server draws the
frame, the script fills it from `/admin/v1` with the operator's session. Four
registrations are refused at startup rather than at a click, and two matter
more than the rest. A path outside the panel's prefix would be bound where the
panel's session ring does not run — an operator's screen with no operator check.
One colliding with a screen the panel ships would let a plugin take over the
catalog, silently, by registration order.

The route, the script's address and the menu entry come from ONE list. The panel
has already been bitten by half of that split — a screen nobody could reach from
the menu — and the other half is as easy to write: an entry whose link answers
404. Nothing else would catch either, because the route audits read prose and a
plugin's path appears in none.

The policy is installed once, on a chi group holding every panel route, and a gate
WALKS the router to prove all twenty carry it — not on the shared guard stack,
which also serves JSON to programs.

Eight mutations, eight bites, three only after the check was fixed — and all three
were the class this repository keeps producing: a check whose subject is not the
thing it claims. Binding the route without the menu entry stayed green because the
test called the helper instead of rendering the page; removing the plugin's own
registration stayed green because nothing asserted it; and replacing the
composition root's one wiring expression with nil stayed green because the
capability had a consumer while the WIRE between them had none.

## Rejected

- **A URL instead of bytes.** It forces `script-src` open for every installation
  to serve one that installed a plugin.
- **A template slot.** ADR 0030 refused server-renderer extension points; every
  plugin's markup would live in this binary.
- **A nonce.** Nothing needs one, and a mechanism nothing needs rots until the
  day it is load-bearing.
- **The policy on the shared guard stack.** A policy about scripts belongs to
  the pages, not to the JSON surfaces.
- **`X-Frame-Options` instead of `frame-ancestors`.** Both are sent: the
  directive is the standard and the header is what an older browser obeys.
