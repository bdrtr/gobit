# What a return gives back — measured 2026-10-09

Evidence for [ADR 0433](../adr/0433-a-return-gives-back-at-most-what-its-units-sold-for.md)
and gap D269. Read on trees at 200d4d1f and bbc45931 (the same figures on
both); every Go command ran as `GOTOOLCHAIN=go1.26.6`, the integration and e2e
lanes against Docker's `postgres:16-alpine` through testcontainers.

## 1. The fault, on fakes

A unit probe of the returns flow on fakes, deleted after. Order: line A
1 x 12 000, line B 1 x 18 000, shipping 1 000, captured 31 000. The return
names A x 1 and is received. The claim is of the refund kind, 3 000. The
payments fake modelled the module's `planRefund` for one capture.

| Act | Refunded |
|---|---|
| Return refunded 12 000, twice | 12 000 + 12 000 = 24 000 naming the return |
| Return refunded 0 | 31 000: line B, the shipping and line A |
| Claim settled 0, twice, stamped | 3 000, then 409 (status) |
| Claim settled 0, twice, unstamped | 3 000 + 3 000 |
| Claim of 3 000 settled 31 000 | 31 000 |

The panel offers Refund on a received return after every refund, and an empty
amount is 0 ("empty: everything the collection has left").

## 2. The fault, on the real wiring

The happy path's order: one line, two units at 45 000 taxed 20% (108 000), no
shipping. A storefront return of one unit is received. With ADR 0433's tests
added and its code not, on both trees:

| Test | Asked | Answered |
|---|---|---|
| `TestAReturnGivesBackAtMostWhatItsUnitsSoldFor` | `GET .../returns/{returnId}` and the list | `lines` absent, `sold_for` absent (0) |
| | 20 000, 34 000, then 1 | 200, 200, then 200: collection refunded 54 001 |
| | 0 on a fresh return | 200: refunded 108 000 |
| | 54 000 twice on a fresh return | 200 twice: refunded 108 000 |
| | 54 001 on a fresh return | 200: refunded 54 001 |
| `TestTheOrderPageRefundsAReturnOnce` | the panel's refund form, empty | 108 000 refunded |
| | the same form again | 200, not 422 |
| | the return form with every quantity empty | 200, a lineless return opened (count 2) |
| `TestACausesRefundsStayUnderItsCeilingAtOnce` | two refunds of 8 000 naming one cause at once | both paid: 16 000, at the server's level and at REPEATABLE READ |
| `TestZeroIsWhatTheCauseHasLeft` | 5 000 then 0 under a 12 000 ceiling, 31 000 captured | 26 000 (7 000 expected) |

The payment tests ran with the call reduced to the old signature (no ceiling)
and the new code's name written as its string.

## 3. Where zero came from

Commit 1c1fdcd7 (2026-09-05), before any record reached returns: a return's
zero was "everything left", a claim's its own figure, the second "because
defaulting to the whole collection would turn settle this claim into refund
the order". ADR 0271 adopted the return's for the panel.

## 4. The two ceilings

- The planned `refund_amount`: 0 on every storefront return, which names no
  money; no route updates it; it is held only to the order's total.
- What the units sold for (`returnedWorth`, ADR 0432): one of three units of a
  10 000 line is 3 333, two 6 666, three 10 000; a lineless return is 0. The
  documents of a return's refund split on the same weights (ADR 0406).

## 5. The lock

`refundPayment` locks collection, session and capture and calls the provider
inside. Every refund of a return or a claim comes out of the order's one
collection (`order_payment` is one to one, under a unique index), and an
exchange's refunds out of its own. The payment repository begins every
transaction at READ COMMITTED, whatever the pool's default (D119), so the sum
read after the lock wait sees the refund that held it.

## 6. A return's lines over HTTP, before the change

| Route | Lines |
|---|---|
| GET /admin/v1/orders/{id}/returns | none |
| GET /admin/v1/orders/{id}/returns/{returnId} | none |
| POST /admin/v1/orders/{id}/returns, /cancel | none |
| POST /store/v1/orders/{id}/returns | none |
| order_return query provider (in process) | items |

## 7. Finding a return refunded past its units after the upgrade

In the payment database:

```
SELECT reference, SUM(amount) FROM refunds
WHERE reference LIKE 'ret\_%' GROUP BY reference;
```

On the order side, each return's `sold_for` from
`GET /admin/v1/orders/{id}/returns/{returnId}`. A reference whose sum exceeds
its `sold_for` was refunded past its units; it refunds nothing more and keeps
what it paid.

## 8. A line returned in parts

Each return values its units by `returnedWorth`, the line's total shared by
its units and rounded down, and the invoicing split weighs a returned row the
same way. A line of three units that came to 10 000, returned one unit at a
time in three returns, is held to 3 333 + 3 333 + 3 333 = 9 999, where one
return of the three units is held to 10 000; seven units of 10 000 returned
one at a time come to 7 x 1 428 = 9 996. The shortfall is at most one minor
unit per unit returned apart, and the payment module's own refund route pays
it (the review's probe on fakes, 2026-10-09).
