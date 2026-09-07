# ADR 0050 — gobit stores ONE language, and the second language is the embedder's — which gobit does not pretend it makes easy

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A11 asks where the second-language product title lives when one shop sells
the same catalog in two languages, and offers four candidates: a sibling column
per language, a side table inside each module that owns the text, one
translation module keyed by (entity, id, field, locale), or gobit storing one
language and the embedder supplying the second.

**The measurement reorders the question, because the storefront has NO notion of
a shopper's language at all.** `grep -rniE 'accept[-_]?language' --include='*.go'
.` returns zero hits. There is no locale query parameter and no locale path
segment. `core/http.Principal` — the verified caller's identity, in
`core/http/auth.go` — is a closed struct of four fields: ID, Kind, Scopes and
SalesChannelIDs. Nothing on a request says which language it wants.

So the storage question is the second one. **A translation table nothing can key
into is a table**, and the TWO candidates that put the locale in a key — the
side table per owning module and the one translation module — assume a reader
that does not exist. The other two do not. A sibling column and the answer taken
here both hand every language back in the same response and let the client
choose, which is precisely why one of them is the only candidate this record can
decide today.

**One sentence in gaps.md is wrong, and correcting it matters because it is the
sentence that made the locale look like a solved word.** gaps.md says every
occurrence of "locale" in the tree is the PostgreSQL cluster's own collation (ADR
0015). Outside one plugin that is true, and the count has to exclude the plugin
to say so: of the 77 mentions across non-test Go files, 45 are INSIDE
`plugins/webpush` — the very plugin the sentence excepts — and every one of the
32 that remain is the cluster's fold, 14 of them in `core/db/casefold.go`, 16
under `internal/` and 2 in `plugins/searchpg/plugin.go`. Inside
`plugins/webpush/api.go` it is not: the subscription request body carries a
`locale` field, the plugin validates it as a language tag, and the migration
stores it on the subscription row. That locale belongs to a DEVICE and picks a
template file; the plugin's own godoc says as much. It never touches a catalog
read, so it does not answer A11 — but it is precedent that a plugin may carry a
locale of its own, and gaps.md should not be read as saying otherwise.

### What an embedder can and cannot do, measured rather than assumed

The preliminary form of this decision justified itself with a sentence that
measurement does not support: that gobit does not prevent an embedder from adding
translations in their own module, because that is what the read layer is for.
**The reason in it is FALSE, and the claim is true only in a way the sentence did
not mean.** Three of the four seams it assumes are closed. The fourth — the third
below — is open, and what opens it is not the read layer but a route collision
gobit never checks.

**One — there is no middleware seam.** The whole embedder surface is the root
package: New and the four `App` methods in `gobit.go` — Version, Add, Use and
Main — and its godoc says a program imports that package and nothing else from
the framework. What those methods contribute lands in `internal/app.Options`,
which is NOT a second surface: its own package godoc says none of it is a
contract and that an outside program names the facade, never anything under
`internal/`. Options carries three fields — Version, Modules and Plugins — and
its godoc says everything in it is ADDITIVE, with deliberately no way to remove
one. The four methods reach nothing else.
`grep -rn "Middlewares:" --include='*.go' internal/ | grep -v _test` returns ONE
hit, in `internal/app/setup.go`, where the router's middleware list is filled
from gobit's own guard stack. An embedder cannot add a middleware to THAT stack,
so it cannot put a locale on a request that a handler of gobit's own will read.

**Two — there is no plugin seam either.** `core/plugin.Host` exposes fourteen
methods: Container, Logger, Setting, AddModule, AddRoutes, RegisterJob, Jobs,
five provider registrars, RegisterCallback and Subscribe. **None of them installs
middleware.**

**Three — the collision refusal covers PLUGINS, and a module walks around it.**
`core/plugin.Registry` mounts plugin routes by collecting the patterns already
registered and refusing a collision with `plugin_route_conflict` — "tried to bind
a path that is already registered". It is a startup refusal and not a warning,
and it guards one of the embedder's two route seams. The other one — the module
of its own that two paragraphs below this record says it gets — is guarded by
nothing.
`core/module.Module` requires a `Routes(chi.Router)` method; the mounting loop in
`core/module/registry.go` calls it for every registered module and compares no
patterns; and `internal/app/app.go` adds the embedding program's own modules
LAST, onto the same chi router gobit's modules bound. Measured against the chi
version go.mod pins, v5.3.2: binding `GET /store/v1/products` a second time is
not an error, not a panic and not a log line — the SECOND handler answers, and
binding it through `With` attaches a request-scoped middleware to that route
besides. Nothing in the tree notices; a search for the conflict code and the
pattern collector finds only `core/plugin/plugin.go`, and no arch test compares
module routes to each other. **So an embedder CAN re-serve
`GET /store/v1/products` with a translating handler — by REPLACING gobit's, in
silence, on an ordering that is a comment in `internal/app/app.go` rather than a
promise.**

**Four — there is no identity seam.** `Principal.SalesChannelIDs` is the last of
its four fields and an outside package cannot add a fifth.

**What an embedder CAN do is build a PARALLEL storefront**, and that is a real
capability rather than a consolation. It gets its own module, its own migration
ledger — `core/db.MigrationsTable` gives every owner a `<owner>_schema_migrations`
table of its own, so an outside module's versions never collide with gobit's —
its own routes, and a way to read gobit's catalog: `internal/modules/product/module.go`
registers three Query providers in the container under the names "product.query",
"variant.query" and "category.query", and `core/query` resolves them by that
name. It is not the same claim as "gobit does not prevent you", and the record
says so rather than softening it.

**And by measurement three it can point that storefront at gobit's own paths —
which is a REPLACEMENT and priced like one.** The embedder's handler answers
instead of gobit's, so it owns the response shape, the paging envelope and the
error bodies gobit's OpenAPI document still describes for that path. The price is
sharpest where the route was registered with a per-route middleware: the Routes
method in `internal/modules/region/api/api.go` binds its admin reads on a router
derived with `corehttp.RequireScope`, and rebinding that same pattern on the bare
router — the router an embedder's module is handed — was measured on chi v5.3.2
to answer with the new handler and NO scope check at all. The router-level
guards from `internal/app/setup.go` survive; the chain attached at the
registration does not. A translating shadow of a storefront read is a real
capability; the same gesture aimed at an admin path silently unbolts its
authorization.

### What one language plus embedder metadata actually delivers

The answer to A11's literal question — where a second-language product TITLE
lives — works today with zero gobit change, and the boundary is two measured
lists rather than a principle.

**It works for four tables.** `internal/modules/product/migrations/000001_product_init.up.sql`
puts a `metadata jsonb NOT NULL DEFAULT '{}'` column on exactly four:
product_collection, product, product_variant and product_image. The storefront
path selects it — `productColumns` in
`internal/modules/product/repository/saleschannel.go` names metadata in its
column list — and the models serialize it: `Metadata map[string]any` with a
`"metadata"` JSON tag on the product, on the variant, and on both the collection
and the image in the taxonomy models. The admin create-product body accepts the
same field. An embedder writes a second-language title into product metadata
through the admin API and the storefront hands it back; the client picks.

**It does not work for the rest of the catalog vocabulary.** product_category,
product_tag, product_option and product_option_value have NO metadata column in
that same migration, and neither do region, currency or country — `grep -c
metadata internal/modules/region/migrations/*.up.sql` returns 0 for all three
migrations. There is no hatch to write a translation into for a category name, a
tag value, an option title or an option value.

**And three read paths never see metadata even where it exists.** The searchpg
plugin's index document in `plugins/searchpg/index.go` carries three weighted
pieces — the title, then the handle, subtitle, tags, variant titles and SKUs, then
the description — and metadata is in none of them. The storefront's own free-text
filter in `internal/modules/product/repository/saleschannel.go` is a single
`title ILIKE` clause. The admin panel's thirteen templates under
`internal/adminui/templates` are English strings compiled into the binary, behind
`internal/`, where no embedder can import them. **So a second language stored in
metadata is readable but not searchable, and the panel stays English.**

### The debt this decision does NOT defer

gobit seeds and serves reference data in a language gobit chose. A live database
returns `249|41` for the country and currency row counts, and
`internal/modules/region/api/dto.go` documents the currency field as the
currency's English name in ISO and the country field as the country's English
short name in ISO. **Both sets leave the installation whole on the ADMIN
surface, and not on the storefront.** `internal/modules/region/api/api.go` binds `/admin/v1/countries`
and `/admin/v1/currencies` with Get and with nothing else; the storefront's
`/store/v1/regions` carries one currency per region and only the countries
attached to that region, so on the same live database — zero regions, zero
countries attached to one — it emits none of the 290 today. region carries no
metadata column, and there is no write path for either name: the only UPDATEs in
`internal/modules/region/queries/country.sql` set or clear a country's region,
and `internal/modules/region/queries/currency.sql` has no UPDATE at all.

**So 290 strings are English, chosen by gobit, unreachable by metadata and
unreachable by API.** Shadowing reaches the response and not the row: an embedder
that re-serves the two admin listings changes what one reader is told and leaves
every seeded name, and every other reader of it, in English. They are gobit's to
fix under EVERY candidate, including the one taken here. Without this paragraph
the record would claim a neutrality gobit does not have.

## Decision

**gobit stores ONE language. The second language is the embedder's, and gobit
does not pretend it makes that easy.**

Concretely: gobit adds no locale column, no translation table and no translation
module. The catalog rows hold one string per field. An embedder that needs a
second language today has two honest routes — run one installation per language,
or carry the second language in the `metadata` jsonb of the four tables that have
one and pick client-side — and both of them stop where the two lists above say
they stop.

**The reason for the deferral is a distinction nothing in this tree can make
yet.** ADR 0025 reopens this repository's boundary decisions when the first two
or three customer projects show the surface is missing something they genuinely
cannot express, and there are no customer projects. What they would have to show
is which of two demand shapes is real: a per-DEPLOYMENT language — two
installations, one language each, which already works — or a per-REQUEST language
— one installation, one catalog, the shopper picks. **Only the second needs
storage.** Building storage before knowing which is building for a shape that may
never arrive.

**Two more facts make the timing wrong rather than merely unproven.** No locale
reaches a handler gobit owns, per the measurements above — the one open seam
replaces gobit's handler rather than informing it — and gobit is the only party
that can build the arrival: the request half is the un-deferrable one. And
ADR 0044 is Accepted and unbuilt — `grep -rn '/store/v1/sales-channels'
--include='*.go' .` returns zero hits — and it rewrites the catalog read paths a
locale would ride. Deciding locale delivery into routes already scheduled to move
decides it twice.

**When it reopens, the candidate to reopen with is A11's (3): one translation
module keyed by (entity, id, field, locale).** This record says so now, because
the reason is measurable today and will not be clearer later:

- It is the only candidate that covers the tables with no hatch. Keyed by entity
  and id it needs no cooperation from the owning table, so product_category,
  product_tag, product_option, product_option_value, country and currency are in
  scope on day one — which is exactly where the decision taken here is
  unimplementable.
- It decides the language axis ONCE. The axis crosses product, region,
  fulfillment, search, the panel and the invoice, so the question is how many
  times it gets re-decided: a sibling column per language re-decides it once per
  translatable column per language, permanently, under ADR 0025's rule that a
  field entering a contract can never be taken back; a side table per owning
  module re-decides it once per module, and `core/db.MigrationsTable` guarantees
  those tables cannot be shared.
- It forces no new published wire field. `core/query.GraphSpec` already carries
  `Filters map[string]any`, so a locale reaches a provider through a filter
  `core/query` accepts today.

**The trigger is falsifiable, and it is any one of these.** A customer project
that needs a per-request language rather than a per-deployment one. Evidence that
translating the hatchless set is required rather than optional — that a shop
cannot ship with English category names, option titles, tag values and 249
English country names. A decision to give the embedder a middleware or
request-context seam, which would make the answer taken here honest and make (3)
cheap at the same time. Or proof that the metadata path does not work end to end,
which would remove the only thing this decision currently delivers.

## Rejected alternatives

**(1) A sibling column per language — `title_tr` beside `title`.** It buys the
cheapest possible read: no join, no filter, no read-layer hop, and the storefront
query changes by one column name. It is killed by ADR 0025's own rule that a
field which enters a contract can never be taken out again. Every language is a
migration on every translatable table AND a new field on every published
response, forever, in every installation — including the single-language shops
that are the only kind gobit has. It also answers only the tables it is applied
to, so the hatchless set needs the same decision a second time.

**(2) A side table inside each module that owns the text.** It buys the cleanest
ownership story there is: Principle 2.3 puts the text with its owner, each
module's translations live in its own schema, and no read crosses a boundary. It
is killed by multiplication. The language axis crosses product, region,
fulfillment, search, the panel and the invoice, and `core/db.MigrationsTable`
gives every owner its own migration ledger — so these are not one table applied
six times but six independent tables, six migration sequences, six sets of
queries and six chances to key them differently. The identical decision gets
re-made per module, and nothing compares the six.

**(3) One translation module keyed by (entity, id, field, locale).** It buys
everything the previous paragraph says it buys, and it is the one that should win
when the language question is answered — that is why it appears in the Decision
above rather than only here. What kills it TODAY is not its shape but its key: the
locale in that key has no source. No request that reaches a gobit handler carries
one and no principal holds one; the only party that could produce one is an
embedder that has REPLACED the handler, which is a key gobit would be filling by
proxy. Building the table now means committing a module,
a migration ledger, a container registration and a describe loop to a column whose
values nothing can produce, and then rebuilding the read paths again when ADR 0044
moves them.

**Give the embedder a middleware seam now, so the sentence above becomes true for
the reason it gave.**
It buys the half only gobit can build, and it is the honest response to the
measurements above — one hook and an embedder could WRAP the catalog handlers
with whatever translation strategy it likes instead of replacing them. It is
killed by what a seam is. `internal/app.Options` is not where such a hook could
land: its own package godoc says none of it is a contract and that an outside
program names the root `gobit.App` and never anything under `internal/`. The hook
therefore arrives on that published facade, and its signature, its ordering and
its interaction with the guard stack become promises kept forever, decided with
zero consumers to say what a middleware would need to do.
And it decides more than A11 asks: a general request-wrapping seam is the answer
to a dozen questions nobody has asked, arrived at through the one that happens to
be filed.

**Say nothing and leave A11 open.** It buys not being wrong. It is killed by what
the measurement found: an embedder reading gaps.md today would conclude that
translations are their own problem and solvable in their own module, and that
conclusion is false in three measured ways and true in a fourth that nobody wrote
down — replacement rather than extension, paid for with the whole handler and
with whatever middleware was attached where the handler was first bound. A gap
left open keeps a wrong belief alive; this record's main product is the
correction, and half of that correction is telling an embedder what the one open
route actually costs.

## Consequences

**Positive**

- **The embedder surface is described accurately for the first time.** The root
  facade's five entry points in `gobit.go` — New, Version, Add, Use and Main —
  over the three fields of the unpublished `internal/app.Options`, fourteen
  methods on `core/plugin.Host`, a plugin route registry that refuses a collision
  beside a module mounting loop that compares nothing, and a closed
  `core/http.Principal`: together they say precisely what an embedder can wrap,
  what it can only replace, and what it cannot touch, and they will be the
  reference for the next question of this shape too.
- **Nothing is published for a consumer that does not exist.** No column, no
  table, no module, no wire field and no extension point is committed to a guess,
  and ADR 0025's warning that a published name cannot be taken back stays
  unspent.
- **The one thing that DOES work is written down.** Four tables carry metadata,
  it is selected on the storefront path and serialized on the way out; an
  embedder needing a second product title has a route today and does not have to
  discover it by reading the repository.
- **The reopening is specified rather than promised.** The candidate, the reason
  it wins, and four events that would fire it are named here, so the next round
  starts from an argument instead of from the gap's candidate list again.

**Negative, and accepted**

- **gobit is a single-language commerce library, and this record says so
  plainly.** A shop selling one catalog in two languages to one audience cannot
  use gobit for that today. Not "it is hard" — it is not built, and the request
  side is not built either.
- **290 strings are English by gobit's choice, and nothing an embedder does
  changes the rows.** 249 country names and 41 currency names sit in gobit's own
  seed with no metadata column and no update path, and they leave whole on
  `/admin/v1/countries` and `/admin/v1/currencies`, both bound with Get only. An
  embedder can re-serve those two listings and translate one response; the seeded
  names and every other reader of them stay English. This decision does not fix
  them and does not pretend the embedder could.
- **The one open seam is open by omission, and this record does not close it.** A
  module the embedder adds can bind a pattern gobit already bound, and chi v5.3.2
  answers with the last registration — no error, no log line, no arch test, and no
  per-route middleware carried over from the binding it displaced. It is the route
  by which an embedder can put a locale on gobit's own storefront path, and it is
  the same route by which an admin endpoint can lose its scope check by accident.
  Whether gobit should refuse a module collision the way it refuses a plugin's is
  a question this record measures and leaves standing, because refusing it would
  take away the capability along with the hazard and there is no consumer yet to
  say which one it wanted.
- **The metadata route carries semantics in a bag with no schema.** A key like a
  language-suffixed title is a convention between an embedder's admin call and an
  embedder's client, held by nothing — no validation, no OpenAPI shape, no arch
  test. It works, and it is exactly the kind of contract this repository
  otherwise audits.
- **Even where metadata works, search and the panel do not follow.** The searchpg
  index reads title, handle, subtitle, tags, variant titles, SKUs and
  description; the storefront's own free-text filter reads title alone; the
  thirteen panel templates are English behind `internal/`. A shop with a
  metadata-carried second language has a catalog it can display and cannot
  search.
- **The gap stays open with a heavier bill than when it was filed.** Whoever
  answers it later pays for the request half AND the storage half AND whatever
  ADR 0044 has moved by then, instead of the storage half alone.

## What this deliberately does NOT do

- **It does not decide how a request asks for a language.** No header, no
  parameter, no principal field, no middleware hook. That is the half only gobit
  can build, and this record names it as the un-deferrable one without building
  it.
- **It does not forbid the metadata route, and does not bless it as a
  contract either.** It is described because it is what exists, not because it
  was designed for this.
- **It does not add a metadata column to the tables that lack one.** Doing that
  would look like progress and would be the sibling-column decision arriving one
  jsonb at a time, without a record.
- **It does not touch the webpush plugin's locale.** That locale is a device
  preference selecting a template, and the reasoning is the plugin's own: the
  `loadTemplates` godoc in `plugins/webpush/templates.go` says one customer's
  phone and their work machine can be set to two different languages, so the
  locale belongs to the DEVICE and the device chooses which file renders. It
  stays what the plugin decided.
- **It does not translate the 249 country names and the 41 currency names.** It
  records that they are gobit's debt under every candidate, so that the next
  round cannot treat them as the embedder's.
- **It does not close gap A11.** The question stands; what this record fixes is
  the false premise it was about to be closed on.

## Related

- [ADR 0025](0025-gobit-is-a-library-not-a-template.md) — the library boundary
  this answer follows, the rule that a published field can never be taken back,
  and the reopening trigger the deferral is measured against.
- [ADR 0044](0044-the-sales-channel-moves-into-the-catalog-path.md) — Accepted
  and unbuilt, and it rewrites the catalog read paths a locale would ride.
- [ADR 0004](0004-query-veri-erisimi.md) — the read layer an embedder's parallel
  storefront reads gobit's catalog through, and the filter map a locale would
  travel in.
- [ADR 0015](0015-postgresql-cluster-contract.md) — the cluster collation that
  every other "locale" in this tree means.
- [ADR 0018](0018-web-push-is-a-device-registry-not-a-channel.md) — why the row
  that carries the one reader's locale in the tree is a plugin's own table and
  not the core's. The record never mentions a locale itself; the
  device-preference reasoning is in the `loadTemplates` godoc of
  `plugins/webpush/templates.go`, and neither of them answers this question.
- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the published
  package list that makes a locale filter type on `core/query` a promise rather
  than a parameter. It decides which PACKAGES are published and says nothing
  about a middleware seam; what makes a hook a promise is that it would land on
  the root facade, which this record does not discuss.
