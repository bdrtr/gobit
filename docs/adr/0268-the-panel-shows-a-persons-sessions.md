# ADR 0268 — The panel shows a person's sessions

**Summary:** The admin panel has a Sessions screen, open to every session,
that lists the person's open sessions with the current one marked and closes
one or all the others through the auth module's panel surface.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amended by:** [0276](0276-a-session-names-the-browser-that-opened-it.md): the screen names each session's browser

## Context

ADR 0267 let a person close one of their sessions through the API. An
operator who signs in to the panel, which is where most operators sign in, had
no way to see their sessions or close the one a lost laptop holds without
calling the API by hand.

## Decision

The auth module's panel surface, `auth.admin`, gains the session listing as
JSON and the two closings, and the panel draws a screen every session opens
listing the person's open sessions, each closable but the current one, with a
button closing all the others.

## Consequences

- The surface's type is now `AccountSurface`, the person's own account; the
  panel resolves it under the same name as `SecondFactorAdmin` and
  `SessionAdmin`, each optional and each pinned in `internal/arch`.
- The listing crosses to the panel as JSON whose tags are the contract, as the
  stock levels do; an `internal/app` test reads it through the real surface.
- The current session carries no close button: signing out closes it and
  every other, so a person cannot close the session they are looking with by
  a click that looks like closing another.
- The panel's open routes are twelve, counted exactly, and the no-privilege
  walk names the three with why.
- A session is shown by when it began and ends; the known limit that it names
  no device stands.

## Rejected

- One Account screen holding both the factor and the sessions: the menu names
  what each screen does, and a person looking for one would not look under a
  word that names neither.
- Returning the service's own session type: the panel may not import it.
