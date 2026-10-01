# ADR 0315 — A promotion is limited to categories in the panel

**Summary:** A promotion's page limits a discount on items to the categories
an operator chooses, as one target rule on `category_tree_ids`, and removes
any of its rules. Both writes go through the `promotion.admin` surface under
`promotion:write`.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

ADR 0314 let an operator write a coupon in the panel, and every such coupon
applied to every item a cart held. "Half off shirts" needed a target rule,
written through the admin API with an attribute and an operator the operator
had to know: the cart flow fills `category_tree_ids` with a product's
categories and their ancestors (ADR 0259), and `any_in` is the operator that
reads a list. The page of ADR 0313 showed a rule's category ids, which name
nothing to a person.

## Decision

The page offers a form that writes one target rule, `category_tree_ids any_in`
the chosen categories, when the discount lands on items, and a remove button
on every rule. The categories are offered, and a rule's categories named, only
to an operator who also holds `product:read`.

## Consequences

- An operator limits a coupon to "Shirts" and it reaches a product filed only
  under "Shirts > Linen", as the cart flow's tree attribute does.
- The categories chosen together form one rule and any of them qualifies an
  item; a second rule narrows, since the promotion applies only where every
  rule holds, and the page says so.
- A discount on the order or on the shipping is offered no category form: the
  engine ignores target rules on the order and a shipping line has no
  categories.
- The panel spells the attribute by hand, and internal/arch pins it to the cart
  flow's constant, which is exported for it.
- A rule is removed through the promotion that holds it; another promotion's
  rule is not found.
- Other rules — a customer group, a tag, a buy condition — are read and
  removed on the page and still written through the admin API.

## Rejected

- A free rule form with the attribute and operator as boxes: it would hand
  the operator the vocabulary this decision exists to hide.
- One rule per chosen category: rules combine with "and", so two categories
  would discount only a product filed under both.
- Category ids typed by an operator without `product:read`: the page would
  read the product module's data under another privilege (ADR 0260).
