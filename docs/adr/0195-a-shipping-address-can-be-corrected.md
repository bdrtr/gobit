# ADR 0195 — A shipping address can be corrected before it ships

**Summary:** An operator corrects where a pending order ships while none of its
parcels is on its way, and the address the order was placed with is closed
rather than edited. It costs a history in the address table and a check across
two modules, and every reader of the address reads the correction.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0195](../measurements/0195-a-typo-in-a-street.md)

## Context

An order's addresses were written once and changed only by erasure. Since ADR
0193 and 0194 the shipping address has two readers: the operator's record and
the label a carrier prints. A customer who rings about a typo in their street
had nothing to ask for: no route wrote an order's address, and a paid order
cannot be canceled to be sold again.

## Decision

`PUT /admin/v1/orders/{id}/shipping-address` closes the order's current
shipping address and writes the corrected one beside it in the same
transaction, for a pending order whose data was not erased, in the country the
order already ships to. The fulfilling flow refuses it while any of the order's
parcels is pending, shipped or delivered, because that parcel's carrier holds
the old address.

## Consequences

Both readers take the current row: the admin record and the carrier of a
parcel opened afterwards. The invoice's buyer is untouched, because only the
billing address fills it. The row the order was placed with stays, stamped
`superseded_at`. The person's file lists both, the erasure empties both, and
the timeline dates each correction with an entry that names the closed row and
carries no address. The customer sees that entry.

A canceled parcel never left and a returned one came back, so neither stops a
correction; correcting the street a parcel was returned from is the case this
exists for. A correction identical to the current address writes nothing.

The country cannot change. The tax and the shipping price were computed on it,
so keeping it keeps both true; another country is another sale. An order that
recorded no shipping address has nothing to correct and no country to keep.

The parcels are read, and then the order is written, with no transaction across
the two modules. A parcel opened in between carries the old address. It needs
two operators acting on one order at the same moment.

The storefront cannot correct an address: its read of an order is open to
whoever holds the id (ADR 0008), and a write there would let them send the
goods elsewhere.

A rollback of the migration refuses a database that holds a correction, rather
than dropping what the order held before. The order module's migration test
runs in a database of its own for that reason (D141).

## Rejected

- **Editing the address row in place.** The order would no longer say where it
  was going when it was placed, and the timeline could not date the change.
- **A correction record beside an unchanged address.** Every reader would have
  to know to prefer it; with the current row there is one rule, and a second
  current row is refused by the index.
- **Correcting the billing address as well.** An issued invoice keeps what it
  printed, and a correction before issue is its own decision.
