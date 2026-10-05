# What the goods cost — measured 2026-10-05

Evidence for [ADR 0401](../adr/0401-an-order-line-keeps-what-its-goods-cost.md).
Measured on a tree at 97d25ee2 with Docker's `postgres:16-alpine`, every Go
command run as `GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`.

## Nothing held a cost, at 97d25ee2

```
$ P='^\s*(ADD COLUMN( IF NOT EXISTS)? )?[a-z_]*(cost|margin)[a-z_]*\s+(bigint|integer|numeric|int|jsonb|text)|CREATE TABLE( IF NOT EXISTS)? [a-z_]*(cost|margin)'
$ git grep -l -i -E "$P" 97d25ee2 -- 'internal/modules/*/migrations/*'
(no output, exit 1)
$ git grep -l -E 'json:"[^"]*(cost|margin)' 97d25ee2 -- internal core plugins
(no output, exit 1)
```

No migration created a table or a column named for a cost or a margin, and no
JSON name in the production trees carried one. The word itself is in migration
comments ("The bcrypt cost parameter", "An index here would cost") and in a
delivery change's constraint name, so the first probe reads definitions rather
than the word. Neither probe is blind: on the changed tree, with `--untracked`,
the first answers order 000041 and product 000015, and the second the five
files that now carry a cost or a margin, the end-to-end test among them.

The order journal's accounts are `receivable`, `sales`, `sales_discounts`,
`tax_payable`, `shipping`, `credit_allowances`, `sales_returns`,
`claim_allowances` and `gift_card` (ADR 0188 and its amendments): none is a
cost of goods.

## Where a cost would have leaked

The storefront's product embeds the variant model, so a field on
`models.Variant` is on every storefront product:

```
$ git grep -l -E "^\s+models\.Variant$" 97d25ee2 -- internal/
97d25ee2:internal/modules/product/service/store.go
```

The admin order list and the storefront's list of a customer's orders wrote the
same `orderDTO`, and the admin order embeds the storefront's order record with
its `lineItemDTO` lines:

```
$ git grep -l "toOrderDTO(" 97d25ee2 -- internal/modules/order/api/
97d25ee2:internal/modules/order/api/admin.go
97d25ee2:internal/modules/order/api/api.go
97d25ee2:internal/modules/order/api/store_own_orders.go
```

So the admin list's row is a type of its own that adds `placed_margin` to the
storefront's order record, the admin order shadows the storefront's lines with
its own, and the cost lives in a table and a model of its own. The invoice
surface's line is `interopInvoiceItem`, which carries neither, and
`TestTheInvoiceSurfaceCarriesNoCost` holds that.

## What the gate reads

`TestACostIsPublishedOnlyWhereDeclared` parses every Go file of the production
trees, generated ones included, and reads three things:

- every name a struct field publishes to JSON: its json tag's name, or its Go
  name when the tag gives none and the field is exported; `json:"-"` and an
  unexported field publish nothing;
- every named type of this module that a publishing field holds, and every one
  a field embeds, through pointers, slices, arrays and map values;
- nothing else: a cost put into a map or an `any` at run time is not read.

A type carries a cost when a name it publishes contains "cost" or "margin",
when it is `models.VariantCost` (whose names are `currency_code` and
`amount`), or when it holds a type that does. Twenty-four types may carry one,
each named with why: the six admin DTOs, the checkout's plan, its lines, the
variant facts and the order snapshot and its lines, the order module's input,
interop snapshot, models and four sqlc rows and parameters, and the auth
module's two `Options`, whose `BcryptCost` is a work factor and no money.
`TestNoGraphQLSchemaNamesACost` reads every `.graphqls` file git lists,
tracked or not, cuts its descriptions and comments, and refuses any name
containing "cost" or "margin": gqlgen binds the storefront's `Variant` to
`service.StoreVariant`, so a schema field served by a resolver has no Go JSON
name to read.

The first version of the gate read only non-empty json tag names on the struct
declaring them. Each leak below was planted in the tree and run against both
versions (`TestACostIsPublishedOnlyWhereDeclared` and
`TestNoGraphQLSchemaNamesACost`):

| Planted leak | First gate | This gate |
|---|---|---|
| `lineItemDTO` gains `UnitCost *int64` tagged `json:",omitempty"` | pass | fail: it publishes "UnitCost" |
| `lineItemDTO` gains an untagged `UnitCost *int64` | pass | fail: it publishes "UnitCost" |
| `orderDTO` holds `*placedMarginDTO` as `"pm"` | pass | fail: it holds `placedMarginDTO` |
| `orderDTO` embeds `*placedMarginDTO` | pass | fail: it holds `placedMarginDTO` |
| `models.Variant` holds `[]VariantCost` as `"prices"` | pass | fail: it holds `VariantCost`, and through it `StoreVariant`, `StoreProduct`, `StoreAddOn` and five more |
| `type Variant` in `schema.graphqls` gains `unitCost: Int` | pass | fail |

