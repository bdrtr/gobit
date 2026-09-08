# Storefront speed and checkout — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The brief: product and category endpoints carrying ETag/Cache-Control so a CDN
can serve them, with cart and price personalisation moved to separate endpoints;
real-time stock and price over SSE or WebSocket; single-page checkout, guest
checkout, saved cards, passkey login; and visual/semantic search on pgvector in
the same database.

### Edge cache: the repository already argued against it, in writing

**No JSON response anywhere carries `Cache-Control`, `ETag`, `Vary`,
`Last-Modified` or `Age`.** `corehttp.WriteJSON` writes exactly two things — a
content type and a status — and takes no ETag parameter, so no caller could
supply one. Only two responses in the whole tree are cacheable: the admin
panel's stylesheet (`WriteAsset`, `immutable`) and `GET /files/{key}`
(`public, max-age=3600`, identity-free, and its godoc is the only place that
reasons about shared caches at all).

**And the premise does not hold as stated: the product body already varies by
caller.** `RequireStore` reads `x-publishable-api-key`, resolves it to a
principal carrying sales-channel ids, and the storefront listing and detail
filter on those channels in SQL. Two keys on the SAME URL get different bodies —
covered by four integration tests — and no `Vary` is emitted for that header.
`Vary` appears in exactly one place in the repository, for `Origin`, and only
when CORS is configured, which by default it is not.

The codebase has already written this argument down. From the GraphQL handler:
*"The GET transport was DELIBERATELY not added. GET's only real gain is
intermediate caches and that gain does NOT EXIST here: the response varies with
the request's publishable key, that is, with the sales channel. A shared cache
would either have to vary by the key header (that is, cache almost nothing) or
serve one storefront's catalog to another — which is exactly why the channel
filter exists."*

So the brief's instinct — separate the personalised part — is right, and the
personalised part is not the cart. **It is the sales channel, and it sits on the
catalog endpoint itself, in a header.** Making the catalog edge-cacheable means
deciding one of: the channel moves into the PATH (a cache key a CDN can see), or
single-channel installations opt in explicitly, or the cache is per-key and the
hit rate is accepted for what it is.

Two further facts a cache design has to survive:

- **Price does not vary per caller on the product endpoint** — every rule-bearing
  price is dropped by the provider, with the reason written where it happens:
  a rule needs a context (region, customer group) the provider does not carry.
  So the catalog body is genuinely impersonal apart from the channel.
- **The body is time-varying with no write behind it.** A price-list window
  opening or closing changes the product response with no product write, because
  the provider evaluates against the clock. That defeats an ETag computed from
  the record and it defeats a long `max-age` — it is the fact that decides
  whether the answer is a short TTL or an explicit purge.

There are 42 routes under `/store/v1`, and the GraphQL endpoint is POST-only.

### Real-time stock: nothing runs, and more of it is present than expected

There is no SSE, WebSocket, long-poll or streaming endpoint. `text/event-stream`
appears once in the tree, in a test.

But the groundwork is unusually far along, and by accident:

- **The shared response-writer wrapper is already streaming-correct and
  tested.** It forwards `Unwrap`, `Hijack` and `Flush`, and its godoc names
  WebSocket explicitly. A streaming handler would not have to fight the
  middleware.
- **`github.com/coder/websocket` is already in `go.mod`** (indirect, pulled by
  gqlgen), and gqlgen's transport package on disk already ships `sse.go` and
  `websocket.go`. The transports are in the module graph and simply never added
  — the storefront schema has a `Query` type and no `Subscription`.
- **The storefront product response already carries live stock**:
  `inventory_item.available_quantity` reaches the client through the link
  expansion, because the expansion requests no field list and therefore gets the
  provider's full set.

Four blockers, and each is a decision rather than a library choice:

1. **Inventory publishes no events at all.** `SetInventoryLevel`,
   `AdjustInventory`, `Reserve`, `ReleaseReservation` and `ConfirmReservation`
   are silent. There is nothing to push.
2. **The event bus cannot fan out to N web processes as configured.** Redis
   Streams consumer groups deliver each message to exactly ONE consumer in the
   group, and every subscriber joins the same group. Pushing the same change to
   every connected browser needs a second delivery shape.
