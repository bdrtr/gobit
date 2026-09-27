# A variant that came back — measured 2026-09-27

The evidence behind [ADR 0215](../adr/0215-a-wishlist-item-can-ask-for-its-stock.md).

## 1. What an alert could stand on

| Part | State |
|---|---|
| the waitlist as specified | refused by ADR 0051: a row an unidentified party writes, acted on later toward an unverified address |
| the wishlist | ADR 0190: reached by the proven customer alone through `ProvenCustomer`, declared personal data, deleted by an erasure |
| an event for stock | none: the inventory module publishes nothing (ADR 0063) |
| "in stock" | ADR 0040's badge, computed by the product module's storefront path over the warehouses the request's channels serve, for a published product visible in those channels |
| a request's channels | `corehttp.SalesChannelIDs`: nil with no principal, the key's channels otherwise |
| the notification module | claims a (template, reference) pair before sending; a repeat returns nil without sending, a failure leaves the claim |

## 2. The flow and its surfaces

`TestAVariantThatComesBackIsMailedOnce`: out of stock arms the mark, back in
stock mails the customer's own address with the variant and product names and
clears the mark, and a third pass mails nothing. `TestAMarkOnAVariantInStockWaitsForItToRunOut`:
a mark on a variant in stock is not armed and mails nothing.
`TestTheStockIsJudgedInTheMarksChannels`: stock visible only to another channel
does not mail. `TestAPassThatStopsAfterTheMailSendsNothingTwice`: a mail already
sent under the mark's reference is not sent again, and the mark is cleared.
`TestAFailedMailKeepsTheMark`: a failed mail keeps its mark and does not stop
the other marks. `TestAPassReadsEveryPage`: the hundred-and-first mark is read.
`TestAMarkWithNoChannelIsAskedWithNone`: no channel and the empty set are asked
as two different questions.

`TestVariantsInStockIsTheStorefrontsBadge`: `product.interop` answers each
variant with the storefront's badge, and an unknown variant is absent.
`TestAVariantInvisibleInTheChannelsIsAbsent`: a product assigned to another
channel is absent for this one and present for its own. A product with no
channel assignment is visible in every channel, as the storefront's rule says.

`TestAStockAlertCarriesTheRequestsChannels`: the storefront route marks the
proven customer's item with the channels of the request's key. The identity
walk over every storefront route naming a customer now includes the two new
routes, which refuse with nothing bound.

## 3. On the real schema

`TestAStockAlertLivesOnTheRealSchema`: a mark saves the variant, the page names
it with its channels and a mark with no channel with `null`, arming happens
once, a clear for another arming takes nothing, a clear leaves the items, and an
arming without a mark is refused by `customer_wishlist_item_alert_marked`.
`TestAMarkSetAgainWaitsAgain`: marking an armed item forgets the arming and takes
the new channels. `TestADeletedCustomersMarksAreNotRead` and
`TestAFullWishlistTakesNoNewMark` hold the deleted customer and the cap.
`TestADisclosureShowsTheWishlistInTheDatabase`: the person's file shows each
item with its mark.

## 4. On the production wiring

`TestAWishlistVariantBackInStockIsMailedOnce`: a proven shopper marks a variant
at zero stock through the storefront; a pass mails nothing; the variant is
stocked at the channel's warehouse; the next pass mails the shopper's own
address once with the product's title, the wishlist shows the mark cleared, and
a further pass mails nothing. `TestAStockAlertNeedsTheProvenShopper`: another
shopper's session is refused 403. The authorization matrix classifies the new
route as naming a person. `TestEveryJobTheRootDeclaresCanBeBuiltAgainstARealInstallation`
names the `stock-alert` job.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| Z1 | the badge read with no channel | the product test |
| Z2 | every shown variant answered in stock | the product test |
| Z3 | the mark dropping its channels | the module's integration test |
| Z4 | a deleted customer's marks read | the module's integration test |
| Z5 | a mark armed again | the module's integration test |
| Z6 | a clear for any arming | the module's integration test |
| Z7 | a mark set again keeping its arming | the module's integration test, once written |
| Z8 | a mark in stock armed | the flow's tests, the end-to-end test |
| Z9 | an unarmed mark mailed | the flow's test |
| Z10 | the mark not cleared | the flow's tests, the end-to-end test |
| Z11 | the reference without its arming | the flow's test |
| Z12 | the stock asked with no channel | the flow's tests |
| Z13 | the first page only | the flow's test |
| Z14 | a failure stopping the pass | the flow's test |
| Z15 | the storefront route dropping the request's channels | the API test, once written |
| Z16 | the job built and not registered | the job registration gate, the production test |
| Z17 | the mark not declared as personal data | the declaration gate |
| Z18 | the mark not in the person's file | the disclosure test |
| Z19 | an arming without a mark allowed | the module's integration test |
| Z20 | the route not mounted | the API's tests, the end-to-end test |

Z7 survived the first run: nothing marked an armed item twice.
`TestAMarkSetAgainWaitsAgain` was written. Z15 survived too: the end-to-end
variant is visible and stocked in every channel, so a mark with no channel gives
the same answer, and the API test that reads the channels handed to the service
was written.
