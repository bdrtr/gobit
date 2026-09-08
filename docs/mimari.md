# gobit — Architecture

This document explains **why** the system is built this way. For what it does,
see the [README](../README.md); for the individual decisions,
[`docs/adr/README.md`](adr/README.md); for what has been measured,
[`docs/measurements/`](measurements/); for the faults found and closed,
[`docs/gaps.md`](gaps.md).

In case of conflict the order is: **ADR > this document**. The implementation
plan that used to sit above this one is no longer in the repository — it was a
design notebook, not a source of decisions, and keeping it here made readers
treat it as binding. The decisions are in `docs/adr/`.

---

## 1. In one sentence

gobit is a **modular monolith** that runs as a single binary: the modules are
unaware of each other at compile time, which is why any one of them can later be
extracted into a separate service.

"Modular monolith" is not a slogan here, it is an enforced constraint. Isolation
is enforced in three separate places:

| Constraint | Where it is enforced |
|---|---|
| the core cannot import the modules | `.golangci.yml` depguard (both `core/**` and `internal/core/**`), and for the published half more strictly by `internal/arch` — it may not import `internal/` at all |
| Modules cannot import each other | depguard + `internal/arch` (second line of defense) |
| `internal/workflows/**` cannot import the modules | `internal/arch` (ADR 0006) |
| `plugins/**` cannot import the modules | `internal/arch` |
| No cross-module foreign key | `internal/arch` (scans the migration files) |
| Money is an integer minor unit | `internal/arch` |

If a rule can be caught in a review round, it can be caught in a test too; once
it is caught in a test it is never written again.

---

## 2. Layers

```
gobit.go              the published facade an embedding program calls (ADR 0027)
  │
internal/app          COMPOSITION ROOT — the one place that knows everything
  │
  ├── core            the PUBLISHED surface: does NOT KNOW the modules (ADR 0026)
  ├── internal/core   the unpublished core: does NOT KNOW the modules
  ├── internal/modules    commerce modules: do NOT KNOW each other
  ├── internal/workflows  cross-module sagas: do NOT KNOW the modules
  ├── internal/adminui    the panel: a fourth tree (ADR 0011)
  └── plugins             plugins: do NOT KNOW the modules
```

What stands out is the number of "does not know"s. **Every decision** about who
talks to whom is made **in one package** (`internal/app`); every other package
only describes what it needs. It was `cmd/server` until ADR 0027 moved the
lifecycle behind the facade, so that an embedding project could add its own
module without owning a copy of the composition root; `cmd/server` is now the
smallest program that can run gobit, and it is also the example one copies.

The price of this is that the connections cannot be checked by the compiler: if
the signature a module publishes and the signature its consumer expects drift
apart, the error shows up at run time. The price was accepted deliberately and
is met with two things: (1) the end-to-end tests build the production wiring
**exactly**, (2) every interop surface has an integration test that runs against
real dependencies.

---

## 3. The life cycle of a request

```
request
 └─ RequestID          a traceable identity for every request
     └─ Telemetry      open a span (the route pattern is known AFTER the handler)
         └─ RequestLogger
             └─ Recoverer          panic -> 500, the connection does not drop
                 └─ Rate limit     scoped to /admin/v1 and /store/v1
                     └─ Identity   the login endpoint is EXEMPT
                         └─ Idempotency
                             └─ chi route match
                                 └─ handler -> service -> repository
```

Every link in the order answers a failure scenario:

- **RequestID first**: so the logger and the recoverer can report the request by
  its identity.
- **Telemetry ABOVE the Recoverer**: so that when the handler panics and the
  Recoverer writes a 500, the span sees that state. Below it, the request that
  gets looked at most would be the one recorded most incompletely.
- **Rate limit BEFORE identity**: so an attacker guessing passwords does not
  make every attempt cost a bcrypt and a database round trip.
- **Idempotency AFTER identity**: so the record key is held together with the
  caller's identity; two different callers must not collide on the same key.

Middleware is attached **while the router is being built** — chi refuses with a
panic if `r.Use` is called after a route has been registered. Modules, on the
other hand, register their routes on a flat router with the **full path**
(mounting the same prefix twice would panic), so chi's natural scoping tool is
lost. `corehttp.Scoped` fills that gap: the scope is established inside the
middleware itself rather than in the router tree.

The stack's **order is written in a single place** (`corehttp.APIGuards`) and
the end-to-end tests build that very stack. If the test had its own copy, then
when the production order changed the test would verify the old order and stay
green.

