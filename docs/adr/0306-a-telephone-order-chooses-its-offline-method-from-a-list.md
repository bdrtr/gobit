# ADR 0306 — A telephone order chooses its offline method from a list

**Summary:** The payment module's panel surface lists the registered methods
whose money comes after the order is placed, and the telephone order's
completion offers them as a list to an operator who holds `payment:read`. An
operator without it types the method as before.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The operator's completion of a telephone order takes only a method whose money
arrives after the order is placed (ADR 0286), and the panel asked for it in a
box with "bank_transfer" as its hint. The methods are the shop's
`PAYMENT_OFFLINE_METHODS` and any provider plugin whose money comes later
(ADR 0284), and the payment module already told them apart for the checkout.
Its panel surface recorded the money of an offline session (ADR 0287), and
the panel reads a module's data under that module's privilege (ADR 0260).

## Decision

The payment module's surface gains `OfflineMethods`, the registered providers
whose money comes later, and the cart page asks it when the operator holds
`payment:read` as well as the cart's write. The completion form offers the
methods as a list, choosing again the method a refused form carried.

## Consequences

- An operator places a telephone order with one of the shop's offline methods
  by choosing it, and a method the completion would refuse is not offered.
- A provider plugin whose money comes later is listed with the shop's own
  methods, as the completion accepts it.
- An operator without `payment:read`, or a panel without the payment surface,
  keeps the box, and the payment module is asked nothing.

## Rejected

- Listing every provider: the completion refuses those whose money moves at
  the checkout, and the list would offer a choice that fails.
- Reading the methods from the configuration in the panel: a provider plugin
  is registered with the payment module, not configured for the panel.
