# ADR 0399 — Stock on its way has a date the storefront shows

**Summary:** Units a supplier owes a warehouse are an expected supplier receipt, received through the ledger as `supplier_receipt`.
A storefront variant with nothing to sell shows when units are expected on sale again, after the waiting backorders take theirs.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0055](0055-a-location-closes-empty.md), whose close is also refused while the warehouse expects a supplier receipt

Measurement: [measurements/0399](../measurements/0399-stock-on-its-way.md)

## Context

The inventory module knows what is on the shelf and nothing about what is
coming: no inventory migration held an expected quantity or date. A supplier's
delivery was written as a stock count or an adjustment, so the ledger (ADR 0068)
could not tell delivered goods from a correction. A storefront variant with
nothing to sell said only that (ADR 0040, ADR 0093), and since ADR 0392 the units
that do arrive go first to the backordered lines waiting for them, so the badge
could not say whether a shopper waits a week or for ever. The storefront's
inventory record was the provider's default field set, which carried the
warehouse breakdown ADR 0093 keeps from shoppers (D258).

## Decision

The inventory module records units a supplier owes a warehouse as an expected
supplier receipt naming an item, an open warehouse, a positive quantity and the
moment they are expected to be sellable there, and receiving one writes the
counted units through the ledger as `supplier_receipt` in the transaction that
closes it. A storefront variant with nothing to sell at the request's warehouses
carries `restock_expected_at`, the first expected moment at which units are left
for sale there once the receipts before it have filled ADR 0392's waiting claims
oldest first.

## Consequences

- The ledger gains a seventh reason, `supplier_receipt`: positive, from an admin
  request, naming its receipt, one row per receipt in the schema. The movement
  listing publishes every row's `reference`.
- A receipt raises the sellable quantity through the ledger's two writes, so the
  waiting claims are filled first (ADR 0392) and ADR 0215's alert hears the rest
  as any restock.
- Receiving takes the counted quantity, which may differ from the expected one,
  and opens the level at a warehouse that had none. The receipt closes either
  way; units the supplier still owes are a new receipt.
- Receiving a received receipt with the same count, or canceling a canceled one,
  answers it unchanged; any other repeat answers 409
  `inventory_supplier_receipt_not_expected`.
- A changed date is a cancel and a new receipt. A receipt whose moment has
  passed stays expected and leaves the forecast, which then errs late.
- A warehouse expecting a receipt does not close (409
  `inventory_location_not_empty`), and an item expecting one is not deleted (409
  `inventory_item_expects_units`).
- The date is an estimate, not a promise: the checkout does not read it, no
  order line or claim carries it, and a bundle and a variant with something to
  sell carry none. It is computed on read, in one more graph call, only for the
  variants a page shows with nothing to sell.
- Inventory's per-warehouse fields leave its provider's default field set, so
  the record the storefront publishes carries no warehouse breakdown and a
  caller naming no fields no longer computes one (D258).
- `reference` is the embedder's own document number and is declared open
  personal data; gobit keeps no supplier, cost or purchase order.
- The ledger's published description counts seven reasons, three from an admin
  request, and names what each reference points at (D257).

## Rejected

- A purchase order with supplier, price and lines: a second procurement record beside the embedder's, whose number `reference` carries.
- An `incoming` quantity on the level: it carries no date, and the date is the answer.
- A partial receipt that leaves the record open: the remainder is a promise nobody restated.
- Receiving through the count or the adjustment with an id: a reason a caller passes is one a caller gets wrong.
- A 409 on a repeated receive: a client whose response was lost would read it as a cancel.
- `arrival` and `receipt` as the names: the module already calls a customer's return both.
- An expected calendar date: gobit holds no shop time zone.
- A promised date on the order line or the claim: ADR 0048's pre-order, decided with its own record.
- A forecast stored and refreshed by every write: stale the moment stock moves.
- Supplier receipts in the fulfillment module: it ranks warehouses and counts no units (ADR 0010).
