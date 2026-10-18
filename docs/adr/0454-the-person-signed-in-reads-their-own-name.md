# ADR 0454 — The person signed in reads their own name

**Summary:** `GET /admin/v1/auth/me` answers a signed-in person's own e-mail address, first name and last name beside their id and privileges; an API key's answer carries none.
It costs a user read per call, and buys a panel the name of who is signed in without `auth:read`.

- **Status:** Accepted
- **Date:** 2026-10-17

## Context

depremtek's backend, a consumer, shows the operator or seller signed in to
its panels (D309). `GET /admin/v1/auth/me` answered an id, a kind and the
privileges; the address and name were on `GET /admin/v1/users/{id}`, which
asks `auth:read`, a privilege a staff or seller account is not given, since it
reads every colleague's record. A panel could not say who was signed in.

## Decision

`GET /admin/v1/auth/me` answers a user principal's own `email`, `first_name`
and `last_name`, read from their record, and leaves the three out for an API
key. It still asks identity alone, and reading another user's record still
asks `auth:read`.

## Consequences

- A panel names the person from the call it already makes to learn their
  privileges.
- The route reads the user once more per call; it was already reading the
  second factor.
- A person whose second factor is owed reads their name with an empty set of
  privileges.

## Rejected

- Opening `GET /admin/v1/users/{id}` to one's own id: a second rule on a route whose privilege is about others.
- Putting the name in the session token: a renamed person would read the old name until the token expires.
