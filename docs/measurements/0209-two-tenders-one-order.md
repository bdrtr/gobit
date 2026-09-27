# Two tenders, one order — measured 2026-09-27

The evidence behind [ADR 0209](../adr/0209-a-gift-card-pays-first-and-a-provider-the-rest.md).

## 1. What a split payment could stand on

| Part | State |
|---|---|
| the checkout | one `payment_provider_id`, one session holding the whole amount, one capture |
| a collection's second session | opened for the amount less what is captured and what live sessions hold; an AUTHORIZED session holds only what it blocked (`SumLiveSessionAmounts`, ADR 0118) |
| a partial authorization | allowed by the contract (`AuthResult.AuthorizedAmount`); the payment module keeps what the provider reports |
| a decline | the payment module's `AuthorizePayment` returns it as an error (`payment_authorization_declined`), not as a zero authorization |
| the capture step's rollback | only when the collection shows nothing captured; otherwise the execution stops for a person |
| a collection's refund | draws the newest capture first (`ListPaymentsByCollection`, `created_at DESC`) |
| recovery after an authorization | stopped by the capture step (`BlocksRecovery`); the records are read back, not acted on |

## 2. On the production wiring

`TestACardAndAProviderPayOneOrder`: a 3,000 TRY card and the manual provider
pay one cart. The collection holds two captures, 3,000 from `gift_card` and the
rest from `manual`, and the card is empty. A refund of 1,000 through the
collection puts 1,000 back on the card, the newer capture; a refund of the
rest puts the card back to 3,000. The code is in no payment session and no
workflow record.

`TestACardThatCoversTheOrderLeavesTheProviderUnasked`: a 50,000 card beside the
manual provider pays the whole cart; the collection has one capture and no
session at the provider.

`TestASplitWithACardThatCannotPayOpensNoOrder`: an unissued code beside the
manual provider is refused with 422 and no order is opened.

`TestACardAloneThatDoesNotCoverTheOrderIsReleased`: a 1,000 card alone for a
larger cart is answered 409 `checkout_workflow_payment_underauthorized`, and the
card holds 1,000 again.

## 3. In the saga

The checkout's tests script the payment module and follow each path: the card's
session opened first; the provider's session opened for the rest, or not at
all; the provider captured before the card, each for what it held; both holds
released when the provider falls short or its capture fails with nothing
captured; a card declining before the provider is asked; a card capture failing
after the provider's stopping for a person; and both steps' records restoring
the card's session, what it held and its capture.

The authorization step first carried two guards on the card's answer, a card
holding nothing and a card holding more than the order. The payment module
returns a decline as an error, reads a report of zero as the whole session and
refuses one above it, so neither guard could be reached; both were removed.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| S1 | a card declining instead of paying what it holds | the provider's test, the end-to-end test |
| S2 | an empty card authorized for nothing | the provider's test |
| S3 | the card captured first | the saga's test |
| S4 | the provider captured for the whole amount | the saga's test, the end-to-end test |
| S5 | a provider shortfall leaving the card held | the saga's test |
| S6 | the compensation leaving the card held | the saga's test |
| S7 | the provider asked when the card covers all | the saga's test, the end-to-end test |
| S8 | the card not checked before the order | the saga's test, the end-to-end test |
| S9 | the code written to the record | the saga's test, the end-to-end test |
| S10 | a card paying the rest of itself | the saga's test |
| S11 | the storefront dropping the code | the end-to-end tests |
| S12 | the record restoring no card | the saga's test |
| S13 | the code under another key | the name gate, the end-to-end tests |
| S14 | the provider having to cover everything | the saga's tests, the end-to-end test |
| S15 | an empty card passed over | the saga's test |
