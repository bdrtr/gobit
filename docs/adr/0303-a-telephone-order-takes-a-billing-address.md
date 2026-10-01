# ADR 0303 — A telephone order takes a billing address

**Summary:** The panel's telephone order writes the cart's billing address
through the cart module's surface, with the company among the address fields.
The cart provider offers the billing address, and the page shows it and draws
its form with the shipping address until one is written.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0291 left the billing address on the admin API: the panel wrote the
shipping address only, so a telephone order for a company placed an order
invoiced to the delivery address unless the operator turned to an API client.
The panel's address form had no company field either. The admin API's billing
write and the order's billing address already existed, and the cart provider
read every address of a page of carts in one read, keeping the shipping one.

## Decision

The cart module's surface gains `SetBillingAddress`, the billing endpoint's own
act on an operator's cart, and the cart provider offers `billing_address` from
the same read as the shipping address. The cart's page prints the billing
address and offers a billing form, drawn with the shipping address until a
billing address is written, and both forms ask for the company.

## Consequences

- An operator takes a company's telephone order with the invoice addressed to
  the company and the parcel sent to its delivery address.
- With nothing changed, the billing form writes the shipping address as the
  billing address.
- The billing form's fields carry their own names, so a refused billing write
  draws the shipping form with the cart's address.
- The billing write reprices the cart as the shipping write does, the
  endpoint's own act.
- The two addresses cost one read for a page of carts, made only when either
  is asked for.

## Rejected

- Copying the shipping address into the billing address when none is written:
  the order would claim a billing address nobody gave.
- A "same as shipping" switch: a form drawn with the shipping address is the
  same act with one control less.
