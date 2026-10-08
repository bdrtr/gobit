# What an exchange prices — measured 2026-10-07

Evidence for [ADR 0432](../adr/0432-an-exchange-names-its-return-and-prices-what-it-sends.md)
and gap D247, first commit (the exchange names its return and prices what it
sends; the documents are the second). Read on a tree at 7c143c96; every Go
command ran as `GOTOOLCHAIN=go1.26.6`, the integration and e2e lanes against
Docker's `postgres:16-alpine` through testcontainers.

## 1. The reproduction, before the change

`internal/e2e/exchange_pricing_test.go` places an order of two shirts at
45 000 taxed 20% (108 000) through the checkout on the production wiring,
opens a return of one shirt, and opens an exchange naming that return over
`POST /admin/v1/orders/{id}/exchanges` with `return_id`.

On 7c143c96 with the test added, the exchange is refused before anything is
priced:

```
expected: 201
actual  : 422
{"error":{"code":"order_invalid_request","message":"the request body could not be parsed"}}
```

The body's `return_id` is a field the route does not know: an exchange named
nothing it took back, its replacement item carried no price, and its
`difference_due` was whatever the request typed. The order module's own tests
of the record do not compile on that tree, since the input, the model and the
store have none of the fields they name.

## 2. A shirt for a jacket, at two rates

The e2e test sends a jacket the order never sold, priced 61 000 in the
order's region and taxed 20% by the tax module.

| Step | Figure |
|---|---|
| one shirt back, 108 000 / 2 | 54 000 |
| the exchange opens at | −54 000 |
| the jacket's quote: 61 000 + 12 200 tax | 73 200 (`priced_by` `quote`) |
| the difference, 73 200 − 54 000 | 19 200 |
| a collection opened and captured for 19 200 | funded |
| the shirt's return received, its refund asked | 409 `returns_workflow_return_settled_by_exchange` |
| the order's refunded total | 0 |

The order module's own tests stand on an order whose figures do not divide
by their quantities, so every share is rounded:

| Line | Units | Sold | Discount | Tax | Total |
|---|---|---|---|---|---|
| A, 20% | 3 × 1 000 | 3 000 | 199 | 560 | 3 361 |
| B, 1% | 2 × 700 | 1 400 | 0 | 14 | 1 414 |

| Act | Figure |
|---|---|
| one unit of A back: ⌊3 361 / 3⌋ | 1 120, the exchange opens at −1 120 |
| one unit of A sent: ⌊3 361 / 3⌋, tax ⌊560 / 3⌋ | 1 120 and 186, difference 0 |
| two units of A sent for two back: ⌊3 361 × 2 / 3⌋, tax ⌊560 × 2 / 3⌋ | 2 240 and 373, difference 0 |
| two jackets quoted at 900 taxed 1% | 1 818, difference 1 818 − 1 120 = 698 |
| the unit of A withdrawn | difference 698 again |
| funding checked against 1 818 | 409 `order_exchange_difference_moved` |
| funding checked against 698 | funded; the jackets' withdrawal 409 `order_exchange_difference_held` |

A stacked line of 7 units, 7 913 with 350 at 5% and 563 at 8% compound,
sends 3 units at ⌊7 913 × 3 / 7⌋ = 3 391 with ⌊913 × 3 / 7⌋ = 391 tax,
shared over the rates by what each charged and the largest remainder:
149.89 and 241.10 become 150 and 241, on bases 3 000 and 3 150.

A line's units are priced as the next units of it the exchange sends, so an
even swap in pieces nets to the returned units' worth. On a line of 3 362
over three units (the remainder is two), before the review and after:

| Act | Per item | As the next units |
|---|---|---|
| two units back | −2 241 | −2 241 |
| the first unit sent | 1 120 | 1 120 |
| the second unit sent, in its own replacement | 1 120 | ⌊3 362 × 2 / 3⌋ − 1 120 = 1 121 |
| the difference | −1 | 0 |

A withdrawal while the exchange is requested prices the live pieces of every
line again from its first unit, in the order they were written. The follow-up
review's probe is a line of 10 005 over ten units, whose units are worth
1 000 and 1 001 in turn, with four back (−4 002). Single units are sent and
withdrawn: X1, Y1, X1 withdrawn, Y2, X2, Y3, X2 withdrawn, Y4.

| Rule | Y1, Y2, Y3, Y4 | Difference |
|---|---|---|
| a piece keeps the units it was written for | 1 001, 1 001, 1 001, 1 001 | +2 |
| the live pieces are priced again after each withdrawal | 1 000, 1 001, 1 000, 1 001 | 0 |

With Y3 and Y4 withdrawn as well, Y1 is 1 000 and Y2 1 001, and the
difference is 2 001 − 4 002. A seeded test runs twelve sends and withdrawals
on four lines (10 005/10, 3 362/3, 7 913/7, 12 011/12) with six seeds each,
and after every step the live pieces' totals and taxes are the share of the
units they send and the difference that share less the return's worth. A
settled exchange's pieces are not priced again; reopened by a canceled
parcel it is: 10 005 over ten, two back, pieces of 1 000 and 1 001, the first
withdrawn after the exchange settled at 0 (the second stays 1 001 and the
figure 0), then the second's parcel canceled: requested again, the second is
1 000 and the difference 1 000 − 2 001.

A stack of three rates charging 1, 3 and 3 over six units splits the tax for
three units 1/1/1 and for four 0/2/2; the fourth unit's first rate would be
−1, so that item's own tax is shared instead, 0/1/0. A line of 50 over a
hundred units with 8 of tax gives a single unit at the thirteenth place 0 with
1 of tax: written there it answers 409 `order_replacement_tax_above_total`,
and so does a withdrawal that would price a piece there.

