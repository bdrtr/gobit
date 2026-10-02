# ADR 0374 — A replaced password ends the sessions before it

**Summary:** A shopper's credential keeps the moment their sessions count from,
and a cookie issued before it proves nobody; a password reset, an operator's
replacement and a shopper ending their other sessions move it.

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

A shopper's session is a signed cookie with no record behind it (ADR 0127), so
nothing ended one before it expired, thirty days by default. A password reset
(ADR 0373) signed the person in and left every earlier session working: a
shopper resetting because somebody learned the old password locked nobody out.
An operator replacing a password had the same gap, and a shopper had no way to
sign out a phone they lost.

## Decision

The credential row keeps the moment its customer's sessions count from, a
session cookie carries the moment it was issued, and the verification refuses a
cookie issued before that moment. A password reset, an operator replacing a
password and `POST /store/v1/auth/sessions/revoke-others` move it to now, before
anything else is written, and the request that moved it gets a cookie issued
after it.

## Consequences

- Every request whose cookie is otherwise good reads one column by primary key.
  A request with no cookie, or one that fails its MAC or expiry, reads nothing.
- Failing that read answers a classified error, so a storefront route reports
  a failure (ADR 0371) instead of treating the shopper as nobody.
- The moment is written with the application's clock and truncated to the
  millisecond it is compared at, so the cookie issued after it in the same
  request is never earlier, whatever the database's rounding; a session issued
  in that millisecond survives it.
- The moment never moves back: `GREATEST` keeps the later of two racing ends.
- Moving the anchor before writing the password means a failure between the two
  has signed people out and replaced nothing.
- A cookie sealed before this change carries no moment of issue. It works until
  the customer's first anchor, which ends it.
- Migration 000004 adds the nullable column `sessions_valid_from` to
  `customer_credentials`. It is declared as personal data and reported in a
  dossier, and an erasure takes it with the row.
- `SessionAnchors` is an optional store capability; a store without it, and a
  customer with no credential here, keep the old shape. The revoke endpoint is
  not mounted for the first and answers 409 for the second.
- `Sessions.Issue` keeps its signature, so `contrib/identity-passkey` and an
  embedder's own sign-in issue cookies the anchor can judge.
- One session still cannot be ended alone; ending sessions takes all of them.

## Rejected

- A session row per sign-in, as admin sessions have (ADR 0267): a write per
  sign-in and a sweep for one capability a shopper asks for rarely.
- A generation counter in the cookie: issuing would need a read, and
  `Sessions.Issue` takes no context.
- `updated_at` as the anchor: the database writes it with its own clock, which
  the cookie's issue cannot be compared against.
- Ending sessions inside `Put`: a store an installation binds would have to
  know that writing a password also ends sessions.
