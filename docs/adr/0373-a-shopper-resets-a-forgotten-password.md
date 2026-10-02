# ADR 0373 — A shopper resets a forgotten password

**Summary:** `contrib/identity-session` mounts a storefront password reset
behind the same proof as self-registration: a single-use link sent to the
address of an account, which replaces the password and signs the person in.

- **Status:** Accepted
- **Date:** 2026-10-02
- **Amended by:** [0374](0374-a-replaced-password-ends-the-sessions-before-it.md): a reset ends every session issued before it

## Context

ADR 0133 gave a shopper their own account, and its registration answers an
address that already has one with a message saying so. Where that message could
send somebody who forgot their password, there was nothing: only an operator
could replace a password, through the admin endpoint. A storefront offering
accounts had no "forgot my password" to link to.

## Decision

`POST /store/v1/auth/password-reset` answers 202 for any address and sends a
single-use link to one whose credential is here, and `.../confirm` takes the
link and a new password, replaces the credential and signs the person in. The
flow is mounted only when the installation binds a `PasswordReset` messenger
and a store that keeps pending resets.

## Consequences

- The answer is the same for an address with an account and one without, and
  only the first is mailed, so the endpoint is neither an oracle nor a way to
  mail strangers from the shop.
- The messenger and the store capability are optional interfaces of their own;
  `Verification` and `PersonalRecords` are published and keep their methods.
- Migration 000003 adds `customer_password_resets`, holding the customer, the
  link's hash and its moments, never the address. Its foreign key to the
  credential cascades, so erasing the credential erases the pending reset; a
  dossier shows it without the hash.
- A new password is checked before the link is spent, and the link is spent
  before the credential is written, in one statement, so it works once.
- Registration and reset share one rate limit per client: each sends mail.
- A reset signs nobody out. A session is a signed cookie with nothing behind it,
  so one issued before the reset works until it expires (`docs/known-limits.md`).
- The starter binds its log-only messenger for the reset too.

## Rejected

- A third method on `Verification`: every shop implementing it would stop
  compiling for a flow it may not want.
- Mailing an address with no account "you have no account here": the endpoint
  would mail any address a stranger typed.
- Revoking sessions on reset: it needs a session record, which ADR 0127 chose
  not to keep.
