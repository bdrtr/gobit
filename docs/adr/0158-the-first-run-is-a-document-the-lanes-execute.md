# ADR 0158 — The first run is a document the lanes execute

**Summary:** The path from an empty database to a paid order is written down as a
pasteable block and a smoke scenario runs it; a document that chains commands
and nothing executes is refused by a population gate.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0158](../measurements/0158-fifteen-calls-nobody-wrote-down.md)

## Context

An operator cannot buy anything from a fresh installation until fifteen calls
have been made, and eleven of them were written down nowhere: they lived inside
two test harnesses, each of which creates its own region, price binding and
stock. Every lane was green, and the gap was invisible BECAUSE both harnesses
did the work themselves.

The one document that walks a reader from an empty database is `security.md`,
and it stops at reading the catalog. On an empty database that is an empty list,
so the call succeeding says nothing about whether anything can be bought.

`gobit seed` does not close it either: that verb rebuilds the load rig — fifty
two thousand products in bulk SQL — and creates no region. Its purpose is
measurement, not a usable shop.

## Decision

The first run is a document whose block a smoke scenario executes against the
real binary, asserting a status per binding and the order at the end. The rule
that forces this already existed — `internal/arch`'s chained-flow gate refuses a
document whose block feeds one request's output into the next and that no test
runs — and the new document joined it the only way it can: as a failure, until
its witness was registered.

## Consequences

Writing the path down found something neither harness could. The storefront
scenario binds TWO countries to its region, so it is taxed through the branch
that fires when no single jurisdiction can be named — the region's own rate. A
ONE-country region, which is what an ordinary first installation has, is taxed
by the tax module instead, and with no tax region configured there the cart came
back untaxed while the region carried a rate. The document therefore creates a
tax region and a default rate, and names the trap.

That trap is not a defect: the server warns in the log
(`tax_source=tax_unconfigured`) and the reasoning for taking tax's answer as it
is is written where the choice is made. What was missing was anybody saying so to
an operator.

The population rule cost nothing to extend because it was already there, and
finding that out cost a gate that was written and thrown away: it had been
searched for in the smoke package and under the word "documented", not in
`internal/arch` under the word it carries. The existing one is the better of the
two — its keys are derived from the documents, so it fails BOTH ways: an
unexecuted flow, and a witness named for a flow that has been summarised away.

The cost is a second executed document to keep running, and a scenario that
boots a real server and makes fifteen calls. It runs in the smoke lane, which
already boots one.

## Rejected

**Making `gobit seed` create a region.** That verb builds the load rig with bulk
SQL and produces no events; giving it business records would make one command
answer two unrelated questions, and the rig's own godoc is explicit about what
it is for.

**A Go test that performs the fifteen calls.** The repository has two already.
Their existence is what hid the gap: a Go re-implementation is free to stay
right while the document goes wrong, which is the defect rather than the fix.

**Extending `security.md`.** Its subject is the guard rings, and a reader
looking for "how do I take an order" would not open it. The population gate
keeps the two honest without merging them.

**A second population gate.** One was written here before the existing one was
found, and deleted. Two gates over one population is worse than one: the weaker
sets the standard, and a reader cannot tell which is the rule.
