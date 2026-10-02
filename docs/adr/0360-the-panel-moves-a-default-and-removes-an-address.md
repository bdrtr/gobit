# ADR 0360 — The panel moves a default and removes an address

**Summary:** Each address's row on a customer's page makes it the default
shipping or billing address, the flag taken from the address that held it
in the same write, or removes the address, under `customer:write` through
`customer.admin`, as the API's default and delete do.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The customer's page adds and corrects addresses (ADR 0342, ADR 0359), but
the address a customer wants their orders sent to by default, and an old
one they no longer live at, were changed only through the admin API. ADR
0342 left the default flags out of the correction because moving one
touches two addresses.

## Decision

The customer module's panel surface makes an address the default shipping
or billing address through the services the API calls, which clear the
previous holder in the same transaction, and removes an address through
the service the API's delete calls. Each address's row offers the defaults
it does not hold and its removal to an operator who may write the
customers.

## Consequences

- The address a telephone order starts from (ADR 0304) is chosen on the
  customer's page.
- At most one address holds each default, as the database's partial
  unique index keeps.
- A default address may be removed; the customer then has no default
  until another is made one.
- A removed address is soft-deleted: an order or a cart that copied it
  keeps its copy.

## Rejected

- Moving a default inside the address correction: it is a second write on
  another address, which the correction's one conditional statement does
  not make.
