# What the wire could not carry

Evidence for [ADR 0144](../adr/0144-a-rule-can-ask-about-any-of-them.md).

Measured 2026-09-12, while choosing the next item from the feature list.

## The row said three things were missing; none of them was

The feature list's A2.4 row is "product / collection / customer group rules",
marked partially done. Measured against the tree, all three targets are wired end
to end:

| Target | Where it is FED | Record |
|---|---|---|
| customer group | `cart/catalog.go`'s `ruleContext`, from `CustomerGroupIDs` | ADR 0049 |
| product | `cart/discount.go`'s `lineAttributes` (`product_id`) | ADR 0103 |
| collection | the same function (`collection_id`, absent rather than empty) | ADR 0103 |

The engine's attribute namespace is open — any string — so what had to be
measured was not which attributes EXIST but which are actually fed. Three rule
types (`context`, `target`, `buy`) and eight operators, and an admin write surface
that is already described.

So the row's own proposed slice — go at category and tag — would have been the
wrong first move, and something narrower was absent.

## What was absent, and the defect it left standing

ONE mechanism: a multi-valued attribute plus an operator comparing a list to a
set. The wire is one string per attribute on three surfaces, and `matchRule` reads
`attributes[rule.Attribute]` as a single value.

Two places in the tree had already named it before this measurement:

- `cart/catalog.go`: "'any of my groups' is not expressible"
- ADR 0103, line 40: "carrying them means a multi-valued attribute and an operator
  to compare against it"

The consequence is not a missing feature but a live one behaving wrongly. The cart
sends the merchant-ranked HEAD:

```go
// The HEAD, because the surface promises rank order (ADR 0049).
attributes[attrCustomerGroupID] = groups[0]
```

so a customer in `{retail, vip}` whose head is `retail` did NOT match
`customer_group_id in [vip]` — a segment discount silently not applying to
somebody in the segment.

## A stale sentence, found in the same reading

`cart/discount.go` carried this, under the heading "Only the region is put into
the context":

> The customer group is NOT put into the context; … The group context is added
> here the day the customer surface publishes the group list.

It is false and had been for a long time: the same file's `discountRequestFor`
calls `ruleContext`, which writes the group, and its own error branch logs
"discounting without a segment". The day the sentence was waiting for had come and
gone.

This is the class that keeps costing rounds (D62, D66, D71, D78, D81): the next
person to look would have read it as proof the leg was missing and built it twice.
Corrected in the same commit.

## Where category and tag actually stand, measured beyond ADR 0103

For the other two targets the cart could not send the data even if the engine took
it, which is why they are a separate decision:

| Piece | State |
|---|---|
| `productRecord` on the Query provider | publishes sixteen fields, none of them a category or tag list |
| the product provider's filters | accept `category_id`/`tag_id` but REFUSE them combined with `id`/`ids` |
| what `productFactsFor` asks for | exactly `FilterIDs` |
| the bulk membership read | exists as `ListCategoriesByProductIDs` / `ListTagsByProductIDs`, consumed only inside the module, on no Query provider |

So category and tag need the product module to publish membership plus a second
batch read on the totals path. The customer group needs no new read at all — the
groups are already in hand.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 27 | the flow sends only the head again (`groups[:1]`) | **bit** |
| 28 | the old operators consult the list too | **bit** |
| 29 | an empty list counts as a match | **bit** |

Mutation 27 is the one the design is most exposed to, and it is this repository's
own recurring class: the operator, the migration and the schema field can all ship
while the producer still sends one group. The result would be a ninth operator no
request could satisfy, passing every gate — because no gate asks whether the
producer feeds the consumer. Its witness is therefore a test whose subject is the
CART FLOW rather than the engine.

Mutation 28 is the one that would have been hardest to notice in production: a
shipped `eq vip` rule quietly starting to match customers whose head is not vip.
Its test is deliberately a SEPARATE case from the new operator's, because one
fixture asserting both would let a wrong-from-the-top implementation trip the
first assertion and never reach the second.

## What was not measured

Whether any shop has lost a discount to this. It would show as a promotion with a
`customer_group_id` rule and customers in several groups, which is
reconstructable, and was not attempted.

Also not measured: whether `any_in` should exist for LINE rules. It is passed
`nil` there on purpose — a line's attributes are one variant's facts, and "in any
of these categories" is the question the separate decision above has to answer.
