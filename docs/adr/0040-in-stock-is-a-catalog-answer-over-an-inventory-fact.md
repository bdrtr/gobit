# ADR 0040 — "In stock" is a CATALOG answer computed over an INVENTORY fact

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

Gap A17 asks what "in stock" means for a PRODUCT, and records that nowhere in
this repository is it defined. A storefront needs it for a badge and for a
filter, and every answer that is not written down is one somebody re-invents per
endpoint.

## Decision

**A VARIANT is in stock when any one of three things is true:**

1. `manage_inventory` is false — the merchant is not counting this variant, so it
   is always sellable;
2. `allow_backorder` is true — it may be sold past zero;
3. its available quantity is greater than zero.

**A PRODUCT is in stock when AT LEAST ONE of its variants is.** A product with no
variants is not in stock; there is nothing to sell.

**A variant with `manage_inventory` true and NO linked inventory item is NOT in
stock.** The link is optional (`LinkVariantInventory`), and a variant that says it
is counted while nothing counts it has no evidence of stock. Answering "in stock"
there would be a guess in the direction that sells something the shop may not
have.

**Region is not part of the answer in v1**, and this is a scope decision rather
than an oversight: `available_quantity` is inventory's sellable total across ALL
locations, and making the answer regional means deciding which locations serve
which region — a question nothing in this repository asks yet.

## Where the answer lives, which was measured rather than assumed

The obvious split — inventory answers for the variant, the catalog aggregates to
the product — **cannot be implemented**, and the reason is in the schema:

| input | table | module |
|---|---|---|
| `manage_inventory` | `product_variant` | product |
| `allow_backorder` | `product_variant` | product |
| available quantity | `inventory_levels` via `inventory_items` | inventory |

`inventory_items` carries `id, sku, title, description, requires_shipping` and the
timestamps. It has NEITHER flag, and under Principle 2.1 it may not read
product's models to get them. **Inventory cannot answer the variant question at
all.**

The catalog can, and already has everything it needs. `ListStoreProducts`
resolves `LinkVariantInventory` over the variant ids and gathers the linked
`inventory_item` record — including inventory's published `available_quantity`
field — through the Query layer's batch provider calls (ADR 0004). Both flags are
its own columns. The three inputs meet in exactly one place, and it is
`StoreVariant`.

**So: the variant answer AND the product aggregation are both the catalog's, and
inventory keeps what it owns — the quantity.**

## The price of that, stated rather than hidden

`StoreVariant`'s own godoc says the enriched records are carried "exactly as they
came from the Query layer (as loosely typed records)", and that "not regaining
type safety here is deliberate; interpreting the fields would mean copying the
pricing/inventory schema into this module (the accepted price of ADR 0004)".

Computing this answer breaks that sentence, narrowly: the catalog has to read ONE
field out of inventory's record and know it is a number. That is a real cost and
it is accepted, bounded as follows.

- It is a FIELD NAME, not a schema. `available_quantity` is not an inventory table
  column; it is a name inventory PUBLISHES for the Query layer, declared as
  `service.FieldAvailableQuantity` beside the provider that serves it. A published
  vocabulary is the thing ADR 0004 exists to let modules share.
- The catalog cannot import that constant — Principle 2.1 — so the name lives in
  product as a string, which is a cross-module contract held by two literals that
  no compiler compares. **That pairing must be audited**, the way this repository
  audits its other name-shaped contracts. ~~It is not audited yet; see below.~~
  **It is audited as of 2026-09-08**, in `internal/arch/provider_fields_test.go`;
  see below.
- Nothing else about inventory's shape is read, and this decision does not license
  a second field.

## Rejected alternatives

**Give `inventory_items` the two flags.** It would let inventory answer alone, and
it is the wrong direction: the flags are merchandising decisions about a variant,
they are edited on the product form, and duplicating them would create two
records that can disagree about one variant with nothing to reconcile them.

**Have inventory expose an `in_stock` boolean.** Same obstacle — it cannot see the
flags — and it would put a catalog concept in inventory's vocabulary.

**Compute it in the storefront handler.** It would need the same field read, and
it would put the definition in a place no other caller can reach. A definition
that lives in one endpoint is the state A17 was filed against.

**Store it as a column on the variant.** It is derived from another module's
mutable number; a stored copy is stale the moment stock moves, and keeping it
fresh means the catalog subscribing to inventory events for a value it can
compute on read.

## Consequences

**Positive**

- **One definition, in one place**, that a badge, a filter and a feed can all
  call.
- **Inventory keeps its boundary.** It publishes a quantity, which it owns, and is
  not asked to know what a merchant meant by a checkbox on a product form.
- **The answer cannot silently become regional.** It is defined over the sellable
  total, and a regional answer would be a different decision with its own record.

**Negative, and accepted**

- **The catalog now knows one inventory field name**, and the two literals that
  must agree are in different modules with nothing comparing them. This is the
  first such pairing this decision creates and it needs an audit, in the shape of
  the ones that already hold `emailNormalizers` and the describe loops honest.
  **That audit exists as of 2026-09-08 and it compares the pairing in both
  directions**, so the sentence now costs a test failure rather than a screen.
