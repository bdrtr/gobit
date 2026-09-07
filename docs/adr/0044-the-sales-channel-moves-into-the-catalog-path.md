# ADR 0044 — The sales channel moves into the catalog PATH, and the publishable key becomes a gate rather than a body input

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A8 asks how the storefront catalog becomes cacheable by anything in front of
the origin, and it writes down three candidate answers: the channel moves into
the PATH, single-channel installations opt in explicitly, or the cache stays
per-key and the hit rate is accepted for what it is.

None of the three is free, because the catalog body already varies per caller
and it varies through a HEADER. `RequireStore` in `core/http/auth.go` reads
`x-publishable-api-key`, resolves it to a principal carrying sales channel ids,
and the catalog filters on those ids in SQL. Measured on the tree, the channel
filter is applied on exactly five read paths:

| path | where the channel enters |
|---|---|
| `GET /store/v1/products` | `internal/modules/product/api/store.go` |
| `GET /store/v1/products/{id}` | `internal/modules/product/api/store.go` |
| `GET /store/v1/option-values` | `internal/modules/product/api/store.go` |
| `POST /store/v1/graphql` | `internal/modules/product/graph/resolver.go` |
| `GET /store/v1/search` | `plugins/searchpg/api.go` |

All five reach the same rule, `SalesChannelIDsFromContext` in
`internal/modules/product/graph/graph.go`, or a copy of it written out in the
search plugin. Every one of them takes the channel set from the VERIFIED
IDENTITY and from nowhere else. The other storefront reads are not
channel-scoped at all: collections, categories and tags are served unfiltered,
and the cart, customer, order and payment routes are personal and would never be
edge-cached.

Nothing in this repository emits `Vary` for that header. `Vary` is written in
exactly one place in the tree, in `core/http/cors.go`, for `Origin`, and only
when a CORS policy is configured — which by default it is not. And no JSON
response carries `Cache-Control`, `ETag` or `If-None-Match` anywhere: the only
two cacheable responses in the tree are the admin panel's stylesheet and
`GET /files/{key}`. Of the two `Cache-Control` values written anywhere in the
tree, only the one at `cacheControl` in `internal/modules/file/api/serve.go` is
argued against SHARED caches — it names a CDN and a reverse proxy and picks one
hour so that a delete reaches everywhere within the hour; the other, written by
`WriteAsset` in `core/http/response.go`, is argued from the BROWSER not
refetching an embedded asset. Two further places reason about a shared cache
while emitting no cache header at all, and both are quoted below: the GraphQL
handler's godoc and `core/http/cors.go`. So the catalog is not cached badly.
**There is no cache key a shared cache can see.**

The repository has already written this dilemma down, in the GraphQL handler's
godoc arguing why the GET transport was deliberately not added: the response
"varies with the request's publishable key, that is, with the sales channel",
and a shared cache "would either have to vary by the key header (that is, cache
almost nothing) or serve one storefront's catalog to another". **That sentence
equates key-variance with channel-variance — and it is the EQUATION, not the
variance, that this decision breaks.**

Two further measured facts any cache design here has to survive.

- **The body is impersonal apart from the channel.** Every rule-bearing price is
  dropped before the catalog ever sees it, with the reason written at
  `listablePrices` in `internal/modules/pricing/service/provider.go`: a rule
  needs a context (region, customer group) the provider does not carry, and
  ignoring a condition it cannot evaluate would open a segment price to
  everyone. The channel set is the ONLY per-caller input on the store read path.
- **The body is time-varying with no write behind it.** That same
  `listablePrices` takes the clock as an argument, so a price list window opening
  at midnight changes the product response with no product write; and
  `StoreVariant` in `internal/modules/product/service/store.go` carries
  inventory's live `available_quantity` record. This defeats an ETag derived from
  the product record, and it defeats a long `max-age`.

## Decision

**The sales channel is a PATH SEGMENT on the channel-scoped catalog reads.**

```
GET /store/v1/sales-channels/{sales_channel_id}/products
GET /store/v1/sales-channels/{sales_channel_id}/products/{id}
GET /store/v1/sales-channels/{sales_channel_id}/option-values
GET /store/v1/sales-channels/{sales_channel_id}/search
```

