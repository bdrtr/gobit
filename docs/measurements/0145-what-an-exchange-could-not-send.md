# What an exchange could not send

Evidence for [ADR 0145](../adr/0145-a-replacement-can-send-something-else.md).

Measured 2026-09-12, while choosing the next item from the feature list's A
section.

## The subtractive half is complete; the additive half did not exist

Order edit was measured as a whole, because the feature list's A3.3 row asks for
it as a whole. What is there:

| Act | Record | Where |
|---|---|---|
| remove units | `order_line_cancellations` | ADR 0113, stock back by 0134/0139/0142 |
| money down | `order_credit_lines` | ADR 0105 |
| goods back + money back | `order_returns` / `order_return_items` | the returns flow |
| money IN against a placed order | `order_exchanges.payment_collection_id`, `funded_at` | ADR 0120 |
| goods OUT against a placed order | `order_replacements` / `order_replacement_items` | ADR 0089/0090, dispatched by the returns flow |

And immutability is a written decision rather than an accident:
`order/models/models.go` — "After an order is written its AMOUNTS and its LINES do
not change… Corrections that arise afterwards are carried in separate records".
It is enforced by the absence of statements: there is no UPDATE of
`order_line_items` anywhere in the module's queries, and `CreateLineItem` has one
caller, inside `writeOrder`.

## The one column that was missing

No table in the module could name a variant that is not already on a line:

```
$ grep -l variant_id internal/modules/order/migrations/*.up.sql
000001_order_init.up.sql        ← order_line_items only
```

Every after-sales item points at a line: `order_return_items.order_line_item_id`,
`order_replacement_items.order_line_item_id`, `order_line_cancellations.order_line_item_id`
— all NOT NULL foreign keys.

So an exchange could not swap a shirt for a larger one. The only thing it could
send was units of the exact variant already sold, while its money half has been
able to collect a difference since ADR 0120.

The feature list marks A3.5 (exchange) done. It is done in the sense that an
exchange exists, takes money and dispatches goods; the part the row does not say
is which goods it can dispatch.

## Why nothing downstream had to change

Measured before writing anything, and it decided the shape of the slice:

| Piece | What it already does |
|---|---|
| `holdStock` in the returns flow | builds its variant list purely from `detail.Lines[i].VariantID` |
| `ReplacementDetailJSON` | joins the order's lines to DERIVE that variant, because the row had none |
| `openParcel` | opens an itemless parcel — "the item breakdown is NOT given through this surface" |
| the exchange's dispatch guard | refuses until the collection matches the difference |

So the flow never needed a line. The line was there because the row could not say
what it was sending. Giving the row a variant removes the join and changes nothing
else.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 30 | the bought-ceiling applied to variant items too | **bit** (2 tests) |
| 31 | an item naming BOTH accepted | **bit** (1 test) |
| 32 | the detail document joins a variant item anyway | **survived** |

Mutation 32 is the one worth the space. Restoring the join broke no test, because
nothing exercised `ReplacementDetailJSON` with a variant item — and that document
is what the dispatch flow reads. A variant replacement would have been written
happily, accepted by every gate, and then failed at dispatch with "replacement X
names line , which is not on order Y": a message about a line the row does not
have.

That is the inert-mechanism class again, and the same defence as always — a test
whose subject is the thing the CONSUMER reads, not the thing the producer writes.

Mutation 30's two tests are deliberately separate: one asserts the ceiling does
NOT apply to a variant, the other that it still does apply to a line. A single
fixture would let an implementation that dropped the ceiling entirely pass the
first and never reach the second.

## What is NOT closed, and it is a real gap

The goods and the money are related only by a human. `order_exchanges` has no
items, `difference_due` is typed by the operator, and the dispatch guard compares
the collection against that typed figure. So an operator can promise a jacket
against a shirt's difference and nothing will object.

Pricing the variant item would need a pricing read at settlement and a decision
about which price applies to a replacement — a separate record. Until then the
limit belongs in `known-limits.md`.

## What was not measured

Whether any installation wanted this and worked around it. An exchange that sent
the same variant and a separate manual order would be the workaround, and it is
not distinguishable in the data from an ordinary exchange plus an ordinary order.
