# ADR 0321 — The panel limits a promotion to customer groups

**Summary:** The customer module opens its groups to the read layer as a
`customer_group` entity, and a promotion's page offers them under
`customer:read` and writes a context rule over the chosen ones under
`promotion:write`.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The cart flow writes a customer's groups into the rule context, the first by
rank as a value and all of them as a list (ADR 0049, ADR 0144), so a promotion
for one segment needs no new engine. The panel could limit a promotion to
categories (ADR 0315) but not to customers: the groups were only ids on a
customer's record, and the panel reaches the modules through the read layer.

## Decision

The customer module publishes a `customer_group` read entity with each group's
id, name and rank, newest first or by an id set. A promotion's page offers the
groups to an operator holding `customer:read` and `promotion:write`, and its
form writes one context rule, `customer_group_id any_in` the chosen.

## Consequences

- A coupon for wholesale buyers or a loyalty tier is written in the panel and
  applies to a customer in any of its groups, whichever ranks first.
- A guest's cart carries no group, so a group rule never matches it.
- An operator without `customer:read` sees a group rule's ids and is offered
  no form, and the groups are not read for them (ADR 0260).
- The form offers the ninety-nine newest groups and says when there are more:
  the module refuses a page over a hundred, and the panel reads one more than
  it offers to know.
- The panel names the attribute by hand, pinned in internal/arch against the
  cart flow's constant, which is exported for it as the category one was.
- The customer module's registration file is written in English now and has
  left the language ledger.

## Rejected

- A `customer.admin` surface listing the groups: a read the panel needs from
  another module goes through the read layer, as the categories do.
- A `customer_group_id in` rule: it reads the single, first-ranked group, and
  would miss a customer whose first group is another.