The segment is LITERAL rather than a bare parameter under the prefix, because
`/store/v1/{sales_channel_id}/products` would put a wildcard where `carts`,
`customers`, `orders` and `collections` already sit. It is spelled the way the
admin surface already spells the concept in
`DELETE /admin/v1/products/{id}/sales-channels/{sales_channel_id}`, and the
parameter carries the same name, so one concept keeps one spelling on both
surfaces.

**Nothing has to be added to make the id fit in a URL.** The channel id is the
`sc_` prefix plus twenty-six characters of unpadded Crockford Base32 over
`0123456789ABCDEFGHJKMNPQRSTVWXYZ` (`internal/modules/auth/models/ids.go`).
Every character of that alphabet, and the underscore in the prefix, is
unreserved under RFC 3986 — no percent-encoding, no new column, no migration.

**The path NARROWS. It never BROADENS.** The origin intersects the channel named
in the path with the set the authenticated key carries, and a path naming a
channel the key does not hold is refused rather than served. Where there is no
identity at all — the deployment that never wired store authentication up, which
`SalesChannelIDsFromContext` returns nil for on purpose — there is no set to
intersect and the path value stands alone, which is NARROWER than today's
behavior and never wider.

That rule is the whole safety of this decision, and it is what keeps ADR 0008's
boundary intact: a value the client states is a CLAIM, not a fact. The path is
where the claim is written; the key is what turns it into evidence. It is also
what preserves the invariant `internal/e2e/channel_catalog_test.go` pins in
`TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString` — the client may not
reach a channel it was not given.

**The body then becomes a function of the channel alone, and the key degrades to
a pass/fail gate.** Two different keys authorized for the same channel receive
BYTE-IDENTICAL bodies. The cache key is the URL, which every shared cache
already keys on with nothing configured, and the description follows for free:
`pathParameters` in `core/openapi/openapi.go` turns a `{name}` placeholder into
a described parameter, so the new segment appears in `/openapi.json` the day the
route does.

**This decision fixes the cache KEY and nothing else.** No header is emitted, no
freshness policy is chosen, and no cache is turned on by it.

## Rejected alternatives

**Single-channel opt-in: an installation declares it serves exactly one channel,
and the catalog is cacheable at today's URLs.** The strongest of the three
candidates on cost — zero URL churn, no breaking change, and it is honest about
the shop most likely to want a CDN, which really does have one storefront. What
kills it is that it makes a CONFIGURATION change what a URL MEANS. The same
`/store/v1/products` would return a channel-filtered body in one deployment and
an unfiltered one in another, with nothing in the response saying which, and a
cache in front of the wrong one serves the wrong catalog silently. The schema
also says the model is plural: `api_key_sales_channel` in
`internal/modules/auth/migrations/000001_auth_init.up.sql` is many-to-many with
a composite primary key, and it carries a second index specifically for the
reverse direction, whose comment anticipates "listing the keys bound to a
channel". An opt-in that is only correct while a table happens to hold one shape
of row is a promise the schema does not keep — and it helps least exactly where
it is needed most, the installation running two storefronts off one catalog.

**Per-key caching with `Vary: x-publishable-api-key`.** It buys the most: no URL
change, no authorization rethink, and it is CORRECT, because the response does
genuinely vary with the key. It dies on reach and on hit rate. On reach, because
the key is a custom request header and nothing in the tree emits `Vary` for it
today — so this option is also an unwritten change, and the change it asks for
is a header a CDN must be configured to key on instead of a path it can already
see. On hit rate, because the same schema that sinks the previous option sinks
this one: many keys may be bound to one channel, and a per-key cache stores that
many identical bodies where a path-keyed cache stores one, so a key rotation
empties the cache for a body that did not change. `core/http/cors.go` already
reasons about this shape when it explains that a cache keyed by `Vary` stores one
response per value. The GraphQL godoc names the outcome outright: vary by the
key header, that is, cache almost nothing.