An order whose prices include their tax: line A is 3 × 1 200 stickers less a
discount of 1, 600 tax in it, 3 599 paid. One unit back is −1 199; the unit
sent is 1 199 with 200 tax at the sticker 1 200; two jackets quoted at the
sticker 1 210 are 2 420 with 403 tax in them, and a quote of 2 421 for the
two stickers is refused; the difference is 2 420.

A replacement of a priced exchange is withdrawn as follows: while the
exchange is requested, and its line items are priced again and its difference
derived again; while it is
funded, refused with 409 `order_exchange_difference_held` before any unit
goes back (the flow reads `withdrawable` first); after the exchange's refund
withdrew it, withdrawn, the figure 698 staying as it stood; after it was
settled at 0 with one of two one-unit replacements, the other withdrawn and
the figure staying 0.

## 3. The race the exchange's lock exists for

`TestWithdrawingAReplacementDerivesWhatTheExchangeSendsAfterItsLock` holds the
exchange's row on a connection of its own and writes, uncommitted, a
replacement of two jackets at 1 818. The withdrawal of the unit of A waits on
that row, seen in `pg_stat_activity`, and once the holder commits derives
1 818 − 1 120 = 698. Reading the exchange without the lock (mutant M33)
derives 0 − 1 120 from a sum taken before the jackets were committed, and the
write that follows waits on the same row and then puts −1 120.

## 4. Mutants

Each mutant was applied alone to the tree above, its tests run with `-count=1`, and the file restored; the baseline of every command was green first. An integration mutant ran its own tests of `internal/modules/order` with `-tags integration`.

| # | Mutant | Verdict | Red test |
|---|---|---|---|
| M01 | typed difference beside return | killed | `TestAnExchangeThatNamesItsReturnTakesNoTypedDifference` |
| M02 | difference not derived | killed | `TestAnExchangeThatNamesItsReturnOwesWhatComesBack`, `TestAnItemNamingALineIsPricedAtWhatTheLineCharged` |
| M03 | return of another order | killed | `TestAnExchangeNamesAReturnOfItsOwnOrder` |
| M04 | received return named | killed | `TestAReturnIsNamedOnlyWhileItIsRequested` |
| M05 | return named twice | killed | `TestAReturnIsTakenBackByOneLiveExchange` |
| M06 | empty return named | killed | `TestAReturnThatNamesNoLineCannotBeTakenBack` |
| M07 | return not locked | killed | `TestAnExchangeThatNamesItsReturnOwesWhatComesBack` |
| M08 | cancel return under exchange | killed | `TestAReturnAnExchangeTakesBackIsNotWithdrawn` |
| M09b | detail names no exchange | killed | `TestTheReturnDetailNamesTheExchangeThatSettlesIt` |
| M10 | line total ceil | killed | `TestALineSentInPartSharesItsStackByWhatEachRateCharged`, `TestTheGoodsBackAreWorthTheSameShareTheGoodsSentAre` |
| M11 | line priced from subtotal | killed | `TestALineSentInPartSharesItsStackByWhatEachRateCharged`, `TestTheGoodsBackAreWorthTheSameShareTheGoodsSentAre` |
| M12 | worth from subtotal | killed | `TestTheGoodsBackAreWorthTheSameShareTheGoodsSentAre`, `TestAnExchangeThatNamesItsReturnOwesWhatComesBack` |
| M13 | line takes a quote | killed | `TestAnItemNamingALineIsPricedAtWhatTheLineCharged`, `TestWithdrawingAReplacementDerivesTheDifferenceAgain` |
| M14 | quote without channel | killed | `TestAnItemNamingAVariantIsPricedByTheQuote` |
| M15 | currency moved accepted | killed | `TestAQuoteInTermsTheSaleCannotCarryIsRefused` |
| M16 | convention moved accepted | killed | `TestAQuoteInTermsTheSaleCannotCarryIsRefused` |
| M17 | total identity unchecked | killed | `TestAQuoteOutsideItsContractIsRefused` |
| M18 | subtotal units unchecked | killed | `TestAQuoteOutsideItsContractIsRefused` |
| M19 | discount unchecked | killed | `TestAQuoteOutsideItsContractIsRefused` |
| M20 | rate unchecked | killed | `TestAQuoteOutsideItsContractIsRefused` |
| M21 | components sum unchecked | killed | `TestAQuoteOutsideItsContractIsRefused` |
| M22 | negative tax accepted | killed | `TestAQuoteOutsideItsContractIsRefused` |
| M23 | operator price recorded as quote | killed | `TestAnOperatorPriceIsQuotedForItsTax` |
| M24 | operator price not sent | killed | `TestAnOperatorPriceIsQuotedForItsTax` |
| M25 | no quote panics or passes | killed | `TestAVariantIsNotSentUnpricedWithoutAQuote` (panics without it) |
| M26 | components tie reversed | killed | `TestALineSentInPartSharesItsStackByWhatEachRateCharged`, `TestApportionGivesTheRemainderToTheLargestFractions` |
| M27 | price on line accepted | killed | `TestAUnitPriceIsTakenOnlyWhereSomethingIsPriced` |
| M28 | price where nothing priced | killed | `TestAUnitPriceIsTakenOnlyWhereSomethingIsPriced` |
| M29 | create does not derive | killed | `TestAnItemNamingALineIsPricedAtWhatTheLineCharged`, `TestAnItemNamingAVariantIsPricedByTheQuote` |
| M30 | status not rechecked under lock | killed | `TestAReplacementIsAddedOnlyToAnExchangeStillOpenUnderItsLock` |
| M31 | withdrawal does not derive | killed | `TestWithdrawingAReplacementDerivesTheDifferenceAgain` |
| M32 | funded replacement withdrawn | killed | `TestAFundedExchangesReplacementIsNotWithdrawn` |
| M33 | withdrawal reads exchange unlocked | killed | `TestWithdrawingAReplacementDerivesWhatTheExchangeSendsAfterItsLock` |
| M34 | funding figure unchecked | killed | `TestAFundingCheckedAgainstAnotherFigureIsRefused` |
| M35 | flow sends no figure | killed | `TestAHeldDifferenceIsRecorded` |
| M36 | settled return refunded | killed | `TestAReturnAnExchangeTakesBackIsNotRefunded` |
| M37 | quote without facts | killed | `TestAnExchangeQuoteTaxesAGiftCardAsACartDoes` |
| M38 | operator line list priced | killed | `TestAnOperatorsPriceIsTaxedAsTheVariantWouldBe` |
| M39 | quote snapshot without channel | killed | `TestAnExchangeQuotePricesAsTheOrdersCartWould` |
| M40 | operator line untaxed | killed | `TestAnOperatorsPriceIsTaxedAsTheVariantWouldBe` |
| M41 | quote decodes loosely | killed | `TestTheExchangeQuoteCrossesAsJSON` |
| M42 | api drops return | killed | `TestAnExchangeNamesItsReturnOnTheWire` |
| M43 | api hides return | killed | `TestAnExchangeNamesItsReturnOnTheWire` |
| M44 | api drops unit price | killed | `TestAReplacementItemCarriesItsPriceOnTheWire` |
| M45 | api hides price | killed | `TestAReplacementItemCarriesItsPriceOnTheWire` |
| M46 | panel drops return | killed | `TestEachOpeningReachesItsOwnMethod` |
| M47 | panel offers taken return | killed | `TestTheExchangeFormOffersTheReturnsItCanTakeBack` |
| M48 | panel offers received return | killed | `TestTheExchangeFormOffersTheReturnsItCanTakeBack` |
| M49 | panel canceled exchange holds | killed | `TestTheExchangeFormOffersTheReturnsItCanTakeBack` |
| M50 | no priced together | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M51 | components without price | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M52 | any source | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M53 | no shape | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M54 | tax past total | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M55 | rate past 100 | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M56 | components any json | killed | `TestThePriceConstraintsAreTheLastDefence` |
| M57 | return of any order | killed | `TestAnExchangeNamesOneLiveReturnOfItsOwnOrder` |
| M58 | canceled exchange holds return | killed | `TestAnExchangeNamesOneLiveReturnOfItsOwnOrder` |
| M59 | rollback drops the link | killed | `TestARollbackRefusesAnExchangeTakingAReturnBack` |
| M60 | sent counts withdrawn | killed | `TestAPricedExchangeDerivesItsDifferenceOnTheSchema` |
| M61 | live exchange counts withdrawn | killed | `TestAWithdrawnExchangeLetsItsReturnGoOnTheSchema` |
| M62 | components not written | killed | `TestAPricedExchangeDerivesItsDifferenceOnTheSchema` |
| M63 | derived difference on any exchange | killed | `TestASoldLineIsNotRewrittenInSQL` |
| M64 | derived difference when funded | killed | `TestASoldLineIsNotRewrittenInSQL` |
| M65b | price range without its NULL guards | killed | `TestEveryCheckConstraintAnswersTrueOrFalse` (`internal/app`, ADR 0169) |

