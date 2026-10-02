# ADR 0375 — A signed-in shopper changes their password

**Summary:** `contrib/identity-session` mounts `POST /store/v1/auth/password`,
which replaces a signed-in shopper's password given the current one and ends
their other sessions as a reset does.

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

A shopper could replace a forgotten password through a link (ADR 0373), and an
operator could replace anyone's. A shopper who knew their password and wanted
another had to pretend to have forgotten it, wait for mail and follow a link.

## Decision

`POST /store/v1/auth/password` takes the current password and a new one from a
request whose session proves a customer, and replaces the credential when the
current one matches. It is a replacement like a reset's: every session issued
before it ends and this browser gets a new one (ADR 0374).

## Consequences

- The current password is asked for because a session is not the person: a
  cookie left signed in on a shared computer would otherwise lock its owner out.
- A wrong current password answers 403 and changes nothing, so the storefront
  can tell it from a session that ended (401).
- The new password is checked before the current one is read.
- The route is mounted only where the store offers `CustomerCredentials`, an
  optional capability reading a credential by its customer, since a session
  names a customer and not an address. A customer with no password here is
  told so with 409.
- Guessing the current password through this route is bounded as signing in
  is, by gobit's own guard stack, and costs an argon2id verification per try.

## Rejected

- Asking for nothing but the session: a borrowed browser could change the
  password and keep it.
- Answering a wrong current password with 401: a storefront reads 401 as a
  session that ended and would sign the shopper out.
- A method on `Credentials`: every store an installation binds would stop
  compiling.