**Leave the header where it is and let the edge fold it into the cache key.**
Most CDNs can add a request header to their cache key, so this needs no code at
all. It is rejected because gobit is a LIBRARY (ADR 0025) and ships to
installations whose edge it does not own. The cache key would then live in
somebody else's configuration, outside this repository, outside its tests, and
outside every audit under `internal/arch` — and the failure of getting it wrong
is one storefront's catalog served to another, with no code change to point at.
A correctness property that holds only if a stranger's configuration is right is
not a property this repository can claim.

**Put the channel in the QUERY STRING, which a CDN can key on just as easily.**
Cheaper than a path change and mechanically equivalent for a cache. It is
rejected because the repository has already decided the opposite and pinned it in
three godocs and two tests: the storefront handler, the GraphQL rule and the
search plugin each state that the query string is NEVER consulted for the
channel, and `TestStoreListIgnoresChannelQueryParam` together with
`TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString` assert that
`?sales_channel_id=` is ignored. Giving a live meaning to the one input three
places promise is dead means inverting a test whose failure mode is a catalog
leak. A path segment is a NEW input carrying no prior promise, so its rule can be
written once and audited once.

**Give the channel a human-readable handle and put THAT in the path.** It would
make the URL prettier, and the pattern is in-tree three times over — product,
collection and category each pair a handle validated by `resolveHandle` in
`internal/modules/product/service/validate.go` with a partial unique index in
`internal/modules/product/migrations/000001_product_init.up.sql`. The nearest
fourth, `product_tag`, is NOT the pattern: it carries no handle and uniquely
indexes `value` instead. It is rejected as a prerequisite, not as an idea: the
id is already URL-safe, so
a handle buys appearance and costs a column, a uniqueness rule, a validation
surface and a resolution step on every catalog read, for a segment the storefront
generates and nobody types. If channel URLs ever become human-facing that is its
own decision; it is not this one.

**Accept the status quo and scale the origin.** It is the honest option if
caching is not wanted. It is rejected because A8 asks whether the catalog CAN be
edge-cached and today the answer is structurally no rather than not-yet — and
because of WHEN. The repository is at v0.8.0. A URL shape is the cheapest thing
to change before 1.0 and one of the most expensive after it, so deferring this
does not avoid the cost, it picks the worse moment to pay it.

## Consequences

**Positive**

- **The cache key becomes something a shared cache can see with nothing
  configured.** A CDN keys on the URL by default. No `Vary`, no custom cache-key
  rule, no per-installation edge configuration for this repository to depend on.
- **One cache entry per channel instead of one per key.** The schema permits many
  keys per channel; after this, rotating or adding a key does not multiply or
  invalidate a body that is identical either way.
- **The GraphQL godoc's dilemma gets its third answer.** It framed the choice as
  vary-by-header or leak-across-storefronts. A channel in the path is neither: the
  body stops varying by key, so there is nothing to vary on and nothing to leak
  at the origin.
- **The channel becomes visible where the operator already looks.** The span
  attributes written in `core/http/telemetry.go` include `url.path` and no
  request header, so the channel appears in a trace for free — while the span
  NAME stays the route pattern, which is what keeps metric cardinality from
  exploding, so the segment costs no new time series.
- **A multi-storefront installation is legible at the address.** Today two
  storefronts differ only by an invisible header value; after this they differ by
  URL, which an access log, a cache report and a bug report can all read.

**Negative, and accepted**

- **Every storefront catalog URL changes, and that is a breaking change.** Four
  routes move, one of them a plugin's published `SearchPath` constant, and every
  document and client that names the old paths follows. This record is the
  argument for paying that at v0.8.0 rather than the claim it is free.
