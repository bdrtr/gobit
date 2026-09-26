# A label with no destination — measured 2026-09-26

The evidence behind [ADR 0194](../adr/0194-a-carrier-is-told-where-a-parcel-goes.md).

## 1. What a provider was handed

`core/provider.CreateFulfillmentInput` before this change:

| Field | Filled by |
|---|---|
| `Reference` | the parcel's own id (`fulfillment/service/fulfillment.go`) |
| `OptionID` | the shipping option |
| `IdempotencyKey` | the caller's key |
| `Data` | the option's configuration merged with the request's `data` |

The contract's comment on `Data` read "(the address, the item list and so on)".
No caller put an address in it: the fulfilling flow sends no `data`, and the
fulfillment module's own endpoint passes what the operator typed.

## 2. The paths that open a parcel

| Path | Through |
|---|---|
| `POST /admin/v1/orders/{id}/fulfillments` | `fulfilling.OpenForOrder` |
| a claim's or an exchange's replacement dispatch | `returns.openParcel` → `fulfilling.OpenForOrder` |
| `POST /admin/v1/fulfillments` | the fulfillment module alone; it reads no order |

`OpenForOrder` read `OrderContactJSON` and discarded the answer: its godoc said
the flow needed the refusal of an unknown id, not the contact. That call is now
`ShippingAddressJSON`, which refuses the same way and returns the destination.

## 3. Where a copy could have gone

| Place | Erased with the person? |
|---|---|
| `order_addresses` | yes: the order's erasure empties it, keeping the country and metadata |
| `fulfillments.data` (the provider's returned data) | no: the fulfillment module declares no personal data |
| `fulfillment_manual_shipments.data` (the box provider's ledger) | no |

`personaldata.Subject` names a customer id and an e-mail. Neither reaches a
parcel, whose only handle is its order's id. So the destination is handed on
and kept by nobody but the order, and the contract asks a provider not to echo
it into its data.

## 4. The end-to-end path

`internal/e2e/carrier_test.go` installs a spy carrier through
`Host.RegisterFulfillmentProvider`, the call a carrier plugin makes, and opens a
parcel on a spy option for an order whose cart had a gift recipient's shipping
address and a company's billing address:

| Read | Observed |
|---|---|
| the spy's `Create` input, `Destination` | the recipient: name, street, city, postal code, country, and the address's metadata |
| the spy's `Create` input, `Data` | no address key |

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| P1 | the service drops `Destination` from the provider's input | the provider input gate, the service's unit test, e2e |
| P2 | the flow hands on nil | the flow's unit test, e2e |
| P3 | the order sends the billing address as the destination | the order's unit test, e2e |
| P4 | unknown destination fields accepted | the service's unit test |
| P5 | the city filled from the province | the service's unit test, e2e |
| P6 | an unknown order no longer refused | the flow's existing unit test |
| P7 | the address's metadata dropped | the order's unit test, e2e |

P1 is the one the widened gate exists for: with the field dropped from the only
literal that builds the input, `TestEveryQuoteInputFieldIsFilledByTheTree`
reports `CreateFulfillmentInput.Destination` as written by no production file.
