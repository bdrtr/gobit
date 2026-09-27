# A list against its period — measured 2026-09-28

The evidence behind [ADR 0220](../adr/0220-a-price-list-can-be-tried-on-past-orders.md).

## 1. What already existed

| Need | Where it already was |
|---|---|
| The orders of a period, their lines, quantities and unit prices | the promotion trial's `trialOrders` in the cart flows (ADR 0176), 93 days and 5,000 orders at most |
| A variant's price set | the `product_variant_price_set` link, read for many variants in one call |
| The rule context a cart carries | the cart flows' `ruleContext`: region, customer, company, head group and every group |
| Which price wins | pricing's `selectPrice` over a set's candidates, each with its list's type, status and window |
| A list's status and window in the race | `listAvailable`, the filter the storefront's query provider shares |
| A price of a list | `price.price_list_id`; a list's prices live in the sets they price |

Nothing new is stored. The trial reuses the promotion trial's reading of the
orders and the cart's own context, and asks pricing through the surface the
cart flows already held as `Prices`, which gains one method.

## 2. The comparison in pricing

`TestAListIsComparedAsIfActiveAndAsIfAbsent`: a draft sale list whose window
closed two days ago gives 700 against a base of 1,000, and 600 from three
against the base's 950 from three; the vip override of another list at 900 outranks the sale both
ways for a vip context; an expired sale of a third list at 500 counts neither
way; EUR, which the set does not price, is unpriced both ways; a currency given
in lower case is read. `TestAListIsComparedAsTheTypeItIs`: a draft override at
1,100 takes the place of an active sale at 950. `TestAComparisonNeedsAListThatExistsAndBoundsItsSize`:
an unknown list, a list id of another kind, 5,001 purchases, 100,001 lines and
a set id of another kind are refused, and 5,000 purchases and 100,000 lines are
answered.

## 3. The flow

`TestAListTrialComparesTodaysPriceWithAndWithoutTheList`: three lines of two
orders, the list lowering one set from 1,000 to 800; the sums are 1,650
charged, 1,750 baseline and 1,550 trial, one line changed, the unchanged order
not named, and each line asked at its own quantity with the customer's context.
`TestAListTrialLeavesOutWhatItCannotPrice`: a canceled order counted apart; a
variant with no set, one with two sets and a set with no price counted unpriced;
an order none of whose lines was priced not counted as priced.
`TestAListTrialReadsEachCustomersContextOnce`: three orders of one customer, one
read of the groups. `TestAListTrialNamesTheLargestChangesFirst`: of 101 changed
orders the hundred largest are named, the largest first, and of two equal
changes the later order is named and the earlier dropped.
`TestAListTrialReadsOnlyAnAnswerAboutItsOrders`: an answer with no entry, about
another order, with no line or not an object is refused; a line the list prices
and the base does not is unpriced. `TestAListTrialRefusesWhatItCannotAnswer`: a
blank list, an empty and a backwards period, a period over 93 days, a line of a
quantity no cart holds, and pricing unavailable.

## 4. The endpoint

`TestAListTrialAnswersWhatTheFlowReports`: the list and the period reach the
flow, a `to` given at +03:00 read as the same instant in UTC.
`TestAListTrialNeedsOrderRead`: `pricing:read` alone and `order:read` alone are
refused 403 and the flow is not asked. `TestAListTrialNeedsAPastPeriod`: no
`from`, no `to`, a date, a moment with no zone and an end an hour ahead are
refused 422. `TestAListTrialAnswersOnlyWhatItCanRead`: an unbound flow, a flow
failure passed through as it was, and a report with a field the endpoint does
not publish, which is refused rather than passed on.

The query parameter audit refused the first draft: the handler read `from` and
`to` as literals, and the audit matches a read to its description by the
constant's name.

## 5. On the production wiring

`TestAPriceListCanBeTriedOnPastOrders`: a variant priced at 10,000, two orders
of two and of one placed and a third declined, then a draft sale list written at
8,000 and at 7,000 from two. The trial names the two orders with 20,000 → 14,000
and 10,000 → 8,000, their currency's trial minus baseline is −8,000 with two
lines changed while other scenarios' orders share the window, the list stays a
draft, `pricing:read` alone is refused 403, an end an hour ahead 422, and an
unknown list 404.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| S1 | the list kept in the baseline | the comparison and end-to-end tests |
| S2 | the list offered at its own status | the comparison and end-to-end tests |
| S3 | the list offered with its window | the comparison test |
| S4 | the list offered as a sale | the type test |
| S5 | the baseline's quantity ignored | the comparison test, once written |
| S6 | the trial's quantity ignored | the comparison and end-to-end tests |
| S7 | the rule context ignored | the comparison test |
| S8 | the currency not normalized | the comparison test |
| S9 | one purchase over the bound | the bound test, once written |
| S10 | one line over the bound | the bound test |
| S11 | a set id unchecked | the bound test |
| S12 | a list id unchecked | the bound test |
| S13 | the quantity not normalized | the comparison and type tests |
| S14 | the baseline read from the trial's candidates | the comparison, type and end-to-end tests |
| C1 | canceled orders priced | the unpriced test, the end-to-end test |
| C2 | the baseline summed per unit | the comparison flow test |
| C3 | the charged sum taken from today | the comparison flow test |
| C4 | unchanged orders named | the comparison flow test |
| C5 | the smallest change first | the ordering test |
| C6 | the earlier order first on a tie | the ordering test |
| C7 | one order over the bound named | the ordering test |
| C8 | a variant with two sets priced | the unpriced test, once written |
| C9 | the context read per order | the context test |
| C10 | the context not sent | the comparison flow and context tests |
| C11 | an answer of another length taken | the answer test |
| C12 | an answer about another order taken | the answer test |
| C13 | a line with no baseline priced | the answer test |
| C14 | every priced line counted as changed | the comparison flow test |
| C15 | an order with no priced line counted | the unpriced test |
| C16 | an empty period taken | the refusal test, once written |
| C17 | a period over the bound taken | the refusal test |
| C18 | an order's baseline summed per unit | the comparison flow and end-to-end tests |
| Q1 | a quantity cut to fit | the refusal test |
| A1 | `order:read` not asked | the scope and end-to-end tests |
| A2 | an end in the future taken | the period and end-to-end tests |
| A3 | a field the report does not publish passed | the answer test |
| A4 | the route not mounted | the endpoint tests, the end-to-end test |
| A5 | the module binding no trial | the end-to-end test |
| A6 | the flow resolved by another name | the end-to-end test |
| A7 | another list's id passed | the end-to-end test |
| A8 | the zone dropped | the endpoint and end-to-end tests |

Four survived the first run, each for a fixture that could not tell two rules
apart. The base had no quantity tier, so pricing the baseline at one priced it
the same (S5). The 5,001 purchases carried no currency and were refused for it
before their number was read (S9). The variant with two sets listed the unpriced
set first, so taking the first priced nothing (C8). Only a backwards period was
refused, never an empty one (C16). C15 did not compile in its first form and
was rewritten; Q1 was added with the bound it proves, since the first draft cut
a stored quantity to 32 bits unchecked.
