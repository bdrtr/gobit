# Three rule matchers — measured 2026-10-05

Evidence for [ADR 0396](../adr/0396-a-rule-condition-is-read-by-one-evaluator.md)
and gap D252. Read on a tree at 4cd58356, every Go command as
`GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`.

## 1. The three copies

Each module read a rule with its own `matchRule` and `matchNumeric`, and
promotion a `matchAnyIn` besides:

| Module | File | Functions |
|---|---|---|
| pricing | `internal/modules/pricing/service/calculate.go` | `matchRules`, `matchRule`, `matchNumeric` |
| fulfillment | `internal/modules/fulfillment/service/eligibility.go` | `matchRules`, `matchRule`, `matchNumeric` |
| promotion | `internal/modules/promotion/service/rule.go` | `matchRules`, `matchRule`, `matchAnyIn`, `matchNumeric` |

Each module's `models.go` spelled the operators as string constants of its own
`RuleOperator` type, with its own `Numeric` and `MultiValue` switches.

## 2. Where the copies had drifted

| Point | pricing | fulfillment | promotion |
|---|---|---|---|
| Words admitted | eight | eight | nine (`any_in`, ADR 0144) |
| Trim at match | no | both sides (`strings.TrimSpace`) | no |
| Integer check on write | `strconv.ParseInt` in `validateRule` | **none** | `strconv.ParseInt` in `validateRuleInput` |
| Value-count cap on write | none | 100 (`maxRuleValues`) | 100 (`MaxRuleValues`) |
| Trim at write | no | yes | no |
| Empty value on write | `""` refused, spaces kept | refused after trim | `""` refused, spaces kept |

Fulfillment's `validateRuleInput` trimmed, counted and length-checked the
values and never parsed them, and its table's CHECK
(`000001_fulfillment_init.up.sql`) asks for a known operator word and at least
one value, nothing of the value itself. The eligibility godoc required an integer threshold, and
`TestABrokenRuleDoesNotOpenTheOptionToEveryone` said the service could never
write such a row.

## 3. D252, reproduced

At 4cd58356, before any change, through the service with the fake store:

```
write: rule={... Attribute:subtotal Operator:gte Values:[500.5] ...} err=<nil>
subtotal=0 offered=0
subtotal=500 offered=0
subtotal=501 offered=0
subtotal=1000000 offered=0
```

`TestRuleValidation` with `gte "500.5"` and `gte "fifty thousand"` added was
red on both cases ("An error is expected but got nil"). Over HTTP the same
input is `POST /admin/v1/shipping-options/{id}/rules`, which answered 201.

## 4. The writers

Every writer of a rule goes through one validator per module:

| Module | Validator | Callers |
|---|---|---|
| pricing | `validateRule` (`validate.go`) | `buildRule`, from `service.go` and `price_list.go` |
| fulfillment | `validateRuleInput` (`validate.go`) | `CreateShippingOptionRule` (`catalog.go`) |
| promotion | `validateRuleInput` (`validate.go`) | `AddPromotionRule` (`rule.go`) |

## 5. What reaches the context

- Fulfillment's free rule context reaches the matcher only from the interop
  (`internal/modules/fulfillment/service/interop.go`); the HTTP eligibility
  endpoints never read it (`internal/modules/fulfillment/api/eligibility.go`).
- The cart's shipping quote request carries no attributes field
  (`quoteRequest` in `internal/workflows/cart/shipping.go`).
- Pricing and promotion receive the cart's `cart.` metadata, string values
  only (`addCartMetadata` in `internal/workflows/cart/catalog.go`).
- Tax matches a rate rule's reference by equality and specificity
  (`matchSpecificity` in `internal/modules/tax/service/local.go`); it is not a
  rule of this shape.
- The segment flow (`internal/workflows/segment/segment.go`) has a typed
  `compare` over int64 and a `place` over countries.

## 6. What the gate sees

`grep -rl` for a `case` naming `OpGt`, `OpGte`, `OpLt`, `OpLte` or their
strings, in non-test files under `internal/modules`, `internal/workflows` and
`plugins`, before the change:

```
internal/workflows/segment/segment.go
internal/modules/promotion/models/models.go
internal/modules/promotion/service/rule.go
internal/modules/pricing/models/models.go
internal/modules/pricing/service/calculate.go
internal/modules/fulfillment/models/models.go
internal/modules/fulfillment/service/eligibility.go
```

After it, the three `Valid` methods and the segment flow's `compare`, which
the gate's allowlist names. No case names a comparison word by its string.

The gate resolves a case's value through the constants it names rather than
matching their names, because the words have other names in the tree: the
customer module spells them `SegmentGt` to `SegmentLte`
(`internal/modules/customer/models/segment.go`), used today only in its
`SegmentOperators` map, and the segment flow as its own `OpGt` to `OpLte`. Run
over the tree after the change it finds the same switches as the census above.

## 7. The fixtures are integers

Every numeric shipping rule a test writes through the service has an integer
value: `internal/modules/fulfillment/fulfillment_integration_test.go` (three,
all `"50000"`), `internal/modules/fulfillment/service/eligibility_test.go`
(`"50000"`, `"3"`, `"1000"`), `internal/modules/fulfillment/service/interop_test.go`
(`"50000"`) and `internal/e2e/cart_shipping_options_test.go`
(`strconv.FormatInt`). Two others write one without the service's door:
`TestABrokenRuleDoesNotOpenTheOptionToEveryone` in `eligibility_test.go` puts
`gte "fifty thousand"` straight into the fake store and asserts it matches
nothing, which the evaluator keeps; `internal/modules/fulfillment/api/api_test.go`
sends `gte` with `"50000"` and `"1000"` to a fake service, which runs no
validation. No test in the three modules writes a spaced number, so nothing
relied on fulfillment's trim at match.
