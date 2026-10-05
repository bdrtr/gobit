# ADR 0405 — A stack's base carries the stack's rules

**Summary:** A rate others stand on may carry rules written before the stack or after it, and they select the whole stack.
It costs a stack scope that moves with every rule edit on its base, and one stack no longer depends on the order of two writes.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Amends:** [0095](0095-a-rate-can-stand-on-another.md), whose stack creation refused a ruled base

## Context

ADR 0095 lets a rate stand on another of its region and gives a stack its
base's scope, one set of rules for the whole stack. The calculation selects
the base by its rules or as the region's default and expands it into the
stack it heads. Stack creation refused a base carrying rules, while the rule
write refused a rule only on a default or a standing rate, so a stack on a
ruled base was refused when the rule came first and kept when it came second
(D249). Under that refusal a stack could be scoped only by standing on the
region's default, or on a rate that was later made the default.

## Decision

A rate others stand on may carry rules, written before the stack or after it,
and a line those rules select is taxed by every rate of the stack while any
other line is taxed by none of them. Stack creation no longer reads its base's
rules, and a rule on a rate that stands on another stays refused with 409
`tax_constraint_violation`.

## Consequences

- Both orders write the same rows and tax a line the same way.
  `POST /admin/v1/tax-rates` with `stacks_on_id` naming a ruled rate answers
  201 where it answered 409 `tax_stack_not_allowed`.
- A stack's scope moves with its base's rules: a rule added to the base taxes
  the lines it matches by every rate of the stack, and the base's last rule
  deleted takes all of them off those lines. Neither edit names the rates
  above the base.
- A base is in one of three states: the region's default, applying the stack
  to every line no ruled rate claims; a ruled rate, applying it to the lines
  its rules match; or neither, applying it to no line. A default takes no rule
  and a ruled rate is not made the default, so a base is never two of them.
- A stack can be scoped to a product, a tax class or a product type, so a tax
  on one class of goods with another compounding on it can be written.
- No migration: the schema never refused the pair, and every stored
  configuration is one the calculation already reads.
- Stack creation makes one query fewer. The region's lock and the checks on
  branching, depth, inclusive prices and the stack's sum are unchanged.
- The tax rate trial (ADR 0387) already accepted a rule on a base and is
  unchanged; `tax_stack_not_allowed` no longer names a ruled base.

## Rejected

- Refusing a ruled base in both orders: every stack would stand on the region's default or a rate waiting to be made it, and none could be scoped to a product, class or type.
- Refusing only the rule written after the stack: a base's scope could only shrink once a rate stood on it, and ADR 0095 makes changing a stack a retire-and-rewrite.
- Rules on each rate of a stack: a rate applying only when its own rule also matches is a second selection inside the stack, and ADR 0095 keeps one.
