# Known limits

What this framework does NOT do, and the argument for each absence.

It is for the person deciding whether gobit fits: an operator sizing a
deployment, an engineer embedding it, a reviewer asking what was traded away.
Nothing here is a to-do list. Every entry was investigated, decided on, and its
justification lives in the godoc of the code it constrains — an opening nobody
wrote down is an opening nobody closed.

The entry point for the framework itself is the [README](../README.md); the
decisions are under `docs/adr/`.

This file is in English because ADR 0012 makes language a property of the file
and every new file is English.

---

This document describes **today**: what follows are limits that still hold on
`main`. A version name is deliberately NOT written — it would be a dated claim
needing an update at every release cut, and one that goes quietly stale when it
does not get one. What has closed DROPS OUT of here; which release closed it
stands in [`CHANGELOG.md`](../CHANGELOG.md), because that is a record of the
past and is not corrected retroactively.

## Identity and authorization

- **gobit issues no customer identity, and since
  [ADR 0043](adr/0043-gobit-requires-an-identity-it-still-does-not-issue.md) and
  [ADR 0057](adr/0057-one-comparison-holds-the-storefront-customer-claim.md)
  yours decides every storefront request that names a customer.** This is the
  one limit in this file that an embedder has to act on rather than merely
  accept. Twelve routes ask: the profile and the six of the address book, b2b's
  company and employee reads, and the two cart bodies — cart creation and the
  guest-to-registered handover, the last two only when the body carries a
  `customer_id`. The answer comes from `corehttp.Identity`, a one-method
  interface the framework publishes and does not implement; the embedding
  application registers its own from an ordinary module, in the container, under
  the name `corehttp.IdentityName` (`"core.identity"`). Bound, it decides all
  twelve: a request naming somebody else gets `403 identity_mismatch`.
- **With NO identity bound all twelve refuse, and until ADR 0125 four of them
  did not.** Every one of them answers `401 identity_not_bound` — closed rather
  than open, which is
  [ADR 0007](adr/0007-sertlestirme-arizada-davranis.md)'s row for an unconfigured
  authenticator, and an installation upgrading past `v0.8.0` without binding one
  loses its address book, loudly. The four ADR 0057 added — b2b's two reads and
  the two cart bodies — served the claim unchecked until
  [ADR 0125](adr/0125-serving-an-unverified-customer-claim-is-a-choice.md), so a
  caller who knew an identifier, which travels in every order response, read that
  person's company and allowance and opened a cart in their name. That is now a
  CHOICE rather than a default: `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM=true`
  restores it for an installation that wants it, and the two modules still log a
  WARN naming the empty slot. Guest carts are untouched by either answer. The way
  to close the four properly is unchanged and it is one line of wiring: **bind a
  verifier**. What gobit still does not do is VERIFY the proof itself, and since
  [ADR 0126](adr/0126-a-customer-identity-passes-a-published-suite.md) it does
  hold the SHAPE: `core/identitytest.Contract` refuses an implementation that
  hands the claimed identifier back, invents one, returns one beside an error or
  eats the request body. It opens no signature and never could, so a green run
  means the shape is not wrong rather than that the session scheme is sound —
  and nothing makes an embedder run it. `POST /store/v1/customers` is outside the set because it mints the record;
  so is the order module's storefront read, which names a cart rather than a
  person.
- **Since [ADR 0127](adr/0127-a-working-identity-ships-outside-the-module.md)
  there IS one to bind, and what it does not close is written here rather than
  discovered.** `contrib/identity-session` is a working customer identity in a Go
  module of its own: a signed cookie, argon2id passwords, its own table, storefront
  sign-in and sign-out, an operator endpoint, and since
  [ADR 0133](adr/0133-a-shopper-opens-their-own-account.md) self-registration behind
  a proven address. `contrib/identity-passkey` adds both WebAuthn ceremonies and,
  since [ADR 0130](adr/0130-a-person-can-see-their-passkeys-and-remove-one.md),
  listing a person's keys and removing one. Neither is in gobit's own dependency
  graph — a separate `go.mod`, decided on a measurement: go-webauthn brings nine
  modules gobit does not otherwise have, and an installation that wants a password
  should not pay for them. Binding one is the one line of wiring the bullet above
  asks for, and it is not a verifier gobit vouches for; it is one that passes
  `core/identitytest.Contract`, which holds the shape and opens no signature. Its
  limits:
    - **A signed cookie cannot be revoked before it expires.** That is the price of
      keeping the identity off the read path of twelve storefront routes, and it is
      the same wholesale-only shape the admin side has one bullet down. Rotating the
      signing key does NOT log anybody out (`RetiredSecrets`, ADR 0129) — which is
      the point, and therefore not a revocation either. A key that LEAKED is dropped
      outright, which logs everybody out and is the correct price.
    - **A stolen cookie IS the account.** With the passkey module bound it can
      register its own key and remove the owner's. The rule those endpoints enforce
      is "an account keeps a way in", not "only the owner changes credentials", and
      a gate that looked like the second while enforcing the first would be worse
      than none — so it is named instead of guarded. What a shop can do about it is
      outside these modules: a shorter TTL, a re-authentication step of its own.
    - **A credential store an installation binds itself may answer nothing at all.**
      `Credentials` exists so a shop can keep keys in LDAP or a users table it
      already has, and such a store cannot erase rows out of gobit's tables or hold
      a pending registration. Then the data-subject sweep reports `Retained` with
      the reason and self-registration is not mounted
      ([ADR 0132](adr/0132-the-contrib-identity-modules-answer-a-data-subject.md),
      ADR 0133) — which is true, and is what a controller needs to hear instead of a
      deletion that did not happen.
