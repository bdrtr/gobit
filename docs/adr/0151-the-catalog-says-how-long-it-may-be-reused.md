# ADR 0151 — The catalog says how long it may be reused

**Summary:** The channel-scoped storefront reads carry `Cache-Control` when the
installation sets a TTL, `private` unless it also asks for `public`, and nothing
by default. It costs a shopper seeing a price up to the TTL late, and it buys the
edge cache ADR 0044 made possible.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

ADR 0044 moved the sales channel into the catalog PATH so that two keys
authorized for one channel receive byte-identical bodies — "which is what a shared
cache can store" — and then wrote down what it deliberately did not do: it turned
no cache on and it did not choose the freshness policy. That half has been open
since, and the feature list asks for it as A8.3's first slice.

The fact that decides it was measured there rather than here: the catalog body can
change WITHOUT a write, because a price list window opens against the clock
(`listablePrices` takes the clock as an argument). So a purge on write can never be
complete — nothing writes when 09:00 arrives — and a TTL is the only instrument
that can be.

Measurement: [measurements/0151](../measurements/0151-what-a-cache-was-not-told.md)

## Decision

The three channel-scoped reads write `Cache-Control: <scope>, max-age=<ttl>` on
their SUCCESS path, where the TTL comes from `STOREFRONT_CATALOG_CACHE_TTL` (zero,
the default, writes nothing) and the scope is `private` unless
`STOREFRONT_CATALOG_CACHE_SHARED` is on.

## Consequences

The default is off, which is the judgement `TRUSTED_PROXY_HOPS` records: the
configuration cannot know which installation holds. Turning it on changes what a
shop serves in a way a shopper sees — a price that was true a minute ago — so the
number is a trade an operator makes with their catalog's rhythm in hand.

The two settings are two questions. The TTL is freshness. `shared` is SECURITY: the
publishable key is a gate with no influence on these bodies since ADR 0044, so
`public` lets a CDN serve a stored body to a caller presenting NO key for as long
as the entry lives. Most shops want exactly that — a channel's catalog is what the
storefront shows the world and the key sits in a browser — but it is not this
repository's to decide, so the flag defaults to false and a shared installation
gets a boot WARNING naming the consequence and the remedy.

The header is written per handler, on the success path, and both halves are
load-bearing. A middleware would need the route table a second time, and it could
not see whether the handler is about to answer with a body or a refusal — and a
404 a CDN keeps for the TTL is a product that stays missing after somebody fixes
it. One test asserts the ABSENCE of the header on a refusal, and the in-process
proof asserts it on a refusal the guard RING produces, which the handler never
reaches.

A negative TTL stops startup while zero is accepted: zero is an answer and `-1h` is
a typo that would otherwise look like it did what it says.

No `ETag`, `s-maxage`, `stale-while-revalidate` or `Vary`. A validator is a second
decision needing a version stamp the body does not have; the CDN directives say
something about a cache's behaviour only its operator can choose; and a `Vary`
would key the cache on a header again, which is what ADR 0044 moved into the path
to stop.

## Rejected

- **Purge on write.** The body changes with the CLOCK, so a purge is incomplete by
  construction — and worse than a TTL, because nothing bounds the stale answer.
- **A non-zero default.** It changes what every existing installation serves, and
  the change is invisible until a shopper reads an old price.
- **One setting.** "Sixty seconds, shared" and "sixty seconds, private" are a
  freshness answer and a security answer; one knob would sell both at once.
- **The GraphQL endpoint too.** It stays POST-only for the two reasons ADR 0044
  left untouched, and a cache header on a POST is meaningless to most caches and
  misleading to the rest.
- **The unscoped taxonomy reads.** Collections, categories and tags are not
  channel-scoped, so they are not what this decision measured; scoping them is
  ADR 0044's open question, not this one's.
