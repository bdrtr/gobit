# ADR 0203 — An exchange's difference is on the books

**Summary:** The order journal books an exchange's collected difference as
receivable against sales at the moment it was funded, and a refund naming the
exchange as the same lines the other way. It costs one more window read and
an index, and the two journals now close over an exchange as they do over a sale.

- **Status:** Accepted; amended by [0432](0432-an-exchange-names-its-return-and-prices-what-it-sends.md), whose exchange that names its return moves its documents' tax against sales
- **Date:** 2026-09-27

Measurement: [measurements/0203](../measurements/0203-a-swap-outside-the-books.md)

## Context

ADR 0188 left an exchange's difference outside the order's books: it is paid
into a collection of its own, which the order's payment link does not reach.
The payment journal booked that collection's capture and its refund against
receivable anyway, so receivable over the two journals was off by every
exchange difference collected, and the order journal read a refund naming an
exchange as one naming no record of its own. ADR 0200 repeated the gap beside
the delivery upgrade, which closes.

## Decision

An exchange with `funded_at` in the window is an `exchange_funded` entry at
that moment, debiting receivable and crediting sales with its difference. A
refund whose cause is one of the order's exchanges is an `exchange_refunded`
entry, debiting sales and crediting receivable with the refund's amount.

## Consequences

The entry reads the exchange's own row and not the collection, so it is booked
whatever the collection was opened for; since D142 that is the order.
`funded_at` survives the withdrawal that sends the money back, so a funded
entry stays where it was and the refund reverses it, as a cancellation reverses
a placement.

The difference is booked to sales whole. The exchange records one figure and
no tax in it, so the books cannot split what it does not hold.

An exchange with a negative difference is not funded and writes no entry of its
own; a refund that names it is booked as any exchange's.

`GET /admin/v1/order-journal` lists the two kinds, and its description no
longer calls the exchange outside the books.

## Rejected

- **Booking at the capture.** The capture is the payment module's fact and
  its moment; the order knows the difference was taken when the exchange is
  funded, which is the record it keeps.
- **A separate account for exchange differences.** The difference is goods
  sold for more than the goods they replace; sales is where that lands, and a
  cancellation already reverses sales without an account of its own.