- **A shopper can always decline to name a customer, and that is not closable.**
  A cart without a `customer_id` belongs to a guest, and on a guest order the
  b2b spending rule is not even asked. Requiring the field would not help:
  `POST /store/v1/customers` mints a fresh guest record, bound to no company and
  therefore ruleless, with nothing but the publishable key. The correct sentence
  for the limit is not "the spending limit is not enforced" but "the limit is
  applied only to a purchase that **declares** its customer" — and since
  ADR 0057 a declaration is one the request can prove. The measurement is with
  the B2B spending rule in [`docs/commerce-flows.md`](commerce-flows.md); the
  boundary is [ADR 0008](adr/0008-musteri-kimligi-guven-siniri.md)'s, and the
  side that verifies is still the embedding application.
- **Storefront carts carry no ownership check** — the model is a capability URL:
  the cart identifier is minted from a 48-bit timestamp plus 80 bits of
  cryptographic randomness, it cannot be guessed, and knowing it carries the
  right of access. The storefront therefore has no list endpoint, because a list
  endpoint would turn knowing one identifier into reading every cart. The rules
  of the model, and what it does NOT cover, are written with the cart flows in
  [`docs/commerce-flows.md`](commerce-flows.md).
- **Session revocation is wholesale only.** `POST /admin/v1/auth/logout` and a
  password change drop ALL of the caller's sessions; there is no endpoint that
  drops a single device (see `internal/modules/auth/api`).
- **The panel's privileges are per SCREEN, not per record or per field.** Since
  [ADR 0156](adr/0156-a-panel-screen-costs-a-privilege.md) every panel path is
  listed with the scope it requires, and the scope decides whether the screen
  opens at all. What it cannot do is narrow what an opened screen SHOWS: an
  operator holding `order:read` reads every order, not the ones for their
  region or channel. The read layer the screens go through knows nothing about
  principals, so there is nowhere below the route for a narrower answer to come
  from.
- **The panel's scope table is keyed by path, not by method and path.** The two
  paths bound on both verbs want one privilege each — an edit form an operator
  cannot submit is a screen that wastes their time — but a POST added later to
  a read path would inherit the read privilege, and the router walk that audits
  the table cannot tell the two apart because both answer the unprivileged
  operator correctly.
- **Nothing proves a panel screen asks for the RIGHT privilege.** The panel
  spells its scopes itself (it imports no module, and core knows none), and
  `internal/arch` reads both sides from source to refuse a value no module
  declares. That catches a misspelling. A screen listed under a plausible but
  wrong privilege — one module's scope over another module's data — compiles,
  passes and is caught by nothing; the judgment is written in the table's godoc
  and nowhere else.

## Sales channel scope

- **A product with no channel assignment is visible in every channel.** The rule
  is deliberate and backward compatible (on the day it was turned on, the strict
  alternative would have emptied every existing catalog) but it has a trap:
  deleting the last channel binding does not hide the product, it opens it to
  every storefront. `status` is what hides it. The single source of the rule is
  the SQL template in
  `internal/modules/product/repository/saleschannel.go`.
- **The scope is enforced ON ENTRY; the quantity of a line already in the cart
  can be raised afterwards.** The path that updates a line quantity
  (`internal/workflows/cart/update_line_item.go`) and the completion flow do not
  ask the scope again. The consequence: even after a product has been moved to
  another channel, a client that already has a line for it in its cart can buy
  MORE of that product. This is the price of the decision whose justification is
  written with the sales channel rule in [`docs/security.md`](security.md) — the
  alternative was a catalog edit making a customer's full cart unpayable.

