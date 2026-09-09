# ADR 0095 — A rate can stand on another

**Summary:** A tax rate may declare that it stands on another rate of the same
region, and the line is then taxed by the whole stack — each component floored
on its own base.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

gobit chose exactly ONE rate per line. Markets that levy two taxes on one line,
the second computed on the first, could not be expressed at all: the merchant
could only configure the sum of the two rates, which is a different number
whenever the second compounds.

## Decision

A rate carries `stacks_on_id` — the rate it stands on, in the same region — and
`compound`, which says whether it is computed on the taxes below it as well as
on the line's own amount. Rate SELECTION does not change: exactly one rate is
still chosen, and the chosen rate is then expanded into the stack it heads.

Each component is computed and floored on its own base, and the line's tax is
their sum.

Measurement: [measurements/0095](../measurements/0095-stacked-tax-rounding.md).

## Consequences

A rate standing on another is never a candidate: it may not be the region's
default and it may not carry rules, both refused at write time and dropped from
the candidate pool again when the table is built. The scope of a stack is the
BASE's scope, so a stack has one set of rules rather than one per component.

The stack is a LIST, not a tree: at most one rate stands on any rate, held by a
partial unique index, so its order needs no position column and cannot be
ambiguous. The chain is capped at four components — one deeper than the deepest
real stack anybody configures — because a five-level tax chain is one nobody can
verify by eye.

Prices that INCLUDE their tax cannot carry a stack, and the pair is refused from
both ends: when a stack is written under an inclusive chain, and when an
inclusive province is opened over a chain that already stacks. The reverse
computation is defined for one rate (ADR 0086); peeling n components apart would
have to assign the residue to one of them by fiat.

The rates of a stack cannot together take more than the line. Each rate is
bounded on its own and nothing bounded their sum, so the guard prices the whole
chain at write time with ceiling rounding — strictly more pessimistic than the
arithmetic that will run.

**A line's stored rate is the stack's BASE.** It is a rate really applied on an
amount really recorded, so every existing reader stays correct, and it is
INCOMPLETE rather than wrong: an invoice for a stacked line prints the base rate
alone. Carrying every component to the document is the next change, and it is
written down in `known-limits.md` rather than left implicit.

The expansion costs no query: the region's rates are already loaded for the
selection, so the walk is over memory.

## Rejected

**A cycle guard in the walk.** Written, then measured unreachable and deleted:
the walk starts at the SELECTED rate, a selected rate stands on nothing, and
every step follows a rate's single base — so the chain cannot return to a start
that has no base. The depth cap remains, because a hand-written chain past the
service can still be too deep.

**Changing a rate's place in a stack by UPDATE.** Where a rate sits is part of
what it is; moving it would reprice every line that follows while the rate kept
its identity. To restructure, retire the rate and write another.

**A position column instead of a linked list.** Two rates could then claim one
position, and the order of a stack decides the money.

**Summing the rates and applying one.** That is what a merchant has to do today,
and it is the wrong number whenever a component compounds — the reason this
record exists.
