# The engraving comes back too — measured 2026-09-29

The evidence behind [ADR 0230](../adr/0230-an-add-on-goes-back-with-its-line.md).

## 1. What there was

| Act | Where | What it names | Money |
|---|---|---|---|
| a return | `CreateReturn`; the storefront's `POST /store/v1/orders/{id}/returns` | lines one by one, each with a quantity and a refund | the refund per line, typed |
| a write-off | `CancelOrderLine`; `POST /admin/v1/orders/{id}/line-cancellations` | one line and a quantity | none; an `order.line_canceled` event puts stock back |
| a replacement | `replacements.go` | a line or a variant | none |
| the ceiling | `unitsSpokenFor` | returned plus written off, per line, against what was bought | — |
| a per-line refusal | `refuseGiftCardLines` (ADR 0213) | a line that sold gift cards, in both acts | — |

After ADR 0229 an order line may name the line it is an add-on of; neither act
read it.

## 2. The rules

`TestAReturnTakesAnAddOnWithItsLine`: on an order of three engraved rings, the
ring alone, the engraving alone, and one ring with two engravings are each
refused with `order_add_on_follows_its_line`; one ring with one engraving,
refunding 1,000 on the ring and nothing on the engraving, opens a return of two
lines. `TestAWriteOffTakesItsAddOns`: the engraving written off alone is
refused; two rings written off record two rings and two engravings with the
ring's reason and publish two `order.line_canceled` events, answering the
ring's record; two more rings, past the one left, are refused and nothing more
is recorded.

## 3. On the production wiring

`TestAnEngravingIsALineOfItsRingsOwn`, after the order of ADR 0229: the
storefront's return request naming the ring alone answers 422 with the code, and
naming the ring and the engraving at one each answers 201; the admin write-off
of the engraving alone answers 422, and of one ring 201, after which the order's
cancellations are one ring and one engraving.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| R1 | a return checking no bond | the return test, the end-to-end test |
| R2 | a ring returned without its engraving | the return test, the end-to-end test |
| R3 | an engraving returned without its ring | the return test |
| R4 | the ring's check asking only that the engraving is named | survives: equivalent |
| R5 | a write-off leaving the add-ons | the write-off test, the end-to-end test |
| R6 | an engraving written off alone | the write-off test, the end-to-end test |
| R7 | the add-ons' write-offs published unannounced | the write-off test |

Seven mutants, six killed. R4 is equivalent: a return naming both lines at
different quantities is refused by the engraving's own check, which asks for
its ring's quantity, so the ring's check differs from "the engraving is named"
only on a request the other check already refuses.