## The category tree

- **A category listing walks ONE level, and so does a category PROMOTION.**
  `parent_id` filters by DIRECT parentage and the catalog's category filter
  matches DIRECT membership, so asking for a category does not bring its
  subcategories' products with it. The `category_ids` the Query record publishes
  since [ADR 0148](adr/0148-a-rule-can-ask-what-a-product-belongs-to.md) is the
  same answer, deliberately: a rule reading "any of these categories" matches the
  products filed under the named category and NOT the ones filed only under its
  children, so a merchant running "half off Shirts" over a tree has to name the
  subcategories too. Nothing in this module resolves descendants; the day the SQL
  grows to, the filter and the record are the two places it goes
  (`service/provider.go`).

- **A move whose ancestry is deeper than sixty-four levels is refused, even when
  it would have been legitimate.** The update that reparents a category walks up
  from the new parent to make sure the category is not being placed inside its
  own subtree, and that walk has to be bounded or an ancestry that already holds
  a ring would not terminate. Reaching the bound is treated as a refusal rather
  than as permission, because an ancestor past it would go unseen and the ring it
  closes would be written. Sixty-four is far past any catalog a person maintains,
  and the trade is stated in ADR 0085.

- **A description cannot be emptied through the category PATCH.** A field that is
  not supplied is preserved, which leaves no way to say "make this NULL" — the
  same limit the product update carries and for the same reason.

## Tax

- **In a tax-inclusive market the discount stays a GROSS figure beside a NET
  subtotal.** The tax is taken out of the amount actually being charged, so the
  line's subtotal becomes the extracted base plus the discount that was applied
  to the gross. `Subtotal - Discount` is therefore the net taxable base and not
  "net minus net". The alternative — extracting a net discount too — needs a
  second rounding that has to cancel the first exactly, and ADR 0086 refuses to
  depend on that.

## Installation and operation

- **The optional identity modules have two settings an operator can only get
  wrong once.** Both are named where they are configured, and both are here because
  the cost lands after a deploy rather than at startup.
    - **Changing `Options.RPID` abandons every passkey already registered.** A
      credential is bound to that value by the authenticator that minted it, so a
      domain move — or dropping a subdomain — leaves every existing key unusable and
      no migration can carry them over. The module records the relying party a row
      was written under and answers only for the configured one, so an abandoned row
      is not counted as a way into an account
      ([ADR 0131](adr/0131-a-passkey-belongs-to-one-relying-party.md)) — it was, and
      that made the last-way-in rule remove the only WORKING key. Everybody holding
      one still has to register again.
    - **Self-registration's default rate limit is per PROCESS.** It is kept in the
      instance's own memory, so an installation behind several instances gets that
      many times the bound until it binds a shared limiter
      ([ADR 0133](adr/0133-a-shopper-opens-their-own-account.md)). The default exists
      because one request makes the shop send mail to an address a stranger chose;
      the published description says what it is, because a limit that is a fraction
      of itself still looks like a limit.
- **The admin panel writes the EDITABLE part of the catalog, not the creatable
  part.** The panel under `/admin/ui`
  ([ADR 0011](adr/0011-yonetim-paneli-dorduncu-agac.md)) carries login, logout,
  the product list, the product page, the variant page, the order list, the
  order page, the sales report, the customer list, the customer page, the
  inventory list and the moderation queue. Of those, THREE forms WRITE
  ([ADR 0013](adr/0013-panel-write-surface.md)): a product's
  title/handle/status, a variant's BASE price per currency, and PHYSICAL stock
  per location. Every other screen is read-only; and there is no single-item
  page for inventory or for a sold line at all — the detail of a stock item IS
  its per-location levels on the variant page, and the context of a sold line IS
  the order the line is attached to.

  **The moderation queue is the exception to the sentence above, and to the
  paragraph's whole shape** ([ADR 0076](adr/0076-the-panels-migration-begins-with-the-review-screen.md)).
  It writes — approving and rejecting a review — and it writes through NO panel
  form and no module admin surface: it is a client of `/admin/v1`, which is what
  [ADR 0030](adr/0030-the-panel-becomes-an-admin-api-client.md) decided every
  screen becomes. It is the first, the other eleven are still rendered on the
  server, and the order they move in is not decided. Until they do, the panel
  carries two shapes and this entry describes both.

  Creating something that does not exist and deleting something that does still
  happens over `/admin/v1`, with `Authorization: Bearer`: product, variant,
  price set, stock item, stock location, links. Campaign prices and prices
  carrying a RULE are not shown in the panel and cannot be edited there either —
  the form knows only the base price. This is not a presentation preference: the
  price write is lossless and writes the prices it does not see back
  UNCHANGED, but it does not let them be edited.

  There are two more write limits. Concurrent editing is last-writer-wins: there
  is no version field in the form and no optimistic lock under it. And editing
  one price regenerates ALL the price identifiers in that set, because the
  writer underneath does not update the set, it rewrites it; those identifiers
  are named only by pricing's own `price_rule` rows, so the effect stays inside
  the module.

  Every new write means a primitively typed admin surface method in the owning
  module and a form in the panel; both show up in a diff. The panel does not
  import a module, so the write path has to go through the surface the module
  publishes.

  The panel's session cookie is NOT ACCEPTED by the admin API, and that is a
  decision rather than a shortcoming: the API's CSRF immunity comes from the
  token living in a header the browser does not add by itself.

  The catalog screen shows a price as a raw minor-unit integer, and says so,
  when the currency's number of decimal places is NOT KNOWN. The scale is read
  from the region record; in an installation with no region defined at all one
  sees `19990 TRY (minor units)`. Assuming a fixed 100 would show the WRONG
  amount for currencies with 0 and 3 digits, such as JPY and KWD.
