# ADR 0238 — A bundle is replaced from its parts

**Summary:** A replacement of a line that sold a bundle keeps the parts the line
sold and sends them, each part set aside, confirmed and given back under a
promise of its own.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amends:** [0235](0235-a-bundle-sells-from-its-parts.md), which left a bundle's replacement refused
- **Amended by:** [0244](0244-a-bundle-variant-is-replaced-from-its-catalog-parts.md): an item naming a bundle variant is recorded with the catalog's parts and sent from them

Measurement: [measurements/0238](../measurements/0238-a-crushed-box-sent-again.md)

## Context

ADR 0235 sold a bundle from its parts and left its replacement refused: the
dispatch sets aside units of the line's own variant, and a bundle has no
inventory item, so a claim on a crushed gift box could only be settled with
money. A replacement item held one promise, on its own row (ADR 0090), and the
withdrawal gives back the promises the items name (ADR 0237).

## Decision

An item that replaces a line that sold a bundle is written with the line's
parts, and the dispatch sets each part aside for the item's quantity times its
units, under a promise written on the part. The record is sent only when every
part holds a promise, and a withdrawal gives each one back.

## Consequences

- Order migration 000034 adds `order_replacement_item_parts`: the variant, its
  units, its rank and its promise. The item's own promise stays empty.
- The parts are the sale's. A bundle edited after the sale sends what was sold,
  as the put-back flows do.
- The order interop's `RecordReplacementReservation` names the variant the
  promise holds: a part of the item, or the variant the item sends. Any other
  is refused, `order_replacement_line_unknown`.
- The admin replacement read and the flow's document carry `parts` on such an
  item, and `sent_units` counts the parts' units, as a received return does.
- A part no warehouse stocks refuses the dispatch, as a line with no inventory
  item does. The parts set aside before it stay held, and the withdrawal gives
  them back.
- An item that names a bundle variant rather than a line is still refused with
  `returns_workflow_no_inventory_item`: its parts would be the catalog's, which
  this flow does not read.

## Rejected

- **The parts read from the catalog at dispatch.** A bundle edited after the
  sale would send parts the customer never bought.
- **One replacement item per part.** The ceiling counts items against the
  line's bought quantity, so a box would count as its number of parts, and the
  record would no longer say the parts are one box.
- **A second verb for a part's promise.** The flow holds a line and a part
  alike, and one verb naming the variant lets the order module refuse a promise
  for goods the item does not send.
