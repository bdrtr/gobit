# ADR 0251 — An order's page shows its payment and parcels to who may read them

**Summary:** The panel's order page expands the order's payment and parcel
links, and reads and prints each only for an operator holding that module's own
read privilege; the order's privilege opens the page.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

After ADR 0250 the panel's order page showed what was sold and not what became
of it: the money taken and given back, and the parcels. Both belong to other
modules and are bound to the order by links, `order_payment` (one to one) and
`order_fulfillment` (the parcel an addition joined included, ADR 0197). The
read layer expands both. The collection's own reference is the cart the
checkout opened it for, so the link is the only way to reach it from the order.

The panel's screens are guarded by one privilege each, and a screen that
printed another module's data under its own privilege would grant that data
through a door the API keeps shut: the payment endpoints ask for
`payment:read` and the parcel endpoints for `fulfillment:read`.

## Decision

The order page expands the `order_payment` and `order_fulfillment` links and
prints the collection's status, amounts and the moments its money moved, and
each parcel's status, tracking and moments, oldest first. Each is read and
printed only for an operator holding `payment:read` or `fulfillment:read`
respectively; without it the page names the privilege and reads nothing.

## Consequences

- An operator with `order:read` alone sees the order, its lines and its
  amounts, and is told which privilege would show the payment and the parcels.
  The page does not read what it will not print.
- Each section is its own read of the order with one expansion, so a payment
  read that fails costs the payment section and not the page or the parcels.
- The page states the order of the parcels itself, oldest first with the id
  breaking a tie, because a link promises none.
- A parcel's items are not shown: the shipment entity offers no items field.
  Nor are the quantities returned, canceled or exchanged per line, which the
  order module does not offer to the read layer. The known limit names these.
- The two privileges are spelled in the panel's scope table, where the gate
  that holds every panel scope to one a module declares reads them.
- Each view costs up to two more reads.

## Rejected

- Reading the payment and the parcels under `order:read`: it would grant two
  modules' data through a privilege the API does not accept for them.
- Hiding the sections without a word: an operator who cannot see a payment
  could not tell a missing privilege from an unpaid order.
- Filtering the collection by the order's id: its reference is the cart's, and
  the link is what binds it to the order.
- One read carrying both expansions: one failing link would take the other's
  section down with it.