- **Search AND the e-mail guards depend on the database cluster's CTYPE setting,
  and that setting is fixed at initdb time.** Three things leave case folding to
  PostgreSQL: the storefront's own `?q=` filter (`title ILIKE`), the `search-pg`
  plugin's index (`to_tsvector`), and the `CHECK (email = lower(email))`
  constraint that `auth`, `customer` and `b2b` each put on their e-mail column. A
  cluster created with `--locale=C` folds ASCII only, so a search for a lowercase
  word carrying a non-ASCII letter does NOT FIND the product whose title carries
  the uppercase form of that letter — with no error, silently.

  The third one fails differently and it is worth stating on its own: the
  constraint keeps accepting rows, it just stops refusing the wrong ones. On such
  a cluster it still rejects an unfolded ASCII address and accepts an unfolded
  non-ASCII one, so the last defense behind gobit's own folding is absent for
  exactly the addresses that need it. **This is defense in depth, not the
  mechanism** — every write path in those three modules folds in Go first
  (ADR 0038), so an installation is not storing unfolded addresses because of it.
  What is lost is the backstop against a direct SQL write. The startup probe
  reports this path as `case_lower`. (The letter pair that shows it is pinned by the probe in
  `core/db/casefold.go` and quoted in
  [ADR 0015](adr/0015-postgresql-cluster-contract.md). It is not repeated here
  because ADR 0012 forbids a Turkish letter in a translated file, and an
  ASCII substitute would demonstrate nothing — an ASCII pair folds on every
  cluster.)

  `deploy/docker-compose.yml` now uses `--locale=C.UTF-8`, and the application
  probes the state at startup and warns when it is broken. But **an existing
  data directory keeps its old locale**: fixing an installation created with
  `--locale=C` takes a dump/restore. If you bring your own Postgres, the
  cluster's CTYPE has to be a UTF-8 aware locale; an ICU provider is NOT
  ENOUGH — it fixes `ILIKE` and leaves the search index broken.
- **Search scores EVERY matching document; the cost grows linearly with the
  catalog.** A GIN index cannot satisfy the `ORDER BY`, so returning a single
  page reads and scores ALL of the matching rows. Measured (52,000-document
  index, a word occurring throughout the catalog, LIMIT 20): the ranking used to
  take **663 ms** and today takes **24 ms**, and **24 ms** of that is the match
  scan itself — that is, there is nothing left to win from the ranking, the
  remaining cost is the scan, and on a 500,000-product catalog the same word
  rises to half a second. Going below that means the index satisfying the
  ordering as well (RUM); adding a mandatory EXTENSION is
  [ADR 0015](adr/0015-postgresql-cluster-contract.md)'s dated decision and
  cannot be taken as a one-line speedup.

  A second limit follows it: **an exclusion carries no relevance.** Scoring is
  done with the positive part of the query, so `gomlek -mavi` is still ordered
  by relevance; but a query consisting ONLY of an exclusion (`-mavi`) leaves no
  positive signal to rank by, and the results come back in indexing order.
