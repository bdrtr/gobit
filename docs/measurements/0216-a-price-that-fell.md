# A price that fell — measured 2026-09-27

The evidence behind [ADR 0216](../adr/0216-a-wishlist-item-can-ask-for-its-price.md).

## 1. What a price alert could stand on

| Part | State |
|---|---|
| the stock mark | ADR 0215: a proven customer's mark on a wishlist item, with the request's sales channels, answered by the `stock-alert` job every five minutes |
| the customer module | reads no catalog and no price; it holds the mark and pages it through `customer.service` |
| a cart's unit price | the cart workflow's: the region's currency, one bulk `CalculateAmountsJSON` request, and a rule context naming the region, the customer, their company and their head group (ADR 0185) |
| a promotion | the cart's discount, computed over its lines and codes after the price; not part of the unit price |
| a region | deletable by an operator; `RegionCurrency` answers `NotFound` for one that is gone |
| the visibility of a variant | the product module's storefront path, `VariantsInStock` (ADR 0215): absent when unpublished or not visible in the channels |

## 2. The quote

`TestAQuoteIsTheCartsOwnPriceForOne`: the region's currency, one bulk request,
quantity one for every item, the cart's rule context with the head group, and a
variant with no price set absent from the answer.
`TestAVariantWithNoPriceInTheCurrencyIsAbsent`: pricing flags it unpriced and
the others are answered. `TestAQuoteNeedsARegionThatExists`: an empty region is
refused as invalid; a region that is gone is `cart_workflow_quote_region_unknown`,
and a region that could not be read is not.
`TestAVariantBoundToTwoPriceSetsIsAbsent`: a variant the cart would refuse as
ambiguous is not asked, and the others are answered.

## 3. The flow and its surfaces

`TestAPriceThatDropsIsMailedOnce`: the first pass records the price the quote
gives for the mark's customer in the mark's region, a pass at the same price
mails nothing, a lower price mails the customer's own address with both amounts
and the currency and clears the mark, and a pass after that mails nothing.
`TestAPriceThatRisesIsNotADrop` and `TestAPriceInAnotherCurrencyIsNotCompared`
mail nothing. `TestAVariantTheStorefrontDoesNotShowIsNotPriced`: a variant not
visible in the mark's channels records nothing.
`TestAPriceMarkAndAStockMarkOnOneItemAreTwoAnswers`: one pass arms the stock
mark and records the price of the same item.
`TestAMarkInARegionThatIsGoneIsNotAFailure`: the pass prices the other marks and
returns no error. `TestAQuoteThatFailsIsAFailure`: a quote that fails otherwise
reports the pass incomplete. `TestThePriceIsJudgedInThePriceMarksChannels`: an
item marked for both is shown for its price in the price mark's channels.
`TestAMarkIsPricedForItsOwnCustomerRegionAndChannels`: marks are asked together
only when they share the customer, the region and the channels.
`TestAPriceMarkedAgainAfterItsMailIsMailedAgain`: a new mark mails under a new
reference.

`TestAPriceAlertCarriesTheRegionAndTheRequestsChannels`: the storefront route
marks the proven customer's item in the body's region with the channels of the
request's key.

