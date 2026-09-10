# ADR 0116 — A cardinality can be widened

**Summary:** A declared link cardinality may move to a freer one, and the
declaration drops the indexes that freer constraint no longer needs; narrowing
stays refused. It costs a release that cannot be rolled back through this path
and buys the change three places in the tree already promised was free.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

ADR 0114 left an exchange that owes money OPEN after its goods leave and named
the trigger — "the day the order-to-payment link becomes one-to-many". The
payment module's link definition says it in the imperative: "That day this
becomes OneToMany and nothing else changes." The capability list agrees.

None of the three had been tried, and nothing else changes is false in the
strongest way: the declaration would not start. `core/link` compares an incoming
definition against a durable ledger row for EQUALITY, so a changed cardinality
is a startup conflict. That comparison's godoc says what it was written for — a
NARROWING would apply a constraint without seeing the rows that already violate
it — and equality refuses the other direction for a reason that does not apply.

The schema half is the same shape. The DDL is `IF NOT EXISTS` throughout, so a
looser declaration creates nothing and removes nothing: the index built under
the old cardinality survives and keeps enforcing it.

## Decision

A declaration whose sides are unchanged and whose cardinality is WIDER than the
stored one is applied: the ledger row moves and the indexes the new cardinality
does not require are dropped, in the transaction that already holds the
declaration lock. Every other difference, a narrowing included, stays a conflict.

## Consequences

The safety argument is one sentence, and it is the whole reason this direction
is different: every pair a narrower cardinality admits is admitted by a wider
one, so a widening reads no data to know it is safe. A narrowing would have to.

`verifySchema` gained the other half of its question: it asked whether the
required indexes exist and now asks whether the rest are GONE. Without it the
failure reaches an operator as a record already bound under a cardinality that
permits it.

A release that widens a link CANNOT be rolled back through this path: the older
binary declares the narrower cardinality and is refused. The refusal names the
allowed direction, because the reader who hits it is rolling back.

The drops run on every startup rather than only the one that widens, so the
schema converges on the declaration and not on what the ledger noticed.

Widening past `OneToOne` SPENDS a concurrency guarantee: `from_uniq` is the only
structural bar to two writers binding two targets to one record, because flows
read this link and then write it and the advisory lock wraps `Define` alone. The
cost is `OneToMany`'s own meaning; this is the record that makes it reachable.

`order_payment` is NOT widened here. Its readers assume one collection and say
so, and a rule for choosing between two is unwritable until the second has a
meaning.

Measurement: [measurements/0116](../measurements/0116-the-widening-lane.md)

## Rejected

**A second link name, to avoid changing `core/link` at all.** It leaves the
ledger refusing a widening that is safe and prices every future one at a new
name; whether the exchange's collection wants its own name is not settled here.

**Allowing a narrowing when no row violates it.** The index build already fails
loudly on violating data, but a narrowing that fits today silently tightens a
promise embedders wrote against.

**A migration that edits the ledger row.** It puts a framework-owned table into
every embedder's migration path, and the declaration is the only place that
knows the new cardinality — `Define` under its own lock IS the migration.

**Dropping the obsolete index on the first Create that needs it.** A schema
changing under load, on a path that holds no DDL lock.
