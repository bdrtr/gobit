# ADR 0156 — A panel screen costs a privilege

**Summary:** Every panel path is listed with the scope it requires, spelled as
the API spells it; the route refuses, the menu hides and the door redirects. It
costs a second spelling of every privilege, held to the modules' by a gate.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0156](../measurements/0156-the-second-door.md)

## Context

The admin API names a scope on seventy-one routes, and `internal/e2e`'s
authorization matrix proves end to end that a valid identity holding none is
answered 403. The panel is the second admin surface over the same data and
checked identity alone: its ring resolves a principal, puts it in the context,
and reads back only whether one is there at all.

An operator with a narrow grant is not hypothetical: `POST /admin/v1/users`
takes a scope list and `PATCH` changes it. Such an account signs into the panel
and reads the whole catalog, every customer, the sales report and the stock —
and edits a product — because the screens go straight to `core.query` and to
the module write surfaces, neither of which knows anything about principals.

The evidence was in the tree: six panel tests build a principal with an empty
scope list and assert the screen renders, and the composition root's own fake
authenticator returned one. Nothing was wrong with those fixtures — there was
nothing for them to carry.

## Decision

Every panel path is listed in ONE table with the privilege it requires, spelled
with the same string the module's API uses, and the route refuses an operator
who does not hold it with the panel's own 403 page naming the missing grant.
The menu drops the entries that operator cannot open, the entry point redirects
to the first screen they can, and a screen a plugin registers names its
privilege too — a registration without one is refused at startup.

## Consequences

The panel stops being a way around the API's scopes: an installation that
grants `customer:read` to nobody now means it on both doors.

The privilege is spelled twice: by the module for its endpoints, here for the
screen over the same data. The panel imports no module and core knows none, so
only a gate holds the two together — `internal/arch` reads both from source and
refuses a value no module declares. That catches a misspelling, not a screen
under a plausible WRONG privilege; the table's godoc carries that judgment. It
is keyed by path, not method and path, so a POST added later to a read path
would inherit the read privilege silently.

Nothing changes where every operator carries `admin` — that scope satisfies all
others, the rule the whole admin surface stands on.

The binding line names its path twice, to bind and to ask the table. Two router
walks hold it: one proves every route refuses an unprivileged operator, the
other that each demands the privilege its own path is listed under.

## Rejected

**A middleware on the route group, the way the content policy is installed.**
chi runs group middleware before it resolves the pattern, so it would have
nothing to look the screen's scope up by.

**Prefix matching.** The variant price form sits under the products prefix and
costs the pricing privilege, not the product one.

**Filtering the menu and leaving the routes open.** Hiding a link only stops
offering one; the refusal has to be the route's.

**A panel-specific vocabulary (`panel:customers`).** An installation could then
grant through one door what it refused at the other.

**An empty scope meaning "any operator who can sign in".** A plugin author who
forgot the field would publish an unguarded screen, failing at a click.

**Making the read layer scope-aware instead.** A larger, different decision —
principals in a read surface that has none — and not an answer to what a SCREEN
costs.