- **A variant with no inventory link reads as out of stock**, which will surprise
  a merchant who created a variant and has not linked it yet. The alternative
  surprises a shopper who buys something that does not exist, and that is the
  worse of the two.
- **It is computed per read.** The inputs are already fetched for the storefront
  listing, so the cost is arithmetic rather than a query — but a caller that wants
  only the flag still pays for the whole enrichment.

  That holds for the BADGE. For the FILTER the build measured a second cost this
  bullet does not cover: because the answer exists only after enrichment, and
  enrichment happens after the page is cut, a filtered listing walks up to five
  hundred catalog rows for one page and gives up its counter. See "What this
  deliberately does not do".

## What this deliberately does NOT do

- ~~**It does not build the filter or the badge.**~~ **Both built 2026-09-08.**
  The badge is `in_stock` on every storefront product and on every storefront
  variant, computed in `service.toStoreProducts` on the one path every storefront
  body takes, and written unconditionally rather than behind a parameter: the
  inputs are fetched for the listing anyway, so the answer is arithmetic over
  records the request already reads. The filter is `in_stock=true|false` (REST)
  / `inStock` (GraphQL); leaving it out filters nothing, and both directions are
  answered because both are somebody's question.

  **The build found a cost this record did not anticipate, and it is about the
  PAGE rather than the answer.** "It is computed per read" is true of the badge
  and misleading about the filter: the enrichment happens AFTER `LIMIT` has cut
  the page, so a filter applied to what came back would fill the page short —
  and with a sparse match it would produce an EMPTY page in the middle of the
  catalog, which a client cannot tell from the end of it. This module already
  refuses that shape twice in writing (repository/saleschannel.go put the sales
  channel filter into the database for it; the variant provider rejects a
  combination rather than "opening a surface that paginates wrongly"), so the
  listing SCANS instead: it walks the catalog in the listing order in chunks of
  a hundred, enriches each chunk with the one batch Graph call it would have
  made anyway, and stops at a full page or at five hundred rows examined. Three
  consequences are carried and none of them is free:

  - the page can come back SHORT with a cursor, and `next_cursor` being ABSENT
    stays the only end-of-catalog signal;
  - an OFFSET is refused beside it, because an offset counts rows the database
    returned rather than matches;
  - the listing is NOT counted, and an explicit `with_count=true` beside it is
    refused rather than answered with a missing field — counting the matches
    means enriching the whole catalog, which is what the budget exists to
    prevent.
- ~~**It does not add the name audit** that the field-name pairing needs. Until
  it exists, an inventory rename would silently make every product read as out
  of stock — a failure with no error, which is the class this repository has
  been bitten by three times this week.~~ **Built 2026-09-08**, in
  `internal/arch/provider_fields_test.go`, beside the one that has held the
  admin panel's field names since ADR 0030. It has the same two halves: the
  catalog's dependency is DECLARED (`catalogReadForeignFields`, one entry) and
  checked against what inventory still publishes, and the catalog's own source
  is READ so that a dependency nobody declared fails there.

  **The second half was too narrow when first written, and it was widened
  2026-09-08 after a mutation walked through it.** It collected constants by the
  `foreign<Name>` prefix, so it enforced this record's "does not license a
  second field" only against a developer who followed a naming convention
  nothing checked: `recordInt(inventory, "reserved_quantity")` written inline
  read a second inventory field with `go build` clean and every test in the
  repository green. `TestEveryForeignFieldTheCatalogReadsIsNamedByAConstant` now
  audits the READ SITES instead of the declarations — it refuses a field name
  that is not one of those constants, whether it is spelled as a literal or as a
  constant named something else.

  What the pair holds, stated as narrowly as it is true: a foreign field name
  cannot reach a loose record through either of the two shapes the catalog reads
  them by — a call to a reader taking exactly `(query.Record, string)`, and an
  index into a `query.Record` **parameter** — without being declared in the
  `foreign<Name>` block and registered in one of the two maps. What it does NOT
  hold is a read through a local or range variable (the catalog has one such
  loop, and its keys are the catalog's own aliases rather than another module's
  vocabulary) and a name arriving by reflection or by a JSON round trip into a
  tagged struct. Neither shape exists in this module today, and the audit will
  not notice the day one does.

  **The audit also had to register a hole it cannot close**, and the hole is not
  inventory's. ADR 0041's price filter, built in the same round, reads six names
  off pricing's price sub-records — and pricing declares them as UNEXPORTED
  constants, so there is no published vocabulary to compare them against.
  Writing them into the checked map would have produced a green assertion about
  a name nothing publishes, which is worse than no assertion; they are listed in
  `catalogUnboundForeignFields` instead, with a test that fails the day pricing
  exports them and the entry has to move. Until that day a rename inside pricing
  silently empties a price-filtered catalog, and the fix is one word per
  constant in a file this audit cannot reach on its own.
- **It does not decide reservations.** Whether a reserved unit is "available" is
  inventory's own definition of `available_quantity`, and this decision takes that
  number as given rather than reopening it.

## Related

- [ADR 0004](0004-query-veri-erisimi.md) — the Query layer this answer is
  assembled through, and the boundary this decision narrowly crosses.
- [ADR 0039](0039-an-option-value-is-matched-by-a-folded-form.md) — the other
  storefront-facing definition decided in the same round.
