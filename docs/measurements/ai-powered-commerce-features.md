# AI-powered commerce features — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The brief: natural-language search ("winter, under 500 TL, dark colour"),
automatic review summaries and Q&A, attribute extraction from product photos, a
chat assistant with tool-use that can read an order and start a return, and
price/stock forecast SUGGESTIONS an operator applies rather than the system.

Five areas measured in parallel against the tree. One is genuinely close; the
rest are blocked by something more basic than the AI.

### Natural-language search: the filters it would translate INTO are half built

The layer the brief describes turns a sentence into filters. Two claims made
here on 2026-09-04 were overtaken by B2 and B3 the next day and are struck
rather than deleted, because the shape of the miss is worth keeping:

~~**The entire structured filter surface of the storefront is one collection id
plus free text** — `collection_id`, `q`, `limit`, `offset`, `after`,
`with_count`, on REST and GraphQL alike.~~ **Measured again 2026-09-05:**
`category_id` and `tag_id` are on both surfaces, and the test that pins REST and
GraphQL to each other holds them together.

~~There is also **no storefront endpoint that enumerates collections, categories
or tags**, so an NL layer has no public vocabulary to resolve a word to an
id.~~ **B3 built all three listings**, so the word→id half now has a public
vocabulary.

What is still missing is narrower and no longer one thing — the split above is
the measurement. "Under 500 TL" has nowhere to land and will not until **A16**
is answered; "in stock" until **A17** is; "dark colour" needs both the
option-value filter and a vocabulary endpoint for option values, neither of
which exists; "cheapest first" needs a sort parameter no surface accepts, and a
cursor that would silently page through the wrong order if one were added
carelessly. "Winter" still has no first-class home — season is not a column —
but of its natural carriers, collection, category and tag, ALL THREE are
filterable now rather than one.

Colour and size ARE modelled (`product_option`, `product_option_value`, and the
variant join) and neither the listing nor the search index reads them.

Two findings worth carrying:

- **There are two independent search paths, not one.** The product module's own
  `?q=` is `title ILIKE '%…%'` — a leading wildcard, no index, a full scan that
  ADR 0015 measured at 58.9 ms on the 52k fixture. The searchpg plugin is a
  separate endpoint with a real weighted `tsvector` and a GIN index, its own
  module and its own migration ledger. They share no contract, and searchpg
  accepts no filters at all.
- **Search is not a provider slot.** Payment, fulfillment, notification and file
  have registries; search does not. Swapping the engine means replacing a
  package rather than registering an implementation.

The honest order was: the filters first, then a vocabulary endpoint, then the
layer that maps a sentence onto them. Two of those three steps are part-done,
and the ordering claim survives its own progress — an NL layer built before the
filters would be a translator with no target language, and the target language
is currently four words short: price, in-stock, option value, sort. Two of the
four are sentences somebody has to write (A16, A17), not code somebody has to
type, and that is the only reason the order still holds.

### Review summaries and Q&A: the data exists now, the two hooks do not

**The review module landed on 2026-09-06 (B4), so "there are no reviews" is no
longer the blocker.** What blocks C11 now is narrower and it is named in the
module's own package doc: it publishes no read-layer provider and no event, and
both absences are deliberate. A provider nothing resolves fails the consumer
audit; a topic nothing subscribes to fails the topic gate. C11 is the first
reader of both, so both land in the package that brings the reader — which is
this repository's standing rule for a capability, applied rather than waived.

Read the two hooks separately, because they are needed for different halves of
the feature. Without a PROVIDER a summariser cannot read a review at all: it
lives in another module and ADR 0001 forbids the import, so the read layer is
the only door. Without an EVENT a stored summary has nothing to invalidate it,
and the section below on `product.metadata` says why the summary has to be
stored somewhere of its own rather than on the product.

**One number a summariser should know before it stores anything.** The review
module already computes the count and the average on READ, and that was measured
rather than preferred: against PostgreSQL 16 on a rig of 505,000 reviews over
20,001 products, with the module's partial index on the approved rows, the
aggregate costs 0.17-0.21 ms for a product with 19 approved reviews, 1.3-2.0 ms
at 5,000 and 9.3 ms at 50,000, against 33-38 ms with no index at all — where it
is a full parallel sequential scan whose cost does not depend on the product's
own review count. The first page of twenty reviews is 0.03-0.04 ms at every one
of those sizes, because the LIMIT stops the index scan. The index is 40 MB
against a 348 MB table.

