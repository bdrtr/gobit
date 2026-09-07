# ADR 0035 — The schema vocabulary is published, and the silence gets a voice

- **Status:** Accepted
- **Date:** 2026-09-07
- **Phase:** after the roadmap

## Context

`openapi.Describer` is an OPTIONAL capability: a module that implements it writes
its endpoints' bodies into `/openapi.json`, and a module that does not still
appears there with its path, its method and its security. The interface was
deliberately kept off `module.Module` — a required method would have produced a
crop of empty implementations for modules that genuinely have nothing to add.

The package holding it lived under `internal/`, and Go's own `internal` rule
made that placement a decision rather than a detail: **a module written
outside this repository could not implement the interface at all.** Not "chose
not to" — could not name the type.

This was not predicted. It was measured, and the proof was already in the tree:
`examples/starter/loyalty` is a module in a SEPARATE Go module, written to prove
that the published contract is enough to build a commerce module from outside.
It binds `GET /store/v1/loyalty/balance`. That endpoint went into the document
bodiless, and **nothing anywhere said so.**

`core/personaldata` named this as a live defect in its own package doc on
2026-09-06, while arguing why the erasure vocabulary had to be published:
> For a schema that costs a missing path. For an erasure it would cost a green
> build and a false answer to a person who asked to be forgotten.

That argument was accepted for erasure. It is the same argument, and it was left
unapplied to the package it was drawn from.

**The second half is the silence.** `Doc.UnmatchedDescriptions` answers "a
description with no route" and has since the beginning. The mirror — "a route
with no description" — did not exist. So even an in-tree module that simply
forgot produced no signal: a bodiless endpoint does not look missing, it looks
like an endpoint that takes nothing.

## Decision

**The schema package moves out of `internal/` and becomes `core/openapi`, the
sixteenth published package.** And `Doc.UndescribedRoutes` is added, reported at startup and gated by
a shrink-only ledger.

The membership rule in `internal/arch/public_surface_test.go` is not a matter of
taste: a package belongs in the published tree when a program outside this
repository must NAME it to compile. An embedder's own module is exactly such a
program and `Describe(d *openapi.Doc)` is exactly such a name. The package
qualifies under the rule as written; leaving it out was an omission, not a
judgement.

**Publishing it adds no new external dependency to the surface.** `Doc.Build`
takes `chi.Routes`, and chi is already in the published tree — `module.Module`
requires `Routes(chi.Router)`. That was the objection worth checking before
deciding, and it does not hold.

**`Doc.UndescribedRoutes` is the mirror of `UnmatchedDescriptions`**, built from
the same two maps the document already keeps. `checkSchema` in `internal/app/setup.go` logs it
as a warning beside the existing one. It warns rather than refuses on ADR 0007's
distinction: a schema is documentation, not the product's correctness, and
closing the store over a missing body costs more than the missing body.

**The gate is a ratchet, because the first measurement was 38.** The audit lives
in `internal/e2e` — the only place it can, see below — and compares the real
document against `internal/e2e/testdata/undescribed_routes.txt`. That list MAY
ONLY SHRINK, the same instrument this repository already uses for the language
debt, and for the same reason: a gate that goes red for a debt nobody can pay in
one commit is a gate somebody deletes.

Both directions are enforced. A route missing from the ledger is new debt. A
route ON the ledger that has since been described is a stale line and fails too,
because a paid line left behind quietly forgives the next endpoint somebody
forgets.

## What the measurement found, and it is two different problems

**Sixteen of the thirty-eight are one root cause, and the tree had already
diagnosed it — twice, independently.** An OpenAPI component's name is derived
from the Go type name, so `customer/api.addressDTO` and `cart/api.addressDTO`
both want the component `Address`, and `payment/api.collectionDTO` and
`product/api`'s both want `Collection`. Two types wanting one name does not
degrade one endpoint: `Doc.Build` FAILS and `/openapi.json` returns 500 for
EVERY module. Both modules therefore chose to leave their endpoints bodiless
rather than take the whole document down, both wrote down why, and both
concluded — in the same words — that the fix is a namespace decision belonging
to the core.

