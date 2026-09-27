# A card that ran out — measured 2026-09-27

The evidence behind [ADR 0214](../adr/0214-a-gift-card-can-expire.md).

## 1. What a moment could stand on

| Part | State |
|---|---|
| a card | paid for as long as its code was presented; no column held a moment |
| closing a card | ADR 0213: under the card's lock, refused while a payment holds it, voiding what it held and booking it by the card's source |
| the installation's settings | read by the composition root and handed to the payment module as options, as the loyalty rate is |
| jobs | run on an interval by the runner, with no state between runs; the scheduled publisher writes at a moment an operator named |
| the sale's mail | `code`, `amount`, `currency_code`, `order_id` |

## 2. The service and the provider

`TestAnOperatorNamesACardsMoment`: the moment given at issue is the card's.
`TestAMomentBehindIsRefused`: a moment already past is refused and no card is
made. `TestTheInstallationsValidityReachesEveryCard`: with a validity of 365
days, an issued card with no moment and a sold card are both written with it.
`TestTheValidityIsBounded`: −1 and 36,501 days are refused at construction, as
the configuration refuses them at load (`TestTheGiftCardValidityIsBoundedAtBothEnds`),
and a gate holds the two ceilings equal.

`TestExpiredCardsAreClosedAndHeldOnesWait`: of three cards, one expired is
closed with the reason `expired` and a void row, one expired and held by a
payment is counted and left, and one without a moment is left.

`TestAnExpiredCardOpensNoPayment`: a card an instant past its moment is answered
409 `payment_gift_card_expired` when a session is opened, and one an hour
before it pays (`TestACardBeforeItsMomentPays`). `TestARefundOntoAnExpiredCardIsRefused`:
a captured payment is not refunded onto a card past its moment.

`TestTheInteropCarriesACardsMoment`: the interop hands the sale flow the card's
moment in RFC 3339, and nothing for a card that never expires; the flow's test
holds the mail to carry it.

## 3. On the real schema

`TestAGiftCardExpiresOnTheRealSchema`, with a validity of 30 days: a sold card's
`expires_at` is its `created_at` plus 30 days to the microsecond, both from the
insert's `now()`. An operator's moment stands. A moment equal to the issue is
refused by `payment_gift_cards_expires_after_issue`. With its moment moved
behind, a card's code opens no session (409 `payment_gift_card_expired`); the
expiry pass then closes it with the reason `expired` and a zero balance, leaves
the sold card open, and a second pass closes nothing.

`TestAnExpiringCardHoldsBackTheRollback`: 000012 does not roll back while a card
has a moment, stopped by `payment_gift_cards_none_expiring_on_rollback` itself.

## 4. On the production wiring

`TestAnInstallationsValidityExpiresItsCards` opens an installation with
`PAYMENT_GIFT_CARD_VALIDITY_DAYS=30`: a sold card is made 30 days out, so the
setting crossed the configuration, the composition root and the module. With
the card's moment moved behind, the `gift-card-expiry` job taken from
`registerJobs` reports `closed 1 expired gift cards; 0 wait for a payment that
holds them`, the card is closed as expired with nothing left, and a second run
reports nothing closed. `TestEveryJobTheRootDeclaresCanBeBuiltAgainstARealInstallation`
names the job.

`TestAnExpiredCardIsRefusedAtTheStorefront`: an operator issues a card an hour
out through the admin API, which answers the moment; with the moment moved
behind, a guest cart paid with the code is answered 409
`payment_gift_card_expired` and no order is opened.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| X1 | the validity not handed to the store | the service's test, the module's integration tests, the production test |
| X2 | the store giving every card one day | the module's integration test, the production test |
| X3 | an operator's moment dropped | the module's integration test, the end-to-end test |
| X4 | a moment behind accepted | the service's test |
| X5 | an expired card paying | the provider's tests, the module's integration test, the end-to-end test |
| X7 | a held card stopping the pass | the service's test |
| X9 | the job built and not registered | the job registration gate (D147), the production tests |
| X10 | the job asking for no cards | the job's test, the production test |
| X11 | the application not passing the validity | the configuration gate, the production test |
| X12 | the module not passing the validity | the production test |
| X13 | the configuration not bounding the validity | the configuration test |
| X14 | the service not bounding the validity | the service's test |
| X15 | a moment before the issue allowed | the module's integration test |
| X16 | 000012's rollback not stopping | the rollback test |
| X17 | closed cards read again by the pass | the module's integration test, the production test, once they ran a second pass |
| X18 | the mail without the moment | the flow's test |
| X19 | the interop without the moment | the interop test, once written |
| X20 | the API dropping the moment | the end-to-end test |

X17 survived the first run: nothing ran the pass twice. Reading closed cards
again would count them as closed on every pass and, past a hundred of them,
spend each pass's batch on cards already closed; both tests now run a second
pass and see nothing closed. X19 survived the first run: the flow's test fakes
the interop, and nothing else read the moment through it; the interop test was
written. X18 first failed to compile, which is not a kill, and was written
again. X2 was first written as a query that could not type its result and
failed every insert; it was written again as a card of one day, and was killed
by the assertions on thirty.
