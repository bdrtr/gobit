# ADR 0235 — A bundle sells from its parts

**Summary:** A bundle is in stock when every component can supply its units for
one bundle; checkout reserves each component, the order line keeps what it was
made of, and a write-off, a canceled parcel and a return put each part back.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amended by:** [0238](0238-a-bundle-is-replaced-from-its-parts.md): a line that sold a bundle is replaced from its parts, each part held under its own promise

Measurement: [measurements/0235](../measurements/0235-three-boxes-sold-one-sent-back.md)

## Context

ADR 0234 let a variant name what it is made of and left the bundle unsellable:
counted with no inventory item, it read out of stock and checkout refused it.
Selling it touches every place a line meets stock: the badge, the reservation,
the order, and the three acts that put units back on a shelf. A bundle sold
without the last three would leave its parts deducted for good once its line
was written off or sent back.

## Decision

A bundle's stock is its components': the storefront badge answers in stock when
each component can supply its units for one bundle, checkout reserves each
component for the line's quantity times its units, and the order line keeps the
composition it sold. A write-off, a canceled parcel and a received return put
each component back in the same multiple, read from the order line.

## Consequences

- The badge is computed on the one storefront path, so the listing filter,
  GraphQL and stock alerts answer alike. An uncounted or backordered component
  never limits a bundle; a counted one with no item makes it out of stock. The
  parts' stock rides on the page's graph call, and their flags cost one query.
- The checkout reads the parts the cart does not hold in one more catalog call.
  A bundle line takes one reservation per counted component, each from the
  warehouse its own item ranks first, so one box can be reserved from two; the
  parcel still ships the line whole.
- The execution record names a component's reservation by variant, and one it
  skipped under `unreserved_components`; recovery counts reservation units
  rather than lines. A record written before this decision holds no bundle and
  counts as before.
- The order line carries `components` (order migration 000033), on the admin
  and storefront reads and on the answers the put-back flows read. Editing the
  bundle after the sale changes nothing that is put back.
- The write-off reads the order's lines for the composition, as the parcel act
  already did; `order.line_canceled` is unchanged.
- The ledger keeps one location per item per order (ADR 0134), so a component
  also sold on a line of its own from another warehouse can be put back on the
  other shelf, as two lines of one variant already could.
- A bundle is taxed and discounted as its own product, and a replacement of one
  is refused with `returns_workflow_no_inventory_item`, as before.

## Rejected

- **Bundle lines expanded into component lines in the cart.** The price is the
  bundle's own; N lines need an allocation, N ceiling slots and a return rule
  for ratios.
- **Components deducted after the order is placed.** Nothing would hold the
  parts between the badge and the payment, and a deduction after capture has no
  compensation.
- **The composition read from the catalog when stock is put back.** A bundle
  edited after the sale would put back parts it never took.
- **The composition carried on `order.line_canceled`.** A second copy of it in
  a public payload, read in two encodings, where the order's lines are one read
  away.
