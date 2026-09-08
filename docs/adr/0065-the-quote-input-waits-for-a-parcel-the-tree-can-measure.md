# ADR 0065 — The quote input waits for a parcel the tree can measure

**Summary:** The published shipping quote input is not widened for the district
and the desi, because nothing in this tree could fill either field. It costs a
carrier plugin the ability to price by district until a parcel exists to measure.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

`coreprovider.QuoteInput` carries six fields and stops at the country. A Turkish
carrier's tariff is a function of two numbers it cannot express: the ilce
(district) and the desi (volumetric weight, which needs three dimensions).

Adding the fields is a one-line edit, and that is the trap. The rehearsal is
already in the tree: `QuoteInput.TotalWeight` is the one field of this shape the
struct has, and the only trusted producer — the cart flow that prices an option
server-side — hands it a literal zero, because the cart carries no weight. A
carrier reading that zero cannot tell "no parcel" from "this installation does
not measure one".

Filling a district and a desi is a schema change in three modules. No district
column exists anywhere; `customer_address` holds no sub-country column at all,
so a saved address cannot bring one to the cart; and `product_variant` carries a
weight but no box — only its parent `product` does, so a variant would borrow
the carton of the size it exists to differ from. The package is published
(ADR 0026), so a field that lands empty stays.

Measurement: [measurements/0065](../measurements/0065-carrier-quote-input.md).

## Decision

**The quote input is not widened, and the gap becomes a decision with a
trigger: the day this tree can address and measure a parcel.** Two facts, both
checkable — `product_variant` carries the three dimension columns `product`
already has, and the sub-country unit below the city means ONE thing across the
customer address book, the cart and the order.

## Consequences

- **An option priced by district stays flat, or carries its tariff in the
  option's own stored data.** That is the cost, it is paid by whoever integrates
  a carrier, and the module's eligibility godoc already names it as the route.
- **C6 stops waiting on this record, and the dependency was pointing the wrong
  way.** A carrier plugin can ship against the surface as it stands; the fields
  cannot be chosen without a tariff to choose them for. Postal code, district,
  collection branch, payment on delivery — four plausible answers, no evidence,
  and a published package keeps whichever one is guessed.
- **A gate holds the rule rather than a paragraph.**
  `TestEveryQuoteInputFieldIsFilledByTheTree` refuses a field of the published
  input that no production file assigns. It does not freeze the struct: when the
  trigger is observed the fields land with the code that fills them.
- **The second trigger fact is now a live correction (D33), not a footnote.**
  `province` already means two things — the tax schema's il and, in the
  repository's own end-to-end shipping test, an ilce — invisible only because
  the cart's tax request sends the province always empty.
- **The eligibility godoc's claim that dimensions are "a variant's box" is
  corrected in place.** It was false against the schema, and it is what made
  this row look smaller than it is.
- **The third blocker is untouched and stays on C6**: a carrier still has
  nowhere to deliver what it receives.

## Rejected

- **Widen the input now and let the fields sit empty.** That is the refused
  shape this repository has paid for twice, on a surface that cannot take it
  back before 1.0.0.
- **Carry the district through the untyped data bag.** That bag holds the
  OPTION's stored configuration, not the request's; a district in it can gate
  whether an option is offered and cannot influence what it costs.
- **Put the dimensions on the shipping option.** They are a property of the
  goods, not of the delivery method, and one option serves every parcel.
- **Read the parent product's box for a variant.** It answers with confidence
  for the case it is most often wrong about.
- **Rename `province` to `district` in the cart and the order.** It picks one of
  two readings for a column four migrations use; the choice belongs to whoever
  adds the missing one.
- **Write a carrier plugin now, to be the first consumer.** Inventing the caller
  is how a guessed field gets ratified instead of measured.
