# What a line was charged — measured 2026-09-24

The evidence behind [ADR 0168](../adr/0168-an-order-line-remembers-the-price-it-was-charged.md).

The feature list's C1 row asked for price provenance in two halves. ADR 0167
made the first half possible, which is what a set charged over time. This
record is the second half: an order line naming the price it was charged. What
follows was read from the tree at `dff95cb` before any code, and then measured
with the change in place.

## 1. What the line kept: the amount alone

- `order_line_items` held `unit_price` and nothing about where it came from.
- Pricing's ladder (`selectPrice`) picks one row among a set's base prices,
  sale and override lists, quantity tiers and rules. `CalculateAmountsJSON`
  answered `{amount, priced}` and dropped the row it had picked.
- Two quantity tiers, or a base price and an override that happen to be equal,
  give the same amount. So the amount cannot be traced back to a row, and "was
  this line sold at the sale price" had no answer after the fact.

## 2. The route the origin has to cross

| Hop | From | To | Parser |
|---|---|---|---|
| 1 | pricing `CalculateAmountsJSON` | cart workflow `priceResponseItem` | `json.Unmarshal`, drops unknown fields |
| 2 | cart `LineTotals` | checkout `planLine` (`prepare`) | Go value |
| 3 | `planLine` | the saga's stored plan | JSON; a plan written before the upgrade has no origin |
| 4 | `orderSnapshotJSON` | order `PlaceOrderJSON` | `json.Unmarshal`, drops unknown fields **on purpose** (`service/interop.go`, "does not use DisallowUnknownFields") |
| 5 | order service | `order_line_items` | sqlc, three nullable columns |
| 6 | the row | admin and store order views | `lineItemDTO.price_origin` |

Hops 1 and 4 drop what they do not know, which is ADR 0096's shape for the tax
breakdown. A sender that shipped first would have had the origin dropped in
silence, and every order would have recorded "unknown" as if it were an
answer. All six hops changed in one commit.

## 3. Why the id and not a copy of the row

A price row is deleted when its set is replaced (ADR 0047). Since ADR 0167, the
row survives in `price_set_history`, so the id is enough to recover the amount,
the list, the tier and the rules it carried. A foreign key would refuse the
first reprice after a sale. The line keeps the list id and type next to the
price id because "was this a sale" is the question an operator asks most, and
the history is a join away.

## 4. The cart does not keep it

The cart's totals body (`cart/service/interop.go`, `interopTotals`) has no
field for the origin, and it gets none. ADR 0109 rejected storing which
promotions applied on the cart as a second source of truth; the origin has the
same shape, since every round recomputes it. The checkout takes the origin from
the round it runs itself.

The survey found the `Totals.Applied` godoc saying the opposite: "it travels in
the body written to the cart because the CART has to remember it". The cart's
parser has no `applied` field and drops it; ADR 0109 said why. That is D124,
fixed in the godoc.

## 5. What a line does not get: which promotion reduced it

The promotion engine returns `LineDiscount{ID, Amount}` per line and
`AppliedPromotion{PromotionID, Code, Amount}` per promotion
(`promotion/service/compute.go`). Nothing in it is promotion × line, so
attributing a line's discount to a promotion would mean inventing a split. It
is not in this record.

## 6. A CHECK that passed what it was written to refuse

The first draft of the constraint was:

```
(price_id IS NULL AND price_list_id IS NULL AND price_list_type IS NULL)
OR (btrim(price_id) <> '' AND price_list_id IS NULL AND price_list_type IS NULL)
OR (btrim(price_id) <> '' AND btrim(price_list_id) <> ''
    AND price_list_type IN ('sale', 'override'))
```

A CHECK passes when its expression is NULL, not only when it is TRUE.
`btrim(NULL) <> ''` is NULL and `NULL IN (...)` is NULL, so any shape with a
missing part made one disjunct NULL and the whole expression `false OR NULL`.
The schema witness (`TestTheSchemaRefusesAnIncoherentOrigin`, which writes past
the service with a raw UPDATE) failed three of its five cases on the first
run:

| Written | First draft | Guarded |
|---|---|---|
| list id + type, no price id | passed | refused |
| price id + list id, no type | passed | refused |
| price id + type, no list id | passed | refused |
| a type the ladder does not have | refused | refused |
| a blank price id | refused | refused |

The service's own validation refuses all five as a 400, so no unit test could
see it. Every disjunct now tests `IS NOT NULL` before it compares.

The tree already knew the trap: `promotion/migrations/000001_promotion_init.up.sql`
explains it in a comment and uses a CASE for exactly this reason. Every CHECK
constraint on a fully migrated schema was read back from `pg_constraint` (345),
and the 58 that reference a nullable column were read one by one. None other
relies on a NULL comparison it does not intend. The one bare comparison on a
nullable column, `workflow_executions.idempotency_key <> ''`, admits NULL
because NULL is the column's "no key".

## 7. The mutations

Every mutation was applied by a script that restores the file and checks its
hash afterwards. It also runs the command once WITHOUT the mutation and applies
nothing if that run is red. The check was added after P1's pricing test was
counted as a witness while it was already failing, because its expectation
predated the origin. All rows below were re-run with the check in place.

| # | Mutation | Red test |
|---|---|---|
| P1 | pricing's bulk answer omits the price id | e2e `TestAnOrderLineRemembersThePriceItWasCharged`; `TestCalculateAmountsJSONPreservesOrder` |
| P2 | the plan's `prepare` drops the three fields | e2e; `TestThePlanCarriesThePriceOriginFromItsOwnRound` |
| P3 | the order snapshot drops them | e2e; both plan tests |
| P4 | the order's interop drops them | e2e; `TestTheSnapshotSchemaCarriesThePriceOrigin` |
| P5 | the order repository writes no price id | e2e; `TestAPriceOriginSurvivesTheRoundTripOnTheRealSchema` |
| P6 | the cart accepts a price with no id | `TestCalculateTotalsRefusesAPriceThatNamesNoOrigin` |
| P7 | the cart accepts a list type with no list | same |
| P8 | the order service accepts a list with no price | `TestAnIncoherentOriginIsRefused` |
| P9 | the order service accepts a type with no list | same |
| P10 | the order DTO hides `price_origin` | `TestALinesPriceOriginIsPublished`, both views |
| P11 | the DTO drops the list pair | same |
| P12 | the repository reads no list type | `TestAPriceOriginSurvivesTheRoundTripOnTheRealSchema` |
| P13 | the CHECK admits a third list type | `TestTheSchemaRefusesAnIncoherentOrigin` |
| P14 | the CHECK loses the type's `IS NOT NULL` guard | same |
| P15 | the cart round drops the list id | `TestCalculateTotalsCarriesAListPricesOrigin` — **survived** the cart's unit tests until it was written; only the e2e test saw it |
| P16 | the cart round drops the list type | same, written for P15 |
| P17–P19 | `prepare` drops the price id, the list id, or the type | `TestThePlanCarriesThePriceOriginFromItsOwnRound` — **all three survived** the checkout's unit tests until it was written |
| P20–P22 | the snapshot drops each of the three | both plan tests — **survived** likewise |
| P23 | the snapshot's `price_list_type` key renamed | `TestTheOrderSnapshotCarriesThePriceOrigin` |

P15–P22 are the finding that shaped the tests. The e2e walk killed P1–P5, and
it was tempting to call the chain witnessed. It was, but only in the lane that
needs Docker and runs last. Each hop now has a witness in its own package, and
the e2e test is what proves the hops agree.