- **The storefront listing's TOTAL COUNT gets more expensive as the catalog
  grows.** The `count` field of the
  `GET /store/v1/sales-channels/{sales_channel_id}/products` response has to
  count the whole of the set the sales channel filter is applied to; the page
  size does not change that. Measured (52,000 products, 52,000 channel
  assignments, local Postgres): the plain unfiltered count **2 ms**, the
  channel-filtered count **64 ms**, all the remaining SQL of the same request
  **1 ms**. So on a large catalog almost the whole of a storefront request is
  the counter.

  This is not a defect but the price of the pagination contract: if a total is
  asked for **and can be counted**, a total is counted. There is now one place
  where it cannot be: beside `in_stock` or a price bound (ADR 0040, ADR 0041)
  the matches are chosen after the rows are read, so an explicitly requested
  total is REFUSED with 422 `product_count_unavailable` rather than counted or
  quietly dropped. Every request that could be counted before still is. The list
  query itself does not carry this cost (measured: 0.14 ms), so the only thing
  that gets slower is `count`.

  The counter CANNOT BE MADE CHEAPER, but it can now be NOT ASKED FOR:
  `?with_count=false` (in GraphQL, not selecting the `count` field) does not run
  the counting query at all and the envelope carries no `count` field. The
  default did not change. Why it cannot be made cheaper was measured: the
  channel filter runs one subquery per product and that subquery is ALREADY
  index-only (`EXPLAIN`: `Heap Fetches: 0`), so the set to be walked cannot be
  shrunk, only left unwalked. The panel's product list never pays this price —
  it deliberately pages without counting
  (`TestProductListPagesWithoutCounting`).
- **Building a cart writes rows in the SQUARE of the line count** — but it no
  longer RUNS statements in the square. Every request that adds a line rewrites
  the amount of all the cart's lines, so building a 100-line cart still writes
  5,050 line amounts; what changed is that this is done with 100 UPDATEs instead
  of 5,050. The time spent under the cart's lock thereby became almost
  independent of the line count (measured, 100 lines: the write phase 8.0 ms →
  0.55 ms).

  The remaining limit is in two places: the number of ROWS written is still
  quadratic, and the real time under the lock is now the commit's WAL flush
  (measured on a durable cluster, 6.2 ms independently of the line count) —
  there is no way to shorten that at this layer.

  Today's protection is a CEILING: a cart carries at most 100 distinct lines and
  anything beyond that is refused with `cart_workflow_line_limit_reached`. The
  ceiling looks at the snapshot taken outside the cart's lock, so two concurrent
  additions can exceed it by a few lines; it is not a hard upper bound but a
  gate that cuts off unbounded growth.
- **An interrupted payment leaves reserved stock waiting for MANUAL
  intervention.** The cart flow runs synchronously inside the HTTP request; if
  the process dies in the middle (a deploy, an OOM, a pod eviction) the
  compensation NEVER runs and the stock reserved up to that moment hangs in
  `inventory_reservations`. The `SHUTDOWN_TIMEOUT` default is **15 seconds** and
  the saga's budget is **2 minutes** — so an ordinary deploy can produce this.
  The application WARNS about that gap at startup.

  It is no longer silent: when an execution's LEASE expires the next attempt
  closes it and, if work had been done, writes `compensation_failed` and logs at
  ERROR saying manual intervention is needed (so it reaches the collector when
  error reporting is on). Which reservation is left hanging stands in the
  `output` field of the step records.

  It can now also be LISTED: `gobit stuck` prints the half-done executions and,
  for each, which of its steps is still holding what. It covers both classes at
  once, because the status query alone is not enough: if the process died in the
  middle of the saga and the customer never came back, the record does not even
  GET a `compensation_failed` — it stays `running` forever, and the command
  finds that class as "lease expired and still holding a step".

  **The compensation can now be run FROM THE RECORDS**
  ([ADR 0017](adr/0017-recovering-abandoned-sagas-from-the-record.md)): a caller
  returning with the same key finds the abandoned execution, the shared state is
  rebuilt from the steps' own durable outputs and the chain runs; the record
  becomes `failed` and releases its key, so the stock is released and the
  customer can pay for their cart again.

  **At one point it deliberately STOPS, and that point is the payment.** The
  engine writes a step's record after Invoke returns, so a process dying inside
  the collection leaves no trace at all; if recovery counted it as "never ran",
  a customer whose card had been charged would have their stock released, their
  key freed, and would be charged a SECOND TIME. That is why a collection step
  with no record stops the recovery, and the decision is left to manual
  intervention.

  **Recovery can now also be TRIGGERED:**
  `gobit recover <execution-id> -confirm <execution-id>` runs an execution's
  compensation chain. The engine's own recovery happens by coincidence — a
  caller returning with the same key triggers it — and that covers the customer
  who retries, nobody else. An abandoned cart has no returning caller;
  `gobit stuck` lists it and there would be NOBODY TO RELEASE it.

  The command carries the same gate as the other irreversible command
  (`migrate down`): without `-confirm` repeating the identifier, nothing runs at
  all. The engine's own refusals are a second gate and the command CANNOT
  OVERRIDE them — a live lease, a record in a terminal state, and a collection
  step with no record stop the run whatever is typed.

  A scheduled sweeper is still deliberately ABSENT: recovery runs work that has
  side effects, and handing that to an unwatched background job is the "decide
  silently" class this repository refuses. Here a HUMAN makes the decision and
  names the execution.

  **Recovery is EXCLUSIVE.** An abandoned record is in nobody's ownership, so
  every caller arriving with the same key finds it; without a claim they would
  all run the compensation chain (measured with four concurrent callers: the
  chain ran FOUR times). The engine now CLAIMS the record BEFORE recovering: a
  single conditional UPDATE, holding only while the record is still `running`
  AND `updated_at` is the value the decision was based on. Once the winner
  stamps it the others are eliminated, and the lease is refreshed throughout the
  recovery. Measurement: the same four callers, ONE compensation.

  The capability is OPTIONAL (`workflow.ClaimingStore`); no method was added to
  `Store` so that the contract of anyone who wrote the store elsewhere does not
  break. The price of that is that a wrapper EMBEDDING `Store` silently hides
  the capability — an embedded interface carries only its own methods. The
  decision is in
  [ADR 0017](adr/0017-recovering-abandoned-sagas-from-the-record.md).
