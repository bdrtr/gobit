# ADR 0452 — A route takes a quota, and signing in has one

**Summary:** A plugin declares a quota on any bound route with `Host.RegisterRouteQuota`, and the admin sign-in, the invitation's acceptance and the panel's form share one quota per client, 20 per 15 minutes by default (`ADMIN_SIGN_IN_LIMIT`).
It costs an office behind one address its sign-ins counted together and a plugin a startup refusal for a quota on an unbound route, and buys a quota on core routes without a proxy rule.

- **Status:** Accepted; amended by [0462](0462-the-admin-surface-counts-a-network-and-names-an-origin-a-browser-sends.md): an IPv6 client is its /64
- **Date:** 2026-10-17
- **Amends:** [0137](0137-a-colleague-sets-their-own-first-password.md), whose acceptance had the whole-API limit alone

## Context

depremtek's backend, a consumer, limits sign-ins, return requests and
PayTR's token to a few a minute per client, as its old platform did (D307).
Its own routes take a quota in its handlers, but gobit's sign-in, invitation
acceptance, storefront return request and payment session are the core's: a
plugin cannot bind them again, and the identity module's routes were reached
only by wrapping the module from `main`. The rest went to nginx `limit_req`.
And gobit put no quota of its own on signing in: the account lock bounds the
guesses at one account, and one client spreading guesses over many accounts
met only the 600-a-minute whole-API limit.

## Decision

A plugin declares a quota in `Setup` with `Host.RegisterRouteQuota`, a name,
the routes it counts together as `/openapi.json` prints them, a limit and a
window, and the guard stack counts it per client after the whole-API limit
and before identity, on the guard backend, answering a spent one 429
`rate_limited` with `Retry-After`. `ADMIN_SIGN_IN_LIMIT` per
`ADMIN_SIGN_IN_WINDOW`, 20 per 15 minutes unless set, is the core's own quota
on `POST /admin/v1/auth/login`, `POST /admin/v1/auth/accept-invitation` and
`POST /admin/ui/login`, and zero turns it off.

## Consequences

- The default sits beside the lock, five failures for fifteen minutes: one
  client tries at most four accounts' worth of guesses per lock, and a person
  who mistypes meets their own account's lock first.
- A quota is keyed as the whole-API limit is (`TRUSTED_PROXY_HOPS`) and counts
  with that limit off; behind a proxy at zero hops it is one bucket for the
  shop, and a shared environment warns at startup.
- Quotas on one route are spent in declaration order and the first spent one
  refuses, those before it having counted the request; a counter that cannot
  answer lets the request through (ADR 0007).
- A quota names routes by method and pattern; a name a counter key cannot
  take, a second quota with one name, a catch-all, a percent-encoded byte, a
  limit or window that is not positive, and once everything is bound a route
  nothing serves stop the installation (`route_quota_invalid`), so a renamed
  route cannot leave a quota counting nothing.
- On Redis a quota counts under `<REDIS_KEY_PREFIX>.quota.<name>`, apart from
  the whole-API limit's counters.
- An embedder building the guards itself passes `GuardOptions.RouteQuotas`.

## Rejected

- A configuration variable listing quotas: the plugin that knows the route is the one that declares it, and the core's own needs one number.
- A middleware a plugin wraps around a core route: one plugin's panic or slowness would be every route's.
- Keying sign-ins by the address tried: anybody could lock a victim out, which the per-account lock already bounds.
- Leaving the sign-in to the whole-API limit: 600 a minute is a password spray.
