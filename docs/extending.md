# Extending gobit: plugins, the file provider, the domain events and the customer identity

This document covers the four places where an installation adds behaviour
without changing the core or a module: **plugins**, the **file upload** surface
and its provider, the **domain events** a subscriber can listen to, and the
**customer identity** eight storefront routes now require. It is
for the person who is adding something to gobit rather than operating it — a
payment provider, a search backend, an error reporter, an integration that
reacts to an order.

Three of the four are optional. The fourth is not: an installation that binds no
customer identity loses its address book, so if you are upgrading, read that
section first.

For what the framework does and how it is configured, see the
[README](../README.md); for why the architecture is shaped this way, see
[`docs/mimari.md`](mimari.md).

Two things the README establishes are assumed here. The shell examples reuse
`$TOKEN` (an admin session token) and `$PK` (a publishable key) exactly as the
README's authorization walkthrough produces them, and a variable given on the
command line **overrides** `.env`, which is what makes `PLUGINS=… make run`
work whether or not you have an `.env` file.

This file is in English because ADR 0012 makes language a property of the file
and every new file is English.

---

## Plugins

A plugin is an ordinary Go package; it lives under `plugins/` and imports no
commerce module. It takes its contract from `core/provider` and its
registration point from `core/plugin.Host`. Adding a plugin **changes** neither
the core nor a module: a line is added to the catalog map in `internal/app` and
an installation selects it with `PLUGINS`.

```bash
PLUGINS=payment-stripe STRIPE_API_KEY=sk_test_… make run
```

Installation has two phases: `Install` runs **before** the modules (so that a
module a plugin brings can go through the lifecycle too), and `Start` runs
**after** them (a provider registration only exists once the payment module is
up). An unknown plugin name or a missing setting fails at startup.

The plugins show four different ways of extending:

| Plugin | What it does | Which extension points |
|---|---|---|
| `payment-stripe` | **skeleton** — registration and lifecycle work in full, no Stripe API calls have been made, and every money-moving method returns an explicit "not implemented" error | a MODULE's provider registration |
| `search-pg` | **real feature** — listens to the product events, keeps a PostgreSQL full-text index fresh, opens the `GET /store/v1/sales-channels/{sales_channel_id}/search` and `POST /admin/v1/search/reindex` endpoints | a module and a migration of its own, an event subscription, routes of its own |
| `error-sentry` | **real feature** — reports server faults to Sentry (or to a Sentry-compatible collector) | a slot the CORE owns; it needs no module at all |
| `error-otlp` | **real feature** — reports the same faults to an OpenTelemetry collector as a LOG RECORD | the same slot, a SECOND implementation |
| `ai-anthropic` | **real feature** — answers a closed question about a piece of text with Anthropic's Messages API | a core-owned slot whose CONSUMER is optional too: installing the plugin is what turns the feature on |

### Error reporting (`error-sentry`, `error-otlp`)

```bash
PLUGINS=error-sentry SENTRY_DSN=https://abc123@sentry.example.com/42 make run

# or the second implementation of the same slot:
PLUGINS=error-otlp OTLP_LOGS_ENDPOINT=http://localhost:4318/v1/logs make run
```

The two fill the **SAME slot** (the core holds one reporter), so one of them is
chosen. The reason the second implementation exists is an experiment:
[ADR 0014](adr/0014-error-reporting.md) said "only a second implementation can
show whether the contract has the right shape", and `error-otlp` tested that by
picking the model furthest from Sentry's — the OpenTelemetry log model has no
"issue", no grouping key, everything is an attribute.

The outcome is written down in the ADR; two of its sentences land here as well:
NO field was added to `ErrorEvent`, and the fingerprint, which is a FIELD in
Sentry, became a CONVENTION here — the error code goes into the
`exception.type` attribute and a collector that has never heard of gobit still
groups the faults correctly. **The code is enough for a collector that groups
by type; no stack trace is needed.**

What feeds the report is the **log**: `corehttp.WriteError` already writes an
ERROR line for every server fault and `Recoverer` does the same for every panic,
so wrapping the log handler closes all three doors at once and adds no
obligation to the code that produces the fault. A call that can be forgotten is
a fault nobody hears about.

**The core decides what goes into a report**
([ADR 0014](adr/0014-error-reporting.md)), not the plugin:

- The reporter **never sees the error itself**. `provider.ErrorEvent` carries
  only strings. A reporter that was handed the real error could walk the chain
  and send everything it found there — a connection string, a bound query
  parameter; what it cannot get it cannot send.
- Attributes pass through an **allowlist**, and the **names** of the dropped
  keys are still carried. The default list holds **no business identifier at
  all** (`user_id`, `cart_id`, `order_id`…): a report is a copy that leaves the
  building.
