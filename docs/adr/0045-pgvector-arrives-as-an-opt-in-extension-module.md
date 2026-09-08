# ADR 0045 — pgvector arrives as a separate OPT-IN extension module, and the cluster contract does not move

**Summary:** ADR 0015 is not reopened and the extensions row keeps the value
`none`. pgvector arrives as a separate OPT-IN extension module an installation
chooses.

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A10 asks one question: does pgvector reopen ADR 0015, the record that turned
the PostgreSQL dependency into a written contract and whose extensions row reads
**none**?

The question is not idle, because ADR 0015 armed its own trigger: "Reopen when
the first EXTENSION becomes required." Semantic search, visual search and
embedding recommendations all want `vector`. **And the repository has already
answered this the other way, twice, in prose that nothing checks.** docs/gaps.md
says under its AI-features item that "the honest reading is that semantic search
reopens ADR 0015 rather than sitting on top of it", and makes the same claim in
different words under visual and semantic search, where `pgvector` being
unavailable on the cluster and the contract's zero required extensions mean
"this item reopens that ADR rather than sitting on top of it". Those two
passages are what this record contradicts, and naming them first is half of why
it is being written.

### What is on the ground

Measured 2026-09-07 against the development cluster — the container the shipped
deploy/docker-compose.yml describes, reporting PostgreSQL 16.14 on
`postgres:16-alpine`:

| probe | answer |
|---|---|
| `SELECT count(*) FROM pg_available_extensions` | 61 |
| `vector` among them | 0 rows |
| `CREATE EXTENSION vector` | `ERROR: extension "vector" is not available` |
| `pg_trgm`, `unaccent`, `btree_gin`, `fuzzystrmatch` available | yes |

The error is not bare. It carries a DETAIL naming the control file it could not
open and a HINT saying the extension must first be installed on the server. That
matters in decision 4 below.

The contract's evidence still holds today, and the grep it rests on is worth
running rather than quoting. `grep -r "CREATE EXTENSION" . --exclude-dir=.git`
finds hits in FOUR files and **every one of them is prose** — a note in the
invoice module's second migration, ADR 0015's own table row, ADR 0032's
discussion of `plpgsql`, and this record, which names the statement repeatedly in
the course of deciding where it may live. Not one of them is a statement the
database will ever run.

### Where an extension would land, and what already stands in the way

A plugin that brings a module is the established shape for a new capability:
`plugins/searchpg` carries its own table, its own GIN index and its own version
ledger (`searchpg_schema_migrations`), and it is present only when an operator
names it in `PLUGINS`. Four of the NINE plugins the catalog in
internal/app/plugins.go registers ship migrations today: paymentpaytr, searchpg,
webhookout and webpush. The directory listing counts ten, but
`plugins/payment-stripe` holds one empty `.gitkeep` and no Go code, and it is the
catalog rather than the listing that decides what a plugin is.

That is also where the cost is. Since 2026-09-06 the migration walk in
internal/arch/arch_test.go reads every production tree, `plugins` included, so a
new `plugins/<name>/migrations` enters three gates on the commit that creates it
— the rollback-pair gate, the cross-module foreign-key gate, and
`TestMigrationsCanReallyBeRolledBack`, which runs EVERY migration directory
up, down and up again on a real PostgreSQL brought up by testcontainers. That
walk finds twenty-five directories today, and CI runs the integration lane on
every pull request and on every push to `main`, with no service definition of its
own — the image comes from a Go constant. **So a migration containing
`CREATE EXTENSION vector` fails the build on the commit that lands it**, on the
image measured above.

Three separate facts bound how expensive that is, and all three were checked
rather than assumed:

- The image is NOT one repository-wide constant. Thirty-three test packages each
  declare their own, one per directory; internal/arch's lives in
  internal/arch/migrations_integration_test.go and is used at exactly one place.
