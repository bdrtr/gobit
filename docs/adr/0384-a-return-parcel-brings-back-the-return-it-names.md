# ADR 0384 — A return parcel brings back the return it names

**Summary:** A parcel on a return option names the return it brings back and holds at
most what it still awaits, bound to no order. It costs a column, one read and an unlisted parcel.

- **Status:** Accepted; amended by [0413](0413-a-returns-parcel-is-opened-from-its-return.md), which opens and lists its parcel from the panel, and [0420](0420-every-parcel-waits-for-its-orders-lock.md), which counts its return's parcels under its order's lock
- **Date:** 2026-10-04
- **Amends:** [0135](0135-a-parcel-cannot-hold-more-than-the-order-owes.md), whose bound made no exception for direction, and [0140](0140-a-parcel-records-which-order-it-is-for.md), which bound every parcel to its order

## Context

The fulfillment module has said since migration 000001 that goods a customer
sends back travel in a second fulfillment on a shipping option marked
`is_return`. ADR 0135 bounds every parcel by what the order still owes, so that
parcel was refused for units already shipped with 409
`fulfillment_line_not_dispatchable` (D234). One without items passed the bound
through the order's door, was handed the customer's address as its destination,
and was bound to the order as one of its shipments. A parcel bound to an order
counts as goods gone out in the dispatch bound and in both stock targets
(ADR 0139, 0142), holds off an address correction and a delivery change
(ADR 0195, 0199), and can take an addition's goods (ADR 0197). Nothing named
the return a parcel served.

## Decision

A parcel on a return option names the order return it brings back in
`fulfillments.return_id` and holds, per line, at most what that return names
less what its live parcels hold, while the order module says the return awaits
its goods. Such a parcel is not bound to its order and is never counted as goods
gone out, and a parcel whose option and `return_id` disagree about its direction
is refused before anything is asked.

Measurement: [measurements/0384](../measurements/0384-a-return-parcel.md)

## Consequences

- `POST /admin/v1/fulfillments` takes `return_id` with items. A return option
  without one, or an outgoing option with one, answers 422
  `fulfillment_option_direction_mismatch`, so the order's parcel route no longer
  opens a parcel on a return option.
- A key that already names a parcel is answered with it before the direction
  is asked, so a retry outlives a change to its option's `is_return`.
- A return of another order, or one received or canceled, answers 409
  `fulfillment_return_not_awaited`; a line the return does not name answers 422
  and more than it still awaits 409, both `fulfillment_line_not_dispatchable`.
- The fulfilling flow answers the return's lines from the order module's return
  detail, whose `awaits_goods` is that module's own rule; the fulfillment module
  subtracts its own return parcels. It asks before its transaction and fails
  closed, as ADR 0135 does.
- A live return parcel is pending, shipped or delivered. A canceled one, or one
  that came back to the customer undelivered, frees its units.
- Committed units count parcels without a return in the query itself, so no
  caller's list of parcels can count one.
- The order's shipment list, its timeline and the panel do not show a return
  parcel as the order's; its reference is still the order id.
- Delivering a return parcel moves no stock. Receiving the return restocks, and
  neither looks at the other; cancelling a return leaves its parcel to the
  operator.
- The provider is handed no destination and no direction. No label is bought,
  and a carrier's return shipment is its own decision.
- Two parcels for one return opened at the same instant can both pass, the
  window ADR 0135 leaves for outgoing parcels.

## Rejected

- Exempting return options from the bound: their items would be unbounded, the fault D72 closed.
- Binding the parcel to its order and filtering each reader: a filter in every reader the measurement lists, and a join that would load an addition's goods into it.
- Refusing return options until a carrier plugin exists: the module's documented answer stays false.
- A return-shipment table and a `ReturnShipper` provider verb: a published name chosen with no carrier to test it (ADR 0026, 0065).
- Comparing the return's status word in the flow: the order module answers `awaits_goods`, the D59 class.