M09 was first written as deleting the field, which does not compile, and M65 as a clause that left a parenthesis open, which does not parse; M09b and M65b are the versions that build, and they are the rows above.

The independent review ran two mutants that survived the first commit, R1
and R2; both are killed now. The rest are the review round's own.

| # | Mutant | Verdict | Red test |
|---|---|---|---|
| R1 | the derived write's narrowing with `OR id = $1` appended | killed | `TestASoldLineIsNotRewrittenInSQL` |
| R2 | the tax-inclusive identity of a quote not checked | killed | `TestAnInclusiveQuoteWhoseTotalIsNotItsStickersIsRefused` |
| N01 | a withdrawal refused on every exchange not requested | killed | `TestTheExitLetsAFundedExchangesReplacementGo`, `TestASettledExchangesOtherReplacementIsWithdrawn` |
| N02 | a withdrawal allowed while funded | killed | `TestAFundedExchangesReplacementIsNotWithdrawn`, `TestTheExitLetsAFundedExchangesReplacementGo` |
| N03 | the difference derived after the exchange left requested | killed | `TestTheExitLetsAFundedExchangesReplacementGo`, `TestASettledExchangesOtherReplacementIsWithdrawn` |
| N04 | the flow releases before it reads `withdrawable` | killed | `TestAReplacementItsExchangeHoldsMoneyForIsRefusedBeforeAnyRelease` |
| N05 | the detail ignores a funded exchange | killed | `TestTheDetailSaysWhetherAReplacementCanBeWithdrawn` |
| N06 | the detail ignores a dispatch | killed | `TestTheDetailSaysASentReplacementCannotBeWithdrawn` |
| N07 | a line priced from its first unit | killed | `TestAnEvenSwapSentInPiecesOwesNothing` |
| N08 | the units already sent not read | killed | `TestAnEvenSwapSentInPiecesOwesNothing` |
| N09 | the rates' earlier shares not taken off | killed | `TestAStackSentInPiecesGivesEachRateItsShareOfTheWhole`, `TestARateNeverGoesBelowNothing` |
| N10 | the units already sent counting withdrawn replacements | killed | `TestWithdrawalsPriceAnExchangesLineAgainOnTheSchema` (integration; rerun after the follow-up round) |
| N11 | a rate left below nothing | killed | `TestARateNeverGoesBelowNothing` |
| N12 | a rate's base shared per piece | killed after the fixture's base was made not to divide by the units; it survived on one that did | `TestAStackSentInPiecesGivesEachRateItsShareOfTheWhole` |

