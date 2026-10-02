# ADR 0369 — The address book keeps a province

**Summary:** A customer's saved address keeps a province, the sub-country unit
ADR 0067 defines, so a shopper who checks out from the address book brings it
to the cart instead of typing it again.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

ADR 0067 settled what `province` means and left it out of `customer_address`:
a column nothing read was a field with no reader, and it was to arrive when the
province was first read. It is read now. The order's address carries it to the
fulfillment module and the invoice, the panel prints it on the order's page,
and a storefront asks for it at checkout because the order needs it. A shopper
who picks a saved address there brings every printed field but this one, and
types it again on every order.

## Decision

A customer's saved address keeps a province, optional as its other optional
fields are, written and read wherever the address's other printed fields are:
the storefront and admin address endpoints, the panel's forms, the provider's
address keys, the disclosure and the erasure. It is the sub-country unit, an il
in Turkey, and not the district, which still has no field anywhere.

## Consequences

- The customer module's migration 000007 adds `province TEXT NOT NULL DEFAULT
  ''`; an address written before it holds the province empty, and nothing
  fills it, since gobit holds no list a city could be looked up in.
- The service bounds it at the length of a city, and no CHECK stands behind it,
  as none stands behind the address's other optional fields.
- The panel's correction (ADR 0342) compares it among the printed fields, so a
  province corrected meanwhile refuses a stale write as a stale city does.
- An erasure empties it and a disclosure shows it, declared as `Named`.
- The provider's address keys now match the order's, which carried `province`
  already.
- The cart's and the order's erasure declarations described the column as "the
  province or district", the second reading ADR 0067 refused; they say what it
  is now (D218).
- ADR 0065's district remains open; a province in the book does not fill a
  carrier's quote.

## Rejected

- Waiting for a carrier to read it: the order reads it already, and the shopper
  pays for the wait on every order.
- Deriving it from the city or the postal code: gobit holds no gazetteer, and a
  guess written into a person's address is a wrong address.
- A nullable column as the cart's is: the book's optional fields are all `NOT
  NULL DEFAULT ''`, and one null among them would give the book two kinds of
  empty.
