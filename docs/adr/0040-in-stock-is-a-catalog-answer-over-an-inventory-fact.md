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
  audits its other name-shaped contracts. It is not audited yet; see below.
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
- **A variant with no inventory link reads as out of stock**, which will surprise
  a merchant who created a variant and has not linked it yet. The alternative
  surprises a shopper who buys something that does not exist, and that is the
  worse of the two.
- **It is computed per read.** The inputs are already fetched for the storefront
  listing, so the cost is arithmetic rather than a query — but a caller that wants
  only the flag still pays for the whole enrichment.

## What this deliberately does NOT do

- **It does not build the filter or the badge.** It defines what they would mean.
- **It does not add the name audit** that the field-name pairing needs. Until it
  exists, an inventory rename would silently make every product read as out of
  stock — a failure with no error, which is the class this repository has been
  bitten by three times this week.
- **It does not decide reservations.** Whether a reserved unit is "available" is
  inventory's own definition of `available_quantity`, and this decision takes that
  number as given rather than reopening it.

## Related

- [ADR 0004](0004-query-veri-erisimi.md) — the Query layer this answer is
  assembled through, and the boundary this decision narrowly crosses.
- [ADR 0039](0039-an-option-value-is-matched-by-a-folded-form.md) — the other
  storefront-facing definition decided in the same round.