The follow-up review's round:

| # | Mutant | Verdict | Red test |
|---|---|---|---|
| P01 | a requested withdrawal not pricing the pieces again | killed | `TestWithdrawingAndSendingAgainKeepsAnEvenSwapAtNothing`, `TestAnyWritesAndWithdrawalsKeepALinesItemsAtItsShare` |
| P02 | the pieces read again counting withdrawn replacements | killed | `TestWithdrawalsPriceAnExchangesLineAgainOnTheSchema` (integration) |
| P03 | the pieces priced again in the reverse order | killed after Y3 and Y4 were withdrawn too; three single pieces read the same either way | `TestWithdrawalsPriceAnExchangesLineAgainOnTheSchema` |
| P04 | a settled exchange's pieces priced again | killed | `TestASettledExchangeIsNotPricedAgainUntilItReopens` |
| P05 | a reopened exchange's figure not derived | killed | `TestASettledExchangeIsNotPricedAgainUntilItReopens` |
| P06 | a reopened exchange's pieces not priced again | killed | `TestASettledExchangeIsNotPricedAgainUntilItReopens` |
| P07 | a piece with more tax than total written | killed | `TestUnitsWorthUnderAMinorUnitAreNotSentAboveTheirTotal` |
| P08 | a piece priced again with more tax than total | killed | `TestAWithdrawalThatWouldPriceAPieceAboveItsTotalIsRefused` |
| P09 | an absent `withdrawable` read as an answer | killed | `TestAnUnansweredWithdrawableReleasesNothing` |

Every line of the exchange is priced again, not only the withdrawn piece's:
while the exchange is requested the other lines' pieces already run from
their first unit and nothing is written for them, and a reopened exchange
needs every line. A "re-pricing other lines" mutant therefore has no line
filter to remove.

## 5. What a parcel that came back holds, with ADR 0423

ADR 0423 holds a parcel that came back undelivered to what a line's live
returns ask back plus what its live replacements send again. An exchange that
names its return names the same goods in both. Its follow-up review's F2:
three units sold, a parcel of three came back, and an exchange takes one unit
back through its return and sends one of the same line again.

| Count | Spoken for | Owed again |
|---|---|---|
| the sum | 1 + 1 = 2 | 1 |
| the exchange once, the more of the two | 1 | 2 |

On 6a86ab7e with the tests added, `TestAnExchangeSpeaksForAUnitItTakesBackAndSendsAgainOnce`
read 2 where 1 is right, and the e2e subtest "an exchange of one unit leaves
the other two owed" of `TestAParcelThatCameBackIsSentAgainOrPutBack` opened a
parcel of two and got:

```
expected: 201
actual  : 409
fulfillment_line_not_dispatchable: line … owes 1 more unit(s) and the parcel asks for 2
```

With the fix both pass. An exchange that sends two for one back speaks for
two, one that takes two back and sends one for two, and a return beside an
exchange that names no return, or beside a claim's replacement, still adds to
it.

| # | Mutant | Verdict | Red test |
|---|---|---|---|
| S1 | the sum kept | killed | `TestAnExchangeSpeaksForAUnitItTakesBackAndSendsAgainOnce`, the e2e subtest |
| S2 | the fewer counted instead of the more (`GREATEST` taken off) | killed | `TestAnExchangeSpeaksForTheGoodsItNamesTwiceOnceOnTheSchema` (integration) |
| S3 | the named return left out entirely | killed | `TestAnExchangeSpeaksForTheGoodsItNamesTwiceOnceOnTheSchema` |
| S4 | applied to an exchange that names no return | killed once the redundant `order_return_id IS NOT NULL` went with the join; with it kept the mutant changes nothing | `TestAnExchangeSpeaksForTheGoodsItNamesTwiceOnceOnTheSchema` |

## 6. Lanes

Run last on the commit rebased onto 6a86ab7e (ADR 0423 and ADR 0430 merged):

| Lane | Result |
|---|---|
| `gofmt -l` on the changed files | empty |
| `go vet ./...`, `go vet -tags integration ./...` | clean |
| `sqlc generate` for every module | the generated files unchanged |
| `make lint` | 0 issues in the root and the five separate modules |
| `go test -count=1 -p=2` on the order and fulfillment modules, the panel, every flow and `internal/arch` | green |
| `go test -race` on the changed packages | green, before the rebase |
| `internal/modules/order` with `-tags integration`, whole | green |
| `internal/modules/fulfillment` with `-tags integration`, whole | green |
| `internal/e2e` with `-tags integration`, whole | green |
| `internal/app` `TestEveryCheckConstraintAnswersTrueOrFalse` | green over both migrations; red with the price range's NULL guards removed |
| `internal/arch` with `-tags integration`, whole | green before the reviews, not rerun |
| `go mod tidy` | `go.mod` and `go.sum` unchanged |

`make vuln`, `make smoke` and `make test-integration` as a whole were not
run; the integration packages the change touches were.

## 7. The documents, second commit

The record's second commit documents an exchange that names its return. Read
on a tree at 3a3ae9ad with the same toolchain and lanes.

### 7.1 The reproduction, before the change

`internal/e2e/exchange_documents_test.go` places an order of two shirts at
45 000 taxed 20% (108 000, of which 18 000 is tax) and invoices it. A return
takes one shirt back, and an exchange names that return. Its replacement is
then sent and dispatched:

- in one test a jacket the order never sold, taxed 1% by a rate rule on its
  product, with the difference funded;
- in the other the shirt line's own unit.