- **Error reporting is a SIGN, not a copy of the event.** The `error-sentry` and
  `error-otlp` plugins ([ADR 0014](adr/0014-error-reporting.md)) send the
  collector the failure code, the safe message and the `request_id`; everything
  else stays in the log. This is deliberate — the reporter never sees the error
  itself, so it cannot leak it — but the consequence is this: whoever reads a
  report must have access to the log as well.

  Three concrete limits: the report an API failure produces carries no METHOD
  and no PATH (`corehttp.WriteError` logs the error, the code, the status and
  the `request_id`; the access log line that does carry both is skipped on
  purpose, because it would report the same failure a second time) — a failure
  that reaches `slog.ErrorContext` on its own is not bound by that, and several
  do not: the panic recoverer's line and the audit middleware's two failure
  lines log method and path, while the panel's two failure writers
  (`internal/adminui/failure.go`) and the callback guard's error lines log the
  path, and the default allow list lets both keys through as the request's
  SHAPE; the "safe message" rests on a godoc promise and no audit MECHANICALLY
  verifies that a caller did not write an email address into it; and the
  default allow list holds no business identifier at all, so fields such as
  `user_id` enter a report only if the installation adds them to the list.
- **There is no multi-tenancy.** One tenant = one installation = one database =
  one process; several INSTANCES are not several TENANTS, because instances
  share the same database and the same catalog. The detail is with the
  single-instance discussion in [`docs/security.md`](security.md), the decision
  in [ADR 0009](adr/0009-cok-kiracililik-kurulum-siniri.md).
- **Migration rollback is for ONE owner and does not KNOW the order.** The
  surface now exists (`gobit migrate status`,
  `gobit migrate down <owner> -confirm <owner>`) and the forward direction stays
  automatic at startup. But the command rolls back one owner, not several: the
  operator calls the modules that have to be rolled back together one after
  another, and the command does not say which order is the right one. Because
  there are no cross-module foreign keys, this is not a constraint today.

  The second limit is a WAIT, and it cannot be interrupted: golang-migrate takes
  the advisory lock with `context.Background()`, so while somebody else holds
  the lock neither a deadline nor Ctrl-C ends the waiting (measured: a version
  read whose context expired in 5 s had still not returned 15 s later).
  `migrate status` takes the lock while reading versions too, so a command run
  in the middle of an ongoing deploy can wait silently.
- **The location policy expresses region SCOPE and PREFERENCE order, and nothing
  else.** Stock distribution ("put the location with the most stock first"),
  cost, and an order-level decision ("ship all lines from a single location")
  CANNOT BE EXPRESSED; why each of them cannot is written in
  [ADR 0010](adr/0010-depo-secim-politikasi.md). Priority is per **location**,
  so "A first for R1, B first for R2" cannot be written either — the only thing
  writable per region is exclusion.
