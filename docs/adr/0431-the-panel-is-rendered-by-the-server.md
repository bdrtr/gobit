# ADR 0431 — The panel is rendered by the server

**Summary:** The panel's screens are rendered by the server and reach the modules through their admin surfaces; the review screen stays its one browser client of `/admin/v1`.
It costs an admin surface beside each API a screen writes through, and keeps every screen under tests this repository runs.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Supersedes:** [0030](0030-the-panel-becomes-an-admin-api-client.md), whose single-page client was not built past one screen
- **Amends:** [0076](0076-the-panels-migration-begins-with-the-review-screen.md), whose migration stops at the screen it began with

## Context

ADR 0030 decided the panel would become a client of `/admin/v1`, served as
static assets, every read and write going over the API an external client
uses. ADR 0076 moved one screen, the review queue, to that shape. Every screen
since was built as a server-rendered form that writes through a module's admin
surface (ADR 0013): `internal/adminui/templates/` holds forty-four templates,
and `internal/adminui/assets/reviews.js` is the panel's one script. ADR 0290
refused a browser screen because its behaviour would carry no test this
repository can run. ADR 0030 still read "current" while the tree did the
opposite (D34).

## Decision

The panel's screens are rendered by the server and reach the modules through
their admin surfaces, and a new screen is built that way. The review screen
stays a client of `/admin/v1`, and no other screen of the panel's own moves to
the browser.

## Consequences

- A screen's reads and writes run in-process, under tests this repository
  runs, as the panel's screens since ADR 0013 do.
- A module a screen writes to carries an admin surface beside its API, which
  internal/arch pins to its module; no module gains one before a screen needs
  it.
- ADR 0076's widened cookie and its same-origin check on `/admin/v1` stay, for
  the review screen, for a plugin's screens (ADR 0155) and for an embedder
  calling the API from a browser.
- A plugin's screen is still a script that fills a shell, and a shipped screen
  still takes no section (ADR 0416); this record decides the panel's own
  screens.
- This reopens when a screen needs behaviour a server form cannot give, such as
  live updates, or when the panel ships apart from the binary.

## Rejected

- Finishing ADR 0030's migration: more than forty screens rebuilt in the browser, with no test this repository runs.
- Moving the review screen back to the server: it works, and it is tested where it stands.
- Leaving ADR 0030 current: a decided future written as a present fact is the class of defect D34 names.
