# ADR 0396 — A rule condition is read by one evaluator

**Summary:** Pricing, fulfillment and promotion read a rule condition through `internal/core/condition`, each admitting its own subset of nine operators.
It costs the edges where the three copies disagreed, and a rule written with the same words now means the same thing in every module.

- **Status:** Accepted
- **Date:** 2026-10-05

Measurement: [measurements/0396](../measurements/0396-three-rule-matchers.md)

## Context

A price, a shipping option and a promotion carry rules of one shape, an
attribute, an operator and its values, and each module ANDs them. The three
spell the same eight operators, promotion a ninth (`any_in`, ADR 0144), and
each read them with its own `matchRule`. The copies drifted: fulfillment
trimmed a number before comparing it and the others did not, and fulfillment
wrote a numeric rule whose value is not an integer, which pricing and
promotion refuse and which then offered its option to no cart (D252).

ADR 0001 forbids a module importing another and a shared model package; a pure
function over strings below the modules is neither, and `internal/core` is not
published (ADR 0026). The storefront's customer claim is already one comparison
in the core, because a rule copied per module keeps answering after one copy
drifts (ADR 0057, ADR 0370). A tax rate rule is not of this shape: it names a
reference by equality and the most specific match wins (ADR 0094, ADR 0101).

## Decision

`internal/core/condition` holds the nine rule operators and the one function
that reads a condition against a rule context, and pricing's ladder,
fulfillment's eligibility and promotion's rules call it in place of their own
copies while each module admits its own subset of the words. Every writer of
such a rule refuses a value that function could never read, so a shipping rule
with `gt`, `gte`, `lt` or `lte` and a value that is not an integer answers 422
`fulfillment_invalid_input`.

## Consequences

- The composition stays in the modules. Rules are ANDed in all three; the
  ladder and the buyer rank (ADR 0049, ADR 0185) are pricing's, the target and
  context rules promotion's. The function reads one condition.
- A number is compared as written. Fulfillment no longer trims at match; its
  writer trims every value, so the change reaches only a value an embedder
  sends through fulfillment's interop with spaces, or a row written by hand,
  and such a rule stops matching rather than starts.
- A condition holding a value no writer accepts matches nothing. An empty
  value, or a number that is not an integer, closes the rule in every module;
  a hand-written `ne ""` used to match every present value.
- An earlier shipping rule with a non-integer threshold stays, hiding its
  option, until the operator deletes and rewrites it; no migration touches it.
- The spelling has one source. Each module's `RuleOperator` constants are
  converted from the package's, so the three models packages take their first
  import outside the standard library; `Valid`, the table's CHECK and the API
  stay the module's, and `any_in` stays promotion's alone (ADR 0144).
- `TestOneConditionEvaluator` holds it. It fails for a `switch` case whose
  value, resolved through the constants it names, is a comparison operator, in
  a non-test file under `internal/modules`, `internal/workflows` or `plugins`
  outside a models package's `RuleOperator.Valid`; for a `RuleOperator`
  constant not converted from the package's; and for a `Valid` that admits
  anything else. It reads syntax; an `if` chain outside `Valid` is the review's.
- Tax and the segment flow are not on it yet: the segment flow's `compare`
  stays on the gate's allowlist until a later record moves it onto `Compare`,
  and tax joins when a rate rule needs a second operator.
- The write checks other than readability still differ: pricing caps no value
  count and only fulfillment trims a value.

## Rejected

- A string expression language. Every consumer ANDs triples that three tables
  and the panel's forms already store, and none has asked for OR or grouping;
  it reopens when a rule needs a disjunction across attributes.
- A shared rule struct in `internal/core`: ADR 0001's shared model.
- Trimming at match in all three: a `cart.` value the storefront writes with
  spaces would start matching prices and discounts.
- A CHECK refusing a non-integer numeric value: `ParseInt` restated as a
  regular expression in three tables, for rows the evaluator never matches.
- A gate holding three copies equal: it keeps three places to fix.
- Tax rate selection on the evaluator now: equality ranked by specificity
  gains nothing from eight unused operators.
