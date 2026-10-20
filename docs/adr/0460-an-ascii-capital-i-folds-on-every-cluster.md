# ADR 0460 — An ASCII capital I folds on every cluster

**Summary:** Each admin search document translates the ASCII capital I to i before `lower()`, as it translates the table's letters, through a migration per module that replaces the function and rebuilds its index.
It costs a rebuild of four indexes, holding off writes to their tables while it runs, and buys a search that finds a name typed in capitals on a cluster with a Turkish CTYPE.

- **Status:** Accepted
- **Date:** 2026-10-20
- **Amends:** [0456](0456-an-admin-list-is-searched-in-the-database.md), whose letters folded on every cluster but the ASCII capital I

Measurement: [measurements/0460](../measurements/0460-what-a-turkish-cluster-folds.md)

## Context

ADR 0456 folds a search document with `lower(translate(...))`: translate()
takes every letter of `internal/core/textfold`'s table to its base and
leaves ASCII to `lower()`, which folds it alike on every cluster but one
letter. A Turkish or Azerbaijani CTYPE, which ADR 0015's contract admits,
lowers the ASCII capital I to the dotless i, after translate() has passed.
A name stored in capitals was indexed with a dotless i, and the same name
typed in small letters, in capitals or with Turkish marks folds to an i: on
such a cluster none of them found it (D315). The arch gate held each
migration's strings to the Go table, and the table had no ASCII letter.

## Decision

`textfold.SQLTranslate` maps the ASCII capital I to i beside the table's
letters, and order 000047, customer 000009 and review 000006 replace each
module's search document with that translate() and rebuild the indexes on
it. The arch gate reads, for each module, the definition of each search
function its migrations leave in effect.

## Consequences

- `lower()` folds no letter a search document matches by that differs between clusters; a letter outside the table still folds as the cluster's does (ADR 0015).
- The query function folds by the document function, so it changes with it and its own definition stays ADR 0456's.
- The rebuild holds off writes to `orders`, `order_addresses`, `customer` and `reviews` while it runs, as the first build did.
- A down migration restores ADR 0456's function and rebuilds the index by it.
- The startup case-folding probe is unchanged: it holds what `lower()` folds for the checks, not what the search translates first.

## Rejected

- Requiring a CTYPE other than Turkish: ADR 0015 admits any UTF-8 locale, and a Turkish shop is the one most likely to choose it.
- `lower(v COLLATE "C")`: it folds only ASCII, and leaves every capital of the table to translate() just the same; one rule in one place is the table.
- Editing ADR 0456's migrations: an installation that ran them keeps their function, and a migration runs once.