The job's line reads `mailed N wishlist alerts; armed N marks that ran out;
recorded N prices at their mark`.

## 4. On the real schema

`TestAPriceAlertLivesOnTheRealSchema`: a mark names its region and channels and
no price; a price read for another mark is not recorded; the price is recorded
once; a mark set again forgets it and takes a new moment and region; a clear for
the old mark takes nothing and a clear for the new one leaves the stock mark
beside it. `customer_wishlist_item_price_marked`,
`customer_wishlist_item_price_unmarked_empty`,
`customer_wishlist_item_price_baseline_whole` and
`customer_wishlist_item_price_amount_nonneg` refuse what they name.
`TestUnmarkingAPriceLeavesTheStockMark`: the two marks are taken off one at a
time, and unmarking again is not an error.

## 5. On the production wiring

`TestAWishlistPriceDropIsMailedOnce`: a proven shopper marks a variant priced
20,000 in the taxed region through the storefront; the first pass records
20,000 TRY and mails nothing; a raise to 21,000 mails nothing; a price of 17,500
mails the shopper's own address once with `previous_amount` 20000 and `amount`
17500, and the wishlist shows the mark cleared; a price of 15,000 after that
mails nothing. `TestAPriceAlertNeedsARegion`: a mark without a region is refused
422, and another shopper's session 403. The authorization matrix classifies the
new route as naming a person.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | the quote asked for two units | the quote test |
| P2 | the quote without the cart's rule context | the quote test |
| P3 | a gone region not told apart from an unread one | the quote test |
| P4 | an unpriced answer kept | the quote test |
| P5 | a variant bound to two price sets priced | the quote test, once written |
| P6 | price marks not paged | the end-to-end test; the module's integration test once written |
| P7 | a mark set again keeping its price | the module's integration test |
| P8 | a price recorded twice | the module's integration test |
| P9 | a price recorded for any mark | the module's integration test |
| P10 | a clear for any mark | the module's integration test |
| P11 | an unmark taking the stock mark too | the module's integration test |
| P12 | the mark dropping its channels | the module's integration test |
| P13 | the item losing its recorded price | the module's integration test, the end-to-end test |
| P14 | the page losing the region | the module's integration test, the end-to-end tests |
| P15 | the page losing the channels | the module's integration test |
| P16 | a mark without a region | the end-to-end tests |
| P17 | the storefront route dropping the request's channels | the API test, once written |
| P18 | the mark route not mounted | the API's route description tests |
| P19 | the unmark route not mounted | the API's route description tests |
| P20 | the mark not declared as personal data | the declaration gate |
| P21 | the mark not in the person's file | the disclosure test, once it marked a price |
| P22 | half a price allowed | the module's integration test |
| P23 | a negative price allowed | the module's integration test |
| P24 | a price left on an unmarked item allowed | the module's integration test |
| P25 | a mark without its moment or region allowed | the module's integration test |
| P26 | price marks not handled | the flow's tests, the end-to-end test |
| P27 | a price mail not counted | the flow's test, the end-to-end test |
| P28 | a failed quote dropped | the flow's test |
| P29 | a gone region counted a failure | the flow's test |
| P30 | the visibility read and ignored | the flow's test |
| P31 | the visibility asked in no channel | the flow's tests, once written |
| P32 | the visibility asked in the stock mark's channels | the flow's tests, once written |
| P33 | one quote for every customer in a region | the flow's test, once written |
| P34 | one quote for every region of a customer | the flow's test, once written |
| P35 | one visibility read for every channel set of a customer | the flow's test, once written |
| P36 | a rise mailed | the flow's test, the end-to-end test |
| P37 | the same price mailed | the flow's test |
| P38 | a price in another currency compared | the flow's test |
| P39 | the price recorded for no mark | the flow's tests |
| P40 | the price mark not cleared | the flow's test, the end-to-end test |
| P41 | the price mail's reference without the mark's moment | the flow's test, once written |
| P42 | the previous amount written as the new one | the flow's test, the end-to-end test |
| P43 | the price mail sent with the stock template | the end-to-end test |
| P44 | a price mark handled as a stock mark | the flow's tests, once the fake armed only a stock mark |
| P45 | the mail without its currency | the flow's test, the end-to-end test |
| P46 | the prices recorded left out of the job's line | the job's test |
| P47 | the quote asked for no customer | the flow's tests |

Ten survived the first run. P5: no test bound a variant to two price sets;
`TestAVariantBoundToTwoPriceSetsIsAbsent` was written. P6 died only end to end:
every price mark in the module's test sat on an item with a stock mark; the test
now pages a price mark alone. P17: the end-to-end variant is shown in every
channel, and `TestAPriceAlertCarriesTheRegionAndTheRequestsChannels` reads what
the route hands the service. P21: the disclosure test marked no price. P31 to
P35: every flow test had one mark or one group, so
`TestThePriceIsJudgedInThePriceMarksChannels` and
`TestAMarkIsPricedForItsOwnCustomerRegionAndChannels` were written. P41: no test
marked a price again after its mail; `TestAPriceMarkedAgainAfterItsMailIsMailedAgain`
was written. P44: the fake armed a mark with no stock half, which the query does
not; the fake follows the query now, and the price test asserts that a price
mark asks the catalog once, in its own channels. P46's first form did not
compile and was rewritten.

P16 and P14 also failed the stock scenario end to end: a price mark with an empty
region, left by another scenario, made the shared pass fail.