`TestTheCostScanIsNotBlind` feeds both scans what each must find, an empty
json name, an untagged field, a held and an embedded cost type, a map of them
and an anonymous struct among them, and a schema field among descriptions that
say "cost".

## The plan is made before any step runs

```
$ git grep -l -E "w\.prepare\(ctx" 97d25ee2 -- internal/workflows/checkout/
97d25ee2:internal/workflows/checkout/complete_cart.go
```

`CompleteCart` builds the plan with `prepare` and hands it to the saga after,
so a cost list refused while the plan is made refuses the checkout before a
reservation or a payment. `TestACostListThatDoesNotReadIsRefused` asserts that
no order is opened and nothing is reserved, for ten shapes of a list that does
not read.

## The read layer's default field set

A reader that names no fields receives the provider's whole record, and the
storefront has published such a record whole before (D258). Every constant
spelling the entity, and every file naming one in an `Entity:` field, at
97d25ee2:

```
$ git grep -l -E '^\s*[A-Za-z_]+ *= *"variant"' 97d25ee2 -- '*.go'
97d25ee2:internal/adminui/catalog.go
97d25ee2:internal/modules/order/service/replacement_bundles.go
97d25ee2:internal/modules/product/service/links.go
97d25ee2:internal/modules/review/purchases.go
97d25ee2:internal/workflows/cart/deps.go
97d25ee2:internal/workflows/checkout/deps.go
97d25ee2:internal/workflows/stockalert/stockalert.go
$ git grep -l -i -E 'Entity: +[a-z]*EntityVariant\b|Entity: +"variant"' 97d25ee2 -- internal plugins core
97d25ee2:core/query/query_test.go
97d25ee2:internal/adminui/carts.go
97d25ee2:internal/adminui/product.go
97d25ee2:internal/adminui/product_add_ons.go
97d25ee2:internal/adminui/variant_bundle.go
97d25ee2:internal/adminui/variant_edit.go
97d25ee2:internal/modules/order/service/replacement_bundles.go
97d25ee2:internal/modules/product/service/export.go
97d25ee2:internal/modules/product/service/links.go
97d25ee2:internal/modules/product/service/links_test.go
97d25ee2:internal/modules/product/service/store.go
97d25ee2:internal/modules/review/purchases.go
97d25ee2:internal/workflows/cart/catalog.go
97d25ee2:internal/workflows/checkout/plan.go
97d25ee2:internal/workflows/stockalert/stockalert.go
```

Two of those are tests, and `links.go` names the entity in its link
definitions and reads nothing. Each of the other twelve files' reads passes a
`Fields` list; the review module's names only the id. `unit_costs` is nevertheless kept
out of the default set, as ADR 0399 keeps inventory's per-warehouse fields, so
the next reader that names none does not publish a cost:
`TestTheVariantRecordCarriesItsCostsOnlyWhenNamed` reads the record with no
fields and finds no `unit_costs` and no read of the table.

## The bound

The cost's bound is a price's, 10^12 minor units, so a line's cost is at most
10^18, an order total's bound. An order may hold 500 lines, so the sum may
pass both that bound and an int64; the aggregate sums the cost as `numeric`
and states none past the bound. `TestThePlacedMarginIsReadFromTheLinesAsSold`
places a million units at 10^12 on one line (10^18, the bound: a cost, and a
margin of -10^18), on two lines (2 x 10^18, past the bound but inside an
int64: no cost) and on ten lines (10^19, past both: no cost and no error).
The first version had only the ten lines, which overflow an int64 under any
bound, so a bound of `math.MaxInt64` and a `<` for `<=` both lived.

## The lanes

Every Go command also ran with `GOFLAGS=-p=2` and
`TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT=5m` after the review's fixes, one
container at a time.

| Lane | Result |
|---|---|
| `go vet` and `go vet -tags integration` on product, order, inventory, the checkout, `internal/arch`, `internal/e2e` | clean |
| `bin/golangci-lint run --timeout 45m --allow-parallel-runners` on product, order, inventory, the checkout, `internal/arch`, `internal/e2e` | 0 issues, 97 s |
| `go test -count=1` on the product, order, inventory and checkout packages | ok |
| `go test -count=1 ./internal/arch/` | ok, 146.5 s |
| `go test -tags integration -count=1 ./internal/modules/product/`, whole | ok, 36.7 s |
| `go test -tags integration -count=1 ./internal/modules/order/`, whole | ok, 30.0 s |
| `go test -tags integration -count=1 ./internal/modules/inventory/`, whole (a migration comment changed) | ok, 125.9 s |
| `go test -tags integration -count=1 ./internal/e2e/`, whole, before the review | ok, 176.3 s |

