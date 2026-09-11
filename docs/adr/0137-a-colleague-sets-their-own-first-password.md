# ADR 0137 — A colleague sets their own first password

**Summary:** An administrator invites a user, the notification module's new
cross-module surface carries it, and the invited person sets their own first password
through a second unprotected admin endpoint. It costs that surface and one table, and
ends one person choosing another's secret.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

Adding a colleague meant one of two wrong things. `CreateUser` takes a password, so
the creating administrator typed it and told them — one person knowing another's
secret, and the shop's record of who can act as that user false from the first
minute. Or it was called without one, which writes no `auth_identity` row and leaves
an account that cannot log in with nothing saying why.

The fix needs a message to go out and gobit could not send one: the only way anything
here caused a notification was to PUBLISH AN EVENT, and an event is durable in a
stream, delivered at least once, and FORWARDED to whatever endpoints an operator
registered — with a gate that fails the build in both directions. A one-time token
cannot be any of those.

So the notification module had no cross-module surface at all — one of the two
modules without an `interop.go`.

## Decision

The notification module gains `notification.interop` with one primitive-typed
`Send`, and the auth module gains an invitation: `POST
/admin/v1/users/{id}/invitations` opens one and has it carried, `POST
/admin/v1/auth/accept-invitation` spends it and sets the first password.

## Consequences

The token reaches the sender and never the response. An invitation handed back over
the admin API would be an administrator holding a colleague's first-password link,
which is the thing this flow exists to stop.

The accept endpoint is the SECOND unprotected admin path, and has to be: the person
calling it has no account yet. It uses login's own mechanism — a published path
constant in `GuardOptions.AdminExempt` — so the exemption follows the path. It is
exempt from identity only; the audit ring and the rate limit sit outside that ring
and still apply.

The token is consumed by `DELETE … RETURNING`, one statement, so single use needs no
lock and holds whatever the timing. It is spent BEFORE the password is written, because
the other order leaves a link that could later reset a changed password.

Accepting does NOT hand back a session. The storefront's registration does (ADR 0133)
because proving an address there IS the account; here the person has a password and an
ordinary login to make with it, and a session from an unauthenticated endpoint would be
a second way to get one.

A link lasts seventy-two hours where the storefront's lasts one: a shopper who
registers is at the keyboard, and a colleague may be away for a weekend.

Inviting again REPLACES the pending invitation: one live link per account, because two
is two chances for whoever finds one.

`Send` carries a secret in its data map, safe by default rather than by convention: the
log-only provider records that map's KEYS and never its values.

Measurement: [measurements/0137](../measurements/0137-who-chooses-the-first-password.md)

## Rejected

**An event carrying the token.** Durable in a stream, delivered at least once, forwarded
to third-party endpoints by a gate that fails in both directions.

**A seam the installation binds.** Right for `contrib/identity-session`, which is
optional and outside gobit's graph; an operator inviting a colleague expects gobit's
own admin to work without wiring.

**Returning the token to the inviting administrator.** It is the defect this record
closes, with an extra step.

**Signing the person in on acceptance.** A second way to obtain an admin session, from
the one endpoint that cannot ask who is calling.