- **The channel id becomes public.** It goes into every catalog URL, and
  therefore into browser history, referrers, shared links and access logs. Its
  first characters are a millisecond timestamp by construction, so the URL also
  discloses roughly when the channel was created. Neither matters while the
  origin authorizes, because the filter stays an authorization THERE — but an
  operator who lets the edge answer without contacting the origin has made that
  channel's catalog readable by anyone holding the URL. That is a deployment
  choice this decision ENABLES and does not make. `RequireStore`'s own godoc
  states the threat model that makes it defensible ("The key is NOT A SECRET;
  its purpose is to bind the request to a sales channel, not to keep anything
  confidential") — defensible is not automatic, and an installation whose
  catalog must not be read by strangers has to keep the origin in the loop.
- **A key bound to several channels loses its union view.** Today a key holding
  two channels sees both catalogs merged in one response, and unit tests in
  `internal/modules/product/api/saleschannel_test.go` exercise exactly that
  shape. With one channel per path it makes two requests. This is probably the
  right shape — a storefront serves a storefront — but it is a capability
  removed, and **whether any consumer needs the union was not measured**, only
  that the in-tree end-to-end fixtures all bind a single channel.
- **The channel now enters through TWO doors and only one of them is audited.**
  The catalog read takes it from the path intersected with the identity; the cart
  write still takes it from the identity alone, in
  `internal/workflows/cart/saleschannel.go`.
  `internal/arch/sales_channel_scope_test.go` holds those two surfaces together
  by pinning that they derive the same set FROM THE IDENTITY — after this, the
  identity is only half of the read surface's input, and the intersect is outside
  what that test can see. The audit has to grow or it goes quietly stale, which
  is precisely the class this repository keeps being bitten by.
- **The storefront's URLs stop being uniform.** Products, the single product,
  option values and search gain the segment; collections, categories and tags do
  not, because they are not channel-scoped today. A reader will ask why, and the
  answer — those endpoints genuinely do not vary by channel — is true but has to
  be written somewhere a reader will find it.
- **A cacheable KEY is not a cacheable BODY.** The measured time-variance stands:
  a price list window opens against the clock and the variant carries a live
  quantity, so a record-derived ETag would be wrong and a long `max-age` would
  serve a closed campaign. This decision makes the catalog KEYABLE and leaves it
  no fresher than it was.

## What this deliberately does NOT do

- **It does not turn on a cache.** No `Cache-Control`, no `ETag`, no
  `If-None-Match` is added to any JSON response. What exists today stays the only
  two cacheable responses in the tree.
- **It does not choose the freshness policy.** Short TTL against explicit purge
  is the open half of A8, and the fact that decides it — a body that changes with
  the clock and not with a write — is measured above rather than settled here.
- **It does not reopen the GraphQL GET transport.** Only ONE of the three reasons
  written into that handler's godoc is weakened by this decision. The other two
  are untouched and are enough on their own: the whole query would land in URLs,
  proxy logs and browser history, and a long query dies at common proxy limits
  with a 414 the client cannot diagnose. The endpoint stays POST-only and stays
  uncached.
- **It does not add a channel handle.** The id is URL-safe as it stands, so no
  column, no uniqueness rule and no resolution step is introduced by this record.
- **It does not scope the taxonomy endpoints.** Collections, categories and tags
  are unfiltered today and stay unfiltered; whether they SHOULD be channel-scoped
  is a separate question this decision does not answer in either direction.
- **It does not change what a channel MEANS.** The visibility rule is untouched:
  a product with no assignment is visible in every channel, a product with one is
  visible only in the channels it is assigned to.
- **It does not let the client choose its channel.** The path can only narrow
  within what the key already carries. A path segment that could widen the answer
  would be the query-string mistake with a different spelling.

## Related

- [ADR 0008](0008-musteri-kimligi-guven-siniri.md) — the boundary that keeps a
  client's declaration out of an authorization decision, which is what the
  narrowing rule exists to honour.
- [ADR 0025](0025-gobit-is-a-library-not-a-template.md) — gobit ships to
  installations whose edge it does not own, which is why the cache key may not
  live in a CDN configuration.
- [ADR 0035](0035-the-schema-vocabulary-is-published.md) — the description that
  follows a route, so a new path segment arrives in `/openapi.json` with it.
- [ADR 0040](0040-in-stock-is-a-catalog-answer-over-an-inventory-fact.md) — the
  inventory field this same body carries live, and part of why the body is
  time-varying.
- [ADR 0041](0041-a-price-filter-compares-the-base-price-at-quantity-one.md) —
  the other half of that time-variance, evaluated against the clock.
