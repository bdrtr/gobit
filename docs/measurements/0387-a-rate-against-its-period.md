# A rate against its period — measured 2026-10-05

The evidence behind
[ADR 0387](../adr/0387-a-tax-rate-can-be-tried-on-past-orders.md) and gap
D237. Read on the tree at a5750ac5, before the change, unless a section says
otherwise.

## A tax rate has no draft

No tax migration carries a status column: `git grep -il status -- internal/modules/tax/migrations`
finds nothing. A value, a rule or a correction (ADR 0378) is read by the next
cart's calculation. The one rate that waits is a ruled rate with no rule: the
local provider selects a ruled rate only when one of its rules matches the
line, and `DeleteRateRule`'s godoc says a rate left without rules matches no
line item and does not become the default. A new default rate, and a rate
standing on another, apply the moment they are written.

## What the cart sends the tax module

Read in `internal/workflows/cart/tax.go` and `totals.go`:

| Field | What the cart sends | Where it comes from |
|---|---|---|
| country | the one country the cart's region is bound to | the region record, through the Query layer |
| province | always empty | the cart holds no address on this surface |
| amount | the line's subtotal less its discount | a cart line's subtotal is unit price times quantity |
| product | the variant's product | one catalog read per round |
| type | the product's type | the product facts the round already read |
| gift card | the line is not sent | today's product flag (`giftCardLines`) |
| shipping | `taxable: false` | never taxed |

A region bound to no country or to several sends nothing: the cart is taxed at
the region's flat rate (`TaxSourceRegion`).

## What an order keeps of its sale

The order line entity offers `unit_price`, `quantity`, `discount_total`,
`tax_total`, `tax_rate_bps` and `is_giftcard`, the last as the product was
flagged at the moment of sale (ADR 0211); `git grep -l '"is_giftcard"' -- internal/modules/order/service`
lists `interop.go` and `line_item_provider.go`. It does not offer the product id or the
per-rate components. The order entity does not offer `prices_include_tax`:
`git grep -l prices_include_tax -- internal/modules/order/service` lists the
interop surface and a test, not the provider. The order line's subtotal is
net of tax where prices include it (ADR 0246), so the amount the cart sent is
rebuilt from unit price and quantity, as the promotion trial already does.

So the trial takes from the sale which lines were sent and at what amount, and
from today how they are taxed: country, classes, every other rate and rule,
inclusion.

## The arithmetic the trial must not fork

`newRateTable`, `applyTo` and `stackFrom` in `service/local.go` are the whole
local calculation. `applyTo` does not cap the tax at the line; `validateLine`
in `service/calculate.go` refuses a tax above the amount as a provider fault
(500). The provider is the chain's most specific non-empty `provider_id`
(`providerFor`), and an external provider's table is not this module's to
amend. The trial builds both tables with the provider's own read, extracted as
`loadRateRows`, and holds both answers to `validateLine`;
`TestABaselineIsWhatTheCartIsCharged` draws inclusion, a stack, compound or
not, up to three ruled rates with up to two rules each and tax classes, and
holds the baseline to `CalculateTax` line by line.

## Gap D237: a value write did not check its stack

`git grep -l "assertStackWithinBase(" -- internal/modules/tax/service`
lists `stack.go` and a property test; the one call in `stack.go` is inside
`assertStackable`, which only `CreateTaxRate` reaches. `UpdateTaxRate` validated a new `rate_bps` against
zero to ten thousand and wrote it; `ReviseTaxRate` the same. ADR 0095 says the
stack's cap is refused at write time. A 6000 bps default with a 3000 bps rate
on it accepted `PUT {"rate_bps": 6000}` on the top rate, and a cart line the
stack then taxed failed `validateLine` with 500 wherever its floored components
summed past its amount: 6000 + 6000 takes 2 + 2 of a 4 and passes, and 3 + 3
of a 5 and fails.

Both writes now read the rate, take the region's row `FOR UPDATE`, read the
region's rates and check the stack the rate stands in with its new value. The
lock is exclusive because the cap is a sum: two writers raising two members
under the creation's shared lock each check against the other's old value.
`TestTwoRaisesOfOneStackAreCheckedOneAfterTheOther` holds both rates' rows in
an open transaction. Under the write lock the first writer takes the region and
waits at its row, and the second waits at the region before its check; under a
shared lock both would check and then wait at their rows. The test releases the
rows and asserts that one raise is written and the other refused.

## Mutants

Each was applied alone, its test run with `-count=1` after a green base run,
and reverted.

