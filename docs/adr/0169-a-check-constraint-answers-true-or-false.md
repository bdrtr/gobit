# ADR 0169 — A CHECK constraint answers true or false

**Summary:** Every CHECK constraint in the repository's migrations evaluates to
TRUE or FALSE, never NULL, and an integration test applies every migrations
directory in the tree and holds each constraint to it. The promotion rules'
constraint, which let an empty array through, is replaced.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0169](../measurements/0169-what-a-check-let-through.md)

## Context

PostgreSQL passes a row whose CHECK expression is NULL, not only one that is
TRUE. A comparison on a nullable column before its NULL test makes the
expression NULL, and so does a function that answers NULL for a value the
column can hold, as `array_length` does for an empty array. Such a constraint
passes exactly the row it was written to refuse. The tree had written the trap
down three times, in two migration comments and one repair. ADR 0168's first
draft fell into it anyway, and the service's validation hid that from every
unit test.

## Decision

Every CHECK constraint, over every combination of NULL (where its column
admits it), the literals it names and a few values of each column's type, has
to evaluate to TRUE or FALSE, and a test in the integration lane applies every
migrations directory in the tree and evaluates each one. A constraint whose
NULL answer is intended is named in that test with its reason, and there is one.

## Consequences

The class is closed mechanically instead of by prose. A new constraint tests
each column `IS NOT NULL` before it compares it, or uses a function that
answers for every value (`cardinality`, not `array_length`).

The literals are what make the evaluation bite. A value the constraint names
is often the only one that makes the rest of a conjunct TRUE, and a TRUE
conjunct is where a NULL beside it shows.

The population is the migrations directories, not the sources startup
applies, so the two catalog plugins that need credentials and the contrib
modules, which are separate Go modules, are held too. The domain is sampled,
not exhaustive: a function that answers NULL only for a value outside it still
passes. A CHECK on a domain type cannot be varied and is refused until the
test learns it.

The first run found `promotion_rule_values_check` written with `array_length`,
the constraint pricing had already replaced on its own rules. A promotion
migration replaces it with `cardinality` and is not `NOT VALID`, for pricing's
reason: an installation holding a rule with no values fails the upgrade and is
told which constraint. The service already refused such a rule, and the
calculation reads one as not matching, so no promotion opened because of it.

The one excused constraint is `workflow_executions_idempotency_key_not_blank`.
NULL is an execution with no key, and its migration says so. An excuse that no
longer answers NULL fails the test.

## Rejected

- **Parsing the SQL for unguarded comparisons.** Function-made NULLs escape it.
- **The sources startup migrates.** Two plugins will not install without keys.
- **Rewriting the idempotency constraint.** A table scan to restate its comment.
- **A check per module.** The class is repository-wide; copies drift.
- **`NOT VALID` for the promotion constraint.** Pricing's reason: a half gate.
