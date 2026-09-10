# A stamp is not a balance — measured 2026-09-10

Serves [ADR 0124](../adr/0124-the-dispatch-asks-whether-the-money-is-still-there.md).

## The reproduction

Funding an exchange, emptying its collection through the payment module's own
route, and dispatching the replacement. Every step is a live admin endpoint and
no step is exotic.

```
POST /admin/v1/orders/{id}/exchanges                                  7800 owed
POST /admin/v1/orders/{id}/exchanges/{exchangeId}/replacements        one unit
POST /admin/v1/payment-collections  → session → capture               7800 taken
POST /admin/v1/orders/{id}/exchanges/{exchangeId}/funding             stamped
POST /admin/v1/payments/{id}/refunds                                  7800 back
POST /admin/v1/orders/{id}/exchanges/{exchangeId}/replacements/{replacementId}/dispatch
```

What it answered, before the fix:

```
collection: captured=7800 refunded=7800 held=0
dispatch:   200 {"fulfillment_id":"ful_…","sent_units":1,"already_sent":false}
exchange:   status "completed", funded_at set
```

The units left the shelf, a real parcel was opened, and the order module wrote
`completed` on a record whose money had gone back. The shop gave the goods away
and its own report said the exchange was settled.

## Why nothing stopped it

Three guards stand on that path and each is right about its own question.

`Exchange.Settleable()` asks whether the row may be closed and answers from the
row: `DifferenceDue == 0 || FundedAt != nil`. The CHECK
`order_exchanges_completed_is_settled` asks the same thing of the same row. Both
are correct — an order row may not hold a payment figure as its own truth
(ADR 0119), so the stamp is all it can keep.

`FundExchangeDifference` asks the payment module and asks it properly:
`captured - refunded == difference_due`. It asked at funding time. Nothing asked
again, and the collection is reachable by a published refund route with no flow
anywhere on it — the same absence of a flow that gap D55 records one module over.

So the defect is not a missing rule. It is a rule asked once about a thing that
changes.

## Where the question belongs

Two places, and they answer different questions at different moments.

**Before the stock moves.** Below the hold the units are in a box and no answer
puts them back. Asking here costs a refusal that costs nothing; the goods stay
on the shelf and the operator is told why.

**Before the record is closed.** The settle also runs on the RETRY path, where
nothing asked, and the money can leave between the parcel and the settlement.
Here refusing is not available — the goods are with the customer — so the honest
end is that the exchange stays open and the loss is logged at ERROR with both
figures.

Asking twice is not a duplicated read. It is the rule ADR 0119 states: the
deciding flow asks payment at the moment it decides, and these are two moments.

## The cost

One payment read per exchange-sourced dispatch, on each of the two paths. A
claim-sourced dispatch asks nothing: a claim is settled by the goods alone. An
exchange owing nothing asks nothing either — it names no collection.

## The mutations

| Mutation | Result |
|---|---|
| the pre-flight refusal removed | the e2e ships the goods and the stock falls |
| the settle-side question removed | the retry-path unit test marks a drained exchange settled |

Both with `-count=1`. The second needed a test of its own: the first version of
this work proved only the pre-flight half, and the settle half was green under
its own mutation until the retry path was written down.
