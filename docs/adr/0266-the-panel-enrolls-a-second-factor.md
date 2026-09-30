# ADR 0266 — The panel enrolls a second factor

**Summary:** The admin panel has a Second factor screen, open to everybody who
can sign in, that enrolls, proves, replaces and removes the person's own
authenticator through the auth module's panel surface; the panel's door sends
a person no privilege opens a screen for there.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

Since ADR 0265 an installation can require a second factor, and a person who
owes one holds no privilege until they enrol. The panel offered no way to
enrol: its door answered such a person 403, and the known limits said they had
to call the API by hand.

## Decision

The auth module registers `auth.admin`, a surface in primitives with the
person's status and the enrolment, confirmation and removal, and the panel
resolves it optionally and draws a screen every session opens. The door sends
a person to the first screen a privilege opens, and to this one when none
does.

## Consequences

- The screen acts on the person the session proved and takes no identifier,
  as the API endpoints do, and each act is the auth service's own, so
  replacing or removing a proven factor takes its current code (ADR 0264).
- The secret is printed once, right after it is drawn, as text and as an
  `otpauth://` link; there is no QR code, because drawing one would take a
  dependency the panel does not carry. The link is exempt from the template's
  URL filter only after its scheme is checked.
- An owing person reads why no other screen opens; an account granted nothing
  and owing nothing lands on the same screen instead of a 403.
- The panel's open routes are nine, counted exactly, and the surface, its
  container name and the two new refusal codes are pinned against the auth
  module in `internal/arch`.
- An installation that binds its own identity without the surface gets a
  screen that says so, as the write surfaces do.
- Recovery codes are still absent; the known limit keeps them.

## Rejected

- Drawing a QR code: a dependency, or an encoder written here, for what a
  link and a typed key already do on a phone.
- Resolving the service itself rather than a surface: its enrolment returns
  the module's own type, which the panel may not import.
- Keeping the 403 at the door for an account granted nothing: the screen it
  can open is the one thing it can do.