They were right, and this ADR does not make that decision; it makes it visible
and counted instead of resting in two package comments nothing reads. What it
does change is that the sixteen can no longer grow to seventeen quietly.

**The other twenty-two have no reason on file at all.** Webhook and search
endpoints from the two plugins, the shipping-option surface, and ten order
endpoints. Nothing anywhere explains them; they are simply endpoints whose
`Describe` was never written, which is precisely the omission that had no way of
being seen before today.

## Rejected alternatives

**Publish a thin vocabulary package instead, and keep the builder internal.**
This is what `core/personaldata` does — types and interfaces only — and the
symmetry is attractive. It was rejected on cost against capability: an
out-of-tree module would still need `Operation` and `Parameter`, so the data
types get published either way, and what stays behind is only `Build` and
`Handler`. Buying their concealment costs a second name for one concept, a sink
interface, and a changed signature on thirty-five existing `Describe` methods —
for an embedder who can then do strictly less.

**Make `Describe` part of `module.Module`.** Rejected for the reason the
interface's own godoc already gives, which this ADR does not overturn: an
undescribed module is a valid model, and a required method produces empty
implementations that assert nothing. The problem was never that describing was
optional; it was that NOT describing was invisible.

**Put the coverage audit in the unit tree.** Attempted first, and it failed in
the most instructive way available: a module's `Routes` binds nothing until its
services exist, and its services do not exist without a database. A walk over
modules built from an empty config found ZERO routes, and the assertion over it
PASSED — reading nothing, proving nothing. It was caught by the blindness guard
written alongside it, which is the only reason this ADR does not record a green
test that measured an empty set.

## Consequences

**Positive**

- **An out-of-tree module can describe its endpoints**, and one does:
  `examples/starter/loyalty` implements `openapi.Describer` in a separate Go
  module, which is the compile-time proof that the impossibility is gone.
- **A forgotten description is now visible three ways**: at startup as a warning,
  in the e2e gate as new debt, and in a ledger with a number on it.
- **The component-name collision has a number.** Sixteen routes, and a stale-line
  check that will notice the day the namespace decision is made and they come
  off the list.

**Negative, and accepted**

- **Thirty-eight endpoints are documented as bodiless rather than fixed today.**
  The ledger is honest about that and refuses to grow, which is what makes it a
  ratchet rather than a permission slip; it is not the same thing as having
  described them.
- **`core/openapi` is now a compatibility promise**, including `Operation`,
  `Parameter` and the component-naming rule. Component names are what a client
  generator turns into class names, so the namespace fix the sixteen are waiting
  for is a RENAME in the published contract. That is the price of publishing
  before making the decision, and it is accepted because the alternative was
  keeping an unusable interface unusable for longer.
- **The audit needs a database.** It runs under the `integration` tag with the
  rest of e2e, so a developer running `go test ./...` does not see it. That is
  the same trade every e2e gate here already makes, and the startup warning is
  what covers the gap for anybody actually running the server.

## Reopening the decision

Reopen the naming half — not the publish — the moment somebody needs two modules
to describe types of the same name, which is already true and is what the
sixteen are. The decision to make there is whether a component name gains a
module prefix always, only on collision, or by the module's own declaration; the
first is the only one that is deterministic and the only one that renames
components that work today.

## Related

- [ADR 0026](0026-the-published-surface-is-fourteen-packages.md) — the membership
  rule this decision applies, and the ADR whose own text named this defect.
- [ADR 0027](0027-tek-satirlik-kurulum-facade.md) — the facade an out-of-tree
  program boots through; the starter module that proves this decision is built
  on top of it.
- [ADR 0007](0007-sertlestirme-arizada-davranis.md) — why a schema failure warns
  instead of stopping startup.
