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
