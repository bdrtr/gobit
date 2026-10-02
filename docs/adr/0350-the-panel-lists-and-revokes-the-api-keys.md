# ADR 0350 — The panel lists and revokes the API keys

**Summary:** An API keys screen lists the auth module's keys under
`auth:read` through `auth.admin`, the open ones first, each with its token
only as redacted, its privileges and when it was last used; an operator
holding admin revokes one, which stays listed with when and by whom.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

Integrations call the API with keys, and a key that leaks has to be
revoked before anything else, but the keys were listed and revoked only
through the admin API. An operator who learnt of a leak had no screen to
find the key on, nor a button to close it.

## Decision

The auth module's panel surface lists a page of the keys, the open ones,
the revoked ones or every one, and revokes a key in the operator's name
through the service the API's revoke calls. The panel's API keys section
shows them to an operator who may read the users, the open keys first,
and offers the revocation to one holding admin.

## Consequences

- A leaked key is found and closed where its privileges and its last use
  are read.
- The token crosses the surface only as redacted; its hash does not cross.
- A revoked key stays listed with when and by whom, so which key was open
  can still be answered after a leak; revoking it again is refused.
- Making a key, whose token is shown once, is not offered here.

## Rejected

- Deleting a key from the panel: a deleted key leaves the list, and the
  record of what was open goes with it; the API keeps the deletion for a
  key made by mistake.
- Asking the operator to confirm by typing the key's title: a revoked key
  is replaced by making another, and the press that matters is the fast one.
