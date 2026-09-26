# A typo in a street — measured 2026-09-26

The evidence behind [ADR 0195](../adr/0195-a-shipping-address-can-be-corrected.md)
and D141.

## 1. What wrote an order's address

| Statement | Where |
|---|---|
| `CreateOrderAddress` | the order's own transaction at checkout, and now a correction |
| `AnonymizeOrderAddresses` | the erasure: nine columns emptied, the type, country and metadata kept |

Nothing else. 000005's unique index allowed one row per type per order outright.

## 2. What the country carries

| Computed on | Source |
|---|---|
| the tax | the cart's region, and the country when the region maps to exactly one; the province is sent empty (`workflows/cart/tax.go`) |
| the shipping price | `QuoteInput.CountryCode`, the country alone (ADR 0065) |

So a correction inside the country leaves both figures true, and one across a
border would not.

## 3. Which parcel statuses stop a correction

The fulfillment module's statuses and the reason each is on its side of the
line:

| Status | Stops it | Why |
|---|---|---|
| `pending` | yes | the label is printed with the old destination (ADR 0194) |
| `shipped` | yes | the carrier is carrying it there |
| `delivered` | yes | it arrived at the old address |
| `canceled` | no | it never left |
| `returned` | no | it came back; this is the parcel a wrong street sends back |

## 4. The end-to-end path

`internal/e2e/address_correction_test.go`, on an order whose cart had a gift
recipient's shipping address and a company's billing address:

| Step | Observed |
|---|---|
| `PUT …/shipping-address` | 200, the admin record's shipping address is the correction, the billing one unchanged |
| `GET /store/v1/orders/{id}/timeline` | an `order.shipping_address_corrected` entry, no street in the body |
| a parcel opened afterwards on the spy carrier | its destination is the correction |
| a correction while a parcel is pending | 409 `fulfilling_parcel_underway` |
| the same after the parcel is canceled | 200 |
| a correction into `DE` | 409 `order_address_country_changed`, nothing written |

## 5. The migration test that ran after seven files

`TestMigrationIsReversible` in the order module rolled the module back to
nothing and forward again in the database every test shares. Its comment said
it "has to run before the others" and "stands at the top of the file". It did
stand first in `order_integration_test.go`. But `go test` runs a package's files
in name order, and seven files ran before it before this change (`claim_evidence_…`,
`cursor_…`, `disclosure_…`, `erasure_…`, `journal_…`, `line_item_…`,
`order_credit_…`), eight with `addition_…`. Their rows were rewound, which
nothing noticed because nothing read them afterwards.

000023's down file refuses a database that holds a correction, and
`addition_integration_test.go` writes one. So the test now runs in a database
of its own, as the payment module's has since D135, and
`TestARollbackRefusesADatabaseHoldingACorrection` holds the refusal.

The same shape remains in eleven other test files: `core/db`, the b2b, cart,
customer, fulfillment, inventory, promotion, region and tax modules, and the
analytics and searchpg plugins. None of their down files refuses on data
today, so none fails. The first one that does fails the way this one would
have.

## 6. Mutations

| # | Mutation | Unit | Integration / e2e |
|---|---|---|---|
| C1 | a superseded row read as current | killed | killed |
| C2 | the old row not closed | killed | killed |
| C3 | the country not held | killed | killed |
| C4 | the pending rule dropped | killed | — |
| C5 | the erasure rule dropped | killed | — |
| C6 | a pending parcel allowed | killed | killed |
| C7 | a returned parcel blocks | killed | — |
| C8 | no timeline entry | killed | killed |
| C9 | the entry hidden from the customer | killed | killed |
| C10 | an identical correction rewritten | killed | — |
| C11 | an unknown field accepted | killed | — |
| C12 | the supersede statement closes closed rows too | — | killed |

C12 survived the first run. A second correction re-dated the row the first had
closed, so the timeline would have placed the first correction at the second's
moment. Nothing did two corrections on the real schema;
`TestASecondCorrectionLeavesTheFirstDated` does now.