The checkout's own integration package (`recovery_integration_test.go`) was not
in this run's lanes.

## Mutants

Each mutant was applied alone after a green base run of its package, run with
`-count=1`, and the files restored byte for byte (sha256 compared). The arch
mutants ran the cost tests of `internal/arch`, which read no database.

| # | Mutant | Killed by | Result |
|---|---|---|---|
| P1 | duplicate currency accepted | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P2 | code not upper-cased | `TestAVariantsCostsReadBackAsWritten` | killed |
| P3 | code not trimmed | `TestAVariantsCostsReadBackAsWritten` | killed |
| P4 | negative amount accepted | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P5 | amount bound strict (refuses the bound) | `TestAVariantsCostsReadBackAsWritten` | killed |
| P6 | amount bound dropped | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P7 | list cap dropped | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P8 | list cap strict (refuses 50) | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P9 | code length not checked | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P10 | code letters not checked | `TestACostListIsCheckedBeforeAnythingIsWritten` | killed |
| P11 | write takes no lock (deleted variant written) | `TestADeletedVariantHasNoCosts` | killed |
| P12 | no cost read as nil | `TestAVariantsCostsReadBackAsWritten` | killed |
| P13 | read of a deleted variant not refused | `TestADeletedVariantHasNoCosts` | killed |
| P14 | costs in the default field set | `TestTheVariantRecordCarriesItsCostsOnlyWhenNamed` | killed |
| P15 | costs filled unasked | `TestProviderProjectsFields`, `TestTheVariantRecordCarriesItsCostsOnlyWhenNamed` | killed |
| P16 | no cost is nil, not an empty list | `TestTheVariantRecordCarriesItsCostsOnlyWhenNamed` | killed |
| P17 | one read per record | `TestTheVariantRecordCarriesItsCostsOnlyWhenNamed` | killed |
| P18 | no placeholder in the record (field refused) | `TestTheVariantRecordCarriesItsCostsOnlyWhenNamed` | killed |
| C1 | costs asked in every round | `TestOnlyTheCartsOwnVariantsAreAskedTheirCost` | killed |
| C2 | costs never asked | `TestABackorderedLineIsClaimedAtItsPosition`, `TestAnOrdinaryCheckoutNamesNoParent`, `TestTheOrderAddsToWhatTheCartAddsTo`, `TestTheOrderIsSoldTheCartsDeliveries` | killed |
| C3 | lenient read (a list that does not read is no cost) | `TestACostListThatDoesNotReadIsRefused` | killed |
| C4 | duplicate currency read | `TestACostListThatDoesNotReadIsRefused` | killed |
| C5 | currency letters not checked | `TestACostListThatDoesNotReadIsRefused` | killed |
| C6 | negative cost read | `TestACostListThatDoesNotReadIsRefused` | killed |
| C7 | cost bound strict | `TestACostListThatDoesNotReadIsRefused` | killed |
| C8 | first entry in currency order taken | `TestAnOrderLineCarriesTheCostInTheOrdersCurrency` | killed |
| C9 | cost dropped from the order snapshot | `TestACostOfZeroIsACost`, `TestAnOrderLineCarriesTheCostInTheOrdersCurrency` | killed |
| C10 | snapshot cost a plain int64 with omitempty | `TestACostOfZeroIsACost` | killed |
| C11 | plan cost written when nil | `TestAPlanSavedBeforeTheCostReadsNone` | killed |
| C12 | cost not copied onto the plan line | `TestACostOfZeroIsACost`, `TestAnOrderLineCarriesTheCostInTheOrdersCurrency`, `TestOnlyTheCartsOwnVariantsAreAskedTheirCost` | killed |
| O1 | interop drops the cost | `TestAnOrderLineCostOutOfRangeIsRefused`, `TestPlaceOrderJSONKeepsTheUnitCost` | killed |
| O2 | interop field misnamed | `TestAnOrderLineCostOutOfRangeIsRefused`, `TestPlaceOrderJSONKeepsTheUnitCost` | killed |
| O3 | line write drops the cost | `TestPlaceOrderJSONKeepsTheUnitCost`, `TestTheInvoiceSurfaceCarriesNoCost` | killed |
| O4 | order's cost check dropped | `TestAnOrderLineCostOutOfRangeIsRefused` | killed |
| O5 | order's cost bound is a total's | `TestAnOrderLineCostOutOfRangeIsRefused` | killed |
| O6 | margin is cost less sales | `TestAMarginIsSalesLessCostWhenThereIsACost` | killed |
| O7 | no cost read as zero cost | `TestAMarginIsSalesLessCostWhenThereIsACost` | killed |
| O8 | admin line carries no cost | `TestTheAdminOrderCarriesItsCostAndMargin` | killed |
| O9 | admin order reads no margin | `TestAnOrderWithoutACostSaysSo`, `TestTheAdminOrderCarriesItsCostAndMargin` | killed |
| O10 | admin list rows carry no margin | `TestEachAdminOrderRowCarriesItsMargin` | killed |
| O11 | admin list reads margins per row | `TestEachAdminOrderRowCarriesItsMargin` | killed |
| O12 | cost on the shared line DTO | `TestTheStorefrontOrderCarriesNoCost` | killed |
| O13 | margin on the shared order DTO | `TestDescribedEndpointsDescribeTheirBodies`, `TestTheStorefrontOrderCarriesNoCost` | killed |
| O14 | unit_cost undeclared in the personal-data audit | `TestPersonalDataCoversEveryColumnOfTheSchema` | killed |
| A1 | planted cost on the storefront line DTO | `TestACostIsPublishedOnlyWhereDeclared` | killed |
| A2 | planted cost on models.Variant | `TestACostIsPublishedOnlyWhereDeclared` | killed |
| A3 | checkout spells the field differently | `TestTheCostNamesAgree` | killed |
| A4 | catalog's cost bound wider | `TestTheCostNamesAgree` | killed |
| A5 | the scan forgets margins | `TestTheCostNamesAreCaseFree`, `TestTheCostScanIsNotBlind` | killed |
| A6 | an allowed type renamed away (entry stale) | `TestACostIsPublishedOnlyWhereDeclared` | killed |

