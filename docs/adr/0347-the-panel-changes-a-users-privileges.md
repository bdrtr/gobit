# ADR 0347 — The panel changes a user's privileges

**Summary:** A user's page shows them under `auth:read` and changes their
privileges under `admin` through `auth.admin`, from the ones the page was
drawn with; the auth module writes them only while they are, as a set, the
ones read, refusing `auth_user_scopes_revised` otherwise.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The Users screen lists who holds what (ADR 0345), but a privilege was
granted or taken only through the admin API's partial update, which writes
the list it is sent over whatever the user holds by then. Two operators
changing one user's privileges at once would each overwrite the other's
grant without seeing it.

## Decision

The auth module writes a user's privileges in one statement that matches
the set the caller read, refusing with `auth_user_scopes_revised`
otherwise, under the rules the API's update keeps: no privilege the caller
lacks is granted, and admin is not taken from the last administrator (ADR
0346). The user's page offers the form to an operator holding admin, the
privilege the auth module's API writes privileges under.

## Consequences

- An operator grants and takes privileges where they read them, and a
  change made meanwhile is refused rather than overwritten.
- The privileges are compared as a set: their order and repeats in the row
  are not a change.
- The form offers admin, every privilege a panel screen asks for, and any
  other the user holds; a privilege no screen asks for and the user does
  not hold is granted through the API.
- The panel's write gate takes admin as a write privilege, and the
  privilege walk counts admin as the auth module's, not as another one's
  extra.
- A privilege taken takes effect on the user's next request, since the
  privileges are read on every request.

## Rejected

- Matching on the moment the user was last written: a name or e-mail
  change would refuse a privilege change it does not touch.
- An `auth:write` privilege for the form: the auth module writes privilege
  itself, and one who can write it can grant themselves admin.
- A free-text privilege field: a mistyped name would be granted and never
  asked for, and the API takes one where it is needed.
