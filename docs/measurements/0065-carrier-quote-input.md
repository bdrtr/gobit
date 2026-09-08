# The carrier-capable quote input — measured 2026-09-08

Evidence for ADR 0065, and the re-measurement of `docs/gaps.md` row B10. The row
said the quote-input half was "larger than this row said". It is, and the shape
of the excess is not the one the row's own godoc described.

## What the quote surface takes today

`coreprovider.QuoteInput` has SIX fields: `OptionID`, `CurrencyCode`,
`CountryCode`, `TotalWeight` (grams), `ItemCount`, and an untyped `Data` map
that carries the OPTION's stored configuration and never the request's.

| | count |
|---|---|
| fields on the published input | 6 |
| production files that build one | 1 — `Service.quote`, in the fulfillment module |
| `FulfillmentProvider` implementations in the tree | 1 — `manual.Provider` |
| plugins that register a shipping provider | 0 |

`ProviderRegistry.Register` and `Host.RegisterFulfillmentProvider` both exist
and both work; the registry's own godoc already says the slot ships unfilled.

## What a Turkish carrier prices on

Two numbers, and the surface can express neither.

- **Desi** is volumetric weight: length × width × height in centimetres divided
  by a carrier's constant, compared against the real weight, and the larger of
  the two is billed. It needs three dimensions. `QuoteInput` carries none.
- **The ilce (district)** is the tariff's other axis: an Istanbul-to-Kadikoy
  parcel and an Istanbul-to-a-village parcel are different prices under the same
  country code. `QuoteInput` stops at `CountryCode`.

## The producer side is the blocker, not the struct

This is the correction the row needed. Adding two fields is a one-line edit;
NOTHING IN THIS TREE COULD FILL THEM.

**The rehearsal is already in the repository.** `QuoteInput.TotalWeight` is the
one field of this shape the struct already has. The only trusted producer —
the cart flow that prices a shipping option server-side — hands it a literal
zero, with a godoc that says so plainly: the cart does not carry weight, and
sending zero is the honest answer. On the untrusted HTTP surface the number is a
client CLAIM, and any option with a rule bound to it is dropped from the list
rather than priced. So the field that a carrier would reach for first has, in
practice, no producer at all. A district and a desi added today would land in
exactly that position — except they would land on a PUBLISHED package (ADR 0026)
and could not be taken back before 1.0.0.

### The destination: three findings, one of them new

1. **No district column exists anywhere in the tree.** `cart_addresses` and
   `order_addresses` carry `address_1`, `address_2`, `city`, `province`,
   `postal_code`, `country_code`. Four `.up.sql` files in the repository mention
   a province at all: the cart's, the order's, the inventory location's and the
   tax region's.
2. **The customer address book holds no province either — not even that.**
   `customer_address` has `city`, `postal_code`, `country_code` and no
   sub-country column of any kind; the word does not occur in the customer
   module's Go source. The cart's copy is made by the CALLER, so a shopper
   picking a saved address cannot bring a province to the cart, because the book
   never held one.
3. **`province` already carries two incompatible meanings, and nothing has ever
   compared them.** The tax schema defines a province region as a sub-country
   unit under a country root — for Turkey, an il. The repository's own
   end-to-end shipping test files `City: "Istanbul", Province: "Kadikoy"`, and
   its assertion message says why: *"the province was lost on the way; a
   domestic carrier prices on the district"* — Kadikoy is an ilce, not an il.
   Both readings compile and both pass. The collision has been invisible because
   the cart's tax request sends `province_code` ALWAYS EMPTY, so the two
   spellings have never met. The cart address's own `Province` field carries no
   godoc line at all.

**And the quote path never reads the address.** `Service.ListShippingOptionsFor`
takes its country from the REGION, deliberately, so that shipping and tax cannot
disagree about where the cart is. The shipping address is not an input to
pricing at any point today. A district field on the input would therefore need a
plumbing path that does not exist yet, on top of a column that does not exist
yet.

### The parcel: the row's own godoc was wrong

The eligibility godoc said dimensions are "a CATALOG fact (a variant's box)".
Measured against the schema, a variant has no box:

| table | weight | length | width | height |
|---|---|---|---|---|
| `product` | yes | yes | yes | yes |
| `product_variant` | yes | — | — | — |

A cart line names a VARIANT. Computing a desi today would mean taking the
parent product's box for every variant of it — which is wrong for exactly the
case a variant exists to express, a size that ships in a different carton. And
the cart flow reads the catalog only for the product id a tax rule matches on;
no weight and no dimension crosses that boundary anywhere.

## The third blocker, re-verified

Neither B10 nor C6 named it until 2026-09-06 and it has not moved: **a carrier
has nowhere to deliver what it receives.** The fulfillment module's cross-module
write surface is five methods and none of them moves a shipment to shipped,
delivered or returned. The admin routes can, but a carrier's webhook holds no
admin credential — the premise `corehttp.CallbackRegistry` is built on — and a
plugin can reach neither: the import is refused by a gate, and a structural
interface cannot be written because the method returns a type the plugin's
package may not name. The shape the method should take when it lands is already
written down in the module's interop godoc, one method carrying the carrier's
own instant.

## So the dependency in C6 points the wrong way

C6 says carrier plugins wait on B10's quote input. Measured, the reverse is the
part that binds: the fields cannot be chosen without a tariff to choose them
for. Postal code or district? Both? A collection branch? Payment on delivery as
a price input? Five plausible answers, no evidence, and a published package that
keeps whichever one is guessed. A carrier plugin CAN ship against the surface as
it stands — the eligibility godoc already names the route, a flat option or the
tariff carried in the option's own `Data` — and it is that plugin's tariff
function that turns the field list from a guess into a transcription.

## What was built instead

`TestEveryQuoteInputFieldIsFilledByTheTree` in `internal/arch/quote_input_test.go`:
every field of the published input must be assigned by at least one production
composite literal. The population is two independent sources — the struct's own
fields by reflection, the assignments by a walk of the production trees — so
neither is derived from the property audited.

Proved by mutation, each reverted from a copy taken aside, all with `-count=1`:

| mutation | result |
|---|---|
| add `DistrictCode` to the published struct with no writer | FAIL, naming the field |
| delete `TotalWeight` from the one production literal | FAIL, naming the field |
| point the audit's import path at a package that does not exist | FAIL on the blindness floor |
| rewrite the production literal with positional fields | FAIL, once, plus the floor |

It is a producer-side rule on purpose. The READER of a quote input is a
provider, and a provider is written by whoever integrates a carrier — outside
this repository. A reader-side audit would go red today on `CountryCode`, which
the one shipped provider does not look at and a real carrier certainly would.
The tree can only be held to the half it owns.

The gate does not freeze the struct, which is the point: when the trigger in
ADR 0065 is observed, the fields land together with the code that fills them and
the audit stays green.
