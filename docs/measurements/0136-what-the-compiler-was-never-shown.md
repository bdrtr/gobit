# What the compiler was never shown — measured 2026-09-11

Serves [ADR 0136](../adr/0136-the-compiler-checks-every-interop-pair.md).

## The defect, and why nothing saw it

`POST /store/v1/carts/{id}/promotions` and `DELETE
/store/v1/carts/{id}/promotions/{code}` are shipped storefront routes, described in
the published document. The cart module resolves `api.CartPromotions` from
`workflows.cart.interop`. The registered value, `*cart.Interop`, carried four
methods — `OpenCartForCountry`, `AddPricedLineItem`, `AddQuotedShippingMethod`,
`SetLineItemQuantity` — and neither coupon method.

Compiled, not read:

```
cannot use (*cartwf.Interop)(nil) (value of type *cart.Interop) as
api.CartPromotions value in variable declaration: *cart.Interop does not
implement api.CartPromotions (missing method ApplyPromotionCode)
```

Four things had to line up for this to ship, and each of them is a mechanism
working as designed:

1. **The two packages cannot see each other.** The consumer declares the interface
   in its own package and the producer never imports it. That is Principle 2.1/2.4
   and it is the point.
2. **Registration does not type-check.** `c.Provide` takes `any`; the assertion
   happens in `Resolve`, at the consumer's type, at the moment the consumer asks.
3. **The resolution is lazy and cached.** `cartPromotions.ApplyPromotionCode` does
   `p.once.Do(p.resolve)`; the failure is stored in `p.err` and every later request
   returns it without retrying. Startup is green and stays green.
4. **The fail-closed nil guard passed.** `Flows.Promotions` is a non-nil wrapper —
   what was absent was one layer deeper than the guard looks.

## Why no test caught it

Every coupon test in `internal/e2e/coupon_test.go` calls
`workflows.ApplyPromotionCode` DIRECTLY on the flow. The flow's logic is thoroughly
covered — four tests, a ceiling case, a cart-data rule — and the bridge between the
module and the flow is covered by none of them.

The routes appear in exactly one test file: `internal/modules/cart/api/describe_internal_test.go`,
which asserts the OpenAPI description exists. So the route was described, the flow
was tested, and the path between them was never driven.

## The sentence that kept the file from being written

`internal/modules/file/service/interop.go` documented the same problem for its own
surface and drew the wrong conclusion:

> The consumer CANNOT import this package, so nothing but an integration test can
> prove the two signatures agree — the compiler never sees them together.

The first half is true and the second does not follow. Neither participant may
import the other; any THIRD package in the same Go module may import both. Proved by
building one:

```
var _ productsvc.UploadReader = (*filesvc.Interop)(nil)   →  exit 0
var _ productsvc.UploadReader = (*filesvc.Service)(nil)   →  does not implement
```

This is the third time in two days that an impossibility claim in this repository
has been wrong, and the second where the claim sat in a record as the reason not to
do something. The other two: "the data-subject sweep cannot be tested from a
separate Go module" (Go's internal rule is path-based, so it can) and "there is no
gate requiring a published topic to be forwarded" (there is).

## What the pins found

Thirty-seven assignments, one per consumer-interface/producer pair. Thirty-six
compiled on the first try; the thirty-seventh is the defect above.

So the drift was ONE pair, not many — which is worth recording in both directions.
The mechanism is not rotting everywhere; it failed once, silently, on a path nothing
drove. A class that fails rarely and invisibly is exactly the class a compile-time
check is cheap enough to carry.

## What the gate found

`TestEveryConsumedInteropNameIsPinned` derives its population the way
`TestTheInteropSurfacesHaveAConsumer` does — names that are both provided and
resolved from outside the owning module — and on its first run it named two pairs
the hand-written list had missed: `auth.interop` and `product.interop`. The second
is a PLUGIN's consumer interface, which no other mechanism could have pinned:
plugins may not import modules at all.

A hand-kept list that decides what gets verified is the fifth of its kind here to be
checked against the world rather than believed. The others: the language detector's
roots, the documentation scan's trees, the separate-module list, the personal-data
audit's trees.

## The mutation table

| Mutation | Bitten by |
|---|---|
| the coupon bridge is removed from the flow's surface | `go vet ./internal/arch/` — compile failure |
| a producer is swapped for the wrong concrete type | compile failure |
| a flow method grows a parameter | compile failure, naming the wanted signature |
| a name is dropped from `pinnedNames` | `TestEveryConsumedInteropNameIsPinned` |
| the derivation stops seeing consumption from outside | the gate's floor |
| the derivation stops matching the interop family | the gate's floor |
| the bridge delegates to the WRONG flow method | `TestTheCouponROUTESReachTheFlow` |

Seven, each restored from a scratchpad copy rather than with `git checkout`.

The last two rows are the division of labor, measured rather than asserted: a
missing bridge cannot reach a test lane because it cannot compile, and a bridge that
compiles and does the wrong thing is invisible to every pin and caught by the route.

## What this does NOT close

The gate prices names and the compiler checks shapes; nothing checks that a pin was
written against the interface the consumer actually resolves. A pin naming the wrong
consumer interface would compile, satisfy the gate, and say nothing about the pair it
was meant to cover. Closing that would mean tying the resolve site's type argument to
the pin, which is the AST comparison this record rejects — so it is named here
instead.