The crossing point is stated instead of hidden: the cost is linear in the ONE
product's approved count, so only a shop with hundreds of thousands of reviews
on a single product is buying anything with a stored counter — and it would buy
those milliseconds by owing a correctness obligation to every path that writes a
review. That is the same trade A16 records against denormalising a price into
the catalog, and it fails for the same reason there: the missing piece is the
invalidation signal. A TEXT summary is the opposite case and this is where the
numbers stop applying — it cannot be recomputed per request at any price, so it
must be stored, and storing it is precisely what needs the event.

The measurement adds two more details that decide where such a summary could
live.

- **`product.metadata` is a whole-value REPLACE, not a merge.** The update is
  `metadata = COALESCE(@metadata, metadata)` and there is no jsonb `||` anywhere
  in the repository. A summariser writing there would clobber whatever else a
  shop had put in it, and two writers would clobber each other.
- **`product.metadata` is publicly readable on the storefront**, in REST and in
  GraphQL. A summary placed there is published by construction, including its
  intermediate states.

The precedent to copy instead is `searchpg`: a per-product derived row in its
OWN table, with its own migration ledger, no cross-module foreign key, and a
rebuild driven by events. Note the constraint that comes with it — **only four
domain events exist repo-wide** (`product.created`, `product.updated`,
`product.deleted`, `order.placed`), so a summary invalidated by a new review
needs a fifth, which is the review module's to publish. That is still exactly
true after B4 landed — the module publishes none — and it is now assignable
rather than hypothetical: the topic, its first subscriber and the summary table
are one change.

### Attribute extraction from photos: the image cannot be read back

Blocked below the AI, and in a way that is easy to miss.

- **`FileProvider` has only `Upload` and `Delete`.** On any real object-store
  deployment the application cannot read an uploaded image back at all. A vision
  pipeline has no bytes to look at.
- **The file module publishes no events**, so nothing can react to a photo
  arriving.
- ~~**A product image and its upload record are not linked.** `product_image.url`
  is free text, there is no `upload_id`, and a cross-module foreign key is
  forbidden (Principle 2.2). Given an image row there is no way to reach its
  storage key.~~ **Corrected 2026-09-06: the link was built later the same day
  (B15).** `product_image.upload_id` is a nullable opaque text column with a
  non-empty CHECK (product migration `000002_product_image_upload`) — no
  cross-module foreign key, so Principle 2.2 is intact — written on image
  create, carried on the admin image DTO, and readable in reverse through
  `GET /admin/v1/product-images/by-upload/{upload_id}`; the binding is declared
  by PRODUCT as `LinkUploadProductImage`, and `file.interop`'s `UploadJSON`
  resolves the id to the upload record. What is still missing is narrower than
  "not linked": the cross-module `uploadRecord` carries the URL, content type,
  size and checksum but deliberately NOT the storage key, and `FileProvider`
  still has only `Upload` and `Delete` — so a pipeline can now reach the RECORD
  of the photo and still not its key or its bytes, which is the first bullet's
  point.
- **Images are write-once at product create** — no per-image endpoint and no
  `Images` field on the update input, so a pipeline could not write back what it
  found.
- **There is nowhere to put a suggestion.** `product_category_map` is a bare
  `(product_id, category_id)` with no confidence, no source and no pending
  state, and the setter replaces the whole set atomically.

Not one of these is about models or prompts. Four were ordinary plumbing
decisions that would each be worth making on their own merits; the image ↔
upload link was made on 2026-09-05, so three are left.

### The chat assistant: the tools already exist, the caller does not

**This is the area that is genuinely close, and for a reason nobody planned.**

There are **seventeen `*.interop` container surfaces carrying sixty-four
methods** — twelve module ones and the five under `workflows.` — and by written
rule (ADR 0001/0006) every one takes and returns primitives, slices of
primitives, or `json.RawMessage`, with composite schemas documented next to the
method. That is a tool catalogue, built for module isolation and arriving fit
for tool-use by accident. The container can even enumerate the names at runtime.
Re-measured 2026-09-06: the figures here were fifteen and sixty-one, and both
had already moved by the close of the date above, because `file.interop` and
`workflows.fulfilling.interop` were registered later that same day.

What is missing is not the tools but three things around them:

1. **There is no customer to be.** ADR 0008 decided the framework will not build
   customer identity: the storefront key identifies the STORE, not the person.
   A customer-facing assistant has nobody to act as, and "show me my order"
   cannot be authorised. That is a decision rather than a gap — and it means the
   assistant's first honest form is an OPERATOR's assistant inside the panel,
   where identity already exists.
