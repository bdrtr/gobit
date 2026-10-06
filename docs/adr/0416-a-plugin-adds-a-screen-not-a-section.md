# ADR 0416 — A plugin adds a screen, not a section

**Summary:** A plugin reaches the panel through a screen of its own (ADR 0155); gobit ships no slot through which a plugin's script adds a section or a column to a screen the panel ships.
It costs a plugin's fact about a customer or an order a second screen, and keeps every shipped screen's content this binary's.

- **Status:** Accepted
- **Date:** 2026-10-06

## Context

ADR 0155 lets a plugin register a screen whose script the panel serves under its
own content policy, and refused a template slot, since every plugin's markup
would live in this binary (ADR 0030). Feature row A8.15 also asks for a content
slot: a plugin's script adding a section to a screen the panel ships. It was
measured on 2026-09-12 and refused in a commit message only, on the premise
that its one candidate, webpush's devices on the customer screen, could never be
current because gobit issues no customer identity. The premise is wrong:
webpush believes the storefront's claim of who is signed in (ADR 0008), and
`contrib/identity-session` proves one (ADR 0127). The devices per customer are
already an admin read, `GET /admin/v1/webpush/subscriptions?customer_id=`.

## Decision

**A plugin reaches the panel through a screen of its own, which may take a
customer or an order as a query parameter; gobit ships no slot through which a
plugin adds a section or a column to a screen the panel ships. This reopens when
a plugin in this tree must show, on a screen the panel ships, a fact that its own
screen cannot carry.**

## Consequences

- A shipped screen's markup and its privilege walk (ADR 0156, ADR 0260) cover
  everything on it; nothing a plugin writes appears there.
- A plugin's view of a customer or an order is its own screen, under its own
  scope, reached by URL; the panel adds no link to it from a shipped screen.
- No extension point exists that nothing calls, so none needs a gate to prove
  it wired; the client-side extension point ADR 0030 counted on is the screen.
- An operator scoped to one region still reads every order (ADR 0156,
  `docs/known-limits.md`); that limit is not decided here.

## Rejected

- **A section slot on the customer and order screens.** Its one candidate is served by webpush's own read, and a slot nothing fills stays green.
- **A template slot.** Refused by ADR 0155 and ADR 0030.
- **A link list on shipped screens filled by plugins.** It is the slot's registry with less content, for the same absent consumer.
- **Reopening when an installation writes such a plugin.** That consumer is outside the tree (ADR 0390).
