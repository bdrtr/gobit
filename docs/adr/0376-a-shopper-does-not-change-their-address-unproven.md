# ADR 0376 — A shopper does not change their address unproven

**Summary:** `PUT /store/v1/customers/{id}` takes every profile field but the
e-mail address, and a body carrying one is refused; an operator changes it.

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

The storefront's profile update decoded the operator's body, `email` included,
and wrote whatever address a proven shopper sent, with nothing proving they
owned it. The address is the one an account is mailed at and, with
`contrib/identity-session` bound, the one its credential signs in under — which
the update did not touch. A shopper could take a stranger's address: the mail
the shop sends an account, a stock or price alert, went to the stranger, and
when the stranger tried to register, the address belonged to an account and
they were told they had one.

## Decision

The storefront update decodes its own body, which has no `email` field, so a
body carrying one is refused as a field the endpoint does not know. The
operator's update keeps the field.

## Consequences

- Integrators that sent `email` to the storefront update get 422
  `customer_invalid_body` and nothing is written, the other fields included.
- The published request schema of the storefront update no longer lists
  `email`.
- A shopper who wants another address asks the shop until a flow that proves
  the new one exists; nothing about the address an account already has changes.

## Rejected

- Writing the address and the credential's together: the storefront still has
  no proof the shopper owns the new one.
- Ignoring the field: a client would believe the address changed.
