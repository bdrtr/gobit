# ADR 0255 — A panel route is its method and its path

**Summary:** The panel's scope table names each route by method and path and
lists its open routes with no privilege; a route the table does not list stops
the panel from being built.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

The panel's scope table was keyed by path (ADR 0156). The paths bound on both
verbs want one privilege each, so the key did not matter for them, but a POST
added later to a read path would have inherited the read privilege. The router
walk that audits the table could not tell the two apart, since both answer an
operator with no privilege correctly. A path the table did not list was bound
open, which is how the login, the sign-out, the stylesheet and the entry point
stayed open, and the one way any route could end up unchecked.

## Decision

A route in the scope table is its method and its path, and the open routes are
listed with no privilege. Binding a route the table does not list panics while
the panel is built, so a new verb on an old path arrives with a decision or not
at all.

## Consequences

- A POST on a read path inherits nothing: neither the read privilege nor an
  open door. Adding one means writing its line in the table.
- The table names five open routes, and the walk still requires exactly five
  routes with no privilege.
- The walk now also refuses a table entry the router does not bind, so a route
  that is removed takes its line with it.
- The menu and the asset cache read a screen's GET entry, which is the one a
  browser navigates to.
- A plugin's screen is entered for GET on its page and its script, as before.
- The panic is a programmer's error surfacing at startup, the way chi refuses a
  conflicting pattern; no request can reach it.

## Rejected

- Keeping the path key and adding a check for methods beside it: two lookups a
  binding line has to keep in step, which is the split the one table exists to
  avoid.
- Binding an unlisted route open, as before, and relying on the walk to refuse
  it: the walk is a test, and a route that nothing refused at startup would ship
  open in any build that skipped it.
- Returning an error from Routes: it is called while the router is assembled,
  where a route the table does not know is a fault in the panel itself, not in
  anything a caller supplied.
