# ADR 0009 — Multi-tenancy: the boundary is the installation, not the row

- **Status:** Accepted
- **Date:** 2026-09-01
- **Phase:** after 10 (the v0.4.0 hardening round)

## Context

The plan document puts multi-tenancy out of scope in two places — in the
"Non-goals (ilk sürümde yok)" sentence and in the "10. Sonraki Sürüm Fikirleri
(şimdilik kapsam dışı)" section — but until this ADR was written it did not say
**why** it did. The concept does not occur in the repository at all: not one of
the 72 tables has a tenant column, no signature has a tenant parameter, no
namespace has a tenant segment.

> ~~(two line-number references pointing at the thirty-fifth and the three
> hundred and ninety-sixth line of the plan document)~~ **Corrected on
> 2026-09-06:** those two references were line numbers, and the second was
> already wrong the day it was written. The commit that added this ADR (9aa8b60)
> added the plan's justification paragraphs at the same time and rewrapped the
> file; in that commit line 396 of the plan file was already the task item
> "İlk commit: `chore: project skeleton (phase 0)`", and the second out-of-scope
> statement had been lifted **out of** the "Sonraki Sürüm Fikirleri" list and
> moved into the sentence "**Çoklu-tenant bu listede değildir**". A heading goes
> in their place: a line number points somewhere else the moment a line is added
> above it, and nothing reports that — this is exactly the form of rot
> `TestTheDocsCarryNoLineNumberReference` measures and forbids. That ban had not
> caught these two references, because its pattern only looks for paths with a
> `.go` extension; a line number given from one document to another stays
> outside the gate.

> **The counts in this ADR belong to the DECISION DATE** (2026-09-01) and are
> left as they were measured that day; they show the size the decision rested
> on, not today's schema. The justification does NOT WEAKEN as the numbers grow,
> it strengthens. The schema's table count today is under `README.md`'s "Tek
> örnek mi, birden çok mu?" heading, and that is the place that describes today.
> When the two numbers diverge, which one holds depends on the question asked:
> for "what did the decision rest on", the one here; for "what is there today",
> the one in the README.

An out-of-scope statement without a justification is not a decision; it gets
re-argued every round, and meanwhile doors close quietly. This ADR closes that
gap.

Three independent studies were done to give the decision its ground: a database
per tenant (A), row-level separation in a shared schema (B), and a leak hunt
over the existing mechanisms. The hunt's count gives the size of the work: of
the 72 tables, **0** can answer the question "whose row is this"; 55 sqlc files
hold **403** named queries; **13** `query.Provider`s are registered in the
container; `db.Pool.Pool()` stands at **19** production call sites; the guard
stack wraps **2** path prefixes.

But what settled the decision was not this volume — it was three structural
facts.

**First: the rehearsal of a scoping rule stands HALF DONE today.** The only
scoping mechanism in the repository is the sales channel filter, and the README
describes it as an *authorization* ("the channel is not taken from the query
string, it comes from the identity — had it been taken from there, the filter
would have stopped being an authorization"). The filter is meticulously applied
on the read surface: listing, counting, single and bulk visibility all go
through one single SQL template
(`internal/modules/product/repository/saleschannel.go`). On the write path it is
absent:

The storefront cart endpoint took `variant_id` from the client body and the flow
resolved it **globally** through Query — with an `id` filter alone. That is, a
client arriving with channel B's publishable key could add to its cart and buy
an A-channel variant it cannot see in the catalog. This was the **write side**
of the fault the repository has already paid for once (the channel binding was
being written but was not being read). Putting a second, larger scoping rule on
top of a half-implemented one would have been exactly this repository's most
expensive class of mistake, repeated at scale.

> **THIS JUSTIFICATION NO LONGER HOLDS, and the record is not left as if it
> did.** The hole was triggered by this ADR itself and was closed the same day:
> the flow now reads the variant scoped by the channels that come from the
> request's authenticated identity, the visibility predicate stays in one place
> (`salesChannelVisibleTemplate`), and an out-of-scope variant returns the same
> error as a variant that never existed. It was measured on the real process
> (404 / empty cart), the hole's reproducibility was proven by mutation (201
> once the filter is removed), and a proxy invariant was placed under
> `internal/arch`.
>
> The decision **stands** without this justification too: the two reasons below
> are the load-bearing ones and both were left untouched. The justification is
> not deleted, because an ADR is worth as much for the record of WHICH fact led
> to its conclusion as it is for the conclusion itself; a reader trusting an
> evidence block with no counterpart in today's code would be a separate fault.