### Error → status mapping

Services return typed errors (`core/errors.Kind`) and the HTTP layer maps them.
Handlers **do not choose** the status code:

| Kind | Status | Message to the client |
|---|---|---|
| `NotFound` | 404 | yes |
| `Invalid` | 422 | yes |
| `Conflict` | 409 | yes |
| `Unauthorized` | 401 | yes |
| `Forbidden` | 403 | yes |
| `TooManyRequests` | 429 | yes |
| `Unavailable` | 503 | yes |
| `Internal` | 500 | **no** (logged) |

---

## 4. The life cycle of a module

`module.Registry` runs three stages **in order, for all the modules**:

```
1. Register(ctx, container)   all modules  ─┐
2. Migrations()               all modules   │  barrier between stages
3. Routes(router)             all modules  ─┘
```

The barrier is mandatory: `Routes` is not reached until every `Register` has
finished, so that one module's handler can safely resolve another module's
service. Conversely, another module's service cannot be resolved **inside**
`Register` — at that moment it may not be registered yet. If it is needed, a
lazy constructor is handed over and the resolution happens on first use.

Every module owns its own migration folder and its own version table
(`<owner>_schema_migrations`, built by `db.MigrationsTable` and handed to
golang-migrate through `postgres.Config.MigrationsTable`, not written into the
DSN — [ADR 0003](adr/0003-migration-iptali.md)), so one module's schema history
advances independently of the others.

---

## 5. Cross-module communication

In Go, interfaces live in packages; importing the provider's interface would
break isolation. This is why **the interface is defined in the consumer's own
package** and the concrete service is resolved from the container **by name**
([ADR 0001](adr/0001-modul-arasi-iletisim.md)):

```go
// in the order module, WITHOUT importing b2b:
type SpendingPolicy interface {
    SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error)
}
policy, err := container.Resolve[SpendingPolicy](c, "b2b.interop")
```

The example is not invented, it is a live seam in the tree: the interface is
defined on the CONSUMING side in
`internal/modules/order/service/spending.go`, the name is a constant in
`internal/modules/order/module.go`, and if the b2b module is not installed the
resolution fails — in which case the order goes through without a spending
limit. That the signature returns `json.RawMessage` is not an accident either:
the only thing two modules share should be a SCHEMA, so that neither names the
other's type nor gives birth to a shared package.

The published surfaces are deliberately **narrow and speak in primitive types**:
every method is a contract and the compiler does not check it — the interface is
defined on the CONSUMING side and the provider satisfies it structurally.
Through `0.x` a signature may change, but the price has to be visible: it is
written into `CHANGELOG.md` as a breaking change, and the proof of a
resolved-by-name seam is the e2e test, because a drifted signature leaves both
packages' unit tests green as well. If rich data is needed, the right path is
not a new primitive method but the Query layer.

The name dictionary in the container:

| Name | Contents |
|---|---|
| `<module>.service` | the primitive cross-module call surface |
| `<module>.interop` | the narrow surface for sagas/core |
| `<entity>.query` | the read provider opened to the Query layer ([ADR 0004](adr/0004-query-veri-erisimi.md)) |
| `<module>.providers` | provider registry (payment, fulfillment) |
| `core.*` | infrastructure: db, redis, eventbus, workflow, link, query |

---

## 6. Data

- **SQL-first**: `sqlc` + `pgx/v5`, separate codegen per module. An ORM's
  FK/graph model would conflict with module isolation.
- **NO cross-module foreign key** (Principle 2.2). Relations are established
  with `core/link`; cardinality is enforced by a database constraint, but no FK
  is created between tables
  ([ADR 0005](adr/0005-link-semasi-migration-disinda.md)).
- **Cross-module reads** go through `core/query`: fetch the root → resolve the
  link → batch fetch → merge. N+1 is structurally impossible.
- **Money** is an integer minor unit (kurus/cent); there is no float, and the
  currency is a separate column.
- **Time** is UTC; `created_at/updated_at/deleted_at`, with soft deletion via
  `deleted_at` for what a merchant EDITS. The exceptions are counted and each
  one is justified at the top of its own table: rows that do not live apart from
  their owner, the ledgers of simulated external systems, **configuration
  tables** (a soft-deleted setting row has the same effect as a row that never
  existed), and — since
  [ADR 0054](adr/0054-an-order-and-a-payment-are-never-deleted.md) — the whole
  of the **order and payment** modules, whose rows are records of something that
  happened and retire by STATUS rather than by being hidden.

