# ADR 0451 — A panel on its own host calls the admin API

**Summary:** `ADMIN_CORS_ALLOWED_ORIGINS` lists the sites whose pages may call `/admin/v1` from a browser with their own bearer token; the admin surface allows no credentials and no wildcard.
It costs an operator a list to keep and every listed site's script the power of the tokens it holds, and buys a separate panel its calls without a same-origin proxy.

- **Status:** Accepted; amended by [0461](0461-a-panel-may-name-the-version-it-read.md): the preflight allows `If-Match`
- **Date:** 2026-10-17
- **Amends:** the historical [0011](0011-yonetim-paneli-dorduncu-agac.md), whose rejection of CORS on the admin surface rested on a token that a separate panel now keeps in its page anyway

## Context

depremtek's backend, a consumer, serves its operator panel at
`admin.depremtek.market` and its seller panel at `satici.depremtek.market`,
each a browser application that signs in at `POST /admin/v1/auth/login` and
sends the token it gets as `Authorization: Bearer` (D306). gobit wrote CORS on
the store surface only, so every preflight to `/admin/v1` went unanswered and
the panels could reach gobit only through a reverse proxy at their own
origin. ADR 0011 refused admin CORS because the token would have to live in a
browser; for these panels it already does, proxy or not.

## Decision

An installation lists the origins whose pages may call the admin surface in
`ADMIN_CORS_ALLOWED_ORIGINS`, and the admin surface answers their preflights
before identity with `Authorization`, `Content-Type`, `Accept`,
`Idempotency-Key`, `X-Request-Id` and `If-None-Match` allowed. It never sends
`Access-Control-Allow-Credentials`, and configuration refuses `*` and any
entry that is not an origin.

## Consequences

- A listed site's script reads every answer the token it sends opens, as it
  did through a proxy; the list says which sites an operator trusts with that.
- The built-in panel's session cookie is not ridden from a listed site: a
  preflighted request does not carry it without credentials allowed, a simple
  one is answered to no script, and a cookie-bearing write is still refused by
  the panel's own origin check.
- An empty list, the default, writes no CORS header on the admin surface, as
  before; a same-origin proxy keeps working and is still the choice when the
  panel and the API share a host.
- The store surface's list and the admin surface's are separate: a storefront
  site does not read admin answers by being listed for the store.
- Answers expose `X-Request-Id`, `ETag`, `Retry-After`, `Idempotency-Replayed`,
  `Location` and `Content-Disposition`.
- An embedder building the guards itself passes `GuardOptions.AdminCORSOrigins`;
  `corehttp.AdminCORS` drops a wildcard there too.

## Rejected

- Keeping the proxy as the only way: the token is in the page either way, and the proxy is a deployment the framework asks for and does not need.
- Allowing credentials so a separate panel could use a cookie: it reopens the CSRF immunity the header gives (ADR 0011, decision 3).
- One list for both surfaces: a storefront's site would read admin answers.
- Taking `*`: a site that may read admin answers is one an operator named.