On 3a3ae9ad with the test added, every step up to the document passes, and
the request for the exchange's documents is refused:

```
--- FAIL: TestAnExchangesDocumentsCarryWhatItsBuyerPaid
    expected: 201
    actual  : 404
    {"error":{"code":"invoicing_act_unknown","message":"order order_… has no exchange act exch_…"}}
--- FAIL: TestAnEvenSwapNetsToNothing
    expected: 201
    actual  : 404
    {"error":{"code":"invoicing_act_unknown","message":"order order_… has no exchange act exch_…"}}
```

The order listed the exchange's funding, which the flow refused as
`invoicing_act_not_documented` ("an exchange's money is on no document"), and
no act carried its goods.

### 7.2 A shirt for a jacket, at two rates

| Step | Figure |
|---|---|
| one shirt back, 108 000 / 2, its tax 18 000 / 2 | 54 000 with 9 000 tax |
| the jacket's quote, 61 000 at 1% | 61 610 with 610 tax |
| the difference, funded | 61 610 − 54 000 = 7 610 |
| the refund, `returned`, key `exchange_returned:<id>`, on the shirt's row | 54 000 with 9 000 tax at 20% |
| the sale, `exchanged`, key `exchange_sent:<id>`, a row of its own | 61 610 with 610 tax at 1% |
| the documents, 108 000 + 61 610 − 54 000 | 115 610 = 108 000 + 7 610 paid |
| `tax_payable` on the order journal, 18 000 − 9 000 + 610 | 9 610 |
| `sales`, 90 000 + 7 610 + 9 000 − 610 | 106 000 = 45 000 kept + 61 000 sent |

The even swap sends the shirt line's unit back for the unit returned. The
difference is 0, so no money entry is written. The refund and the sale are
54 000 with 9 000 tax each, and `tax_payable` stays at 18 000.

### 7.3 Why the refund shares a row's tax by units

A returned row's units are given back at the row's total and tax shared by the
units the row sold, rounded down. That is how the order module counts them in
the difference and prices a line's units it sends again. The return split of
ADR 0406 instead rounds a part's tax on the share of the row's amount, and
the two differ on a discounted line. The flow's unit test uses such a line:
two shirts at 900, 21 off, taxed 20%, so 1 779 + 356 = 2 135.

| One unit | Total | Tax |
|---|---|---|
| sent again, the order module's share | ⌊2 135 / 2⌋ = 1 067 | ⌊356 / 2⌋ = 178 |
| given back by the share of the amount | 1 067 | ⌊1 067 × 356 / 2 135⌋ = 177 |
| given back by the share of the units | 1 067 | 178 |

Shared by amount, an even swap would leave one unit of tax on
`tax_payable`; shared by units, the two documents are equal and opposite.
The rates of a stacked row take their largest-remainder share of that tax by
what each charged, as the units sent do. Each rate's base is shared by units.
A row with less left than the units were sold for is refused, not shortened,
and the refusal names the documents that gave back on it first (§7.7). A rate
short of its share, and the row's last units, are taken as §7.7 says.

### 7.4 The books

The design's arithmetic, D = Vo − Vb booked to sales with each document's tax
moved against sales, holds for every sign of D. The property test
`TestAnExchangesDocumentsNetItsGoodsOnTheBooks` draws the three signs equally
and voids both documents half the time. It holds sales to
(Vo − To) − (Vb − Tb), `tax_payable` to To − Tb and receivable to D, and with
both documents voided to D, 0 and D.

### 7.5 Mutants

Each mutant was applied alone to a green tree by an exact replacement that was
checked to change its file, run with `-count=1` on the lane named, and the
files restored byte for byte.

