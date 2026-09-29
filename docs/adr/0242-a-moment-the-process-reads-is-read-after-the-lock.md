# ADR 0242 — A moment the process reads is read after the lock

**Summary:** A price set's replacement, a price list's update and an invoice
read the application's clock after they take their lock, so a write that waited
is not recorded before the write it waited for.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amends:** [0053](0053-the-two-clocks-stay-and-every-moment-names-its-own.md), whose invoice took its year and its moment from one reading

Measurement: [measurements/0242](../measurements/0242-a-price-that-came-back.md)

## Context

Three writers stamp with the application's clock and read it before they take
their lock (D165), the mirror of what ADR 0241 closed for the database's clock.
A price set replaced by a write that read the clock and waited was recorded
before the replacement it followed, and the history, whose latest snapshot is
the standing price, answered with prices that were gone, which is what the
storefront's reduction compares against. A price list's update did the same to
its status. An invoice that waited for its series took the next number with an
earlier date than the number before it.

## Decision

The pricing repository takes the clock and reads it after it has locked the set
or the list, which it now locks before the update. An invoice reads the clock
again after its series is locked, and when that reading is in another year than
the series, the transaction is rolled back and the issue made once more.

## Consequences

- The standing price and the standing list are the ones written last, and an
  invoice's date does not fall before its predecessor's in its series.
- The injectable clock stays the clock, so tests still choose their moments.
- A price's id is still made before the lock, from the reading the input was
  checked against; nothing orders by it.
- The invoice keeps ADR 0053's pair: its number's year and its date are the
  same year. An issue that waits across midnight on the 31st is numbered in
  the new year, once.
- Across processes, clocks can still disagree, as ADR 0053 accepted.

## Rejected

- **The database's clock for these stamps.** ADR 0053 kept them on the
  application's clock, which the tests move by hand and the invoice needs for
  its year.
- **Ordering the history by its identity column.** The history answers "what
  stood at this moment", which is a question about the moment, not the order.
- **Failing an invoice whose year turned.** The first sale of a year would fail
  at the hour nobody watches, the case the series was opened on demand for.