- Only two things come out of free text and both have a written guarantee: the
  log message (an invariant in gobit's own source) and `errors.Error.Message`
  (whose godoc says it "must contain no sensitive data"). The wrapped chain
  underneath stays in the process.
- The grouping key is the **error code**, not the stack trace: a stack shifts
  when a function is renamed and the same fault then looks like a brand-new
  record.
- At most three reports per code per minute; the suppressed count is carried on
  the next report. An outage does not produce one fault, it produces all of them
  at once.
- The log is written **first**: a collector in another data centre must not be
  able to cost the operator a log line.

### Answering a closed question about text (`ai-anthropic`)

```bash
PLUGINS=ai-anthropic \
  ANTHROPIC_API_KEY=sk-ant-... \
  ANTHROPIC_MODEL=claude-haiku-4-5-20251001 make run
```

The contract is `provider.Classifier`: a caller supplies a question, a text and a
CLOSED set of labels, and gets back one of those labels, a reason and the model
that answered. A free-text contract was refused — every caller would invent its
own parsing and each parser would break separately the day a model phrased
itself differently (ADR 0072).

**Installing the plugin is what turns the feature on, and that is the fourth
extension shape.** The other core-owned slot, the error reporter, is asked for by
the core on every request; this one is asked for by a scheduled job that
registers itself ONLY when the slot is filled. So an installation that does not
name this plugin runs a tree in which no model is called and no job appears in
`gobit jobs` — and one that names it without the review module fails at startup
rather than running a model with nothing to ask about.

The one consumer in the box is `internal/jobs/reviewsuggest`: every quarter of an
hour it asks about the reviews waiting for a moderator and stores what the model
said as a PROPOSAL beside each one (ADR 0071). It approves nothing. The operator
still decides, and the panel's moderation queue can be narrowed by what the model
proposed (ADR 0073).

> **What leaves the installation:** the text of a customer's review. Not their
> name — the job does not send it — but a review is free text a member of the
> public wrote. Naming this plugin makes whoever runs the installation
> responsible for a SUB-PROCESSOR they did not have before, and gobit cannot make
> that decision for a data controller (ADR 0029).

gobit claims no accuracy for any model and ships no threshold: measuring one
needs a corpus of reviews an operator has already decided about. That corpus
accumulates on its own — a proposal is not cleared when the decision is made —
and `GET /admin/v1/reviews/suggestion-agreement` reads it back as two counts per
model, never as a percentage (ADR 0074).

### Search (`search-pg`)

```bash
PLUGINS=search-pg make run

# The catalog that existed BEFORE the plugin was installed is not in the index:
# a one-off reindex. (From then on the events keep it fresh by themselves.)
curl -s -X POST localhost:9000/admin/v1/search/reindex -H "Authorization: Bearer $TOKEN"
# {"data":{"indexed":1,"removed":0,"pages":1}}

# The sales channel is a PATH SEGMENT (ADR 0044): search is scoped to the ONE
# channel the URL names, and that channel has to be one the publishable key is
# bound to -- a key naming a channel it does not hold gets a 403, not an empty
# page. $SC is the channel id; the key's channels come back from
# GET /admin/v1/api-keys.
curl -s "localhost:9000/store/v1/sales-channels/$SC/search?q=t-shirt" -H "x-publishable-api-key: $PK"
```

The index is refreshed **by events** (`product.created` / `product.updated` /
`product.deleted`), so a product added while the plugin is running enters search
by itself. Reindexing is only needed for the period the plugin **did not see**:
products created in an installation that was started without the plugin
otherwise never appear in search at all, and the endpoint reports that not with
an error but with an **empty result**.

The search engine is deliberately **not an external service**: PostgreSQL
full-text search gives a real feature without bringing in a new dependency and a
new compose service; thanks to the plugin boundary, moving to
Meilisearch/OpenSearch later changes nothing anywhere else.

> **Search is not a bypass of the channel filter.** The catalog rule in
> [`docs/security.md`](security.md) — a product with no sales-channel assignment
> is visible in every channel, one with an assignment only in the channels it is
> assigned to — is authorization, not a display preference, and it lives in one
> place. The plugin indexes product *identifiers* only; the records are fetched
> by `product.interop` and the visibility rule stays in that one place. Repeating
> the rule inside the plugin would mean the storefront and search silently
> diverging the day one of them changed.

---

## File upload

`POST /admin/v1/uploads` (multipart) takes an image and returns an address that
can be fetched; that address plugs straight into the existing product-image
flow.

This is the only place where **arbitrary bytes** are accepted from the client,
and the rules are structural:

- **The storage key is generated.** The client's file name enters no path
  expression, which means path traversal (`../`) is *impossible* — solving it by
  "sanitizing" would have meant deciding again on every new encoding trick.
- **The content type is not asked of the client**, it is detected from the
  content. The client's `Content-Type` is a claim; an allowlist that trusts it
  filters nothing.
- **An allowlist, not a denylist.** The default is the common image types only,
  and SVG is **absent**: an SVG is a document, it carries script, and served
  from the same origin it becomes stored XSS. Writing a type that runs in the
  browser, such as `text/html`, into the configuration is **rejected** as well —
  `nosniff` does not stop it, because the response really is of that type.
- On serving, `Content-Type` is written from the **stored** type and `nosniff`
  is present on every response.

The serving endpoint has no identity (an `<img>` in a storefront cannot send a
header) but it is **not without a quota**: identity and quota are separate
decisions, and the surface table in [`docs/security.md`](security.md) — the one
this endpoint's own row sits in — is where that split is drawn for every surface.

> The default `local` provider writes to disk and the root directory must be
> **durable**. A relative root is right for local development; in a shared
> environment it lands on the container's non-durable layer and the images are
> gone on the next deployment — the address on the product record stays where it
> is, so with no error in sight every image returns 404. A warning is logged at
> startup.
>
> **Being absolute is not enough.** `FILE_ROOT=/tmp/gobit-uploads` is absolute,
> it passes the "do not give a relative path" advice, and it still disappears:
> `/tmp`, `/var/tmp`, `/dev/shm` and whatever `TMPDIR` points at are cleaned by
> the operating system, and on most distributions they are tmpfs, so they do not
> even wait for a restart. The warning counts these roots too; the criterion is
> not "is it independent of the working directory" but **"is it still there when
> the process restarts"**.

---

## Domain events

Modules publish their own domain events to the event bus; subscribers (plugins,
integrations) listen to them. Which backend carries them is chosen by
`EVENT_BUS=inmemory|redis`, and the difference between the two is what the
second rule below is about.

Today there are five subscribers: the search plugin `search-pg` (`product.*`),
the notification module (`order.placed`), the browser push plugin `web-push`
(`order.placed`), the **order module** itself (`payment.captured`,
`payment.refunded`) and the outbound webhook plugin `webhook-out` (all six of the
topics below).

The order module is the only subscriber that is not a plugin, and it is a
subscriber for the reason a plugin would not do: money moved by the payment
module's own routes has no flow on its path, so nothing else could tell the
order (ADR 0121).

| Event | Payload |
|---|---|
| `order.placed` | `order_id`, `display_id`, `status`, `region_id`, `customer_id`, `currency_code`, `total`, `item_count`, `placed_at` |
| `product.created` / `product.updated` | `product_id`, `status` |
| `product.deleted` | `product_id` |
| `payment.captured` / `payment.refunded` | `payment_collection_id`, `occurred_at` |

`placed_at` is a timestamp, but it too is a string: the moment the order was
placed, converted to UTC and formatted with `time.RFC3339Nano`
(`EventFieldPlacedAt`). `occurred_at` is the same shape.

The payment events carry **no amount**, and it is worth saying why the narrow
rule below is not the whole reason. A refund is deliberately not idempotent, so
a figure in the payload would be an INCREMENT; delivery is at least once, and a
redelivered increment reports a total that never happened. What a subscriber
wants is the cumulative figure, and the only place that is always current is the
collection.

Two rules are binding:

- **The payload is NARROW.** Putting every field a subscriber might need into
  the event turns the event into a second copy of the record, and the two
  representations diverge. The subscriber holds an identifier; it can read the
  record.
- **All values are strings**, the numeric ones too. The Redis backend writes
  `Data` as JSON; since JSON has a single number type, a field put in as an
  `int64` reaches the subscriber as a `float64` — the same subscriber would work
  in development and **fall over in production**, and money would travel over a
  float on top of that.

**Notification** listens to `order.placed` and sends the order confirmation
through the selected `NotificationProvider`. The recipient address is read
**from the order record, not from the event** (`order.interop`), because
personal data is not put into a durable stream. The default provider is `log`
and it really does not send — it says so plainly at WARN level; real delivery is
a plugin's job (`Host.RegisterNotificationProvider`).

> If a handler returns an error the event is considered **handled**; no backend
> redelivers it (Redis ACKs regardless of the handler's result). Returning an
> error is not a "retry" request, it provides **visibility**.

---

## The customer identity

This is the one entry in this document that is **not optional**. Since
[ADR 0043](adr/0043-gobit-requires-an-identity-it-still-does-not-issue.md) and
[ADR 0057](adr/0057-one-comparison-holds-the-storefront-customer-claim.md),
twelve storefront routes ask whether the caller is the customer the request
names:

| Route | |
|---|---|
| `GET` / `PUT /store/v1/customers/{id}` | the profile |
| `GET` / `POST /store/v1/customers/{id}/addresses` | the address book |
| `PUT` / `DELETE /store/v1/customers/{id}/addresses/{address_id}` | |
| `POST /store/v1/customers/{id}/addresses/{address_id}/default-shipping` | |
| `POST /store/v1/customers/{id}/addresses/{address_id}/default-billing` | |
| `GET /store/v1/b2b/customers/{customer_id}/company` | the employer |
| `GET /store/v1/b2b/customers/{customer_id}/employee` | the spending limit |
| `POST /store/v1/carts` | only when the body carries a `customer_id` |
| `POST /store/v1/carts/{id}` | only when the body carries a `customer_id` |

The last two are the difference between a surface and a claim. A cart body that
names nobody opens a **guest** cart and is never asked for a proof, in an
installation that has bound an identity or one that has not — the guest path is
what the storefront is for. Naming a customer is what puts the burden of proof
on the claim.

All twelve refuse when you have bound nothing, and four of them did not until
ADR 0125: b2b's two reads and the two cart bodies served the claim unchecked, so
an installation that bound no verifier kept them working with the claim in them
BELIEVED. That is a setting now rather than a default —
`STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM=true` brings the old answer back for
an installation that wants it. Either way, a body naming nobody opens a guest
cart and is never asked.

**gobit does not answer that question and will not.** It holds no proof about
the person behind a storefront request: no customer session, no cookie, no
signing key, no rotation policy for a storefront it does not serve
([ADR 0008](adr/0008-musteri-kimligi-guven-siniri.md), which stands). What it
publishes is the shape of your answer — `corehttp.Identity`, one method, every
type in the signature from the standard library so that a type declared in
**your** module can implement it:

```go
// CustomerID returns the customer identifier the request PROVES.
CustomerID(r *http.Request) (string, error)
```

Write it against `core/identitytest` (ADR 0126). One call from an ordinary test
holds the four rules that need no key, no store and no upstream:

```go
func TestCompliance(t *testing.T) { identitytest.Contract(t, mySessions) }
```

It refuses the implementation a first attempt reaches for — read the claim, hand
it back — and if your proof really does arrive in a header your gateway strips,
implement `identitytest.UpstreamTrust` and name it. It opens no signature: a
green run says the shape is not wrong, not that the scheme is sound.

You bind it from an ordinary module, in the container, under the core's own
name. There is no ordering requirement: the customer module resolves the name on
the FIRST storefront request, so your module may be added last.

```go
func (m *SessionModule) Register(ctx context.Context, c *container.Container) error {
	return c.Provide(corehttp.IdentityName, mySessions{})
}
```

Use the constant rather than the literal `"core.identity"`. It is published for
the same reason `core/plugin`'s `CallbacksName` is: the two sides of the name are
two string literals that no compiler compares, and a constant turns a rename into
a build failure instead of a storefront that refuses everything for no stated
reason.

What the routes answer:

| Situation | Answer |
|---|---|
| nothing registered under `corehttp.IdentityName` | `401 identity_not_bound` on the **eight customer routes**, until you bind one. The b2b and cart rows answer as they did before ADR 0057 — the claim is believed — and each module logs a WARN saying so. A guest cart still opens either way |
| your implementation returns an error | **your** error, unwrapped: an expired session `401`, a suspended account `403`, an unreachable provider `503`, an untyped error `500`. You choose the status by choosing the error's kind |
| your implementation returns `"", nil` | `500 identity_unproven` — that pair cannot be told apart from a proof of the empty customer, so it is refused rather than believed |
| what it proves ≠ what the request names | `403 identity_mismatch`. Not a `404`: the caller supplied the identifier, so there is no existence to hide |
| they agree | the request is served, for the PROVEN identifier |

One comparison produces all of them — `corehttp.ProvenCustomer` — and the three
modules call it rather than each holding a copy. A copied authorization rule
keeps answering after one copy drifts.

On the customer and b2b routes the check runs **before the body is decoded**, so
a refused request whose JSON is also malformed still answers the refusal rather
than `422`. On the cart it runs immediately after, because the claim it is about
is a field IN that body.

**Your implementation may not read the body.** The request is handed over whole
so you can read a cookie, a header or the context; consuming the body would take
it away from the handler that is about to decode it — and on the cart it would
take away the very field being checked.

**gobit still verifies nothing.** An implementation that hands the claimed
identifier straight back satisfies the interface, proves nothing, and the
framework cannot tell. What it refuses everywhere is to proceed when your
verifier CONTRADICTS the request. What is still open is written in
[`docs/known-limits.md`](known-limits.md): a shopper can always decline to name
a customer at all, and until you bind a verifier the b2b storefront and the
cart believe whoever they are told.
