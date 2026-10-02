# ADR 0359 — The panel adds a customer's address

**Summary:** A customer's page adds an address under `customer:write`
through `customer.admin`, with its printed fields, as the default shipping
or billing address when ticked, checked as the API's create checks one; a
refusal comes back with what was typed in the form alone.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The customer's page lists and corrects a customer's addresses (ADR 0308,
ADR 0342), but a customer who phones in a new one, for an order support is
about to place for them, was given it only through the admin API.

## Decision

The customer module's panel surface adds an address through the service
the API's create calls, its printed fields as JSON under the provider's
address keys and the two default flags as given. The customer's page
offers the form to an operator who may write the customers.

## Consequences

- Support adds an address where it reads the customer; one added as the
  default shipping address is the one a telephone order's cart for them
  starts from (ADR 0304).
- An address made a default takes the flag from the customer's previous
  default in the same write, as through the API.
- A refused address comes back in its own form; an address's row and the
  contact form keep what they were drawn with.
- Removing an address, and moving a default to an existing one, stay the
  API's.

## Rejected

- Adding the address to the cart being placed instead: an address phoned
  in is the customer's, used again on their next order.
