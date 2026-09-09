# ADR 0099 — A price list carries the field a merchant writes on

**Summary:** `price_list` gains the `metadata` column every merchant-authored
record in the tree already has, and the three computed tables beside it do not.
It costs one column and a rule about which records get one.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

Twenty-seven tables carried a `metadata jsonb` column and pricing had none. The
absence was never decided: the module was written without one and nothing since
asked why, while product, customer, order, cart, promotion, sales channel, tax
region and shipping option all took it.

An embedder storing anything of their own about a price list — the campaign it
belongs to, the team that owns it, an id in the system it was imported from —
had nowhere to put it, and the answer "use a table of your own" is the answer a
library gives when it has not thought about the question.

## Decision

`price_list` takes `metadata jsonb NOT NULL DEFAULT '{}'`, published on the
admin surface for writing and reading. `price`, `price_set` and `price_rule` do
not, and the rule that separates them is that a merchant AUTHORS a price list —
it has a title, a description and a window somebody chose — while the other
three are what the ladder computes over.

## Consequences

The rule is now stated rather than inferred, so the next table in this module
arrives with the question already asked of it. Measured against the tree, the
rule also describes the twenty-six that have the column and the rows that do
not: inventory levels, movements, payments, sessions, order summaries and link
rows are all computed or structural.

The field is REPLACED on an update, not merged, like every other field of that
body. A merge would leave no way to remove a key: a caller could add and change
and never delete.

An empty document reads back as an ABSENT field rather than an empty object, so
a response does not carry `"metadata": {}` for a list nobody wrote on. That is
the same conversion the order module makes, and it is why the column can be NOT
NULL without every reader having to tell NULL from empty.

This module never reads the field. A value that will not decode is therefore
reported as an internal fault rather than silently dropped — it can only have
got there by a hand-written write.

## Rejected

- **Metadata on all four pricing tables** — a column nobody writes is a column
  that still has to be read, converted and described; `price` is a number in a
  currency at a quantity, and nothing authors it.
- **Merging on update** — no way left to remove a key.
- **A separate `price_list_metadata` key/value table** — it buys queryability
  nobody asked for and costs a join on every read of a record the module
  publishes whole.
- **Leaving it to the embedder's own table** — the embedder would carry a
  foreign key to a row this module soft-deletes, which is the coupling
  Principle 2.2 exists to prevent.
