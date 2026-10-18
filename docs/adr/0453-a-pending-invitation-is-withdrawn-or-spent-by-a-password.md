# ADR 0453 — A pending invitation is withdrawn, or spent by a password

**Summary:** `DELETE /admin/v1/users/{id}/invitations` voids a user's pending invitation under `admin`, and every password write for the user removes it in the same transaction.
It costs a resent link sent after a password was set, and buys the end of a mailed link that sets a password after the person chose one.

- **Status:** Accepted
- **Date:** 2026-10-17
- **Amends:** [0137](0137-a-colleague-sets-their-own-first-password.md), whose invitation lived out its seventy-two hours whatever happened to the account meanwhile

## Context

depremtek's backend, a consumer, invites a seller's member through gobit and
also hands the inviter a link of its own, which sets the password through
`POST /admin/v1/users/{id}/password` (D308). Spending that link left gobit's
mailed invitation valid for its seventy-two hours: whoever held the mail
could set the account's password again after the member chose theirs, which
is the replay ADR 0137 spends the token before the write to prevent. gobit
offered no route to void an invitation, so neither an operator who mailed
one to the wrong address nor the consumer could close it.

## Decision

`DELETE /admin/v1/users/{id}/invitations` withdraws the user's pending
invitation under `admin`, answering 204, or 404
`auth_invitation_not_pending` when none could still be accepted. A password
written for a user, through the operator's route or by accepting the
invitation, removes the user's invitation in the transaction that writes it.

## Consequences

- The mailed token is refused as `auth_invitation_not_usable` once the
  password is set or the invitation withdrawn, like a used one.
- An expired invitation is removed by a withdrawal and still answered 404:
  there was nothing to close.
- An operator who resends after a password was set sends a link that works
  until it is used or withdrawn; inviting is still a deliberate act.
- The withdrawal is an admin write, so the audit log names who made it.

## Rejected

- Voiding the invitation only on the routes a consumer uses: a password set any other way leaves the same link.
- Returning the token so a consumer could spend it: an administrator holding a colleague's first-password link is what ADR 0137 stops.
- Answering 204 when nothing was pending: the caller asked whether a link was open, and 404 says it was not.