- The walk already carries a skip rule — a directory holding its own go.mod is
  not descended into, which is why examples/plugin is outside it — and this
  package keeps at least five live exemption maps with anti-rot checks. An
  exemption here would be an idiom, not an innovation.
- A plugin need not ship a migrations directory at all. The registry skips a
  module whose migrations are nil, examples/starter/loyalty does exactly that,
  and core/link/link.go is the in-tree precedent for a schema built by idempotent
  runtime DDL under an advisory lock with a durable definitions ledger — a shape
  ADR 0015's PRIVILEGES row already accounts for, because that row requires the
  application role to be able to run DDL at runtime and not only during
  migration, and names `link.Define` failing at startup as what happens
  otherwise.

### The precedent that decides it

ADR 0015 already contains a clause whose only consumer is a plugin. The
case-folding probe, `core/db.CaseFolding`, tests three paths, and its full-text
lane exists because the searchpg plugin's index depends on it — the field's own
comment says so. The contract absorbed a plugin's need without growing a second
document.

**That absorption was free, and an extension is not.** A CTYPE that folds
non-ASCII case is something a UTF8 cluster either has from `initdb` or does not;
an installation that never runs searchpg pays nothing for the row, and the probe
only warns. An extension is a thing an operator must INSTALL on the server, or
have allow-listed by a managed provider. Putting `vector` in the table would
charge that to every English-only shop that will never compute an embedding. The
two clauses are not the same kind of clause, and that is the whole reason their
right homes differ.

## Decision

**1. ADR 0015 is not reopened. The extensions row keeps the value `none`.**

That row states what EVERY installation must provide. An opt-in plugin nobody is
obliged to install does not change what anybody must provide, and the trigger
ADR 0015 wrote says "required", not "used somewhere in the tree".

**2. pgvector arrives as a SEPARATE extension module** — a plugin, absent unless
its name is in `PLUGINS`, bringing its own module in the searchpg shape: its own
schema, its own `<owner>_schema_migrations` ledger, its own index and its own
reindex path.

**3. The `CREATE EXTENSION` belongs to that module and to nothing else.** Not to
a commerce module's migrations, not to the four core schemas that
`openApplication` applies before any module's, and not to searchpg. A statement
in any of those places is a requirement on every installation wearing the costume
of a plugin's, and it is the thing decision 1 refuses.

**4. Nothing is added to the startup probe.** ADR 0015's decision 4 gives the
criterion and it is not thoroughness: the probe exists for failures that succeed,
return nothing, and look like an empty catalog. Measured above, a missing
extension answers with an ERROR, a DETAIL and a HINT. Adding a check for a row
that already speaks is exactly what that decision removes.

**5. The trigger stays armed, and this is what would fire it.** The day anything
OUTSIDE the opt-in module depends on an extension — the storefront's own `?q=`
filter, a core schema, a commerce module — the requirement has become required
and ADR 0015 is reopened in the same change that creates the dependency.
`pg_trgm` remains the standing candidate there for precisely that reason: it
would accelerate the storefront's OWN filter, which every installation runs with
no plugin at all, and ADR 0015 records the gain it would buy on the
52k-product fixture as 58.9 ms falling to 2.2 ms. That case is not this one.

## Rejected alternatives

**Reopen ADR 0015 and add `vector` to the contract table.** It buys the thing
this repository usually prefers: one place an operator reads to learn what their
cluster must do, and a requirement stated once instead of hidden in a plugin's
package documentation. It is killed by what the contract IS. ADR 0015 says
outright that the contract is a promise and that widening it is a breaking change
for operators even when no Go signature moves — so this option breaks every
existing installation to describe a capability none of them has switched on.
Worse, it would be a promise the repository itself cannot keep: `vector` is not
among the 61 extensions available on the image the compose file ships and CI
runs, so the contract would name a requirement no test in the tree can satisfy.
Whether `vector` is allow-listed by any particular managed provider was NOT
measured here and this record claims nothing either way — which is itself a
reason not to write it into a table that promises.

