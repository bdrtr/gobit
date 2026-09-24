# ADR 0171 — An order can be read as it stood

**Summary:** `GET /admin/v1/orders/{id}/as-of?at=` answers an order as it stood
at a past moment: its status, money, canceled units, after-sales records and
parcels then, all derived from rows that carry their own moment. A field whose
past the records do not keep is said to be unknown rather than guessed.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0171](../measurements/0171-an-order-then.md)

## Context

The feature list's C8 row asks for an order at a moment. ADR 0170 put every
dated row the order reaches on its timeline, but a history is not an answer
to "what did this order look like on the 3rd". The support desk had to fold
the history by hand. The order's recorded money summary keeps only its latest
value, so it cannot say what had been paid on the 3rd at all.

## Decision

The order module derives an order at a past moment from rows that carry their
own moment: each status is the one entered by the latest stamp at or before
it, and the money is summed from the payment movements and credits up to it,
through the live order's own formula. Nothing is stored, a field the records
cannot answer is reported as unknown, and a moment that has not happened or
that precedes the order is refused.

## Consequences

Read at the present, the derivation has to equal the live order, the payment
collection and the parcel. A test in the end-to-end lane holds it to that for
the status, the money, the canceled units and the parcel's status, and a unit
test does the same for every state each record can reach. A derivation that
disagreed with today would disagree with any past.

Two answers are unknowns by construction. An order archived before its
archiving was dated (migration 000007), read after its completion, has a null
status. A contact erased since the moment is `erased_since`. The contact before
an erasure was emptied, and nothing holds it.

The moment compares stamps written on two clocks, the database's and the
capturing process's (ADR 0053). A moment between two events on different
clocks is answered as exactly as those clocks agree. Within a parcel, the
latest reached stamp decides, not its order against the creation, so a skew
between the two clocks cannot hide a transition.

The endpoint is admin-only: it carries money, and the storefront's view of an
order is its narrowed timeline (ADR 0100).

## Rejected

- **Storing a snapshot per change.** A second copy of rows that exist.
- **Reading the order's money summary.** It keeps only the latest value.
- **Taking a missing moment as now.** The order read already answers now.
- **Guessing the undated archive.** Completed or archived; the record is silent.
