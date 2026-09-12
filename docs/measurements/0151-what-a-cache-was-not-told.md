# What a cache was not told

Evidence for [ADR 0151](../adr/0151-the-catalog-says-how-long-it-may-be-reused.md).

Measured 2026-09-12, closing the half ADR 0044 named and left open.

## The open half, in that record's own words

> **It does not turn on a cache.** No `Cache-Control`, no `ETag`, no
> `If-None-Match` is added to any JSON response.

> **It does not choose the freshness policy.** Short TTL against explicit purge is
> the open half of A8, and the fact that decides it — a body that changes with the
> clock and not with a write — is measured above rather than settled here.

Re-measured against today's tree, both sentences still hold: the only
`Cache-Control` values anywhere are the admin panel's embedded assets and
`GET /files/{key}`, and no JSON response carries one.

## The fact that decides it

A price list window opens against the CLOCK. `listablePrices` takes the clock as an
argument, so a price that becomes valid at 09:00 changes the catalog body with no
write anywhere — nothing to hang a purge on.

That settles the choice rather than informing it:

| Instrument | Can it be complete? |
|---|---|
| purge on write | **no** — nothing writes when a window opens |
| TTL | yes — staleness is bounded by a number the operator picks |
| TTL + purge on write | yes, and the purge only shortens the window |

So the TTL is the instrument, and the number is the operator's.

## The other question, which is not about freshness at all

After ADR 0044 the publishable key is a GATE: the channel comes from the path, so
the body is a function of the URL alone. Which means a `public` response can be
stored by a CDN and served to a caller that presents NO KEY — the gate is bypassed
for as long as the entry lives.

That is not a defect of the design; for most shops it is the point, because a
channel's catalog is what the storefront shows the world and a publishable key is
not a secret (ADR 0044 says so: it sits in the browser). But it is a decision a shop
makes, so it is a SECOND setting with a false default, and a shared installation is
warned at boot.

`private` is the middle answer and it is useful on its own: a shopper's own client —
a browser, or a storefront's server-side renderer — stops refetching, and no CDN may
store anything.

## Which responses, measured against the routes

ADR 0044 scoped exactly three REST reads to the channel, and those three are the
ones whose body the URL alone decides:

| path | policy |
|---|---|
| `/store/v1/sales-channels/{sales_channel_id}/products` | **carries it** |
| `/store/v1/sales-channels/{sales_channel_id}/products/{id}` | **carries it** |
| `/store/v1/sales-channels/{sales_channel_id}/option-values` | **carries it** |
| `/store/v1/collections`, `/store/v1/categories`, `/store/v1/tags` | no — not channel-scoped |
| `POST /store/v1/graphql` | no — POST, and ADR 0044's other two reasons stand |
| `/store/v1/search` (searchpg) | no — a plugin, and its body is a rank the query decides |

The three are a list in the test file, and the test walks it: a fourth channel-scoped
read arriving without a policy is how this feature fails silently — half the catalog
cacheable and half not, so a product page is a minute newer than the listing that
linked to it.

## The position of the call matters more than its value

The header is written on the SUCCESS path of each handler. Two alternatives were
rejected by what they cannot see:

- a middleware would need the route table a second time, and it cannot see whether
  the handler is about to answer with a body or a refusal;
- setting the header at the top of the handler is one line shorter and puts
  `max-age` on every 404 and 403 the endpoint produces.

A cached refusal is the expensive one. A 404 stored by a CDN for the TTL is a product
that stays missing after somebody fixes it, and a 401 stored for the TTL locks a
whole channel's catalog out. Both are asserted: the handler's own test on a 404, and
the in-process proof on a 401 — which the handler never produces, because the guard
ring answers first.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 71 | the header is written before the outcome is known | **bit** (1) |
| 72 | a zero TTL still writes a header | **bit** (3) |
| 73 | `private` is written as `public` | **bit** (4) |
| 74 | one of the three reads loses the policy | **bit** (2) |
| 75 | a negative TTL is accepted as "off" | **survived**, then bit |

Mutation 75 is the one worth the space, and it is a hole in the TESTS rather than in
the code: the validator refused `-1h` and nothing asserted that it does. A negative
duration read as "off" would let an operator write `-1h`, see no error, and believe
the catalog is uncacheable — which is the same shape as the mistakes this repository
keeps closing, a value that looks like it did what it says. The test that closes it
asserts both halves: `-1h` stops startup and `0s` survives as zero.

Mutation 73 bit four tests, and that number is worth reading: the word in the header
is asserted per read plus once for the shared case, so a scope that flipped could not
hide in one endpoint.

## What is NOT closed

No validator. An `ETag` needs a version stamp for a catalog page, and the page is a
join over products, variants, prices and stock — so the stamp is a decision about
what "this page changed" means, not a header.

No CDN directives (`s-maxage`, `stale-while-revalidate`), because they say something
about a particular cache's behaviour that only the operator running it can choose.

No purge endpoint. It would shorten the window rather than replace the TTL, and it
needs a list of which URLs a write invalidates — which is the join above, from the
other direction.

## What was not measured

The hit rate. A shop's hit rate depends on its catalog's shape and its traffic, and
nothing here can predict it; what the slice removes is the reason there could not be
one (`Cache-Control` absent) rather than a number this repository can promise.
