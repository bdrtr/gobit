# ADR 0368 — A module keys a client as the installation does

**Summary:** The composition root provides the installation's client key
under `corehttp.ClientKeyName`, and `contrib/identity-session` keys its
registration limit with it, so behind a trusted proxy each shopper has a
quota of their own.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The installation keys its rate limit with `TrustedProxyIPKey`, built from
`TRUSTED_PROXY_HOPS`, and `docs/security.md` says what happens behind a
proxy without it: the connection's address is the proxy's, and a per-client
limit becomes one quota for the whole shop. `contrib/identity-session`
limited its registration endpoints with `ClientIPKey`, the connection's
address, and nothing the installation could set reached it. Behind the
reverse proxy nearly every installation stands behind, or a storefront that
calls gobit from its server, every registration of the shop shared one
quota.

## Decision

The composition root provides the installation's client key under
`corehttp.ClientKeyName`, and a module that limits a route of its own keys
it with that. `contrib/identity-session` reads it when it registers, unless
`Options.LimitKey` names another, and falls back to the connection's address
where the container holds none.

## Consequences

- Behind a trusted proxy, `TRUSTED_PROXY_HOPS` now reaches the registration
  limit as it reaches the installation's, and each shopper has their quota.
- An installation that trusts no hop keeps today's key: the connection's
  address.
- A storefront calling gobit from its server forwards its shopper's address,
  and the installation trusts it as one hop; the storefront has to read that
  address from a header nothing but its own platform writes.
- An embedder composing gobit without its root provides no key, and the
  module keeps the connection's address, as before.
- `ClientKeyName` joins the published names of `core/http`.

## Rejected

- Asking every embedder to set `Options.LimitKey` from their own reading of
  the hops: the default would stay the shop-wide quota, and the setting
  would be read in two places.
- Keying the registration limit by the address registered: a stranger
  pointing the shop at many addresses is what the limit exists to stop, and
  each address would be a fresh quota.
