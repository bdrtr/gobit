# ADR 0345 — The panel lists the users

**Summary:** A Users screen lists the auth module's users under `auth:read`
through `auth.admin`, the newest first, each with the privileges they hold
and whether they have proven an authenticator, those without one on a tab
of their own, and finds one by e-mail.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

An installation may require a second factor from a date on (ADR 0265), and
the admin API can list the users who have not proven one, but the panel
showed an operator only their own factor and sessions (ADR 0266, ADR 0268).
Who may do what in the shop, and who still owes a factor, were answered
only by calling the API.

## Decision

The auth module's panel surface lists a page of the users, the one with an
e-mail or those with or without a proven authenticator, each marked as the
API's listing marks them. The panel's Users section shows them to an
operator who may read the users, every user first, then a tab for those
without a second factor and one for those with.

## Consequences

- Rolling out a required second factor has a screen: the tab of those
  without one is the list still to reach.
- The surface that held a person's own account now also lists the users;
  its name stays, and what it lists is read under `auth:read`.
- A privilege is printed as the name the API grants it under.
- Nothing about a user is changed here: granting a privilege is the
  admin's (`admin` itself), and a user page that writes is a later
  decision.

## Rejected

- A query-layer entity for the users: the auth provider publishes the sales
  channels, and a user's privileges are not data another screen joins.
- A second surface for the users: the panel resolves one surface per module,
  and the auth module's already reaches the panel.
- Filtering by privilege here: the listing can, but a shop's privileges are
  read off the rows of a page, not chosen from a list nobody keeps.
