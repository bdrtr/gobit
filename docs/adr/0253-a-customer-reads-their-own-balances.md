# ADR 0253 — A customer reads their own balances

**Summary:** Two storefront reads answer a customer's store credit and points
balance, for the customer the request proves and nobody else; the history
stays on the admin surface.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

A shop can hold money for a customer (ADR 0152) and points (ADR 0164), and the
customer can spend either at checkout (ADR 0165), but only an admin endpoint
read a balance. A shopper learned what they had when an operator told them or
when a total dropped. The records said the storefront read was missing because
the payment module was not wired to the proof a customer read needs. That proof
is `corehttp.ProvenCustomer`, which the address book, the cart and the b2b
storefront already share.

## Decision

`GET /store/v1/customers/{id}/store-credit/balance` and
`GET /store/v1/customers/{id}/loyalty-points/balance` answer the balance the
admin surface reads, for a currency, when the request proves the customer in
the path. An installation with no customer identity bound refuses them, as it
refuses the address book (ADR 0043).

## Consequences

- A request naming another customer is refused with 403, one that proves
  nobody with the identity's own answer, and a missing identity with 401. None
  of them reads the ledger.
- The balance is the admin one: open holds are subtracted, and points can be
  negative (ADR 0165).
- The history is not offered: a row carries the operator's reason and
  reference, written for the shop. A customer who asks why reads it from the
  shop, not from the API.
- The payment module resolves the bound identity on first use, the fourth
  module with that wrapper after the customer, cart and b2b modules. The
  comparison is not copied; it is the shared one.
- The authorization matrix lists both routes as customer-named, so a
  publishable key alone is refused on them.
- A generated project, which binds no identity (a known limit), serves both
  routes as refusals until its embedder binds one.

## Rejected

- Serving the balance by the path alone when an installation trusts unproven
  claims: that setting already withdraws the person-bound tenders, and a
  balance read with no proof would print one person's money for anyone who
  knows an identifier.
- Offering the history with the reasons stripped: a second shape of the same
  rows, for a question nobody on the storefront has asked yet.
- A `/me` path without the customer in it: every other customer-named
  storefront route names the customer and proves it, and one that did not
  would be a second convention for the same guard.