### The schema surface: status and rollback

The forward direction **is automatic at startup** and stays that way: there is
NO separate "migrate up" command, because in every installation that advances
the schema in a separate step it eventually becomes "I forgot to update the
schema". Running the binary with no arguments starts the server, and that is the
**only** way to start the server.

Rolling back, on the other hand, is invoked by hand (`internal/app/migrate.go`):

```
gobit migrate status                     reports each owner's version and dirty state
gobit migrate down <owner> -confirm <owner>   rolls back ONE owner
gobit help                               the whole surface
```

Three decisions are written down:

- **The confirmation is a REPETITION of the owner's name.** Without `-confirm`
  the command prints the plan (which owner, which version, how many steps) and
  returns a **non-zero** code. The refusal is itself the dry run; printing the
  plan and returning 0 would report a rollback that never happened as a success
  to a script that forgot the flag. A bare `-yes` was not chosen: a command line
  copied out of a runbook carries that flag along with it and ends up confirming
  a **different** owner. `-steps` defaults to 1 and anything below 1 is refused;
  `db.MigrateDown` reads "steps <= 0" as ALL.
- **A dirty ledger is REFUSED and the confirmation does not override it.** Dirty
  means the previous run stopped halfway: part of the current version's
  `.up.sql` ran and part of it did not. The matching `.down.sql`, however, is
  written for the state in which ALL of it ran, so running it is a guess the
  command cannot verify. The error carries the name of the table to be repaired
  (`<owner>_schema_migrations`); golang-migrate's own message does not.
- **The closing line is read from the LEDGER, not from the number of steps
  asked for.** golang-migrate may go fewer steps than asked; a message produced
  from the request would be a number the operator believes but the schema does
  not carry. If the version did not move at all, the command returns an error.

The subcommands read the module list from the registration in `internal/app`
(`registerModules`, the composition root since ADR 0027), including the module a
plugin brings in (`searchpg`). If a second list were kept, `migrate status`
would one day silently skip an owner whose tables are sitting in the database.

