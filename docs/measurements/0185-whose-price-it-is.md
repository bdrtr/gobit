# Whose price it is — measured 2026-09-25

The evidence behind [ADR 0185](../adr/0185-a-contract-price-names-its-buyer.md).

## 1. What the rule context carried

`ruleContext` in `internal/workflows/cart/catalog.go` built the context the
line price, the totals round, the discount request and the promotion trial all
use. It wrote `region_id`, the cart's metadata under the `cart.` prefix, and,
for a customer in a group, `customer_group_id` with the full set beside it
(ADR 0144). Nothing named the customer or a company, so a price rule could not
either.

The b2b module already resolved a customer to a company for the spending limit
(`MembershipOfCustomer`, behind `SpendingLimitJSON`), through the customer's
link to an employee record. An employee record belongs to one company.

## 2. Where a rule-bound price can surface

| Reader | Rule-bound prices |
|---|---|
| the cart's line price, totals and discount request | evaluated against the rule context |
| the admin `GET /admin/v1/price-sets/{id}/calculate` | evaluated against `attr_*` query parameters |
| the storefront `GET /store/v1/price-sets/{id}` | left out (`ListStorePrices`) |
| the catalog's price set through the Query layer | left out (`QueryProvider`) |
| the price history timeline | evaluated with no context, so never matched |

A contract price therefore reaches only the cart it names and the operator's
calculator.

## 3. The ladder, before and after

Before, a customer contract and a segment price, both on override lists with
one rule, tied on list priority, rule count and quantity span, and the amount
decided. The buyer rung decides now:

| Candidates (override unless stated, one buyer) | Winner before | Winner after |
|---|---|---|
| segment 800, company 900, customer 1,000 | segment | customer |
| segment 800, company 900 | segment | company |
| segment and region 800 (two rules), company 900 | segment | company |
| segment 1,000 (override), customer 800 on a sale list | segment | segment |
| segment and region 1,000, `customer_id ne cus_2` 800 | segment | segment |
| segment 1,000, another customer's contract 500 | segment | segment |

The "after" column is the tests in `pricing/service/buyer_test.go`. The
"before" column is read off the ladder's other rungs; removing the buyer rung
turned the tests of the first three rows red, which is the same statement
measured. None of the first three rows could occur before, because no customer
or company attribute was ever in a cart's context.

## 4. The production wiring

`TestAContractPriceNamesItsBuyer` in `internal/e2e` builds the cart workflows
the way the application does, from the container. One variant, a base price of
20,000, a company contract of 18,000 and one customer's own contract of 17,500:

| Buyer | Unit price |
|---|---:|
| the customer with their own contract | 17,500 |
| a colleague at the same company | 18,000 |
| a customer of no company | 20,000 |

The first customer's cart was then completed, and the order line's price
origin names the customer's contract list, type `override`.

## 5. Mutations

| Mutation | Result |
|---|---|
| the buyer rung removed | red: two pricing tests |
| the rung moved below the rule count | red: `TestTheBuyerOutranksTheRuleCount` |
| the rung moved above list priority | red: `TestListPriorityStillComesFirst` |
| `ne` and `nin` given a rank | red: `TestAnExclusionNamesNobody` |
| the company ranked as the customer | red: `TestTheBuyersOwnContractOutranksTheirCompanyAndTheirSegment` |
| the cart writes no `customer_id` | red: four cart tests, and the e2e test |
| the cart writes an empty company | red: `TestOnlyAnEmployeeCarriesACompany` |
| an unreadable company swallowed | red: `TestAnUnreadableCompanyStillPricesTheCart` |
| a guest's company looked up | red: five cart tests |
| the container wiring drops the b2b surface | red: the e2e test |
| one side's spelling of `company_id` changed | red: `TestTheBuyerAttributeNamesAgree` |
