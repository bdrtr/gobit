# ADR 0098 — The changelog names every decision since the last release

**Summary:** Every record dated on or after the newest released section, and not
named in one, must be named in the Unreleased section of `CHANGELOG.md`. It
costs one entry per decision and a gate that derives the population from the two
documents.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

`CHANGELOG.md` says of its own Unreleased section that every item "names its
decision". Nothing asked the other direction, and by the time anybody counted,
fifty-six records had been decided and never announced — ADR 0018 through
ADR 0097, with gaps where an entry happened to exist.

Every one of them was written down correctly. `docs/adr/` holds the record,
`docs/adr/README.md` indexes it, the commit carries the argument. What was
missing is the one document a reader of a RELEASE opens: somebody upgrading
reads the changelog and finds no line for a schema that changed under them.

It is ADR 0097's defect one document over. There, a retention rule lived in a
migration header while the set of tables it applied to kept growing; here, a
completeness rule lived in a paragraph while the set of decisions kept growing.
In both cases the rule was true and nothing could ask it.

## Decision

A record belongs in the Unreleased section unless it has already shipped, and
`internal/arch/changelog_test.go` decides which is which: a record dated on or
after the newest released section's date, and not cited by any released section,
must be cited in the Unreleased section.

## Consequences

Announcing a decision becomes part of making one, the way writing the record
already is. The entry names the decision and stops; the reasoning stays in the
ADR, the argument in the commit, the numbers under `docs/measurements/`.

The changelog and the ADR index now say overlapping things, and that is
accepted rather than resolved: the index answers "what was decided", ordered by
decision, and the changelog answers "what changed in this release", ordered by
release. A reader arriving at either question should not have to know about the
other document.

The population is derived from the DATES the two documents already carry, so
the gate needs no git command — a tag is not always fetched in CI — and no
hand-written floor. The exclusion for records cited in a released section is
what keeps a decision made on the morning of its own release out of the
population.

The gate cannot see whether an entry is TRUE, only that the number is named. A
wrong entry passes; that is the same bound every citation gate in this
repository has.

## Rejected

- **A hand-written floor ADR number** — right until the next release and
  silently wrong after it, which is the class of number this repository keeps
  finding rotted.
- **Deriving the boundary from git tags** — a shallow CI checkout has no tags,
  and a gate that cannot derive its population must either fail falsely or skip
  silently.
- **Requiring every record ever to be named** — the released sections are a
  record of the past and are not corrected retroactively.
- **Dropping the Unreleased list and pointing at the ADR index** — the index is
  not release-scoped, so it cannot answer the question an upgrader is asking.
