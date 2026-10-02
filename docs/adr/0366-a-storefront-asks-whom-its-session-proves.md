# ADR 0366 — A storefront asks whom its session proves

**Summary:** `contrib/identity-session` answers `GET /store/v1/auth/session`
with the customer the session cookie proves and when the session ends,
uncached, and one refusal for every request that proves nobody, so a
storefront that signed a shopper in learns the id the customer routes take.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

Signing in and finishing a registration answer 204 with the cookie alone,
and every storefront route that names a customer takes its id in the path.
The sign-in's description said the id could be read from "the routes that
now work", and each of them needed the id first. A storefront that signed a
shopper in on a new device, or saw them finish a registration whose account
gobit opened, had no way to learn whom it had signed in.

## Decision

The module answers `GET /store/v1/auth/session` with `customer_id` and
`expires_at` for a request whose cookie proves a customer, with
`Cache-Control: no-store`. A request that proves nobody gets 401
`identity_session_none`, one sentence for every way it can fail.

## Consequences

- A storefront signs a shopper in, asks this route once, and opens the
  customer's addresses, balances and wishlist with the id it answered.
- The id leaves in a body and never in a URL, which is what the sign-in's
  empty answer was protecting.
- No cookie, an edited one, one signed with a key the installation does not
  hold and an expired one answer alike, as the cookie's verifier already
  does.
- The route reads the cookie alone and no table, as every request naming a
  customer does.
- It sits under `/store/v1`, so the publishable key and the rate limit guard
  it as they guard the sign-in.

## Rejected

- Answering the sign-in with the id: a client that turned the answer into a
  redirect would put the id in a browser's history, and a registration's
  verify link would still answer nothing to the page that opened it.
- A cookie a script can read, carrying the id: the session cookie is
  HttpOnly so an injected script cannot take it, and a readable twin would
  hand that script the customer.
- Accepting `me` in place of the id on the customer routes: every route
  that names a customer, in every module that has one, would learn a second
  spelling of its path.
