# ADR 0346 — The last administrator keeps admin

**Summary:** A write that would take admin from the last live user holding
it, by narrowing their scopes or deleting them, is refused with
`auth_last_administrator`; the write locks every live administrator in its
transaction, so two writes on the last two cannot both pass (D209).

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The first administrator is seeded only into an installation with no users
at all, so the seed never touches a running shop's privileges. Nothing
else kept an administrator: the admin API could narrow the last one's
scopes or delete them, and the shop could then be managed again only by
editing the database (D209). A panel form that changes privileges would
put that one press away.

## Decision

The auth repository refuses, inside the write's transaction, to take admin
from a user who is the last live user holding it, after locking every live
administrator in the order of their ids. Narrowing a user's scopes and
deleting a user are both held to it, through the API and every caller of
the service.

## Consequences

- A shop always keeps one user who can sign in and grant privileges.
- Two administrators taking admin from each other at once: the second
  waits for the first and is refused, rather than both passing on a count
  each read before the other wrote.
- A delete, and a write narrowing scopes to ones without admin, lock the
  administrators' rows; a write that keeps admin or leaves the scopes alone
  takes no lock.
- An installation run by an admin API key alone is not counted: a key
  cannot sign in to the panel, and the rule is about a person who can.
- An administrator may still take admin from themselves while another
  remains.

## Rejected

- A check before the write, outside its transaction: two writes on the last
  two administrators would each see the other and both pass.
- Seeding an administrator again when none is left: the seed's promise is
  that it never touches a running installation, and a forgotten seed
  password would become a way back into one.
- Refusing an administrator taking admin from themselves: with another
  administrator left it is an ordinary change of hands.