**Second: both designs isolate the DATA, neither isolates the
CONFIGURATION.** The payment provider registry keys an identity by `id` alone
and the same `id` cannot be registered a second time
(`internal/modules/payment/service/registry.go`); plugins are chosen from the
`PLUGINS` environment variable and installed once at startup (`core/plugin`);
the Stripe secret key, the SMTP sender, `FILE_ROOT`, `JWT_SECRET` and every
quota come from a single `Config` per process. A tenant that cannot have its own
payment account, its own sender address and its own quota is not a tenant but a
compartment. That component is **not designed** in A or in B, and it is bigger
than the isolation choice itself.

**Third: the surface cannot vary per tenant, only the data can.** Routes are
bound once to a single router and chi panics on mounting the same pattern a
second time (the `core/http.Scoped` godoc); the OpenAPI document's cache has a
single slot. This is not a fault but a limit that has to be accepted, and it
holds whichever option is chosen.

## Decision

**Multi-tenancy IS NOT BUILT in gobit v1. The boundary is stated as follows:**

> Every gobit installation is **single-tenant**. Isolation is not in the
> application layer, it is in the **deployment layer**: one tenant = one
> installation = one database = one process. The framework **does not
> recognize** a boundary between tenants, therefore does not **enforce** one and
> does not **claim** to enforce one.

This is the justified form of "left to a later release", not its postponement.
In two sentences: a tenant boundary is dangerous precisely when it is half
implemented, and this repository has today half implemented even a smaller
scoping rule. That one gets closed first; the tenant boundary is argued after
it.

The decision has three binding consequences:

1. **The documents tell the truth.** The plan's two out-of-scope places and the
   README now carry the **justification** for leaving it out and link to this
   ADR: the paragraph added under the "Non-goals" sentence, and the sentence
   "**Çoklu-tenant bu listede değildir**" that closes the "Sonraki Sürüm
   Fikirleri" section. The sentence "left to later releases" stands alone
   nowhere.
2. **The choice between A and B IS NOT DEFERRED, it is TRIGGERED.** The
   "Reopening the decision" section below writes by name the data that would
   reopen the decision and the question that determines which one gets chosen
   when that data arrives. The decision cannot be made today because we do not
   have that data — the expected number of tenants and the requirement for
   per-tenant provider credentials are unknown.
