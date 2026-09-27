# A group that follows its rule — measured 2026-09-27

The evidence behind [ADR 0217](../adr/0217-a-customer-group-can-be-a-segment.md).

## 1. What a segment could stand on

| Part | State |
|---|---|
| a group | `customer_group(id, name, metadata, rank, …)`; membership `customer_group_customer(customer_id, customer_group_id, created_at)`, a plain set with no source |
| who reads membership | the cart's rule context alone: the head group (ADR 0049) for pricing and promotions, every group for promotions' `any_in` (ADR 0144) |
| a customer's record | `has_account`, `created_at`, `metadata`; addresses with `country_code` and at most one live default shipping address |
| a customer's orders | no per-customer aggregate; `SumCustomerSpend` sums one customer in one currency for the spending limit |
| rule evaluators | promotion's and pricing's, both private to their modules and over a flat string map |
| scheduled work | `internal/core/job`; ADR 0017 permits a job that writes what was already promised |
| advisory lock classes | 1 spending, 2 jobs, 3 category reparent, 4 store credit, 5 loyalty |

## 2. The order totals

`TestCustomerOrderTotalsFollowTheSpendRules` on a real PostgreSQL: per customer
and currency, the canceled order and the order before the window left out, a
refund of 1,500 on 5,000 deducted through the summary's LEFT JOIN, a customer
with no order absent and a customer outside the page not read; with no window
the order before it counts too. `TestCustomerOrderTotalsAreReadForABoundedPage`:
1 to 500 customers, none blank. `TestTheInteropCarriesTheTotalsPerCurrency`: the
JSON names each row's currency, count and net spend.

## 3. The rule and the module

`TestARuleIsNormalizedAsItIsEvaluated`: codes folded to upper case, a country
listed once, every value in one JSON form. `TestARuleOutsideTheVocabularyIsRefused`:
twenty-three rules refused as invalid with `customer_segment_invalid`, among
them a quoted number, a fraction, a negative, a number over 10^15, a currency
without `net_spend`, a window without an order attribute and an operator the
attribute does not take. `TestASegmentTakesNoHandEdit`, `TestTheSegmentsAreBounded`
(the fifty-first segment refused, a new rule for a segment not counted twice) and
`TestTheFlowReadsAndWritesSegmentsThroughTheInterop` on the module's fakes.

`TestASegmentLivesOnTheRealSchema`: the rule stored as normalized; hand edits
refused; a page writing members only in its id range, the last page reaching the
end of the ids; a deleted customer never put in; a page and a finish for another
rule writing nothing; a new rule forgetting its evaluation; a cleared rule
keeping its members and taking hand edits again; the three constraints refusing
what they name. `TestTheSegmentFactsAreTheCustomersRecords`: the default shipping
address's country and not another's, a guest with none, a deleted customer not
read. `TestTheSegmentCountWaitsForAWriterThatHasNotCommitted`: a second
transaction takes the count's lock as the repository does, makes the fiftieth
segment and holds its commit; a writer asking for one more is refused after it,
and the segments stay fifty.

## 4. The flow and the job

`TestAPassWritesEachSegmentsMembers`, `TestAPassPagesEveryCustomer` (1,001
customers in three pages, the last reaching the end, no order read for a rule
that reads none), `TestAMemberWhoNoLongerMatchesLeaves`,
`TestARuleReplacedDuringAPassStopsItsWrites`, `TestAnUnreadableRuleDoesNotStopTheOthers`,
`TestAFailedWriteStopsOnlyItsSegment`, `TestAFailedOrderReadStopsThePass`,
`TestEachWindowIsReadOncePerPage`, `TestNetSpendIsInTheRulesCurrency`,
`TestTheRecordConditions` (every numeric operator on whole days of age, and a
customer without a country matching no condition, `ne` and `nin` included),
`TestAPreviewCountsAndWritesNothing` and `TestNoSegmentReadsNoCustomer`. The
job's line is the flow's report, written for a failed pass too.

## 5. On the production wiring