**Fold semantic search into the existing searchpg plugin.** The strongest
candidate, and the one with a real argument: a shop wants ONE search endpoint,
and lexical and vector ranking blended in one place beats two indexes over one
catalog that nothing reconciles. It is killed by the upgrade path. searchpg runs
today on a cluster with zero extensions; the requirement a plugin carries is
per-PLUGIN and not per-endpoint, so folding `vector` in would turn every
installation that had switched on lexical search into one that must now install
an extension. That is telling users what they needed AFTER they have installed —
the failure ADR 0015 says it deliberately dated itself ahead of the record that
publishes `core/db` in order to avoid.

**Make it a provider slot instead of a module.** The embedder really is an
outside party that can be ASKED for a value, which is the first shape in this
repository's plugin doctrine and the shape notification and file storage take.
It is killed by storage: a provider slot carries a contract, not a schema, and
the vectors have to be written, indexed and searched. Web push is the measured
case for this exact boundary (ADR 0018) — the slot could not carry a
three-part subscription and the capability became its own module. Note what this
does NOT reject: the pairing, a slot for the embedder beside a module for the
index, is the payment-callback shape and remains open.

**Exempt the extension module's migrations from the round-trip gate.** The
cheapest path, and defensible on house style — internal/arch keeps five
exemption maps with rot checks and the walk already skips nested modules. It is
killed by what would be exempted. That gate was written for a Phase 5 fault in
which a rollback failed at runtime, left golang-migrate's ledger dirty, and the
server never came up again. A `CREATE EXTENSION` against a cluster that lacks it
produces precisely that failure. Exempting the one migration in the repository
that can fail for an ENVIRONMENTAL reason from the gate that exists to catch
environmental migration failures is a hole cut in the shape of the defect.

**Leave A10 open until an AI subsystem exists.** It buys honesty about a
capability nobody has built — docs/gaps.md records the AI subsystem as absent,
and nothing in the tree computes an embedding. It is killed by what already
happened while it was open: the question got answered anyway, informally, in two
passages of a gap document, in the opposite direction, with nothing comparing
them to the ADR they were about. A scoping question does not stay unanswered; it
gets answered by whoever writes the next paragraph.

## Consequences

**Positive**

- **No existing installation's obligations change.** The contract table is
  untouched, so an operator who read it last week read something still true.
- **The requirement has a place to be expressed, and the doctrine for it is
  already written.** A plugin module's `Register` resolves `*db.Pool` from the
  container by the name `core.db`, and `Registry.Bootstrap` runs registration
  BEFORE migrations — so a module that wants to refuse a cluster it cannot use
  can do it before any ledger is touched. searchpg's `Register` states the house
  position in its own documentation: if what it needs is missing, startup STOPS,
  and skipping quietly was not chosen.
- **The trigger keeps its meaning.** ADR 0015's reopening clause survives as a
  test of REQUIREMENT rather than of usage, which is what makes it possible for
  `pg_trgm` — a genuine candidate with a measured case — to still fire it later.
- **Recovery from the bad case is the same knob that caused it.** Plugins are
  selected at runtime from the `PLUGINS` list; a name that is not there is never
  constructed, so its module is never registered and its ledger is never read.
  An operator who switches the plugin on against a cluster without the extension
  takes the name back out and boots.

**Negative, and accepted**

- **ADR 0015's evidence clause goes false the day this module ships a
  migration.** The extensions row is evidenced by `grep -r "CREATE EXTENSION"`
  returning nothing; measured today it returns hits in four files, every one of
  them prose, and a real statement in a plugin's migration would be the FIRST
  that is not. The row's VALUE stays true while its EVIDENCE stops being — the
  clause needs to become a grep that excludes the opt-in plugins, and this record
  does not write it, because it edits no other file. **Whoever lands the module
  owes that edit in the same change.**
