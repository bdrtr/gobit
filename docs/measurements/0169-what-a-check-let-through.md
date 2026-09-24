# What a CHECK let through — measured 2026-09-25

The evidence behind [ADR 0169](../adr/0169-a-check-constraint-answers-true-or-false.md).

## 1. The rule, and where the tree had already written it

A CHECK constraint rejects a row only when its expression is FALSE; TRUE and
NULL both pass. Before this record, the tree stated that in four places:

| Where | What it says |
|---|---|
| `promotion/migrations/000001` | a CASE instead of `A OR B`, because a NULL currency collapses the constraint to NULL (two constraints) |
| `fulfillment/migrations/000001` | `cardinality`, because `array_length` of an empty array is NULL |
| `pricing/migrations/000002` | replaces `array_length(rule_values, 1) >= 1` on `price_rule`: it "blocked nothing at all" |
| `pgstore/migrations/000001` | uses the rule on purpose: a NULL idempotency key passes `idempotency_key <> ''` |

The first three are the trap written down. None of them could stop the next
constraint. ADR 0168's first draft of `order_line_items_price_origin_coherent`
compared `btrim(price_id)` before testing it for NULL and passed three shapes
it was written to refuse (measurement 0168, §6).

## 2. What the test evaluates

**Population.** Every directory named `migrations` that holds a `.up.sql`,
walked from the repository root, outside `testdata`. That is 29 directories:
the core's four schemas, eighteen modules, five plugins and the two contrib
modules. They are applied in startup's order (core, modules, plugins,
contrib) to one fresh PostgreSQL 16. The startup sources were the first
choice, and they were measured short: installing every catalog plugin fails
on `ai-anthropic` without `ANTHROPIC_API_KEY`, and two of the five plugins
that ship tables refuse `Setup` without their settings (read from their
source): `web-push` its private key and subject, and `payment-paytr` its
merchant id, key, salt and two return URLs.
The e2e harness installs neither of those two, so the survey behind ADR 0168,
taken on the e2e schema, read 345 constraints. This test reads **349**.

**Domain, per column.**

| Column | Values tried |
|---|---|
| any nullable column | `NULL` |
| every column | each quoted literal and bare number in the constraint that casts to the column's type |
| text | `''`, `'x'`, `' '` |
| numeric | `0`, `1`, `-1` |
| boolean | `true`, `false` |
| date/time | two distinct instants |
| array | `{}`, `{x}`, and `{literal}` for each literal of the constraint |
| json | `{}`, `[]`, `null`, `"x"`, `1` |

The constraint's expression, from `pg_get_expr(conbin, conrelid)`, is
evaluated over the cross product of its columns' domains in one query. A
combination where it is NULL is reported with its values.

## 3. What it found

| Constraint | NULL for | Outcome |
|---|---|---|
| `promotion_rule.promotion_rule_values_check` | `rule_values = {}` | **D125**: `array_length(rule_values, 1) >= 1`, pricing's pre-000002 constraint, never carried over. Replaced by promotion migration 000004 with `cardinality`. |
| `workflow_executions.workflow_executions_idempotency_key_not_blank` | `idempotency_key = NULL` | intended; excused in the test with its reason |

Nothing else among the 349 answers NULL over its domain, including the fixed
`order_line_items_price_origin_coherent`.

No harm came from D125 at runtime. `validateRuleInput` refuses a rule with no
values, and `matchRule` reads one as not matching, so an empty rule closes its
promotion rather than opening it. What the constraint was for is a writer that
is not the service. The promotion module's "the database constraints are the
last defense" test never tried an empty array, which is why nothing saw it.

## 4. The mutations

All were run with the mutation script's baseline check: the command is run
once without the mutation, and nothing is applied if that run is red.

| # | Mutation | Result |
|---|---|---|
| G1 | the origin CHECK's type loses its `IS NOT NULL` | red |
| G2 | the origin CHECK's base-price disjunct loses its `IS NOT NULL` | **survives — equivalent**: with `price_id` NULL, either the all-NULL disjunct is TRUE or a present list makes the disjunct FALSE |
| G3 | promotion 000004 writes `array_length` again | red |
| G4 | the idempotency excuse is removed | red |
| G5 | an excuse for a constraint that does not answer NULL | red (a stale excuse fails) |
| G6 | a domain type with a CHECK is created | red (refused, not skipped) |
| G7 | the origin CHECK's list-price disjunct loses its `price_id IS NOT NULL` | red |
| G7′ | G7, with the constraint's literals taken out of the domain | **survives**: only `price_list_type = 'sale'` makes the rest of that conjunct TRUE |
| G8 | ADR 0168's whole first draft of the origin CHECK | red |

G7 against G7′ is the measurement behind the ADR's sentence about literals:
without them, the draft shape that needs a named value to show its NULL passes
the test.

## 5. What it does not cover

- A function that answers NULL only for a value outside the sampled domain.
- A constraint on a domain type (none exists; one would fail the test).
- A trigger, since the tree has none that refuses a row, or an exclusion
  constraint.
- Whether a constraint says the right thing. It proves only that the
  constraint answers.
