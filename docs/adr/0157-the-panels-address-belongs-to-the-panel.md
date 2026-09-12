# ADR 0157 — The panel's address belongs to the panel

**Summary:** The content policy moves from the panel's route group to its
PREFIX, and a plugin may not bind a route inside that address — the way in is
`RegisterAdminPage`, which names a privilege.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0157](../measurements/0157-a-door-beside-the-door.md)

## Context

ADR 0155 installed the panel's content policy on ONE chi group, so that a
twenty-first route could not be written without it. The reasoning was right and
the SUBJECT was wrong: a group covers every route the panel binds, which is a
narrower thing than every response at the panel's address.

A plugin's `AddRoutes` runs on the same router, after the panel, and its only
check is a pattern collision. A pattern that collides with nothing is bound
outside the group. Measured with a probe rather than argued: such a route
answered 200 with an empty `Content-Security-Policy` and no `X-Frame-Options`,
while a panel route beside it carried both.

The identity and origin rings were never affected — the composition root scopes
those to the PREFIX, the shape the policy needed and did not have. So the hole
was one ring deep: an operator's browser, inside the session, on a page with no
policy. The privilege is a second and different hole: ADR 0156 prices every
panel path in the panel's own table, and a route the panel did not bind is in no
table, so a signed-in operator holding no grant reaches it.

## Decision

The policy is installed in the composition root's guard stack, scoped to the
panel's prefix and ahead of the origin and identity rings, so that every
response at that address carries it — including a refusal and a 404. And a
plugin may not bind a route inside the panel's address: the registry refuses it
at startup and the refusal names `RegisterAdminPage`, which carries a label, a
privilege and a script the panel serves from its own origin.

## Consequences

The sentence the policy states is now the sentence its audit checks. The gate
walks the prefix rather than the panel's route list, and its population
includes a route bound after the group and a path matching no route at all.

The prefix is written twice: by the panel, and by `core/plugin`, which may not
import `internal`. What holds them together is a BEHAVIOURAL check rather than a
comparison of two literals — the panel's own constant is handed to the registry
and the refusal has to fire. A drifted copy would guard an address nothing
serves, which reads like a rule and is not one.

A plugin that wanted a page of its own markup at the panel's address can no
longer have one. That is the point and it costs something real: the sanctioned
path renders the panel's shell, which a plugin does not choose.

The refusal matches on a segment boundary, so `/admin/uipload` still binds — a
plain prefix test would have turned that path away with a message about screens.
Nothing in the tree bound inside the panel's address, so no installation
changes.

## Rejected

**Fixing the policy and leaving the binding open.** The policy is one of three
rules that live at that address; a route bound beside the panel would still be
in no privilege table.

**Refusing the binding and leaving the policy on the group.** Then the rule
holds only as long as the refusal has no gap, and the first gap is a page with
no policy inside an operator's session. Two mechanisms, because the failure of
either is silent.

**A reserved-prefix list a plugin could extend.** The panel's address is not a
configuration; making it one would put "which rules apply here" in the hands of
the thing the rules are about.

**Exporting the prefix from `core/plugin` so a test could compare two literals.**
It would grow the published surface for a check that proves less than the
behavioural one.
