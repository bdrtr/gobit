# ADR 0426 — The margin gobit keeps is the placed margin

**Summary:** gobit keeps one margin, an order's goods sold less what they cost when it was placed (ADR 0401), and builds no fee, carrier cost, after-sale margin or cost of goods.
It costs a margin that is not the shop's profit, and keeps every figure gobit publishes one it actually recorded.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

ADR 0401 copies a variant's cost onto each order line at the checkout and
publishes the placed margin; ADR 0412 shows it in the panel. The rest of a
shop's margin is held elsewhere: a payment provider's fee and a carrier's
charge reach no `core/provider` contract, a returned unit's value depends on
whether it goes back on the shelf, and the order journal (ADR 0188) keeps no
inventory or cost-of-goods account. ADR 0412 left the product CSV's cost
columns to a record of their own.

## Decision

gobit keeps one margin, an order's goods sold less the costs its lines kept
when it was placed. It records no payment fee, no carrier cost, no after-sale
margin and no cost of goods in the journal.

## Consequences

- `placed_margin` is the margin there is; the panel calls it that.
- A refund, a cancellation or an exchange moves money on the order and leaves
  the placed margin as it was.
- The journal books revenue, discounts, tax and shipping (ADR 0188, ADR 0419)
  and no cost.
- A bundle line costs the bundle's own entry (ADR 0401).
- The product CSV's cost columns are not decided here; they are a slice of
  their own, which ADR 0412 deferred.
- A margin event and a return rate are a report's, built by the embedder from
  the order record and its bus events.
- This reopens when a payment provider plugin reports its fee, a fulfillment
  provider reports what the shop paid, or the inventory module keeps a value per
  unit, each for the figure it would supply.

## Rejected

- A fee or carrier cost typed by the operator per order: a second ledger beside the provider's invoice.
- An after-sale margin from refunds alone: it treats every returned unit as worth nothing or as new.
- Cost of goods from `unit_cost`: the inventory side of the entry has no value to credit.
- A margin event: no subscriber inside gobit acts on it (ADR 0386).
