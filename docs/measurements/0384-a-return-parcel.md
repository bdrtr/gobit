# A parcel coming back — measured 2026-10-04

The evidence behind
[ADR 0384](../adr/0384-a-return-parcel-brings-back-the-return-it-names.md).

## The 409 the documents did not mention

On the tree before ADR 0384 (4d55b7ce), an end-to-end probe over HTTP: three
units of one line bought, two of them in a parcel opened with
`POST /admin/v1/fulfillments`, shipped and delivered; the customer asked from
the storefront to return those two; an `is_return` option was created and a
parcel on it was asked for the two units coming back.

```
POST /admin/v1/fulfillments  (is_return option, items: line x 2)
-> 409 {"error":{"code":"fulfillment_line_not_dispatchable",
        "message":"line oli_… of order order_… owes 1 more unit(s) and the
        parcel asks for 2; what was sold minus what was canceled minus what is
        already in a live parcel is the bound"}}
```

The order still owed one unit, so the bound (ADR 0135) read the parcel coming
back as a second shipment of goods already shipped. Had it asked for one unit,
it would have passed and taken the unit the order still owed.

## The parcel the order's door opened

The same probe, then `POST /admin/v1/orders/{id}/fulfillments` naming the
return option:

```
-> 201 {"data":{"fulfillment_id":"ful_…","already_open":false}}
GET /admin/v1/orders/{id}/fulfillments
-> 200 {"data":[{"fulfillment_id":"ful_…","status":"pending"},
                {"fulfillment_id":"ful_…","status":"delivered"}]}
```

The order's door carries no items, so the bound looped over nothing. The flow
handed the provider the customer's shipping address as the destination, and
the module bound the parcel to the order as one of its shipments.

## Who reads the order's parcels through the link

Every reader of an order's parcels finds them through the `order_fulfillment`
link, so a parcel the link does not carry is invisible to all of them:

| Reader | File | What a bound return parcel would do there |
|---|---|---|
| dispatch bound | `internal/workflows/fulfilling/dispatchable.go` | count as goods gone out |
| write-off stock target | `internal/workflows/ordercancel/ordercancel.go` | count as goods gone out |
| canceled parcel's release | `internal/workflows/ordercancel/parcelcancel.go` | count as goods gone out |
| address correction | `internal/workflows/fulfilling/address.go` | hold it off as a parcel underway |
| delivery change | `internal/workflows/fulfilling/delivery.go` | hold it off as a parcel underway |
| addition's join | `internal/workflows/fulfilling/join.go` | take an addition's goods into it |
| "already open" | `internal/workflows/fulfilling/open.go` | report it on the order's door |
| order page | `internal/adminui/orders.go` | list it as the order's |
| Parcels screen's order column | `internal/adminui/parcels.go` | name the order it was opened for |
| order timeline | `internal/modules/order/service/timeline.go` | list it as a shipment |

The return parcel's own sum reads the fulfillment module's `return_id`
column, so no link and no new fulfillment interop method were needed.

## A return need not follow a shipment

The order module caps a return at bought − returned − canceled under the
order's lock (`internal/modules/order/service/aftersales.go`, `CreateReturn`).
Nothing requires a delivered parcel first, so "a return parcel always follows
a delivered outgoing one" is not a premise the code holds. A return parcel is
bounded by its return alone.

## Rows a migration leaves behind

A parcel opened on a return option before 000006 has `return_id` NULL and now
reads as outgoing; a retry under its old key is still answered with that
parcel, because a key that names a parcel is not asked its direction again. It
counts as goods gone out and is bound to its order. The count to check
on an installation before upgrading:

```sql
SELECT count(*) FROM fulfillments f
JOIN shipping_options o ON o.id = f.shipping_option_id
WHERE o.is_return;
```

The expected answer is 0: no route opened a return option's parcel on purpose.
This query was not run against the starter's database for this record.
