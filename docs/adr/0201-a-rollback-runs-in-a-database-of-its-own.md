# ADR 0201 — A rollback runs in a database of its own

**Summary:** Every test that rolls a migration back does it in a database
`internal/testdb` creates for it and drops afterwards, and an architecture gate
refuses a rollback against the address its package shares. It costs each such
test a database of its own, and it closes the shape D135 and D141 fixed one at a
time.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0201](../measurements/0201-eleven-shared-rollbacks.md)

## Context

D135 and D141 each moved one migration test off the database its package
shares, after its down migration began to refuse on data the other tests had
written. Eleven more test files rolled back against the shared address, three
of them asserting afterwards that a module table held no rows at all. The tests
that already used a database of their own did it through eleven private copies
of one helper, and four of those left their database behind.

## Decision

A test that rolls a migration back does it in a database created for it by
`internal/testdb`, which drops it when the test ends. A gate in
`internal/arch` refuses a `MigrateDown` whose address is a package-level
identifier the enclosing test does not declare again.

## Consequences

The eleven files roll back on their own databases, and the ones that write
data before the rollback build their services on that database's pool. What
they assert about empty tables they now assert of their own tables. The eleven
private copies call the package, and all of them drop their database.

`internal/testdb` is imported by tests alone, `core`'s included: the rule
that a published package imports nothing internal reads production files
(ADR 0026).

The gate reads the call and not a helper's name, so a new test is held to it
without being listed. An address laundered through a function that returns the
shared one passes it. The helper's own test holds the other half: the address
it returns names another database, and the database is gone after the test.

Two tests keep creating their databases themselves, because neither rolls a
migration back: `core/db`'s isolation test alters its database by name, and the
smoke lane opens one per scenario.

## Rejected

- **A container per rollback test.** It pulls and starts PostgreSQL for each;
  a database on the running server is the same boundary in milliseconds, the
  reason the smoke lane gives for its own.
- **Running the rollback tests last.** Go runs a package's files in name order,
  which is the assumption D141's test was written on and was wrong about.
- **Deleting the module tests for `internal/arch`'s round trip.** That one runs
  on empty schemas, and five of these roll back with data in place, the case its
  own documentation leaves to the module's test.
