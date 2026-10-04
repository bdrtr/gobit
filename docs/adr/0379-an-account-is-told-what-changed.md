# ADR 0379 — An account is told what changed

**Summary:** `contrib/identity-session` tells an account's address that its
password was replaced, by a reset link or by its owner, and tells the address
an account left that it moved, through an optional `AccountNotices` seam.

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

A shopper can replace their password through a reset link (ADR 0373) or with
the current one (ADR 0375), and move their account to a proven address (ADR
0377). Each is a change somebody else could make with a stolen session and a
guessed or reused password, and none of them told anybody: the person whose
account it was learned of it when they could no longer sign in.

## Decision

The module calls `AccountNotices.SendPasswordChanged` with the account's
address after a password is replaced by a reset or a change, and
`SendAddressChanged` with the old and the new address after an account moves.
A notice is sent after the change has happened, and one that cannot be sent is
logged rather than failing the request.

## Consequences

- The address an account left is the one told of the move, since the person
  who lost the account still reads it.
- A refused change, a wrong current password or a stale link, tells nobody.
- An operator's replacement through `PUT /admin/v1/customer-credentials` sends
  nothing: the module cannot tell it from creating a credential.
- The seam is optional, its own interface beside `PasswordReset` and
  `AddressProof`; nil sends no notice and changes nothing else.
- The starter's log-only messenger writes both notices to its log.

## Rejected

- Failing the request when the notice fails: the change has been made, and an
  error would tell the person it had not.
- Telling the new address of a move: it proved itself a moment ago, and the
  notice is for the person who may not have made it.