| # | Mutant | Lane | Red test |
|---|---|---|---|
| X01 | the sale issued under the refund's key | unit, e2e | `TestAnExchangeIsDocumentedAsAReturnAndASale`, `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| X02 | `exchanged` fitting a refund, in Go | unit | `TestAReasonFitsItsKind` |
| X03 | `exchanged` fitting a refund, in the CHECK | integration | `TestTheSchemaHoldsAnExchangesReasonToASale` |
| X04 | what came back placed against receivable | unit | `TestAnExchangesDocumentsNetItsGoodsOnTheBooks`, `TestADocumentsTaxMovesFromTheActsAccountToTaxPayable` |
| X05 | what was sent placed against receivable | unit | `TestOnlyADearerDeliveryAndAnExchangeChargeAfterTheSale` |
| X06 | both against receivable, the property alone | unit | `TestAnExchangesDocumentsNetItsGoodsOnTheBooks` |
| X07 | the sale printing the returned row's rate | unit, e2e | `TestAnExchangeIsDocumentedAsAReturnAndASale`, `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| X08 | an exchange that names no return documented by the flow | unit | `TestAnExchangeIsDocumentedOnlyWhenItsGoodsHaveMoved` |
| X09 | an exchange that names no return listed by the order | unit | `TestOnlyAnExchangeThatNamesItsReturnIsAnAct` |
| X10 | the read after the refund removed | unit | `TestAnExchangeIssuesOnlyTheDocumentItLacks` |
| X11 | the read after the refund left stale | unit | `TestAnExchangeIssuesOnlyTheDocumentItLacks` |
| X12 | the first read's both-stand answer removed | unit | `TestAnExchangeIsDocumentedOnce` |
| X13 | documentable ignoring a replacement not sent, order side | unit | `TestAnExchangeThatNamesItsReturnIsOneAct` |
| X14 | documentable ignored, flow side | unit | `TestAnExchangeIsDocumentedOnlyWhenItsGoodsHaveMoved` |
| X15 | the named return's refund allowed (slice 1's refusal) | e2e | `TestAnExchangeThatNamesItsReturnPricesWhatItSends` |
| X16 | the refund's tax shared by amount | unit | `TestAnExchangeIsDocumentedAsAReturnAndASale` |
| X17 | the refund's total not held to its row | unit | `TestAnExchangeGivesBackNoMoreThanItsRowsHaveLeft` |
| X18 | the refund's tax not held to its row | unit | `TestAnExchangeGivesBackNoMoreThanItsRowsHaveLeft` |
| X19 | a rate's tax not held to its row (replaced in §7.7: a short rate gives its excess away) | unit | `TestAnExchangeGivesBackNoMoreThanItsRowsHaveLeft` |
| X20 | the rates shared by what each has left | unit | `TestTheRatesAreSharedByWhatEachCharged` |
| X21 | a rate's base not shared by units | unit | `TestAnEvenSwapIsEqualAndOpposite` |
| X22 | a sent row always multiplied out | unit | `TestAnItemIsPrintedInItsOrdersConvention` |
| X23 | an inclusive sent row's subtotal keeping its tax | unit | `TestAnItemIsPrintedInItsOrdersConvention` |
| X24 | a sent rate's base dropped | unit | `TestAnEvenSwapIsEqualAndOpposite` |
| X25 | an unnamed variant printed blank | unit | `TestAnItemIsPrintedInItsOrdersConvention` |
| X26 | already issued kept from a race the refund was issued in | unit | `TestAnExchangeIssuesOnlyTheDocumentItLacks` |
| X27 | an exchange's sale rows read as carriage | unit | `TestAnExchangesRowsAreNoCarriage` |
| X28 | the act documented with one of its two documents | unit | `TestAnExchangeIsListedWithBothDocuments` |
| X29 | the exchange not routed to its two documents | unit | `TestAnExchangeIsDocumentedAsAReturnAndASale` |
| X30 | the funding's refusal not naming the exchange act | unit | `TestAnExchangeIsDocumentedOnlyWhenItsGoodsHaveMoved` |
| X31 | the journal reading an exchange's keys as refunds | unit, e2e | `TestADocumentsTaxMovesFromTheActsAccountToTaxPayable`, `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| X32 | the journal not placing the sale's key | unit | `TestADocumentsTaxMovesFromTheActsAccountToTaxPayable` |
| X33 | the journal taking any cause as an exchange | unit | `TestADocumentTheJournalCannotPlaceIsAnError` |
| X34 | a withdrawn exchange listed (superseded: §7.7 lists it, Y13) | unit | `TestOnlyAnExchangeThatNamesItsReturnIsAnAct` |
| X35 | a withdrawn replacement sent | unit | `TestAWithdrawnReplacementSendsNothingOnTheAct` |
| X36 | what it sends not summed | unit | `TestAnExchangeThatNamesItsReturnIsOneAct` |
| X37 | the variants never named | unit, e2e | `TestAnExchangeReadAloneNamesTheVariantsItSends`, `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| X38 | the product's title not read | unit | `TestAnExchangeReadAloneNamesTheVariantsItSends` |
| X39 | the sent items left off the interop | unit | `TestAnExchangeReadAloneNamesTheVariantsItSends` |
| X40 | `exchange_sent` documented on a refund | unit | `TestAKeyNamesAnActTheJournalPlaces` |
| X41 | `exchange_returned` not placeable | arch | `TestTheDocumentedActKindsAgree` |
| X42 | `exchanged` unknown to `Valid` | unit | `TestAReasonFitsItsKind` |
| X43 | the fit CHECK answering NULL | integration | `TestEveryCheckConstraintAnswersTrueOrFalse` |
| X44 | the known CHECK without `exchanged` | integration | `TestTheSchemaHoldsAnExchangesReasonToASale` |
| X45 | the rollback's refusal removed | integration | `TestARollbackRefusesADocumentOfAnExchange` |
| X46 | the rollback refusing the sale's key alone | integration | `TestARollbackRefusesADocumentOfAnExchange` |
| X47 | the page offering a half documented exchange nothing | unit | `TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage` |
| X48 | the page not opening each document | unit | `TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage` |
| X49 | the catalog's title field misnamed | e2e | `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| X50 | a refunded difference booked against shipping | unit, the property alone | `TestAnExchangesDocumentsNetItsGoodsOnTheBooks` |
| X51 | a funded difference booked against shipping | unit, the property alone | `TestAnExchangesDocumentsNetItsGoodsOnTheBooks` |

X50 and X51 show only on one sign of the difference. With the property's
"owed to the buyer" draw removed, X50 survives it, and with "owed by the
buyer" removed, X51 does: each sign case is what kills its mutant. Four of
the mutants above also ran on the e2e lane; 55 runs in all, every one red.

### 7.6 Lanes

Run last on the follow-up's tree, rebased onto 200d4d1f (ADR 0428):

| Lane | Result |
|---|---|
| `gofmt -l` on the changed files, `go mod tidy` | empty, `go.mod` and `go.sum` unchanged |
| `go vet ./...`, `go vet -tags integration ./...` | clean |
| `make lint` | 0 issues in the root and the five separate modules |
| `go test -count=1 -p=2 ./...` | green |
| `go test -race` on the invoicing flow, the order service and API, the invoice module and the panel | green |
| `internal/modules/order` and `internal/modules/invoice` with `-tags integration`, whole | green |
| `internal/e2e` with `-tags integration`, whole | green |
| `internal/app` `TestEveryCheckConstraintAnswersTrueOrFalse` | green over 000008; red with the fit CHECK's NULL guard removed |
| `internal/arch` with `-tags integration`, whole | green, `TestMigrationsCanReallyBeRolledBack` over 000008 |

`make vuln`, `make smoke` and `make test-integration` as a whole were not
run; the integration packages the change touches were.

