# One box, two orders — measured 2026-09-26

The evidence behind [ADR 0197](../adr/0197-an-addition-travels-in-its-parents-parcel.md).

## 1. How the link held one order per parcel

| Where | What |
|---|---|
| `fulfillment/service/links.go` | `order_fulfillment` declared `OneToMany` |
| `core/link/registry.go` | one to many creates `link_order_fulfillment_to_uniq` on the parcel side |
| `core/link/service.go` | binding a parcel to a second order: "target … is already bound to another record" |

A declaration may only be widened (ADR 0116). A widening updates the ledger,
creates the `_to_lookup` index and drops `_to_uniq` at startup; `verifySchema`
refuses to start while the dropped index is still there, and the older binary is
refused against the widened ledger.

## 2. Who read the link, and which reading assumed one order

| Reader | Direction | Assumed one order? |
|---|---|---|
| `fulfilling` open, shipments, address correction, dispatch bound | order → parcels | no: each reads its own order's parcels |
| `ordercancel.committedQuantity(ies)` | order → parcels | no |
| `ordercancel.orderOfParcel` (parcel cancel) | parcel → orders | **yes: took `orders[0]`** |
| order timeline and as-of, through the read layer | order → parcels | no |
| tracking, replacement dispatch | by parcel id | no |

The parcel cancel reads the link on purpose rather than the event's `reference`
field, which the fulfillment module never validates (ADR 0134). With two orders
bound, the first one the link answers may be the addition, whose lines the
parcel never held, and the old code logged "a line the order does not have"
and put nothing back. It now maps each held line to the bound order that has
it. The test's fake answers the reverse link sorted, with the addition's id
sorting first, so the old reading fails every run rather than half of them.

## 3. What the order endpoint's parcels hold

`POST /admin/v1/orders/{id}/fulfillments` opens a parcel with no items: the
flow's `CreateFulfillment` takes no item list. So "the addition travels in it"
is a binding and not a change to the parcel's contents, and nothing is sent to
the carrier. The dispatch bound (`bought − canceled − in live parcels`) is read
per order and counts only parcels bound to that order, so an itemless share
adds nothing to anyone's count.

## 4. The end-to-end path

`internal/e2e/parcel_join_test.go`, on a parent whose cart had a gift
recipient's shipping address, with a parcel opened on the spy carrier:

| Step | Observed |
|---|---|
| an addition with no address joins the parcel | 200, the parcel in the addition's shipments |
| the same call again | 200 |
| the parent's shipments | the parcel is still there |
| the addition's timeline | a `shipment.opened` entry |
| an addition with its own, different address | 409 `order_ships_elsewhere` |
| any addition after the parcel is canceled | 409 `fulfilling_parcel_not_waiting` |

The first row is also the proof that the widened link allows two orders on one
parcel: under `OneToMany` it fails on the unique index.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| J1 | the link declared `OneToMany` again | e2e |
| J2 | any parcel joinable | the flow's unit test |
| J3 | a parcel in any status joinable | the flow's unit test, e2e |
| J4 | the addresses not compared | the order's unit test, e2e |
| J5 | an order adding to nothing allowed | the order's unit test |
| J6 | the parent's status not held | the order's unit test |
| J7 | the addition's status not held | the order's unit test |
| J8 | the binding skipped | the flow's unit test, e2e |
| J9 | the parcel cancel reads the first bound order | the cancel flow's unit test |