| # | Mutant | Killed by |
|---|---|---|
| D1 | update: stack check removed | `TestAStackMemberIsNotRaisedPastItsLine` |
| D2 | revise: stack check removed | `TestAStackMemberIsNotRaisedPastItsLine` |
| D3 | stack checked at the stored value, not the new one | `TestAStackMemberIsNotRaisedPastItsLine` |
| D4 | stack walked only upward from the rate | `TestAStackMemberIsNotRaisedPastItsLine` |
| D5 | shared region lock instead of the write lock | `TestAStackMemberIsNotRaisedPastItsLine` |
| D6 | a two-member stack not checked | `TestAStackMemberIsNotRaisedPastItsLine` |
| T1 | trial value not applied | `TestARateIsComparedAtItsTrialValue` |
| T2 | baseline built from the trial table | `TestARateIsComparedAtItsTrialValue` |
| T3 | reached always false | `TestARateIsComparedAtItsTrialValue` |
| T4 | tax classes not attached | `TestARuleIsComparedAsIfWritten` |
| T5 | added/dropped rules leak into the baseline | `TestARuleIsComparedAsIfWritten` |
| T6 | placeholder rule on the wrong rate | `TestARuleIsComparedAsIfWritten` |
| T7 | drop ignored | `TestADroppedRuleIsComparedAsIfDeleted` |
| T8 | inclusion not passed | `TestAnInclusiveMarketTakesTheTrialOutOfTheSamePrice` |
| T9 | reached reads only the line rate | `TestAStackedRateIsComparedThroughItsStack` |
| T10 | stack member not adjusted in the trial table | `TestAStackedRateIsComparedThroughItsStack` |
| T11 | trial stack check removed | `TestAStackTakingMoreThanItsLineIsRefused` |
| T12 | other countries priced | `TestAnOrderOutsideTheRatesCountryIsNotPriced` |
| T13 | order bound removed | `TestAComparisonBoundsItsSize` |
| T14 | line bound removed | `TestAComparisonBoundsItsSize` |
| T15 | lines answered out of order | `TestACompareAnswersInTheRequestsOrder` |
| T16 | baseline ignores the chain's inclusion (drift from CalculateTax) | `TestABaselineIsWhatTheCartIsCharged` |
| T17 | baseline table with no chain (drift from CalculateTax) | `TestABaselineIsWhatTheCartIsCharged` |
| R1 | empty change accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R2 | rule bound removed | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R3 | rate range unchecked | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R4 | unknown reference accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R5 | shipping rule accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R6 | empty reference id accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R7 | duplicate add accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R8 | duplicate drop accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R9 | malformed drop id checked only after the reads | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R10 | drop of a rule the rate does not carry accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R11 | add of a carried rule accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R12 | drop-and-add-back refused as 'already carries' | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R13 | rule on a stacked rate accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R14 | rule on the default accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R15 | province rate accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R16 | external provider accepted | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R17 | TryRate does not check the change | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R18 | CompareRate does not check the change | `TestATrialRefusesAChangeTheWritePathWouldRefuse` |
| R19 | future period accepted | `TestATrialAsksTheFlowOnlyWithAPastPeriodAndABoundFlow` |
| R20 | unbound flow not refused | `TestATrialAsksTheFlowOnlyWithAPastPeriodAndABoundFlow` |
| I1 | unknown fields accepted | `TestCompareRateJSONRefusesUnknownFields` |
| I2 | second document accepted | `TestCompareRateJSONRefusesUnknownFields` |
| A1 | order:read not required | `TestARateTrialNeedsOrderRead` |
| A2 | report decoded loosely | `TestARateTrialAnswersOnlyWhatItCanRead` |
| A3 | rule split at the last colon | `TestARateTrialAnswersWhatTheFlowReports` |
| A4 | rate given twice accepted | `TestARateTrialNeedsAChange` |
| A5 | drop_rule not forwarded | `TestARateTrialAnswersWhatTheFlowReports` |
| A6 | period end not forwarded | `TestARateTrialAnswersWhatTheFlowReports` |
| C1 | the order line's subtotal sent instead of unit x quantity | `TestATaxTrialSendsWhatTheCartSent` |
| C2 | discount not subtracted | `TestATaxTrialSendsWhatTheCartSent` |
| C3 | gift card left out on today's product flag | `TestATaxTrialSendsWhatTheCartSent` |
| C4 | gift card line sent | `TestATaxTrialSendsWhatTheCartSent` |
| C5 | wrong country sent | `TestATaxTrialLeavesOutWhatTheCartDidNotTax` |
| C6 | charged not the sold tax | `TestATaxTrialSumsChargedBaselineAndTrial` |
| C7 | canceled order taxed | `TestATaxTrialLeavesOutWhatTheCartDidNotTax` |
| C8 | region-rate order taxed | `TestATaxTrialLeavesOutWhatTheCartDidNotTax` |
| C9 | outside order summed | `TestATaxTrialLeavesOutWhatTheCartDidNotTax` |
| C10 | region read per order | `TestATaxTrialReadsEachRegionOnce` |
| C11 | smallest change first | `TestATaxTrialNamesTheLargestChangesFirst` |
| C12 | older first on a tie | `TestATaxTrialNamesTheLargestChangesFirst` |
| C13 | no cap on the named orders | `TestATaxTrialNamesTheLargestChangesFirst` |
| C14 | answer about another order accepted | `TestATaxTrialReadsOnlyAnAnswerAboutItsOrders` |
| C15 | answer about another line accepted | `TestATaxTrialReadsOnlyAnAnswerAboutItsOrders` |
| C16 | outside entry with lines accepted | `TestATaxTrialReadsOnlyAnAnswerAboutItsOrders` |
| C17 | entry count unchecked | `TestATaxTrialReadsOnlyAnAnswerAboutItsOrders` |
| C18 | line count unchecked | `TestATaxTrialReadsOnlyAnAnswerAboutItsOrders` |
| C19 | nil tax surface checked after the walk | `TestATaxTrialRefusesWhatItCannotAnswer` |
| C20 | types-unread assumption dropped | `TestATaxTrialSaysWhenTypesWereNotRead` |
| C21 | a type read failure is fatal | `TestATaxTrialSaysWhenTypesWereNotRead` |
| C22 | every line counted reached | `TestATaxTrialSumsChargedBaselineAndTrial` |
| C23 | type not sent | `TestATaxTrialSendsWhatTheCartSent` |
| P1 | the rate trial flow's interop pin removed | `TestEveryInteropConsumerIsPinned` |
| D5r | the repository's write lock reading `FOR SHARE` | `TestTwoRaisesOfOneStackAreCheckedOneAfterTheOther` |
| T18 | a line's type not carried across the JSON surface | `TestCompareRateJSONCarriesEachLinesType` |
| T19 | an order with no reference answered | `TestAComparisonRefusesAMalformedRequest` |
| T20 | a line with no id, or a repeated one, answered (three mutants) | `TestAComparisonRefusesAMalformedRequest` |
| T21 | a negative line amount not checked | `TestAComparisonRefusesAMalformedRequest` |
| T22 | the order's country not normalized | `TestAComparisonRefusesAMalformedRequest` |
| T23 | a line's figure not held to a cart's contract | `TestAComparisonRefusesAFigureACartWouldRefuse` |
| C24 | lines changed counted from reached | `TestATaxTrialSumsChargedBaselineAndTrial` |
| C25 | lines reached counted from the amounts | `TestATaxTrialSumsChargedBaselineAndTrial` |
| C26 | an order with no taxable line sent and priced | `TestATaxTrialSendsNoOrderWithoutATaxableLine` |
| C27 | a line discounted past its amount sent | `TestATaxTrialSendsNoOrderWithoutATaxableLine` |
| C28 | a line left out only when today's product is a gift card too | `TestATaxTrialSendsWhatTheCartSent` |

The first 75 mutants, all killed. Four first failed to compile (an unused variable or import left behind) and one, `charged` taken as a fifth of the line, survived a fixture whose sold tax was exactly 20%; the four were rewritten to compile and the fixture now sells at 18%, after which all five were killed. The lock mutant D5 is killed by the in-memory call count.

A review then found mutants that survived every lane. T18 to T23 and C24 to C27 above are those and their neighbours, twelve mutants; C28 was killed only by a test that scripts no product facts, and now is by the test that claims the case. All thirteen were applied with a `go -overlay`, so the tree was never edited. The survivors lived because no test sent their input: no comparison reached the JSON surface with a type, the cart fake marked a line reached exactly when its tax moved, and no request carried an empty reference, a repeated line, a negative amount, a lower-case code in the rate's country, a stack written past its line, an order of gift cards only or a line discounted past its amount. A test for each was added, and each mutant was then killed with `-count=1`. T18 is killed through the one mapping both JSON methods now share; before, `CalculateTaxJSON`'s copy dropped the type unseen too. The e2e asks a second trial at the default's own value, where both lines are reached and none changes. D5r ran the tax integration package once against the repository-level mutant, and only the lock test failed.
