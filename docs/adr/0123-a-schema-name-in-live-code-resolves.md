# ADR 0123 — A schema name in live code resolves

**Summary:** A constraint or index named in a live comment must still be in the
schema, and the audited vocabulary is the set of names the migrations have ever
defined. It costs one exemption for the audit's own explanation and buys a gate
under the half of ADR 0120's defect that a name can carry.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

Gap D59 cost an exchange its closing: a status was added, a flow's copy of the
vocabulary did not follow, and ten live sentences went on describing the world
before it. Four of those ten named a CHECK that ADR 0120 had dropped, and each
one offered a reader a name to verify the claim against. The name resolved to
nothing.

That is the class `doc_references_test.go` was written for — a reference sends
the reader searching and the thing searched for is not there — in the one
dimension it cannot reach, because a constraint name is a word in a comment
rather than a Go symbol or a path. Nothing in the tree looked at it, and the four
were found by hand while sweeping for something else.

## Decision

A comment in live Go or in a hand-written query file may not name a constraint or
index the migrations dropped and did not put back. The audited vocabulary is
derived from the migrations themselves — every name they have ever defined — so
a word the schema never knew is outside the audit rather than a false positive.

## Consequences

The population is computed by walking the up-migrations IN ORDER, and the order
is the computation rather than a detail: a name is routinely dropped and re-added
one line later, which is how a CHECK is widened here. The set-subtraction version
was measured and called a live constraint dead in three files.

Dated records are out of scope — the changelog, the decision records, the
measurements and the migrations. This repository amends those by ADDING, so a
record naming a constraint dropped later is correct, and one of them does.

Live code keeps the right to name a dead object deliberately, through an
exemption carrying its reason. There is one today: this audit's own account of
why it exists, which cannot be written without the name. A second test refuses an
exemption whose object came back or whose sentence was deleted.

What it does not catch is the larger half of D59: a constraint whose PREDICATE
changed under the same name. Every word in that sentence resolves and the
sentence is still wrong. No name-based audit reaches it, and saying so is part of
the decision rather than an admission next to it.

Measurement: [measurements/0123](../measurements/0123-schema-names-in-live-code.md)

## Rejected

**Auditing every snake_case word that looks like a constraint.** The look is not
a population: the exemption list would carry its own false positives, which is
the shape gap D16 records.

**Including documents.** A dated record naming a dropped object is right, and
the repository's way of changing one is a strike-through rather than a rewrite.

**Reading the down-migrations.** They describe the schema of a rollback; every
one of the five dropped names is restored in one.
