# ADR 0342 — The panel corrects a customer's address

**Summary:** A customer's page corrects each of their addresses' printed
fields under `customer:write` through `customer.admin`, from the ones the
page was drawn with; the customer module writes them in one conditional
statement only while they are still the address's. The default flags are
not corrected here.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel lists a customer's addresses (ADR 0308) and corrects their name
and phone (ADR 0337), but a wrong street or postal code, which support is
asked to fix before an order ships, took the admin API's partial update,
which writes whatever it is sent over whatever the address has become.

## Decision

The customer module corrects an address's nine printed fields together, in
one UPDATE that matches the ones the caller read on a live address of that
customer, and refuses with `customer_address_revised` otherwise. The
customer's page offers each address that form to an operator who may write
the customers, carrying the fields it was drawn with.

## Consequences

- Support corrects an address where it reads it, checked by the rules an
  address is created by, the country upper-cased.
- Two operators correcting one address at once write once; the second is
  told to draw the page again and gets back what they typed in that
  address's form alone.
- The read and the written address cross the surface as JSON under the
  provider's address keys, so the panel names no field the entity does not
  publish.
- A cart or an order already given the address keeps the copy it took.
- Which address is the default stays the admin API's to change: moving it
  touches two addresses, not one.

## Rejected

- Writing through the API's partial update: it writes over a correction
  made meanwhile.
- Matching on the moment the address was last written: the entity does not
  publish it, and the fields themselves name what was read.
- Correcting the default flags in the same form: a flag moved here would
  have to clear another address's in the same write.
