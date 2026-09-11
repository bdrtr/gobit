# ADR 0144 — A rule can ask about any of them

**Summary:** A ninth rule operator reads the context's value SET, and the cart
sends every group a customer is in beside the ranked head. It costs one migration
and a sibling field on two request schemas, and it closes a segment discount that
silently did not apply to somebody in the segment.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

A customer belongs to as many groups as the merchant put them in, and the cart
could send exactly one of them: the merchant-ranked head, because the surface
promises rank order (ADR 0049). A rule reading `customer_group_id in [vip]`
therefore did not match a customer in `{retail, vip}` whose head is `retail`.

That is a segment discount not applying to somebody who IS in the segment — the
defect ADR 0103 opens with, a rule silently stopping short of what it was written
for. Nothing failed and nothing was logged.

The engine's attribute namespace was never the limit; the WIRE was — one string
per attribute, and a matcher reading `attributes[rule.Attribute]` as one value.
Two places already named the missing piece: `cart/catalog.go`'s "'any of my
groups' is not expressible" and ADR 0103's "carrying them means a multi-valued
attribute and an operator to compare against it".

Measurement: [measurements/0144](../measurements/0144-what-the-wire-could-not-carry.md)

## Decision

`RuleOperator` gains `any_in`, which matches when the context's value set
intersects the rule's values, and `ComputeInput` gains `ContextLists` beside
`Context`. The cart flow sends the ranked head in `Context` as before and ALL the
groups in `ContextLists`.

## Consequences

Only the new operator reads the list, and that separation is the load-bearing
part. A shipped `customer_group_id in [vip]` rule means "the group we picked for
this cart is vip" to every installation running today; teaching `in` to read the
whole list would start discounting customers whose head is not vip, and a live
discount would change with nothing announcing it. `ReadsAList` is a separate
predicate from `MultiValue` for exactly that reason — one is about several values
in the RULE, the other about several in the CONTEXT.

The list travels as a SIBLING field, not a retype of `Context`: both decoders
refuse unknown fields, so a rename breaks every caller at once, and the two
surfaces have to carry an identical shape.

An absent list does not match, like an absent attribute: a customer whose groups
could not be read has an UNKNOWN segment, and treating unknown as matched would
open the discount to everybody.

The pricing callers discard the list deliberately: a price set is chosen by the
ranked head alone (ADR 0049), and two sets both matching would be two prices with
nothing deciding between them.

Three mutations bit. The first is worth naming: leaving the flow sending only the
head ships an operator no request can satisfy — a mechanism nothing feeds, passing
every gate because no gate asks whether the producer feeds the consumer. Its
witness is a test whose subject is the CART FLOW.

A stale sentence was corrected in the same change: `cart/discount.go` claimed
"the customer group is NOT put into the context … added here the day the customer
surface publishes the group list". That day had come and gone — the group has been
in the context since ADR 0049 — and a later round would have read it as proof the
leg was missing.

## Rejected

- **Teach `in` to read the list.** It changes what a shipped rule means and
  widens live discounts silently.
- **Retype `Context` to `map[string][]string`.** Both decoders refuse unknown
  fields and the change breaks every caller for a question only one operator asks.
- **Go at category and tag first, as the feature list proposed.** They need the
  product module to publish membership through the Query layer and a second batch
  read on the totals path; the customer group needs no new read at all.
- **Send the groups comma-joined.** A separator becomes load-bearing and a group
  id containing one silently splits into two.