- **A wrong region binding CLOSES the store until an operator repairs it.**
  Binding a region identifier that does not exist (or deleting a region and
  reopening it under the same name — the new record gets a new identifier)
  eliminates that location for every cart; in a single-location installation the
  result is that every completion is refused although the catalog is full. The
  cart is NOT consumed: the refusal is raised in the flow's first step and
  BEFORE any stock is reserved, so there is nothing to compensate — no earlier
  step exists and the failing step took no reservation to release — and the
  execution is written `failed`, a transition that RELEASES the idempotency key
  (see `workflow.StatusFailed`), so the same cart can be paid for the moment the
  binding is corrected. The failure is visible, but the visibility has a limit:
  only the CODE reaches the storefront body
  (`fulfillment_no_serviceable_location`); the dump that names what the
  candidates are actually bound to is in the server log and in the
  `workflow_executions` record. The way back is a single admin write — but it
  depends on the operator being able to reach that record.
- **A region binding is a CONSTRAINT, not a PREFERENCE.** An operator who binds
  two locations to separate regions has accepted that the order FALLS when the
  first location's stock runs out in a race. "A first, B when it runs out" is
  written with PRIORITY, not with a region binding.
- **Deleting the last region binding does not hide the location, it opens it to
  ALL regions** — the same as the sales channel rule, with one difference: there
  the price is visibility, here it is a dropped order.

## The limit of the invariants

- **The cross-module JSON SCHEMA is not checked at compile time.** A narrow
  interface plus resolution by name from the container is
  [ADR 0001](adr/0001-modul-arasi-iletisim.md)'s accepted price: a FIELD NAME
  drifting apart leaves both packages' unit tests green, and the two ends meet
  over a real container in e2e. The composite data crosses as `json.RawMessage`
  and nothing but a test that runs both sides can compare the two schemas.

  The SIGNATURE is no longer part of this limit and the distinction is the
  point. Since [ADR 0136](adr/0136-the-compiler-checks-every-interop-pair.md)
  `internal/arch/interop_pins_test.go` assigns every container-resolved producer
  to the interface its consumer declares, so a method that changes, moves or
  disappears fails the BUILD. The sentence that used to stand here — that the
  compiler never sees the two sides together — is what kept that file from being
  written, and while it stood two shipped storefront coupon endpoints answered
  500 to the first customer who typed a code (D73).
- **`TestEveryWorkflowIsSetUpInTheCompositionRoot` is a SYNTACTIC proxy.** It
  asks the question "can a wrong configuration stop startup" as "does the path
  to setup go through a `go` expression"; when the `go` is hidden behind a
  one-line indirection the audit passes while the property does not hold
  (measured in a real process). The shapes it catches are the ones written by
  accident, the shape it misses is the one that would have to be written
  deliberately — but the sentence "startup fails closed" does NOT FOLLOW from
  this invariant. The scope is written in
  `internal/arch/registration_test.go`.
- **Nothing checks that a secret comparison is CONSTANT TIME.** There are five
  such comparisons in the tree — a TOTP code, an argon2id derived key, two cookie
  MACs and a token digest — and every one of them uses `hmac.Equal` or
  `subtle.ConstantTimeCompare` because somebody wrote it that way, not because
  anything refuses the alternative. A mutation that replaced the TOTP comparison
  with `==` broke NO test, and no test could: timing is not observable from a unit
  test, and a test that measured it would be the flakiest one in the suite.

  What defends the six-digit case is therefore two things, neither of them a
  gate: the shape of the code, and the rate limit in front of the endpoint. A
  million possibilities against an early-exit compare is sixty tries; the rate
  limit is what makes sixty tries not free.

- **An exchange's goods and its money are related only by a human.**
  `order_exchanges` carries no items and its `difference_due` is a figure the
  operator types; the dispatch guard compares the payment collection against that
  figure and nothing else. Since ADR 0145 a replacement can send a variant the
  order never sold, so an operator can promise a jacket against a shirt's
  difference and nothing will object. Pricing the item at settlement needs a
  pricing read and a decision about which price applies to a replacement, and
  neither has been made.

- **A second factor is demanded of whoever holds one, and required of nobody.**
  Since [ADR 0147](adr/0147-a-second-factor-is-demanded.md) an administrator who
  proved an authenticator cannot sign in with a password alone — but an
  installation cannot insist that its administrators enrol. There is no setting,
  and the reason is that its first effect would be locking out every administrator
  at once: nobody has enrolled, so a shop-wide requirement needs a grace period
  and a way to see who is still without one, which is a separate decision.
  Recovery codes are absent for the same kind of reason: the way back from a lost
  phone is `gobit mfa-reset <email> -confirm <email>`, which needs shell access
  and answers the case in one act, so a second readable secret would be a second
  thing to store and re-issue for nothing.

