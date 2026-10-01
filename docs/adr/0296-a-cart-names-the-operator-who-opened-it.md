# ADR 0296 — A cart names the operator who opened it

**Summary:** A cart opened through the admin cart API or the panel's telephone
order keeps the identity of the operator who opened it. The read layer and the
admin listing filter the carts by it, and the panel lists the open ones.

- **Status:** Accepted
- **Date:** 2026-10-01

Measurement: [measurements/0296](../measurements/0296-the-few-carts-an-operator-opened.md)

## Context

ADR 0290 opened telephone carts from the panel and sent the operator to the
new cart's page. Nothing listed them afterwards: a call cut off halfway, or one
handed to a colleague, left a cart whose id only the first page's address held.
The cart had no mark of the door it came through, so no query could tell an
operator's cart from the many a storefront opens and abandons. The audit log
records the admin request with its caller, but it is not a list of carts and
cannot tell which of them are still open.

## Decision

A cart opened through an admin door stores the caller's identity, a user's or
an API key's id, in `opened_by`, and a storefront's cart stores none. The cart
provider offers `opened_by` and an `opened_by_operator` filter, the admin
listing takes the same filter, and the telephone order's page lists the open
carts operators opened to an operator who holds `cart:read`.

## Consequences

- An operator returns to an unfinished telephone order, their own or a
  colleague's, from the page that opens one; the twenty newest are listed, each
  with its caller, its opener, when it was opened and its total.
- The opener is the identity the guard ring proved. A body that names one is
  refused, and an admin door reached without an identity opens no cart.
- The id is free text, as the file module's `uploaded_by` is: no foreign key
  crosses into the auth module, and a removed user's carts keep the id.
- The cart's JSON does not carry the opener. The storefront and the admin API
  answer with one shape, and a shopper holding the cart's link would read the
  operator's identity.
- A completed cart leaves the list; one nobody completes stays until it is
  deleted.
- Carts opened before this record name no opener and count as a shopper's.
- A partial index holds the operators' carts, so the list and its count read
  those few rather than every cart the storefront left behind; the other
  filters walk the table as before.

## Rejected

- Reading the open carts from the audit log: it records requests, not carts,
  and would have to be joined to the cart table to know which are open.
- A boolean column instead of the id: the list could not say who to ask about
  a cart.
- Listing only the operator's own carts: a call handed over would vanish from
  the colleague who takes it.