While reading the version, `migrate status` CREATES the missing
`<owner>_schema_migrations` table (the driver's behavior) and says so at the
bottom of the report; on a fresh database the command leaves one empty table per
owner behind.

The wiring has two proofs: `TestOnlyAnEmptyArgumentListCanStartTheServer` walks
the source and checks that `serve` is reached from exactly ONE call site, while
`TestMigrateSubcommandsRunWithoutStartingTheServer` runs the real binary and
shows that the subcommand exits and never binds the port.

---

## 7. Workflows (sagas)

Every multi-step cross-module operation is a saga on `internal/core/workflow`:
sequential execution, compensation **in reverse order** on failure, retry, an
idempotency key and panic isolation. The execution state is written to Postgres,
so the claim "the same cart cannot be completed twice" is the behavior of a
durable record rather than of an in-process map.

`internal/workflows` does not import the modules either
([ADR 0006](adr/0006-workflow-modul-erisimi.md)); the same narrow interface +
resolution by name rule applies here too.

**Multi-warehouse allocation** was added to the saga later and its seam is split
across two modules: inventory states the fact of "which warehouses have enough
units", fulfillment makes the decision of "which one we ship from". The saga
makes neither by itself — the cart flow has nothing to say about warehouse
policy. Fulfillment's decision is asked ONCE per line and returns not a single
warehouse but a PREFERENCE ORDER: the module eliminates the warehouses that do
not serve the target region and lines the rest up in the operator's priority
order. Because the candidate list is read without a lock, the warehouse at the
head of the order may be exhausted by the time of the allocation; in that case
the next candidate is taken and the shipping module is not asked again. This is
NOT retrying the step (that is closed, because repeating `Reserve` would produce
a second reservation) — a failed call has left no reservation behind. If **all**
of the remaining candidates are eliminated, the line falls; the error then
carries the shipping module's own code and can be distinguished from
insufficient stock.

**Pivot steps** are documented separately. `capture_payment` is a pivot: no
rollback is performed once the capture has been attempted, because treating an
uncertain capture as "canceled" is the quietest way to lose money. The residual
risk and the reconciliation need are written in
`internal/workflows/checkout/doc.go`.

**The surface that LISTS half-finished executions is a subcommand of the
binary** (`gobit stuck`,
[ADR 0016](adr/0016-operator-read-surface-for-half-done-sagas.md)). The records
are in the core's tables and the core does not have an HTTP endpoint the way the
modules do; the panel, for its part, cannot carry this screen without reopening
ADR 0011's Decision 6. The command ONLY READS and stays that way: releasing the
stock of a saga that is still running would allocate it a second time. The thing
that does the releasing is RECOVERY, and it is arrived at from the engine: a
caller returning with the same idempotency key finds the abandoned execution and
the compensation chain is run from the records
([ADR 0017](adr/0017-recovering-abandoned-sagas-from-the-record.md)). That
covers the customer who retries and NOBODY ELSE, which is why the same chain was
given a second door: `gobit recover <execution-id> -confirm <execution-id>` runs
it for one execution the operator NAMES, and that is how a line in the listing
turns into an action. If the chain stopped at the capture step, the record stays
in the list and the work is done by hand.

The two classes the command covers are NOT the same condition, and the second
was found by measurement: `compensation_failed` records have been closed by the
engine and logged at ERROR, but for that state to be reached **something has to
happen**: the engine writes it either LIVE (a step and its compensation fail in
the same call) or on the REPEAT path (a record whose lease has expired is come
back to). If the process DIED in the middle of the saga and the customer never
returned, neither happens: the cart stays `running`, holds its stock and appears
in no log line. The measurement: in a six-record rig, of the two executions
awaiting manual intervention only **one** is found by a status query.

---

## 8. Identity and hardening

For the detail see [`docs/security.md`](security.md) — the two surfaces, the
sales-channel filter, the scope dictionary and the hardening rings.
Architecturally, three points matter:

1. **The core does not know HOW identity is verified.** `corehttp.Authenticator`
   is defined on the consumer side (in the core), the auth module satisfies it
   **structurally**, and it is resolved from the container by name. The core
   does not import auth.
2. **The guard is attached in the composition root, not in the module.** A
   module declares in its godoc which of its endpoints need protecting; the
   binding is done by whoever builds the router.
3. **Identity and authorization are separate layers.** `RequireAdmin` resolves
   "who are you", `RequireScope` enforces "what may you do". Privilege
   escalation is closed in the service layer as well: a caller cannot grant a
   scope they do not hold themselves. One layer would not be enough — the
   middleware map can be loosened one day, whereas the service rule stands next
   to the data.
4. **Failure behavior varies by component**
   ([ADR 0007](adr/0007-sertlestirme-arizada-davranis.md)): identity is
   fail-closed, the rate limit is fail-open, idempotency refuses on reservation
   and releases the key on record. There is no uniform rule, because the answer
   to "what breaks without this component" is different on every line.

The scope dictionary derives from a single rule — `<module>:read` for reading,
`<module>:write` for writing, `admin` covers everything — and every module's
`api` package publishes its own constants. A module that forgets to add the
enforcement cannot stay silent: `internal/e2e/authorization_test.go` **walks**
the router tree, goes to every `/admin/v1` endpoint with an unauthorized token
and expects a 403. A hand-written endpoint list would go blind at the first
endpoint somebody forgot to add — and the endpoint that gets forgotten is
precisely the one just written.

Because the authenticator is born **after** the router, it is bound via
`corehttp.DeferredAuthenticator`; a request that arrives before the binding is
refused.

---

## 9. Extension points

### A new module

1. Under `internal/modules/<name>/`: `module.go`, `models`, `repository`,
   `service`, `api`, `migrations`, `queries`, `sqlc.yaml`.
2. Add a `module-isolation-<name>` depguard block to `.golangci.yml` **and** add
   the new module to the other modules' blocks.
3. Add it to `registerModules` in `internal/app` and to the harness in
   `internal/e2e` — the two carry the same order.

### A new domain event

In the module's `service` package, publish the event name and the payload keys
as **constants**; on the Redis backend the name is also the stream name, and
changing it means every subscriber silently stops receiving the event. The
payload is kept narrow and all values are strings (rationale:
`order/service/events.go`).

Whether a publish failure fails the write varies by module: in order it is
inside the saga, in catalog it is after the commit, and returning an error there
would be telling the caller "it was not applied".

### A new plugin

A package under `plugins/<name>/` implementing `coreplugin.Plugin`; the contract
comes from `core/provider` and the registration point from `coreplugin.Host`. A
catalog line is added to `pluginCatalog` (`internal/app/plugins.go`) and
selected with `PLUGINS`. The core and the modules **do not change**.

Installation has two phases: `Install` before the modules (so a module a plugin
brings in also goes through the life cycle), `Start` after the modules (a
provider registration only exists once the target module is up).

Go's standard `plugin` package (.so) was deliberately not used: it works only on
Linux/macOS, does not support cross-compilation, and requires that **all** the
dependencies of the plugin and of the main binary be compiled at bit-identical
versions. These constraints turn the promise of "plug it in while running" into
"recompile the whole application for every plugin" in practice — that is, into
what compile-time registration already provides, with fragility added on top.

### A new module's scope dictionary

The module's `api` package publishes the constants
`ScopeRead = "<module>:read"` and `ScopeWrite = "<module>:write"`; inside
`Routes`, the read and write sub-routers are built with
`corehttp.RequireScope`. Forgetting is not silent:
`internal/e2e/authorization_test.go` walks the router tree and goes to every
`/admin/v1` endpoint with an unauthorized token.

### A new provider (payment/fulfillment)

Implement the `core/provider` contract and add it to the `<module>.providers`
registry. A second registration under the same identity is refused and the
existing provider is kept: silently overwriting would leave which provider runs
up to the load order — and in payment the price of that is money going to an
unexpected organization.

---

## 10. Technology choices

| Area | Choice | Justification |
|---|---|---|
| Router | `chi` | Lightweight, `net/http`-compatible, middleware-friendly |
| DB access | **`sqlc` + `pgx/v5`** | SQL-first and codegen per module; an ORM's FK/graph model conflicts with module isolation |
| Migration | **`golang-migrate`** | An exact fit for `Module.Migrations() fs.FS`, with one version table per module (`<owner>_schema_migrations`, set through `postgres.Config.MigrationsTable`) |
| DI | ~~**`samber/do` v2**~~ **hand-written** (`core/container`) | ~~It offers contract-named services plus a generic resolve; lazy instantiation and shutdown hooks come ready~~ **Corrected on 2026-09-06:** `samber/do` was never a dependency — its name appears in neither `go.mod` nor `go.sum`. The Section 5.1 contract wants a `Provide` that takes `any`, and because `do` is type-parameterized the diagnostics, the conflict detection and the shutdown order were all lost; the decision is in [ADR 0002](adr/0002-di-container-el-yazmasi.md) and the justification is written in `core/container`'s own godoc ("Why not samber/do") |
| Config | `caarlos0/env` | Reads the environment only; viper's file/remote config weight is unnecessary |
| Log | `log/slog` (stdlib) | Structural, dependency-free |
| GraphQL | **`99designs/gqlgen`** | Schema-first: the schema stays an inspectable artifact and the generated typed resolvers catch a signature drift at compile time (the same discipline as sqlc) |

~~sqlc, golang-migrate and samber/do come into play **between Phases 1 and 4**;
Phase 0 only sets up the skeleton.~~ **Corrected on 2026-09-06:** sqlc and
golang-migrate did come into play **between Phases 1 and 4** and Phase 0 had only
set up the skeleton; `samber/do` never came into play at all — its place was
taken by ADR 0002's hand-written `core/container`.

---

## 11. The core packages

What sits under `core/` is the PUBLISHED surface: a program outside this
repository may import it, and every exported name there is a promise (ADR 0026).
What sits under `internal/core/` is not published; it can still change.

| Package | Responsibility |
|---|---|
| `internal/core/config` | env-based 12-factor config + validation, the production guard |
| `internal/core/logger` | slog JSON/text handler |
| `core/errors` | Typed errors (`Kind`), re-exports the stdlib `errors` helpers |
| `core/db` | pgxpool pool + a migration runner with a separate version table per module |
| `core/container` | Named registration, generic `Resolve[T]`, lazy singleton, cycle detection, shutdown in reverse order |
| `core/module` | The `module.Module` contract + `module.Registry` (register → migrate → routes) |
| `core/eventbus` | `EventBus` + InMemory (dev) and Redis Streams (prod, consumer group + XACK) |
| `core/http` | chi router, RequestID/RequestLogger/Recoverer/Telemetry, RequireAdmin/RequireStore/RequireScope, the `Scoped`/`APIGuards` protection stack, rate limit, idempotency, `Kind`→status mapping |
| `core/link` | Module Links — relations between modules without an FK; cardinality is enforced by a database constraint |
| `core/query` | Cross-module reads — fetch the root, resolve the link, batch fetch, merge; N+1 is structurally impossible |
| `internal/core/workflow` | The saga engine — compensation in reverse order, retry, idempotency key, panic isolation |
| `internal/core/workflow/pgstore` | The Postgres store of the execution state (`workflow_executions`) |
| `internal/workflows/cart` | The cart flows: create_cart, add_line_item, update_line_item, calculate_totals. `internal/app` registers them under the name `workflows.cart.interop`, and the `cart` module's storefront endpoints resolve that name |
| `internal/workflows/checkout` | The `complete_cart` saga: reserve stock → order → authorize → capture → close the cart. Registered as `workflows.checkout.interop`; `POST /store/v1/carts/{id}/complete` calls it |
| `core/provider` | The payment/shipping provider contracts (plan Section 5.6) |
| `core/plugin` | The plugin contract + two-phase installation (`Install` → modules → `Start`) |
| `internal/core/observability` | OpenTelemetry trace + metric setup; genuinely off when there is no collector |
| `core/openapi` | OpenAPI schema generation from the router tree (`/openapi.json`); published so an out-of-tree module can describe its own endpoints (ADR 0035) |

Which backend the event bus runs on, and what each one loses, is in
[`docs/operating.md`](operating.md).

---

## 12. Known limits

| Limit | Effect | Way out |
|---|---|---|
| Cross-module signatures are not checked at compile time | Drift shows up at run time | An integration test for every interop surface (the existing rule) |
| Session revocation is only **wholesale** | No dropping a single device | A jti-based blacklist — which means a new store read on every request |
| Load testing is in-process | Does not produce a capacity plan | An external load tool against a real deployment |
| Rollback is for ONE owner and does not know the order | An operator who wants to roll back modules together repeats the command per owner; the command does not say in which order to roll back | Because there are no cross-module FKs, order is not a constraint today; a definition of order is added when it is genuinely needed |
| There is no command that REPAIRS a half-finished migration | `migrate down` refuses a dirty ledger and sends you to a manual repair; there is no "force" surface | Deliberate: the only party that knows the version correctly is the human looking at the half schema |
| Recovery has to be NAMED, nothing sweeps | The engine's own recovery is stumbled into: it runs when a caller returns with the same key (ADR 0017), which covers the customer who retries and nobody else. An abandoned execution stays `running` and `gobit stuck` keeps listing it until an operator names it to `gobit recover <execution-id> -confirm <execution-id>` | A scheduled sweeper stays deliberately absent: recovery runs work that has side effects. The command the operator CAN TRIGGER was built first, as this row said it should be; a sweeper would still be deciding that unwatched, and that decision stays with the human |
| Recovery STOPS at the capture point | An unrecorded capture step cannot be counted as "did not run" (the engine writes the record after Invoke returns), so the card may have been charged; the chain stays in manual intervention | Writing the step record BEFORE Invoke would narrow the limit but not remove it; rejected in ADR 0017 |
| The list of half-finished executions is not visible from a browser | An operator without a shell cannot run the command | A screen in the panel — but that requires knowingly reopening ADR 0011's Decision 6 |
| The scope dictionary is two entries per module | No resource-level distinction (e.g. reading variants only) | The distinction is added when it is genuinely needed; adding it now would give a false sense of precision |
| The in-memory idempotency store is bounded by a byte budget | When the budget fills, the **oldest** record is dropped; a repeat arriving with that key is processed again (duplicate side effect) | `GUARD_BACKEND=redis`, or a larger `IDEMPOTENCY_MAX_MEMORY_BYTES` |

Multi-instance is no longer a limit but a **setting**: `GUARD_BACKEND=redis`
makes the rate limit and the idempotency store shared (see
`core/http/redisguard`). The `memory` default is deliberate — a single-instance
development setup should not require Redis — but on a shared environment it
produces a warning at startup.

The in-memory store's budget (`IDEMPOTENCY_MAX_MEMORY_BYTES`, 64 MiB by
default) is the reason for that row in the table: without a budget the only
limit was the TTL, and because the CLIENT chooses the key that opens a record,
that limit stopped growth nowhere (measurement: 10,000 records with 64 KiB
bodies came to 630.69 MiB). Dropping the oldest record when the budget fills,
rather than refusing the new request, is a deliberate choice — refusing would
give a single client sending made-up keys the ability to shut down the shop's
entire mutation traffic. The eviction is logged at WARN and the budget is
written at every startup; the full rationale is in the
`corehttp.MemoryIdempotencyStore` godoc.