`TestACustomerSegmentFollowsItsRule`: two customers ship to Iceland and one has
ordered; a group is priced 15,000 by an override list, against a base of 20,000.
The preview over the admin surface counts one member, the rule is set, and a hand
add is refused 409. A pass of the flow puts in the buyer and leaves out the
browser, the buyer's cart is priced 15,000 and the browser's 20,000, and the
group shows when the pass finished. `TestASegmentRuleOutsideTheVocabularyIsRefused`
answers 422 and stores nothing. `TestEveryJobTheRootDeclaresCanBeBuiltAgainstARealInstallation`
names the `customer-segments` job, and the authorization matrix classifies the
three new admin routes. `TestTheSegmentPreviewReachesTheFlowTheRootWires`: the
installation's container answers the preview's name with the flow, which counts
the installation's one account.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| G1 | a canceled order totaled | the order module's integration test |
| G2 | the refund not deducted | the order module's integration test |
| G3 | the window ignored | the order module's integration test |
| G4 | a page over the cap read | the order service test |
| G5 | a blank customer read | the order service test |
| G6 | the interop dropping the spend | the order interop test |
| S1 | a hand add to a segment | the module's integration test, the end-to-end test |
| S2 | a hand removal from a segment | the module's integration test |
| S3 | the cap one over | the held-transaction test |
| S4 | the count not serialized | the held-transaction test, once written |
| S5 | a page for a replaced rule written | the module's integration test |
| S6 | strays removed below the page | the module's integration test |
| S7 | strays removed past the page | the module's integration test |
| S8 | a deleted customer put in | the module's integration test |
| S9 | a replaced rule finished | the module's integration test |
| S10 | a deleted customer read | the facts test |
| S11 | any address a country | the facts test |
| S12 | a new rule keeping its evaluation | the module's integration test |
| S13 | a country not folded | the normalization test, the preview API test |
| S14 | a country listed twice | the normalization test, the module's integration test |
| S15 | a currency with no spend | the refusal test |
| S16 | a window with no orders | the refusal test |
| S17 | a number over the bound | the refusal test |
| S18 | a negative number | the refusal test |
| S19 | any operator for any attribute | the refusal test |
| S20 | a member outside the page | the interop test |
| S21 | a facts page over the cap | the interop test |
| S22 | a number written as text | the refusal test |
| S23 | a rule with no moment allowed | the module's integration test |
| S24 | a rule that is no object allowed | the module's integration test |
| S25 | an evaluation with no rule allowed | the module's integration test |
| S26 | the preview not normalizing | the preview API tests |
| S27 | the preview route not mounted | the API tests, the end-to-end test |
| S28 | the segment route not mounted | the API tests, the description tests, the end-to-end test |
| S29 | the clear route not mounted | the API test, the description tests |
| S30 | the group without its rule | the API test, the end-to-end test |
| S31 | the group without its evaluation | the end-to-end test |
| W1 | gte read as gt | the flow's tests, the end-to-end test |
| W2 | lte read as lt | the record conditions test |
| W3 | eq read as gte | the record conditions test |
| W4 | ne read as gt | the record conditions test |
| W5 | lt read as lte | the record conditions test |
| W6 | a customer with no country matching | the record conditions test |
| W7 | nin read as in | the record conditions test |
| W8 | age rounded up | the record conditions test |
| W9 | spend summed across currencies | the currency test |
| W10 | the window in hours | the flow's tests |
| W11 | one window read for every rule | the window test |
| W12 | a replaced rule written on | the replaced rule test |
| W13 | a failed write stopping the pass | the failed write test |
| W14 | no segment finished | the flow's tests |
| W15 | a segment finished at another moment | the flow's tests |
| W16 | the last page not reaching the end | the paging test |
| W17 | an unreadable rule not reported | the unreadable rule test |
| W18 | any word evaluated | the unreadable rule test |
| W19 | the preview counting everyone | the preview test, the end-to-end test |
| J1 | the job's line not written on failure | the job's test |
| A1 | the flow built and not provided | the application test, once written |
| A2 | the job built and not registered | the job registration gate, the application test |

Two survived the first run. S4: eight writers started together at one below the
cap never overlapped in the window between the count and the commit, so the test
passed without the lock. It was replaced by one that holds a second transaction
at that point, which S3 then had to die against as well. A1: the end-to-end
ground provides the flow itself, so nothing on the production wiring resolved
the preview's name; `TestTheSegmentPreviewReachesTheFlowTheRootWires` was
written. S26, W14, W19 and J1 did not compile in their first form and were
rewritten.
