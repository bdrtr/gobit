# ADR 0348 — The panel invites a user

**Summary:** The Users screen opens a user under `admin` through
`auth.admin`, without a password and with the privileges ticked, none when
none is, and the auth module sends them an invitation to set their own; a
user's page sends the invitation again.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel lists the users and changes their privileges (ADR 0345, ADR
0347), but a new colleague was opened only through the admin API, in two
calls: one that opens the user, which opens an administrator when no
privilege is named, and one that invites them (ADR 0137).

## Decision

The auth module's panel surface opens a user without a password and sends
them an invitation from the operator, taking no privilege given as none,
and names the user it opened even when the invitation could not be sent.
The Users screen offers the form to an operator holding admin, and the
user's page sends an invitation again.

## Consequences

- A colleague is invited where their privileges are later changed, with
  the same boxes.
- The surface never opens an administrator by omission: an empty list is
  sent as no privilege, while the API keeps its default.
- An invitation the notification module could not carry leaves the user
  opened; their page says so and sends it again, a new one replacing the
  one pending.
- Nobody sees the invitation's link, the operator included (ADR 0137).

## Rejected

- Opening a user with a password typed by the operator: the operator would
  know a colleague's password, which the invitation exists to avoid.
- Undoing the user when the invitation cannot be sent: deleting is a second
  write that can fail too, and the page can send the invitation again.
- Offering admin ticked by default, as the API defaults: a box nobody
  unticks would make every colleague an administrator.