2. **A return cannot be started through any surface.** `order.interop` offers
   `ReturnDetailJSON`, `ReceiveReturn`, `ClaimDetailJSON`, `CompleteClaim`, and
   `workflows.returns.interop` offers `RefundReturn`, `SettleClaim` — every one
   acts on a return that ALREADY EXISTS. The only creation entry point is
   domain-typed and unreachable from outside the module.
3. **Tool schemas cannot be generated.** The container returns names, not method
   metadata, and the OpenAPI document is missing a body schema for exactly the
   endpoints this feature needs. Hand-writing a schema per method is fine for
   ten tools and not for sixty-four.

### Forecast suggestions: there is no history to forecast from

- **Stock is a current-value column, not a ledger.** `inventory_levels`
  overwrites `stocked_quantity` and `reserved_quantity` in place, so the
  database cannot answer "how much stock did this item have last Tuesday" and a
  depletion rate is not derivable at all.
- ~~**Demand history exists and is unreachable.**~~ **HALF CLOSED 2026-09-05
  (B14): it is reachable now, and it is still not aggregated.** `orders` and
  `order_line_items` carry quantity, price and `created_at` — a real time series
  — and the read layer offers the line as its own entity, `order_line_item`,
  next to `order`. It takes `placed_from`/`placed_to`, half-open, matched
  against the ORDER's `placed_at` through a join. The join is a decision and the
  migration argues it: copying `placed_at` onto the line would make the listing
  a single-table range scan, which is the cheaper shape, but it would be a
  SECOND source of truth for one fact and nothing in the schema would keep the
  copy right — a line added to an existing order by an exchange carries the day
  of the exchange, and a report drawn from it would disagree with the order it
  belongs to without anything failing. Migration 000006 adds
  `orders_placed_at_idx` and `order_line_items_variant_idx`, so that "last
  month" costs the month rather than the whole sales history.

  Three things are still missing, and they are three different things:

  - **There is no aggregation surface.** The provider returns RECORDS — no
    grouping, no sums, no ranking — because a GROUP BY behind that interface
    would produce records that are not records of an entity, which is the one
    thing the read layer's contract cannot express. So "which variants sold most
    last month" is still a query somebody writes by hand; what exists is one
    clamped page of the rows it would be computed from. The panel's report
    prints no total at all rather than a sum of whichever rows sorted first, and
    the argument is written in the template and the handler — where the next
    person tempted to add one will read it.
  - **There is no link between a line and a variant**, so no Graph request can
    expand from a sold line to the product it sold. The line carries
    `variant_id` as another module's identifier and does not validate it
    (Principle 2.2); a consumer that wants today's catalog name has to read the
    product entity itself, and what the line holds is the title AS SOLD.
  - **Forecasting itself is still absent.** This is the READ SURFACE a forecast
    needs, not the forecast. The other half of it is the bullet above: stock is
    a current-value column, so the depletion rate has no history to come from,
    and that is B7.
- **Price history exists only by accident.** `ReplacePrices` soft-deletes and
  reinserts, so old rows survive — with regenerated ids, no reader and no index.
  Promoting that into a real record is a decision nobody has taken.
- **The audit log cannot reconstruct a change, by design.** It records the
  REQUEST, not the diff, and says so in its own header.

The SHAPE the brief asks for — the system proposes, a human applies — **already
exists here with an ADR behind it.** `internal/jobs/sagawatch` is a job that
measures, reports and deliberately never acts, and ADR 0017 is the written
argument for why. A forecast job is that pattern again. What it lacks is
anywhere to store a suggestion: pricing's and inventory's tables have no
metadata column between them.

### What the five have in common

Four are blocked by something that is not AI — filters that do not exist,
~~reviews that do not exist~~ **reviews that exist since 2026-09-06 but publish
no event and no read-layer entity**, images that cannot be read back, history
that was never kept. The fifth is blocked by a decision (ADR 0008) rather than
by machinery, and its machinery is unusually ready.

The review item is worth watching as it moves, because it changed CATEGORY
rather than closing: it used to be blocked by missing data, and it is now
blocked by two withheld capabilities that its own first reader is supposed to
bring. That is a smaller blocker and a differently shaped one — nobody has to
design a schema for it, somebody has to write the consumer.

The cheapest real move on this list is therefore not a model call. It is the
storefront filter surface: the NL layer needs it, the panel would use it, and
nothing else on the roadmap has to wait for it.

---
