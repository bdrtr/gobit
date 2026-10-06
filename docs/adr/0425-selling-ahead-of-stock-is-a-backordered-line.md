# ADR 0425 — Selling ahead of stock is a backordered line

**Summary:** gobit sells a variant ahead of its stock only as a backordered line: a claim the next units fill (ADR 0392) and an estimate the storefront shows (ADR 0399, 0421).
It costs a pre-order with a cap or a kept date, and buys one mechanism instead of two for units that are not there yet.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

ADR 0048 let `allow_backorder` stop the checkout's refusal and left pre-order,
"a promised date and a stock level that may go negative in a controlled way",
to its own record. Since then a backordered line leaves a claim that arriving
units fill oldest first (ADR 0392), a storefront variant with nothing to sell
shows when units are expected (ADR 0399), and an option says how many days its
delivery takes (ADR 0421). ADR 0399 and 0421 refused a promised date on the
order line, the claim and the shipping method, pointing at that record. The
stock alert mails when the storefront's badge turns in stock (ADR 0215).

## Decision

gobit sells a variant ahead of its stock only as a backordered line: the
checkout records a claim and the storefront shows the expected receipt and the
option's days as an estimate. No level goes below zero, no order keeps a
promised date, and nothing caps how many units are sold ahead.

## Consequences

- A "pre-order" is a variant with `allow_backorder` and an expected supplier
  receipt; the page shows `restock_expected_at` and the option's days, and the
  order line, the claim and the shipping method keep no date.
- A claim is filled when units arrive and opens no parcel (ADR 0392); the
  operator ships it as any line.
- Every unit asked for is sold: a limited edition cannot stop at a number, and
  units sold beyond what is expected wait for a receipt nobody wrote.
- The stock alert mails when the page shows the variant in stock, not when a
  receipt is expected or its date moves; `TestAnExpectedReceiptMailsNoStockAlert`
  holds it.
- A day count is not turned into a date: gobit holds no time zone, calendar or
  holidays (ADR 0421); the storefront renders its own.
- This reopens when a shop must cap the units sold ahead of a receipt, or when
  a document or a notification prints a delivery date the order must keep.

## Rejected

- A negative stock level: every reader of availability would learn a sign, and a claim already counts what is owed (ADR 0392).
- A promised date on the order line, the claim or the method: an estimate kept on a closed record would read as a promise (ADR 0399, 0421).
- A pre-order flag beside `allow_backorder`: two switches for one fact.
- A cap per variant: no consumer in this tree; it reopens with the trigger above.
- An alert on an expected receipt: the date is already on the page, and a moved date would mail a guess.