- **An operator can build a telephone order but cannot finish it.** Since
  [ADR 0146](adr/0146-an-operator-can-build-a-cart.md) the cart's admin surface
  opens a cart and adds priced lines to it, and that is all it does: the shipping
  address, the shipping method and the payment are the storefront's endpoints, so
  the shopper has to complete the cart themselves. Completing it from the admin
  side was refused rather than missed — the money would be taken without the
  shopper ever seeing the total — but a shop whose callers cannot open a link is
  not served by this.

- **A customer cannot see their own store credit.** Since
  [ADR 0152](adr/0152-a-shop-can-hold-money-for-a-customer.md) a shop can hold
  money for a customer and the customer can spend it at checkout, but the only
  way to READ a balance is an admin endpoint: a storefront read needs the
  customer claim in the request proven, and the payment module is not wired to
  the surface that proves it. A shopper learns what they have when an operator
  tells them, or when the total drops.

- **Store credit does not expire and names no cause.** A credit issued today is
  spendable forever, and `reference` is free text, so "this is the compensation
  for return R-19" is a convention rather than a link. Expiry is a scheduled job
  writing negative rows, and what it must not do is race a checkout holding the
  same money.

- **An installation that trusts an unproven customer claim has no store credit
  at all.** With `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` on, the cart's
  customer is not proven, so the provider is not registered
  ([ADR 0152](adr/0152-a-shop-can-hold-money-for-a-customer.md)): the tender
  disappears rather than becoming a way to spend somebody else's balance. A shop
  that needs both has no answer here.

- **A missed cart event leaves the funnel one short, forever.** Since
  [ADR 0153](adr/0153-a-shop-can-see-where-its-carts-go.md) the cart publishes
  what happened to it and `plugins/analytics` counts it, but the bus does not
  redeliver on a handler error and there is no repair path: the outbox relay
  covers the ordinary loss and nothing covers the rest. A reconciliation job that
  re-derived the counts from the modules' own tables was refused — it would be a
  second history of the same facts, over rows a shop is allowed to delete.

- **The funnel starts when the plugin is installed, and knows only days and
  regions.** It says nothing about carts opened before `analytics` was named in
  `PLUGINS`; a cart opened on one day and completed on the next is counted on two
  different days, so a same-day ratio is an approximation; and there is no
  per-customer and no storefront view, because both would need an identity in a
  payload that deliberately carries none.

- **Every published event reaches every registered webhook endpoint, including
  `cart.created`.** `plugins/webhookout` forwards the whole topic set by
  construction — its own gate fails the build in both directions — so installing
  it means one delivery per opened cart. There is no per-topic subscription and no
  rate limit; the only lever is not registering a receiver.

- **No released version of this library can be imported.** Measured 2026-09-12:
  `github.com/bdrtr/gobit@latest` resolves to v0.8.0 and that tag does not
  contain the root package — the facade landed after it — so `require ... v0.8.0`
  plus `import "github.com/bdrtr/gobit"` fails at `go mod tidy`. The only version
  anybody can import today is a pseudo-version of a commit, which is what
  [`gobit new`](adr/0154-a-project-can-be-started-from-the-binary.md) writes.
  The fix is a release, and nothing in the repository can substitute for one.

- **No lane proves a generated project works at the version it PINS.** Every
  out-of-tree proof rewrites the generated `go.mod` to point at the checkout, so
  the lane compiles the template against the tip of the tree. It cannot tell
  "works at the pinned version" from "works at HEAD", and today those differ.

- **A generated project's own help calls itself `gobit`.** The binary name is a
  constant in the composition root, so a project named `shop` tells its operator
  to run `gobit migrate status`. And the generated project is GUEST-ONLY: the
  signed-in-customer adapter the starter example carries is not in the template,
  and the module it needs has no released tag at all.

- **A plugin's admin screen is not sandboxed from the panel.** Since
  [ADR 0155](adr/0155-a-plugin-can-put-a-screen-in-the-panel.md) a plugin can put
  a screen in the panel, and its script runs on the panel's own origin with the
  operator's session cookie — which is what makes it work and also what makes
  installing a plugin a decision about trust. The content policy bounds where
  code may come FROM, not what it may do once it is there.

- **A registered screen gets the frame and a script, and nothing else.** There
  is no way to add a column to an existing screen and no page-level scope: a
  screen is visible to anyone the panel lets in. ADR 0030's refusal of
  server-renderer extension points is why there are no template slots.

- **No lane boots the panel against a real server.** Its gates run over a real
  router with fake services; `make smoke` starts the binary and never opens
  `/admin/ui`. "The panel renders against a real database" is proven by nothing.

- **The load test is in-process** (`make load-test`, `internal/e2e`): it tests
  correctness under load, it does not produce a capacity plan.