### 7.7 The review's follow-up

An independent review of the commit found one High, four Medium and five Low
findings. These are the figures behind the behaviour each one changed.

**A stacked row taken back in two parts.** Two bottles at 99.95 are taxed
10% and then 5% compound:

| Figure | Value |
|---|---|
| net | 19 990 |
| tax at 10% | 1 999 |
| tax at 5% compound, on 21 989 | 1 099 |
| total | 23 088 |

Half of the tax, 1 549, splits 999.5 : 549.5, and the largest remainder gives
the tie to the first rate. Before the follow-up the second half refused:

```
--- FAIL: TestTwoExchangesOfAStackedRowAreBothDocumented
    invoicing_act_does_not_fit: row 1's rate 1 has 999 tax left, and the 1 unit(s) taken back were taxed 1000 at it
--- FAIL: TestAReturnThenAnExchangeOfAStackedRowAreBothDocumented
    the same, after the return's refund split by what each rate had left
--- FAIL: TestAStackedRowTakenBackInPartsNeverOverdrawsARate
    invoicing_act_does_not_fit: row 1's rate 1 has 2 tax left, and the 2 unit(s) taken back were taxed 3 at it
```

With the fix, the per-rate figures of the two halves are these:

| Part | Total | Tax | 10% | 5% | Bases |
|---|---|---|---|---|---|
| the first half, by units | 11 544 | 1 549 | 1 000 | 549 | 9 995, 10 994 |
| the last half, what is left | 11 544 | 1 549 | 999 | 550 | 9 995, 10 995 |

Each rate gives back exactly what it charged, in either order and after a
return's refund. The bases are shared by units, and the last half's are each
base's rest. The base by the share of the amount would print 10 995 for the
first half; the review's mutant R1 survived the old fixture, whose bases
divided by their units, and dies on this one.

A rate short of its share otherwise gives the excess to the rate with the
most room, the earlier rate taking a tie. Three rates that charged 100 each
share 150 as 50 apiece; with 40 left on the second they print 60 / 40 / 50.

The property test draws two-rate stacks at any price from 1.00 to 500.00,
in two to six units. It draws up to six parts, exchanges by units and returns
by amount, and keeps every part that fits the row's total and tax. Each such
part is documented, its rates add up to its tax, and no rate gives back more
than it charged.

**The last units take what the row has left.** On the same row with a tax of
3 099, a return's refund of 11 544 gives back ⌊11 544 × 3 099 / 23 088⌋ =
1 549 and leaves 1 550. The exchange of the other bottle then takes 11 544
with 1 550, not the units' 1 549, and the row ends at nothing.

**A credit documented first.** A credit of 300 is spread by amount over the
flow fixture's three rows and takes 75 of the shirts' 2 135. An exchange of
both shirts is still refused, now with the remedy in the message:

```
invoicing_act_does_not_fit: row 1 has 2060 with 344 tax left, and the 2 unit(s) taken back were sold for 2135 with 356 tax: documents GBT2026000000002 (inv_amend_1) gave back on the row first, and the exchange cannot be documented while they stand on it
```

The message first prescribed voiding them, documenting the exchange and
issuing them again; §7.8 says why it no longer does. The invoice module's
amendable read names, per row, the live refunds that gave back on it.

**Documentable once the goods came back.** Before the fix the e2e test
documented the exchange with the shirt's return still requested:

```
--- FAIL: TestAnExchangesDocumentsCarryWhatItsBuyerPaid
    expected: 409
    actual  : 201
```

The order module's unit test read the act documentable with the return
requested. The act is now documentable once the return is received and every
replacement has left.

**A withdrawn exchange.** An exchange withdrawn after its documents is
listed, withdrawn and not documentable, with its documents. One withdrawn
with none is not listed, and a press on either is refused naming the
withdrawal.

**The answer.** Every answer of the amendment endpoint carries the act's
`documents`, each with `issued`. A press that lost both of an exchange's
races to another answers 200 `already_issued`; before the fix it answered as
if it had issued the sale.

The follow-up's mutants were applied as in §7.5, after green baselines, the
integration lane's included. Five of §7.5's were run again on the moved code,
all red.

