# ADR 0411 — An order read at a moment says where it was going

**Summary:** The as-of read carries the shipping address and each delivery in force at the moment, derived from the rows ADR 0195 and 0199 keep.
It costs one more read per reading and a rule that answers a moment between two database stamps by the row written last.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0171](0171-an-order-can-be-read-as-it-stood.md), whose reading named no address and no delivery

## Context

ADR 0171 reads an order as it stood at a moment from rows that carry their own
moment. ADR 0195 closes a corrected shipping address and keeps it, and ADR 0199
keeps each delivery change beside the method sold; both are dated on the
timeline, and neither was in the reading, so a dispute about where a parcel
was sent, or on which service, was answered from the history by hand. A
correction stamps the old row and the new one on the database's clock, one
after the other, and rows written before order migration 000037 took their
transaction's start.

## Decision

An order read at a moment carries the shipping address in force then, the row
written last at or before the moment, and each delivery as it stood then, the
sold method with its latest change made at or before the moment. Both are
derived from the rows ADR 0195 and 0199 keep, and the address's content is
shown only while the order still holds the contact it held then.

## Consequences

- Read at the present, the address is the order's current shipping row and the
  deliveries are the ones the order is on; the end-to-end lane holds both to
  the live order, as it holds the status and the money, and a unit test holds
  the deliveries to `models.CurrentDeliveries`.
- A moment before every shipping row's stamp, but not before the order, reads
  the row the order was placed with, and a method with no change by then
  stands as sold since the placement.
- `superseded_at` is not read. A moment between a row's closing and its
  successor's writing reads the closed row, and before migration 000037 the
  successor from its transaction's start: as exactly as the stamps agree.
- A delivery change's credit line is stamped before the change in the same
  write, so a moment between the two reads the credit in the money and the
  delivery as it stood before.
- After an erasure the row is still named, with its moment, and its content is
  null; `contact` says which.
- A delivery's reading carries the change's difference and the credit line or
  collection that moved it; the money stays the sale's, as ADR 0200 keeps the
  order's total.
- The billing address is not read at a moment: nothing corrects it.
- One more fixed query per reading; nothing is stored. A client decoding the
  reading strictly sees two new keys, `shipping_address` and `deliveries`.

## Rejected

- Choosing the address by `superseded_at`: a moment between two stamps would read no address, or two.
- Storing the address and the delivery on each status stamp: a second copy of rows that exist.
- Reading the billing address at a moment: it changes only by erasure, which `contact` already reports.
- Adding a dearer delivery's money to the reading's money: the order's total does not move by it (ADR 0200).