3. **`WRITE_TIMEOUT` is 30 seconds, mandatory, and unexempted.** A long-lived
   connection dies at thirty seconds. The pprof listener hit exactly this and
   the answer was a second listener with no write budget — the precedent exists
   and its cost is known.
4. **A browser `EventSource` cannot send `x-publishable-api-key`.** The store
   guard requires that header on every request. Real-time on the storefront
   needs an auth shape the browser can actually produce.

### Checkout: better than the brief assumes, and its one hole is closed

- **Guest checkout works and is the DEFAULT path**, not a mode. `Cart.Guest()`
  is a first-class state, `CompleteCartInput` has no customer field at all, and
  an ADR 0008 conformance test asserts a guest order is never asked for the
  spending rule. **Email is not required anywhere.**
- **A single "complete checkout" call already exists.**
  `POST /store/v1/carts/{id}/complete` runs the whole five-step saga server-side
  — reserve, create order, authorize, capture, clear cart. The storefront never
  touches a payment collection or a session.
- **Minimum cart to paid order is four HTTP calls**, proven end to end over the
  production guard stack. The third is a `GET` and it is forced by a deliberate
  rule: `expected_total` is mandatory on complete, and nothing in the
  add-line-item response carries the total.
- A full-feature checkout (email, addresses, chosen shipping and provider) is
  about ten calls, and **email cannot be passed on the complete call** — it needs
  a separate write to the cart.

~~**The real hole: the cart's addresses never reach the order.** `cart_addresses`
exists; the order module has no address table, column or model field. This is
also why the invoicing flow has to take the buyer's address from its caller —
the order does not have one.~~ **Corrected 2026-09-06: the hole was closed on
2026-09-05, hours after this section was measured, and B11 above already records
it.** The order module's migration `000005_order_addresses` creates
`order_addresses` — one shipping and one billing per order, enforced by the
unique index `order_addresses_one_per_type` — `models.Order` carries
`ShippingAddress` and `BillingAddress`, and the cart's copies travel cart →
interop → `checkout.SnapshotAddress` → order snapshot, written by
`CreateOrderAddress` inside the order's OWN transaction. The invoicing flow does
still take the buyer from its caller, but for the reason `invoicing.IssueInput`
gives in its own godoc — the VKN or TCKN and the tax office are not in this
repository's customer model at all — not because the order has no address.

### Saved cards and passkeys: neither exists, and saved cards share a
prerequisite with subscriptions

- **`PaymentProvider` has five methods — CreateSession, Authorize, Capture,
  Refund, Cancel — all addressed by a per-transaction session id.** There is no
  customer parameter, no tokenize/attach/detach, no mandate, no payment-method
  type; the payment schema has no customer column and no instrument table. A
  second payment cannot reuse a first one's instrument. Both shipped payment
  plugins confirm it: PayTR keys on a per-session iframe token, Stripe is a
  skeleton.

  **This is the same prerequisite subscriptions need** — a stored instrument
  rather than a session — so the two features are one contract change apart, not
  two.

- **Passkey/WebAuthn: zero occurrences**, no library. The auth module supports
  exactly three methods and all are admin-facing; the customer-facing surface
  authenticates the STORE, not a person (ADR 0008).

- **An address book exists and is a leaf.** `customer_address` is a full table
  with DB-enforced single default shipping and billing. But **its storefront
  endpoints are unauthenticated and identify the customer from the path** — the
  module's own package doc says anyone reaching them can read and change a name,
  email and address — and **nothing outside the customer module reads it.** No
  path pre-fills a cart address from a saved one, and the order has no address
  at all.

  That is worth reading next to the KVKK note: it is personal data on an
  unauthenticated endpoint, and it is a known consequence of ADR 0008 rather
  than an oversight — but it is the sharpest form the consequence takes.

### Visual and semantic search

Already measured in the AI-features section and unchanged: `pgvector` is not
available on the cluster, and ADR 0015 fixes the contract at zero required
extensions, so this item reopens that ADR rather than sitting on top of it.

---
