# ADR 0457 — An order's payment status is read from its summary

**Summary:** `GET /admin/v1/orders` takes `payment_status` — `not_paid`, `partially_captured`, `captured`, `partially_refunded` or `refunded` — read from what the order's summary reports collected and refunded against its total less its credits; no fulfillment status filter is added.
It costs a filter that reads every order's summary when given alone, and buys the panel's payment filter in the payment collection's words.

- **Status:** Accepted
- **Date:** 2026-10-18

## Context

depremtek's panel shows each order's payment and fulfillment state beside
its search (D312). The order list could tell an order awaiting its payment
from one that does not (ADR 0294) and nothing finer. The order module holds
a report of the money collected and refunded (ADR 0119), not the payment
collection's status, and holds no parcel's state at all.

## Decision

The order list's `payment_status` names one of five states derived, in the
list's four statements, from the summary's paid and refunded totals and
the order's total less its credits: refunded when refunds reach what was
paid, partially refunded when some was refunded, captured when what was
paid covers what is owed, not paid when nothing was, partially captured
otherwise. A fulfillment filter is not added: the parcels are the
fulfillment module's, and a marketplace's state is its own split's.

## Consequences

- An order authorized and not captured is `not_paid` here, where the payment collection says `authorized`; an order owing nothing is `captured`.
- An unknown value is refused with 422 `order_invalid_input`.
- Given alone on the plain list, the count reads every order's summary, as `awaiting_payment` already did; beside `q` it reads the hits' summaries only.
- The four list statements carry the CASE written out and change together.

Measurement: [measurements/0456](../measurements/0456-what-a-search-read.md)

## Rejected

- The collection's status copied onto the summary: authorization moves it without an event the order module hears.
- A fulfillment status from the parcels through the link table: SQL across a module boundary (ADR 0006).
