# ADR 0367 — A customer lists their own orders

**Summary:** The order module answers `GET /store/v1/customers/{id}/orders`
with the path's customer's orders, newest first and paged, once the
installation's customer identity proves the request is that customer.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

A storefront reads one order by its id with the publishable key alone, the
id acting as a capability, and nothing listed a customer's orders. A
signed-in shopper's account page could show the order just placed and no
other: the storefront would have had to keep every id it was ever handed,
and a shopper on a second device would have none of them.

## Decision

The order module lists the path's customer's orders under
`/store/v1/customers/{id}/orders`, newest first and paged as the admin list
is, after `corehttp.ProvenCustomer` holds the request to that customer. Each
record is the order without its lines.

## Consequences

- A storefront that learned its shopper's id from the customer identity
  lists their orders and opens each through the order read it already uses.
- With no customer identity bound the route refuses, as the customer's
  addresses and balances do: one person's orders have no anonymous reader.
- A request proving another customer is refused before any order is read,
  and the publishable key alone reaches nothing here.
- Every status is listed, the canceled and archived included: they are the
  shopper's history.
- The order module resolves the identity lazily, as the payment module does
  for the balances, since the embedder adds it after the box's modules.

## Rejected

- Listing by e-mail address: a guest's address is typed by whoever checks
  out, and listing by it would hand any order to whoever knows the address.
- Answering the list with each order's lines: a page of orders would cost a
  line read per order, and the account page shows the totals.
- Moving the route under `/store/v1/orders`: whose orders are listed is a
  customer, and every route that names one sits under that customer.
