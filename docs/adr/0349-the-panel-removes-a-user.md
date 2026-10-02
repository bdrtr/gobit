# ADR 0349 — The panel removes a user

**Summary:** A user's page removes them under `admin` through
`auth.admin`, their login identities with them, the last administrator kept
(ADR 0346); an operator's own page offers no removal, and one sent for them
is refused with `panel_remove_self`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel invites a colleague and changes their privileges (ADR 0347, ADR
0348), but one who left was removed only through the admin API. The API
lets an administrator remove themselves too, which from a panel page ends
the session the page was drawn in.

## Decision

The auth module's panel surface removes a user through the service the
API's delete calls, and a user's page offers it to an operator holding
admin. The panel refuses an operator removing themselves, without asking
the module, and does not offer it on their own page.

## Consequences

- A colleague who left is removed where they were invited; their sessions
  end on their next request, since a deleted user's token is refused.
- The last administrator is kept, as through the API (ADR 0346).
- An operator who wants to leave asks another administrator, or uses the
  API, which still allows it.
- The removal is a soft delete: the user's e-mail is free for a new user,
  and what they did keeps naming them.

## Rejected

- Allowing an operator to remove themselves from the panel: the press
  would end the session drawing the page, with nothing on screen to say so.
- A typed confirmation: a person removed by mistake is invited again under
  the same e-mail, and the removal that could not be recovered from, the
  last administrator's, is refused.