| # | Mutant | Lane | Red test |
|---|---|---|---|
| Y01 | an over-drawn rate not clamped | unit | `TestAStackedRowTakenBackInPartsNeverOverdrawsARate`, `TestAnExchangeGivesBackNoMoreThanItsRowsHaveLeft` |
| Y02 | the excess not given to a rate with room | unit | `TestAReturnThenAnExchangeOfAStackedRowAreBothDocumented` |
| Y03 | the excess to the least room | unit | `TestAnOverdrawnRatesExcessGoesToTheMostRoom` |
| Y04 | rates holding less than the row not refused | unit | `TestAnOverdrawnRatesExcessGoesToTheMostRoom` |
| Y05 | the last units' tax not taken | unit | `TestTheLastUnitsTakeWhatTheRowHasLeft` |
| Y06 | the last units' base by units | unit | `TestTheLastUnitsTakeWhatTheRowHasLeft` |
| Y07 | the base by the share of the amount (the review's R1) | unit | `TestARefundsRateBasesAreSharedByUnits` |
| Y08 | the refusal naming no document | unit | `TestACreditDocumentedFirstIsNamedByTheExchangesRefusal` |
| Y09 | the rows' refunds dropped by the flow | unit | `TestACreditDocumentedFirstIsNamedByTheExchangesRefusal` |
| Y10 | a canceled refund named on its row | integration | `TestTheAmendableReadSaysWhatEachRowHasLeft` |
| Y11 | a charge named as giving back | integration | `TestTheAmendableReadSaysWhatEachRowHasLeft` |
| Y12 | documentable ignoring the receipt | unit, e2e | `TestAnExchangeIsDocumentableOnceItsReturnHasComeBack`, `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| Y13 | a withdrawn exchange not listed | unit | `TestOnlyAnExchangeThatNamesItsReturnIsAnAct` |
| Y14 | a withdrawn exchange documentable | unit | `TestAnExchangeIsDocumentableOnceItsReturnHasComeBack` |
| Y15 | a withdrawn exchange's documents dropped from the list | unit | `TestAWithdrawnExchangeKeepsItsDocumentsListed` |
| Y16 | a withdrawn exchange with none listed | unit | `TestAWithdrawnExchangeKeepsItsDocumentsListed` |
| Y17 | a press on a withdrawn exchange not naming it | unit | `TestAWithdrawnExchangeKeepsItsDocumentsListed` |
| Y18 | withdrawn left off the order's interop | unit | `TestOnlyAnExchangeThatNamesItsReturnIsAnAct` |
| Y19 | already issued read from the sale alone | unit | `TestAnExchangesAnswerSaysWhatThePressIssued` |
| Y20 | the refund always answered as issued | unit | `TestAnExchangesAnswerSaysWhatThePressIssued` |
| Y21 | a refund another press wrote answered as issued | unit | `TestAnExchangesAnswerSaysWhatThePressIssued` |
| Y22 | the answer's documents dropped by the endpoint | e2e | `TestAnExchangesDocumentsCarryWhatItsBuyerPaid` |
| Y23 | the withdrawn text missing on the page | unit | `TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage` |
| Y24 | an exchange not yet documentable saying no document carries it | unit | `TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage` |
| Y25 | a half documented exchange not said to have a rest | unit | `TestAnOrdersActsAfterTheSaleAreDocumentedOnItsPage` |
| Y26 | the read after the refund removed, on the moved code | unit | `TestAnExchangeIssuesOnlyTheDocumentItLacks` |
| Y27 | the first read's both-stand answer removed, on the moved code | unit | `TestAnExchangeIsDocumentedOnce` |

One branch first written for the last units, giving each rate what it has
left outright, survived its mutant: the clamp gives the same figures whenever
the part's tax is all the row has left, so the branch was removed and the
last units take their rates through the clamp. A withdrawn exchange whose
goods have all moved is reached by no route; Y14 is killed on a store written
so.

### 7.8 The third round

A second review of the follow-up closed its first-round findings and found
one Medium and four Low.

**The remedy is no longer prescribed.** On an order of one row shipped free,
the remedy fails: void the credit, document the exchange, and the credit
issued again finds no row with room. The shirts' row is empty, and the
exchange's sale rows take no later act's split. Before this round the
message prescribed it:

```
--- FAIL: TestACreditDocumentedFirstIsNamedByTheExchangesRefusal
    "... gave back on the row first; void them, document the exchange, then issue them again" does not contain "cannot be documented while they stand on it"
```

The credit's split was not widened. Voiding a document to make room is the
operator's call, and known-limits says when it cannot be undone.

**The last part keys on the amount, and stays so.** The rule fires when a
part takes what the row has left in amount, not when the row's units are
used up:
- A row whose units' shares do not add up to its total, 23 089 over two
  units, keeps 1 of total and 1 of tax after two one-unit parts. That is ADR
  0406's own residue.
- Take a row of 2 134 with 355 tax on which an earlier refund gave back one
  unit, 1 067 with 177. An even swap of the other unit gives back the row's
  last 178 and sells the unit again at ⌊355 / 2⌋ = 177, so `tax_payable`
  moves −1.

Keying on units would give the swap 177 and 177, but it would leave the 1
stranded on the row. With the rule as it is, the order's tax ends right: the
one unit held carries 177. So the code stays, and the godoc, known-limits and
the CHANGELOG say both shapes.

The rule's guard, that the row's tax left is not more than the part, keeps a
part off a negative net when an admin route's refund gave back off the row's
ratio. `TestARowWithMoreTaxThanTotalLeftKeepsTheUnitsShare` pins it:
- the row is 3 000 with 1 500 tax over 3 units;
- an earlier refund gave back 2 000 with 400, leaving 1 000 with 1 100;
- one unit back takes 1 000 with 500.

**One grouped read.** The rows' refunds come from one query, `RefundsByRow`,
grouped by row and refund in the order the refunds were written. Before, the
read fetched each live refund's lines on every amendable read.
`TestTheAmendableReadNamesTheRefundsOfEachRow` asserts the answer. It passed
on the loop before the change and on the query after.

| # | Mutant | Lane | Red test |
|---|---|---|---|
| Z01 | the last rule without its tax guard (the review's O2) | unit | `TestARowWithMoreTaxThanTotalLeftKeepsTheUnitsShare` |
| Z02 | the refusal naming no document | unit | `TestACreditDocumentedFirstIsNamedByTheExchangesRefusal` |
| Z03 | the grouped read counting canceled refunds, in the generated query | integration | `TestTheAmendableReadSaysWhatEachRowHasLeft` |
| Z04 | the grouped read counting charges, in the generated query | integration | `TestTheAmendableReadSaysWhatEachRowHasLeft` |
| Z05 | the interop dropping the grouped read | unit | `TestTheAmendableReadNamesTheRefundsOfEachRow` |

Z03 and Z04 were first applied to the `.sql` source and survived, because the
build reads the query sqlc copied into the generated Go. Applied there, both
are red.

Lanes of the third round, on the same base: both vets, `internal/arch`, the
unit tests of the flow, the invoice and order modules and the panel, the
invoice integration package whole, the e2e package whole, and `verify.sh`'s
build, `go test ./...` and lint, all green.
