# ADR 0252 — An order line says what became of it

**Summary:** The order line entity offers the units a live return asks back and
the units written off, and the shipment entity offers what a parcel holds; the
panel's order page prints both.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

After ADR 0251 the panel's order page showed the lines, the payment and the
parcels, and could not say what became of each line. The shipment entity
offered no items, so a parcel could not say which lines it carried. The order
module offers its returns and write-offs to no read, though it sums them per
line itself: a return or a write-off is refused past what was bought less what
live returns ask back and write-offs took (`SumReturnedQuantities`,
`SumCanceledQuantities`).

## Decision

The order line entity offers `asked_back_quantity` and `canceled_quantity`, the
two sums the order's own ceiling reads, and the shipment entity offers `items`,
each the order line a parcel carries and its quantity. Each is read only when
asked for, in one query for every record of the call, and the order page
prints them per line and per parcel.

## Consequences

- The page and the refusal read the same sums, so an operator sees the room
  the order module will allow before trying a return or a write-off.
- `asked_back_quantity` counts a return that was requested and one that was
  received alike, and not one that was canceled. It does not say whether the
  goods have arrived; the return's own status does, and the page reads none.
- A replacement's units are in neither sum, as they are in neither half of the
  ceiling.
- A parcel's item names a line by its id. The page gives it the title of the
  line it read, and a line it did not read (the parent's line in a parcel an
  addition joined, ADR 0197) is named by its id.
- A read asking for every field, which is what an empty field list does, now
  also runs the sums and the items' query. A read that names its fields pays
  for what it names.
- The line and the shipment entity each gain a published field set a consumer
  can depend on; the entities' tests list them.

## Rejected

- Counters kept on the line: a second copy of what the after-sales tables
  already say, written by every act that changes them.
- Offering the returns and write-offs as entities of their own now: a larger
  surface than the page needs, which asks for sums.
- Summing in the panel: the panel reads no after-sales record, and would spell
  the ceiling's rule a second time.
