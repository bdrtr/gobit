# ADR 0351 — The panel makes an API key

**Summary:** The API keys screen makes a key under `admin` through
`auth.admin`: a secret one carrying the privileges ticked, none when none
is, or a publishable one attached to the sales channels ticked; the answer
shows the token once and is not stored by the browser.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel lists and revokes the API keys (ADR 0350), but a key was made
only through the admin API, whose secret keys carry admin when no
privilege is named. A revoked key is replaced by making another, so the
screen that closes one was the place an operator would look to open its
successor.

## Decision

The auth module's panel surface makes a key of either type in the
operator's name, taking no privilege given as none, and returns its id and
its token, the token's only copy. The API keys screen offers the form to
an operator holding admin and shows the token on the answer to it, marked
not to be stored.

## Consequences

- A leaked key is replaced where it was revoked.
- The panel never makes an administrator's key by omission: an empty list
  is sent as no privilege, while the API keeps its default.
- The token is on one answer only; a token lost is not brought back, and
  the key is revoked and another made, as through the API.
- The type rules are the module's: a publishable key carrying a privilege,
  or a secret one attached to a channel, is refused on the screen.
- Refreshing the answer sends the form again and makes another key.

## Rejected

- Redirecting to a page that shows the token: the token would have to be
  stored somewhere a second request reads, which the module refuses to do.
- Offering admin ticked by default, as the API defaults: a box nobody
  unticks would put an administrator's credential in every integration.
