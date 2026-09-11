# Which parcels were bound

Evidence for [ADR 0140](../adr/0140-a-parcel-records-which-order-it-is-for.md).

Measured 2026-09-11, against the tree at ADR 0139.

## The two ways a parcel is opened

```
$ grep -rl 'CreateFulfillment(' --include='*.go' internal | grep -v '_test'
internal/modules/fulfillment/service/interop.go    ← Interop.CreateFulfillment, the flow's path
internal/modules/fulfillment/api/fulfillments.go   ← the admin endpoint's handler
internal/workflows/fulfilling/open.go              ← Workflows.OpenForOrder, calling the interop
```

Two production call sites, and they differ in both directions:

| | Items | `order_fulfillment` binding |
|---|---|---|
| `POST /admin/v1/orders/{id}/fulfillments` → the fulfilling flow | **no** — the interop's own godoc says "the item breakdown is NOT given through this surface" | yes, written by the flow |
| `POST /admin/v1/fulfillments` | **yes** | **no** |

```
$ grep -rl 'links.Create(ctx, LinkOrderFulfillment' --include='*.go' . | grep -v '_test'
internal/workflows/fulfilling/open.go    ← inside Workflows.OpenForOrder
```

One writer, on the path that carries no items.

## The probe

Reading is not proof, so the claim was run against a real Postgres: create a
parcel through the module with an item breakdown, declare the link definitions,
and ask both sides what they see.

```go
ful, _ := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
    Reference:        "ord_probe_link",
    ShippingOptionID: option.ID,
    IdempotencyKey:   "key_probe_link",
    Items:            []service.FulfillmentItemInput{{LineItemID: "oli_probe", Quantity: 3}},
})
bound, _ := links.List(ctx, service.LinkOrderFulfillment, "ord_probe_link")
held, _ := svc.CommittedQuantities(ctx, []string{ful.ID})
```

```
PROBE: parcel ful_06G93CY76GZVB0E7VE9HE2WQDM created with 3 units of oli_probe
PROBE: order_fulfillment links for ord_probe_link = [] (len 0)
PROBE: CommittedQuantities for that parcel = map[oli_probe:3]
```

The parcel holds three units and belongs to nobody. The probe was deleted once it
had answered; what replaced it is the test beside the write.

The first run of the probe failed for a different reason — `link_not_defined` —
because the definitions are declared by the module's `Register` and the probe
built the service directly. That is worth recording: it is the shape in which a
link question answers "no" for a reason that has nothing to do with the data.

## What the zero was the middle term of

| Record | Formula | What the always-zero term did to it |
|---|---|---|
| ADR 0134 | put back `min(canceled, bought − committed)` | a write-off returned units sitting in a box — stock added for goods that then shipped |
| ADR 0135 | a parcel may hold `bought − canceled − committed` | a second parcel could hold the units the first already held; the bound's third term never bound anything |
| ADR 0139 | release what a canceled parcel held | the parcel had no order, so the handler returned before doing anything |

All three describe a subtraction, and the thing being subtracted was structurally
zero for every parcel that had anything to subtract.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 11 | bind only a NEW parcel, never on a retry | **bit** (3 tests) |
| 12 | swallow the binding error and return the parcel | **bit** (2 tests) |

Mutation 11 is the one worth keeping. A parcel can commit while its binding fails,
and if the retry does not write the binding again, that parcel is orphaned for
good — the operator's only recovery is a fresh idempotency key, which opens a
second parcel and makes the count worse.

## A finding in the tests rather than the code

Moving the write broke two tests in `internal/workflows/fulfilling`, and only one
of them was about the write.

`TestASecondPressOpensNoSecondParcel` failed because the flow decides "already
open" from what was bound BEFORE it called the module — so with the write moved,
the flow's fake module had to bind, and it did not. The production path is
correct: the real module binds inside `CreateFulfillment`, so the pre-read on the
second press sees it. What was wrong was the FAKE, which stood in for a producer
and did not do what the producer does.

That is the same seam two mutations exposed in ADR 0135's slice and one in
ADR 0137's. It is recorded again because the shape keeps recurring: when a
responsibility moves between two packages, the fake on the far side is the piece
that silently stays behind.

## What was not measured

How many parcels in any running installation are unbound, and whether the
over-return it enables has happened. The evidence would be an inventory movement
with reason `cancellation` for units that later shipped, which is reconstructable
in principle and was not attempted here.