Two mutants needed a second shape. O12 first planted the field on `lineItemDTO`
without filling it: nothing reached a body and the API test passed, while A1,
the same tag alone, fails the arch gate; filled from the line, it fails the
storefront test. O10 and C8 first failed to build and were rewritten to
compile. A1 to A6 ran against the first gate and again against this one, with
the five cost tests of `internal/arch`; the table gives the second run.

## Database mutants

After a green whole run of the product and order integration packages, each
mutant was applied alone and only its killing test run (`-run`, `-count=1`),
then the files restored and their sha256 compared. Where the SQL is mutated,
the mutant is applied to the text sqlc generated, which is what runs.

| # | Mutant | Killed by | Result |
|---|---|---|---|
| D1 | product CHECK: amount >= 0 dropped | `TestTheVariantCostSchemaRefuses` | killed |
| D2 | product CHECK: amount <= 10^12 dropped | `TestTheVariantCostSchemaRefuses` | killed |
| D3 | product CHECK: amount <= 10^12 made < | `TestTheVariantCostSchemaRefuses` | killed |
| D4 | product CHECK: currency check dropped | `TestTheVariantCostSchemaRefuses` | killed |
| D5 | SetVariantCosts takes no row lock | `TestTwoCostWritesLeaveOneWholeSet` | killed |
| D6 | replace's DELETE dropped | `TestACostWriteReplacesTheWholeSet` | killed |
| D7 | replace made an upsert (DELETE dropped, ON CONFLICT DO UPDATE) | `TestACostWriteReplacesTheWholeSet` | killed |
| D8 | order CHECK: unit_cost >= 0 dropped | `TestAnOrderLineKeepsItsUnitCost` | killed |
| D9 | order CHECK: unit_cost <= 10^12 dropped | `TestAnOrderLineKeepsItsUnitCost` | killed |
| D10 | order CHECK: unit_cost <= 10^12 made < | `TestAnOrderLineKeepsItsUnitCost` | killed |
| D11 | line write turns no cost into zero (COALESCE) | `TestAnOrderLineKeepsItsUnitCost` | killed |
| D12 | margin: discount ignored | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D13 | margin: line total read as sales | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D14 | margin: unit_price x quantity read as sales | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D15 | margin: cost without the quantity | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D16 | margin: NOT is_giftcard dropped | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D17 | margin: an uncosted line counted as zero | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D18 | margin: cost bound dropped | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D19 | margin: the bound passed as `math.MaxInt64` | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D20 | margin: `<=` the bound made `<` | `TestThePlacedMarginIsReadFromTheLinesAsSold` | killed |
| D21 | interop drops the cost, end to end | `TestAnOrderKeepsTheMarginItWasPlacedAt` | killed |
