# ADR 0308 — The customer page lists the customer's addresses

**Summary:** The customer provider offers `addresses`, a customer's living
addresses in the order they were written with their default flags, and the
panel's customer page lists them. The field and the default shipping address
come from one read for a page of customers.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The panel's customer page showed a customer's name, e-mail and phone and no
address. Its template and handler said the addresses could not be shown
because the read layer joins across links and not within a module. ADR 0290
had since offered a cart's lines as a list-valued field of the cart, read in
one query for a page, and ADR 0304 offered the customer's default shipping
address the same way. An operator answering a customer still looked their
addresses up over the admin API.

## Decision

The customer provider offers `addresses`: each living address under the
order's address keys with its id and its two default flags, in the order it
was written, read with `default_shipping_address` in one query for the page
when either is asked for. The customer page lists the addresses and marks the
defaults.

## Consequences

- An operator sees where a customer's parcels and invoices go on the
  customer's page.
- The two address fields cost one read for a page of customers together, and
  none when neither is asked for.
- A deleted address is not listed, and a customer with none answers an empty
  list.
- The page reads the addresses under `customer:read`, as the admin API does.

## Rejected

- A separate address entity linked to the customer: an address belongs to one
  customer and is read with them, as a cart's lines are.
- Listing only the defaults: an operator answering a customer needs the address
  the customer names, default or not.
