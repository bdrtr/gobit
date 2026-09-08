# ADR 0076 — ADR 0030's migration begins with the review screen, and the API's immunity becomes a DEFENSE

**Summary:** The panel's first screen served from `/admin/v1` is the moderation
queue, and the cookie that reaches it is guarded by an origin check.

- **Status:** Accepted
- **Date:** 2026-09-09
- **Amends:** [ADR 0011](0011-yonetim-paneli-dorduncu-agac.md) decision 3, as
  [ADR 0030](0030-the-panel-becomes-an-admin-api-client.md) said it would.

## Context

ADR 0030 decided the panel becomes a client of `/admin/v1`. It was accepted on
2026-09-06 and never built; D34 records that, and records a gate's godoc having
described it as done.

Meanwhile the review module grew a complete admin surface — queue, decision,
proposal, filter, agreement report (ADR 0071 to 0074) — none of it reachable
from the panel gobit ships. Building that section the way the other five are
built meant two new module contracts and more of the shape 0030 decided away.

## Decision

**The migration starts with the REVIEW screen.** A section nobody has yet is
where a new shape costs no rewrite. The other five stay server-rendered, and the
order they move in is not decided here.

**The screen is a shell and a script.** `reviews.gohtml` renders a mount point
and two paths; `assets/reviews.js` fetches, renders and posts. No framework and
no build step, for the reason the SMTP plugin writes its own MIME.

**The session cookie's Path widens from `/admin/ui` to `/admin`.** That is the
change ADR 0030 named, and it spends what ADR 0011 called the backbone of the
design: the admin API's CSRF immunity was an ABSENCE — the token lived in a
header a browser never attaches by itself.

**The absence is replaced by a defense.** `UI.APISession` promotes the cookie to
the header `corehttp.RequireAdmin` reads, and a state-changing request that
authenticates BY COOKIE must carry a same-origin `Origin` header.

**A request carrying its own `Authorization` header is untouched.** That is the
whole boundary: `curl`, a server-to-server integration and a CI job send no
`Origin`, and none of them was ever CSRF-able, because nothing makes a browser
attach a bearer token cross-site. Checking every request would break every
non-browser client; checking none would leave the subdomain case `SameSite`
does not cover.

**No module gained a contract.** The review module was not touched: the panel
reads the API it already published.

## Consequences

- **The test that pinned the absence became a matrix.** An absence needs one
  assertion; a defense can be wrong in two directions and only one is loud. Both
  are proved by mutation: removing the check opens the CSRF case, widening it to
  every request breaks the panel's own reads.
- **The panel carries TWO shapes until the migration finishes**, and the review
  section is deliberately last in the menu so the one that behaves differently
  is met last.
- **The script ships embedded with an ETag from its bytes**, like the
  stylesheet. Under the wrong content type it would not run at all —
  `WriteAsset` sends `nosniff` — so the type is a constant with a test on it.
- **A screen that failed silently would show an empty queue**, the one answer
  this feature must never give wrongly. The shell carries a `noscript` block
  naming the API, and tests check it carries its script and its prefix.
- **The cookie now travels to every `/admin/v1` request**, reads included. No
  cross-origin page can read those responses: CORS is applied to the store
  surface only and `Allow-Credentials` is never sent — checked, not assumed.

## Rejected

- **A server-rendered review section.** It needed `review.query` and
  `review.admin`, ~440 lines of module contract that ADR 0030 then discards.
- **Superseding ADR 0030.** Its argument — one admin surface rather than a
  public API beside a private in-process path — was not weakened by anything
  found here.
- **The origin check on every admin request.** It breaks every client that is
  not a browser, and those were never the ones at risk.
- **Migrating an existing screen first.** The cost is a rewrite of something
  that works, paid before the shape has been tried once.