- **Two passages of docs/gaps.md now disagree with a decision record.** They are
  named in the context above rather than left to be discovered. Until they are
  edited, the repository holds the exact defect class it has been bitten by all
  week: a document asserting something the decisions stopped saying. This one is
  created knowingly, which is better than discovering it, and worse than not
  having it.
- **The gate cost is paid on the landing commit, not later, and this record does
  not pay it.** Whichever schema mechanism the module chooses, the integration
  lane runs every migration directory it can see up, down and up on
  `postgres:16-alpine`. Somebody has to choose between a different image constant
  in one file, a written exemption, and no migrations directory at all.
- **A failing extension migration is loud but STATEFUL.** golang-migrate marks
  the version dirty in its own committed transaction BEFORE running the body, so
  a body that fails leaves the plugin's ledger dirty — and gobit ships no force:
  the migrate surface in internal/app/migrate.go offers status and down only.
  Removing the plugin from `PLUGINS` restores the boot, but RETRYING after the
  operator installs the extension needs a command that does not exist. This is
  generic behaviour for any environmentally failing migration rather than
  something pgvector invents; this decision is simply the first to make such a
  migration likely.
- **Search becomes two plugins with two indexes over one catalog.** Nothing here
  blends their rankings, and a shop that wants both runs both reindex paths and
  reconciles the two answers itself.
- **A capability that half the audience wants is gated behind a cluster
  requirement its neighbours do not have.** That is the price of not charging the
  neighbours, and it is the right way round — but it is a price.

## What this deliberately does NOT do

- **It does not build semantic search, an embedder, or a vector index.** It
  decides where such a thing would live and what it may and may not add to the
  contract.
- **It does not pick the schema mechanism.** Three shapes were measured and each
  is implementable: a migrations directory with the image constant changed, a
  migrations directory with a written exemption, or no migrations directory at
  all with the runtime-DDL pattern core/link already uses. What binds all three
  is that the gate must still be able to SEE the module — a walk that silently
  stops finding it is worse than any of them. ADR 0032 deferred its own schema
  mechanism the same way and for the same reason, and it is worth following that
  record all the way: the deferral there is already CLOSED, by an amendment
  carrying this record's own date, which picks the `BEFORE DELETE` trigger and
  reports the measurement that killed `REVOKE DELETE`. Closed in the record
  itself, by the implementer, with a measurement — that is the shape this
  deferral is expected to take too, not an open question left standing.
- **It does not decide whether the module refuses to start.** ADR 0015's
  decision 4 already settles the class this belongs to, and re-litigating it here
  would be inventing a policy the contract has had since it was written.
- **It does not license a second extension by the same argument.** What is
  settled is that an extension arriving with an opt-in module does not by itself
  grow the contract — not that any extension may arrive that way. A statement in
  a core schema or a commerce module is decision 3's boundary and reopens
  ADR 0015 on the spot.
- **It does not take `pg_trgm`.** That is ADR 0015's standing candidate with its
  own measured trade, it is required rather than opt-in, and it is a different
  decision that still has nobody's date on it.
- **It does not change what searchpg requires.** The lexical plugin keeps running
  on a cluster with zero extensions, which is the property that made rejecting
  the fold-it-in option necessary.

## Related

- [ADR 0015](0015-postgresql-cluster-contract.md) — the cluster contract this
  does not reopen, the trigger it reads, and the probe whose full-text lane is
  the precedent for a clause a single plugin consumes.
- [ADR 0032](0032-an-issued-invoice-refuses-erasure-in-the-schema.md) — the last
  record to re-read the extensions row, over `plpgsql`, and the model for leaving
  a schema mechanism to whoever writes the migration and then AMENDING itself
  with their answer — its sub-question was closed on this record's own date.
- [ADR 0018](0018-web-push-is-a-device-registry-not-a-channel.md) — the measured
  case for a new capability becoming its own module rather than a provider slot.