3. **Three things get done today.** None of them is tenancy work; all three fix
   today's repository independently of the decision's direction and none of them
   creates an investment that will have to be undone. No concept, no field and
   no setting is **reserved** in tenancy's name: a capability with no consumer is
   the second class of mistake this repository has named.

   - **The write side of the channel rule.** Either the storefront cart path
     gets filtered by channel, or the hole gets written, as measured, into the
     README's "Aynı ölçütün henüz uygulanmadığı yer" section. A hole that is not
     recorded is a hole nobody closes.
   - **The `eventbus.Handler` godoc gets reconciled with its behavior.** The
     godoc says "the given ctx derives from the ctx of the request that called
     Publish"; that is true only for the in-memory backend
     (`eventbus.inMemoryBus.Publish` derives it from the caller's `ctx`,
     `eventbus.redisBus.dispatch` from the bus's root ctx). Because the default
     is `EVENT_BUS=inmemory`, every design that carries something in the ctx
     passes green in the tests and breaks in production. Fixing the sentence is
     one paragraph, and it is wrong today independently of
     tenancy.
   - **Rate limiting keyed by identity either gets wired up or gets deleted.**
     A `core/http.KeyFunc` implementation with no consumer in production was
     standing there (only its own test called it) and its godoc said it "keys an
     authenticated call by its identity"; but in the guard stack the rate limit
     runs **before** identity (`core/http.APIGuards`), so had it been wired up
     it would always have fallen back to the IP. In one place stood both a
     capability with no consumer and a function whose godoc had drifted from its
     behavior.

     > **Closed: the function was deleted.** The rate limit today keys on the IP
     > alone (`core/http.ClientIPKey`, in production
     > `core/http.TrustedProxyIPKey`); the capability of keying by identity
     > **does not exist** in the repository. It gets rewritten the day a quota
     > per identity is wanted, and on that day the ordering itself (the rate
     > limit runs before identity) has to be solved along with it — otherwise
     > the newly written one falls back to the IP for the same reason.

## Consequences

**Positive.** The framework does not look as if it gave a guarantee it does not
give. A period in which people believe "there is a tenant filter" never begins —
and the price of that period, as measured in the sales channel rehearsal, is a
boundary people trust that does not work, and at tenant scale its price is
another customer's data.

**Positive.** "One tenant = one installation" is not an evasion but a legitimate
and common position for frameworks. A single binary, a single DSN, automatic
migration at startup and a single admin seed — the repository's whole startup
sequence already describes this model. The decision writes into the documents
what the code already says.

**Positive.** Even though the choice is deferred, **the doors have become
counted**: the "Doors that are closing" list below carries by name the decisions
that are free today and will close expensively tomorrow. The list is
measurement, not prediction.

**Negative.** An operator who wants to sell the same product to several
customers from a single installation cannot do it with gobit; N customers means
N installations, N databases and N processes. For a product with many small
tenants this is a real cost and it lowers the sellable strength. Accepted: a
false isolation claim is more expensive than claiming nothing at all.

**Negative.** Because the decision is deferred, the cost of an A→B or B→A
migration is deferred with it; it will be paid on the day it comes. The
countermeasure is that the door list below lives together with this ADR.

## Doors that are closing

These are **not** a work order today; they are the measurements that determine
how expensive tenancy will be when it comes up, and they are written down so
they do not get missed.

- **`orders.display_id` comes from a single sequence with `GENERATED ALWAYS AS
  IDENTITY`.** If the shared schema (B) is chosen, per-tenant order numbering
  can only happen by replacing the column outright; adding a tenant prefix to
  the column is not enough. A database per tenant (A) solves this for free.
- **`ScopeAdmin` is a wildcard** (`HasScope`: `s == scope || s == ScopeAdmin`)
  and `POST /admin/v1/users` accepts arbitrary `Scopes` from the body and
  applies `"admin"` when none is given. The day tenancy arrives, "platform
  authority" cannot be a **scope** — every tenant admin would stamp it on
  themselves. There has to be a third value on `Principal.Kind`, and that is a
  decision that grows more expensive as scattered scope strings multiply.
- **`workflow_executions_idempotency_key_uniq` is over `(workflow,
  idempotency_key)`** and a second call with the same key returns `prev.Output`
  without running the steps. In a single tenant today this is the documented
  idempotency model; the day a tenant is added, unless the index becomes
  `(tenant, workflow, key)` it will deliver cross-tenant order output on the
  happy path.
- **`db.Pool.Pool()` is exposed at 19 production call sites** and 14 repository
  constructors take a `*pgxpool.Pool`. `product` already takes an interface, so
  the pattern is proven in the repository; but the constructor signatures are
  part of the embedded use the README sells, so narrowing them is a public break
  and is not done without a trigger.
- **`internal/core/workflow/pgstore/migrations` is a core schema outside a
  module** and `TestCrossModuleForeignKeyYok` only walks under
  `internal/modules/*`. The day an invariant per table gets written, the walk has
  to **discover** `migrations` directories rather than list them — otherwise
  core schemas quietly stay out of scope.

## Rejected alternatives

**(A) A database per tenant.** The option with the greatest enforcing power, and
its single justification is measured rather than theoretical: the 14 repository
pools use the pool only for `xxxdb.New(pool)` and `pool.Begin/BeginTx`, so a
single `db.Conn` interface takes its place at all 19 call sites and **none of
the 403 sqlc queries changes**. Because the rule is not written in a second
place the first class of mistake disappears; 43 uniqueness constraints, the
`display_id` sequence, the automatic promotion scan and a cross-tenant link row
become **impossible**; deleting a tenant becomes a `DROP DATABASE` rather than a
list. The reason for rejecting it is not its weakness but its price: the
credentials have to move to a control plane (which tenant a key belongs to has
to be known **before** that tenant's pool is opened), so `auth` stops being an
ordinary module and "delete the tenant = drop the database" is no longer true;
migration leaves the startup path and the "run the binary, the schema is ready"
property is lost permanently; the number of tenants is nailed to the process's
connection budget (hundreds, not tens of thousands). The most expensive one is
the one-way door: because no table has a tenant column, moving to a shared
schema afterwards means adding a column to 64 tables and rewriting every one of
the enforcement layers.

**(B) Row-level separation in a shared schema, enforced in the application
layer.** The startup sequence does not change, opening a tenant is adding a row,
it scales to tens of thousands of small tenants, and it solves by name two real
problems A passes over in silence — the rate limit ordering and the `ScopeAdmin`
wildcard. The reason for rejecting it is the KIND of guarantee it buys:
enforcement depends on the correctness of a **syntactic checker** that scans the
`.sql` files and the SQL constants in the Go source. The runtime gate (no
statement runs without a tenant) says "there is a tenant", the check says "there
is a predicate"; even the two together do not say "the value in the predicate is
the right tenant". Beyond that, all 403 queries get rewritten, every integration
fixture of every module has to set up a tenant, and migrating the existing
installations becomes mandatory. It is rejected by deferral, not permanently.

**(C) Hybrid: B's column today, A's placement tomorrow.** Technically the most
defensible long-term shape, and the one-way-door argument supports it: **going
from B to A is only a data move and it preserves the columns; going from A to B
is conjuring the columns out of nothing.** Its strongest end point is here too —
a tenant column + `FORCE ROW LEVEL SECURITY` + wrapping every statement in a
transaction that sets the tenant with `SET LOCAL` closes completely the "our
checker can miss a form" gap B leaves, because the party doing the refusing
becomes the engine itself. The reason for rejecting it today is that the cost is
the **highest** (all of B's price + putting all 403 reads into transactions) and
that what it buys still closes none of the three "solves nothing" items above.
When the decision is reopened, **this is the starting candidate**, not A or B.

**Reading the tenant from an `X-Tenant-ID` header or from the body.** A
letter-for-letter repeat of ADR 0008, and that decision was made together with
its measurement: `customer_id` is "not a fact but an ownership claim requiring no
evidence at all", and because it came from the body it was enough to burn
somebody else's spending allowance. A tenant header is the same claim with a far
larger blast radius. Whichever option is chosen, the tenant is read only from
the credential's **row**; not even from a JWT claim, because a token in hand can
carry a tenant that has been suspended or moved, and the user row is being read
anyway.

**A "multi-tenant mode" turned on and off with an environment variable.** ADR
0007's justification: a switch accidentally set to `false` removes the
protection without producing any error. Here it would fall to the wrong side as
well — an installation that is "off" would read a database full of tenant
columns without a filter.

**Adding a `TenantID` field to `Principal` or a `tenant` package today.**
Rejected, and this is this ADR's most concrete "do not do this" item. Adding the
field with nowhere to read it would be deliberately producing the repository's
second class of mistake (a capability with no consumer); on top of that, once
the field stands there the sentence "there is tenant support" builds itself.
Reserving the concept's name in advance has no technical benefit: there is no
name in the repository that collides with `tenant`, so there is nothing to
reserve either.

## Reopening the decision

This decision is not a closure but a triggered wait. Three questions reopen it
and the answers to two of them we do **not** have today; that is exactly why the
decision cannot be made:

1. **What is the expected number of tenants per process?** Hundreds means A;
   tens of thousands means B or C. This is not a setting but the fork: A's pool
   per tenant ties the number of live tenants to Postgres's connection ceiling.
2. **Will a tenant have its own provider credentials?** (its own Stripe account,
   its own sender address, its own quota.) If the answer is "yes", the
   per-tenant configuration component has to be designed **before** the
   isolation choice; that component exists neither in A nor in B and is bigger
   than both. If the answer is "no", what is wanted is probably not
   multi-tenancy but multi-store — and its answer is the sales channel that
   already exists in the repository today, with the write side closed first.
3. **Is per-tenant restore or data residency wanted?** If it is, A's trump card
   decides it: `pg_dump` is per tenant, and moving a tenant to another server is
   a single row in the control plane.

When the decision is reopened, the things to review are: every item in the
"Doors that are closing" list above (which of them had closed by then, and at
what price), the README's "Aynı ölçütün henüz uygulanmadığı yer" section (was
the write side of the channel rule closed) and ADR 0004 together with ADR 0005 —
the `query.Provider.FetchByIDs` signature has no filter parameter and link
tables are set up at run time; both are places a tenant boundary **cannot be
threaded through**, and on that day either they have to change or the tenant
boundary has to pass underneath them.
