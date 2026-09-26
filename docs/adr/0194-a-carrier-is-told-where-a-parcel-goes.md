# ADR 0194 — A carrier is told where a parcel goes

**Summary:** A parcel opened for an order hands its provider the order's
shipping address as a typed destination on the published contract, and nothing
in this repository keeps a second copy. It costs a published type and a field
that every carrier plugin can read, and it closes the last reader D140 named.

- **Status:** Accepted
- **Date:** 2026-09-26

Measurement: [measurements/0194](../measurements/0194-a-label-with-no-destination.md)

## Context

ADR 0193 gave the operator and the invoice the order's addresses. A parcel's
provider still got a reference, an option, an idempotency key and free-form
data, and the contract's own comment said the address belonged in that data.
Nothing put it there, and no provider in the tree could have told a missing
address from a download. The fulfilling flow already read the order before
opening a parcel, only to refuse an id that named nothing.

## Decision

`core/provider.CreateFulfillmentInput` carries `Destination *Address`, and the
fulfilling flow fills it from the order's shipping address for every parcel it
opens. The destination is handed on and stored by neither the fulfillment module
nor the box provider, and a provider may not return it in its data.

## Consequences

Both paths that open a parcel for an order go through the flow and carry the
destination: the admin order endpoint and a replacement's dispatch for a claim
or an exchange. The fulfillment module's own `POST /admin/v1/fulfillments`
cannot read an order, and its parcels carry none.

The flow reads the shipping address in the call that already refused an unknown
order, so opening a parcel costs no extra read. An order with no shipping
address hands on nil. An erased one hands on the country and the address's
metadata, which is what the order keeps, and is not refused.

The order stays the one holder of the address, and its erasure is the one that
empties it. The parcel stores the provider's returned data, and the parcel is
not erased with the person, so the contract forbids the provider to echo the
destination there. Nothing checks a plugin for that; the conformance kit does
not yet cover shipping.

The box provider reads none of it. It stands for the shop's own hands, and the
operator reads the destination off the order (ADR 0193). The field's reader is a
carrier plugin, and the end-to-end test stands a spy where one would stand.

The fulfillment module refuses a destination with a field it does not know, so
a label never goes out short of something the order sent.

The rule ADR 0065 holds the quote input to, that the tree fills every field, now
holds this input too. `Address` is a published name kept until 1.0.0 (ADR 0026).

## Rejected

- **The address in `Data` under a documented key.** Every carrier would parse
  the framework's key names out of a free-form map, and no gate could see
  whether the tree fills them.
- **The box provider recording the destination in its ledger.** The ledger is
  not erased with the person: an erasure names a customer and an e-mail, and the
  fulfillment module can reach neither from a parcel.
- **The address on the quote input as well.** Pricing waits for the district
  (ADR 0065); this field is for the label.
