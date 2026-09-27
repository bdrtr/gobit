# A card that was closed — measured 2026-09-27

The evidence behind [ADR 0213](../adr/0213-an-operator-closes-a-gift-card-and-a-card-line-is-final.md).

## 1. What could take a sold card's value back

| Path | State before this record |
|---|---|
| canceling the order | refused once money is collected (`CancelOrder`, 409 `order_not_pending`); a paid order is never canceled |
| a return | names lines and quantities; its refund amount is the operator's; receiving it restocks, refunding it sends money back through the order's collection |
| a line write-off | names a line and a quantity; moves no money; the sale flow issues a card for every unit bought whatever was written off |
| a claim, an exchange | name an amount and no line |
| the card | issue, hold, release and refund rows; no row and no column closed a card |
| a refund of a card's capture | written back onto the card as a `refund` row, under no lock of the card |

So a return of a card line could send money back while the mailed code still
paid, and nothing could stop a card from paying.

## 2. The order's side

`TestAGiftCardLineCannotBeReturned`: a return naming a card line and another
line is refused 409 `order_gift_card_line_final` and leaves no record; a return
naming only the other line is written. `TestAGiftCardLineCannotBeWrittenOff`:
the same for a write-off.

## 3. The card's side

`TestAClosedCardHoldsNothing` closes a card of 7,000: a `void` row of −7,000
with no session, the reason trimmed, `disabled_at` set, and the card's lock
taken. A second close writes nothing and keeps the first reason; a card an
authorized session holds is refused 409 `payment_gift_card_held`; a card with
nothing left is closed without a void; a blank reason is refused; a closed card
gets no new code.

The provider answers a closed card's code 409 `payment_gift_card_disabled` both
at the checkout's early check and when a session is opened, and refuses a
refund onto a closed card after taking its lock; a refund onto an open card is
written as before.

## 4. On the real schema

`TestAClosedCardOnTheRealSchema` closes an issued card of 5,000 and a sold card
of 3,000. Both read a balance of zero; the journal books the first as
`gift_card_void` (Dr `gift_card`, Cr `gift_card_granted` 5,000) and the second
as `gift_card_forfeit` (Cr `gift_card_forfeited` 3,000); the issued card's code
opens no session.

`TestAHeldCardIsClosedOnceItsPaymentEnds`: an authorized session's hold refuses
the close; once the session is canceled the close voids the released balance.

`TestACloseWaitsOnTheCard`: a competitor holds the card's lock and writes an
authorized session and its hold of 2,000; the close is seen waiting through
`pg_blocking_pids`, and after the competitor commits it refuses the held card,
which still holds 3,000. `TestTwoClosesAtOnceCloseOnce`: a close that waited
while another closed the card reads it again and succeeds without writing; the
first reason stands.

`TestAClosedCardRefusesTheRefundOfWhatItPaid`: a captured 2,000 is not refunded
onto the card after it is closed. `TestAClosedCardStopsTheCollectionRefundAtItsShare`:
a collection of 5,000 paid by the card (2,000) and the manual provider (3,000),
the provider captured first; after the close, the collection's refund starts at
the card's capture, is refused, and moves nothing; the provider's 3,000 is then
refunded through its own payment.

`TestTheCloseConstraintsAreTheLastDefence` names each refusal: a positive void,
a void with a session, a second void, a close without a reason, and a blank
reason. `TestAClosedCardHoldsBackTheRollback`: 000011 does not roll back while a
closed card exists.

## 5. On the production wiring

`TestABoughtCardIsClosedAndNotReturned`: a customer buys a 5,000 TRY card at
checkout. The storefront's return request for its line is answered 409
`order_gift_card_line_final`. The admin endpoint closes the card, answering a
balance of zero and `disabled_at`. A guest cart paid with the code is answered
409 `payment_gift_card_disabled`. The order journal's `gift_card` credit of
5,000 and the payment journal's forfeit of the card's void row, found by its id,
leave the card owed nothing across the two journals.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| C1 | the close voiding nothing | the service's tests, the module's integration tests, the end-to-end test |
| C2 | the close taking no lock | the service's test, the lock test |
| C3 | the holds not read | the service's test, the held-card and lock tests |
| C4 | a closed card closed again | the service's test |
| C5 | the card not read again under the lock | the double-close test, once written |
| C6 | a closed card opening a payment | the provider's test, the end-to-end test |
| C7 | a refund landing on a closed card | the provider's test, the module's refund tests |
| C8 | the refund's check taking no lock | the provider's test |
| C9 | a sold card's close booked as a grant | the module's integration test, the end-to-end test |
| C10 | the void's sign kept | the chart test |
| C11 | a card line returned | the order service's test, the end-to-end test |
| C12 | a card line written off | the order service's test |
| C13 | every line refused on an order with a card | the order service's tests |
| C14 | a closed card given a new code | the service's test |
| C15 | a positive void allowed | the constraint test |
| C16 | a second void allowed | the constraint test |
| C17 | 000011's rollback not stopping | the rollback test, once it asserted the server's report (D148) |
| C18 | the close's route not mounted | the API's tests, the end-to-end test |
| C19 | a captured session counted as a hold | the module's integration tests |

C5 survived the first run: no test made two closes meet. `TestTwoClosesAtOnceCloseOnce`
was written and kills it; without the second read the waiting close fails on a
card that is no longer open.

C7 first failed to compile, which is not a kill, and was written again.

C17 survived the first run. Without its refusal the rollback was still stopped,
by `payment_gift_card_entries_kind_valid` (the void row is not in the old
vocabulary), and the test asserted the refusal's name in the error's text, which
carries the down file and so names the refusal where it drops it. The test now
asserts `check constraint "payment_gift_cards_none_closed_on_rollback"`, the
server's own report, and kills it. The other four rollback tests of the payment
and order modules asserted the same way and were changed alike (D148).
