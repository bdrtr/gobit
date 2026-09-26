# An address nobody read — measured 2026-09-26

The evidence behind [ADR 0193](../adr/0193-an-order-says-where-it-went.md) and
D140.

## 1. Two commits, six hours apart

| Commit | Time (2026-09-05) | What it said |
|---|---|---|
| `19c48fa` invoicing workflow | 11:48 | the invoice surface carries "no customer id, no addresses, no payment state"; every buyer field but the e-mail comes from the request, "because the order does not know it" |
| `0eba299` B11 | 17:30 | `order_addresses`, because "an order could not say where it went, an invoice could not print a buyer, a shipping label had no destination" |

The first sentence was true when it was written. The second commit made it
false and named the invoice as a reason, and neither file changed after.

## 2. The three readers B11 named, on 2026-09-26

Found by reading every consumer of the order's detail before this change:

| Reader B11 named | What it read |
|---|---|
| the order saying where it went | `GetOrder` loaded both addresses into `OrderDetail`; no DTO in `order/api` had an address field, so neither surface returned them |
| the invoice printing a buyer | `OrderInvoiceJSON` sent no address; `IssueForOrder` filled only an empty e-mail |
| a label's destination | `fulfilling.OpenForOrder` reads the order only to refuse a wrong id; `CreateFulfillmentInput` carries `Reference`, `OptionID`, `IdempotencyKey` and the operator's `Data` |

Everything else that touches the table is the person's: the disclosure reads it
and the erasure empties it.

## 3. Why B11's own test did not see it

`TestTheCartsAddressReachesTheOrder` (`internal/e2e/shipping_test.go`)
reads the order with `orderSvc.GetOrder` — the service, in process — and asserts
"the order has no billing address; an invoice cannot print a buyer". The
service had the address. The invoice did not print it. The test's subject was
the producer and the sentence was about a consumer.

The tests this change adds read through the consumers: the admin HTTP record,
and the printed document from `GET /admin/v1/invoices/{id}` after an issue whose
body names no buyer.

## 4. The end-to-end path

`internal/e2e/order_address_test.go`: a cart given a shipping address (a gift
recipient) and a billing address (a company) on the storefront, checked out.

| Read | Observed |
|---|---|
| `GET /admin/v1/orders/{id}` `shipping_address` | the recipient, street, postal code, country, and the address's metadata |
| `GET /admin/v1/orders/{id}` `billing_address` | the company |
| `GET /store/v1/orders/{id}` | no `shipping_address` |
| invoice issued with `buyer: {tax_number}` only | name `Engines Ltd`, address `12 Main St\nFloor 3\n62701 Springfield North`, the country, the tax number as sent |

`TestAnOrderCanBeInvoicedOverHTTP`, whose body sends a name and whose order has
no billing address, prints the name it sent, as before.

## 5. Mutations

Each applied alone, the named lanes run with `-count=1`, the file restored.

| # | Mutation | Unit | e2e |
|---|---|---|---|
| N1 | the admin read answers the storefront's record | killed | killed |
| N2 | the storefront read answers with the addresses | killed | killed |
| N3 | the invoice surface omits the billing address | killed | killed |
| N4 | the invoicing flow's wire tag misspelled | killed | killed |
| N5 | a name the caller sent is overwritten | killed | survived |
| N6 | the person is printed over the company | killed | killed |
| N7 | the address's metadata dropped from the record | killed | killed |
| N8 | a transition answers the storefront's record | killed | — |
| N9 | the shipping address sent as the billing one | killed | killed |
| N10 | the address printed on one line | killed | killed |
| N11 | an erased address (no street) fills nothing | killed | — |

N5 survives end to end because the one e2e test whose body sends a name has an
order with no billing address, so nothing is filled either way; the unit test
`TestWhatTheCallerSendsIsNeverOverruled` holds it.

## 6. Not decided here

- The carrier's destination: `core/provider.CreateFulfillmentInput` carries
  none, and a parcel's label still has only what the operator types.
- The panel's order page, which reads the order through the query layer and
  shows its amounts only.
