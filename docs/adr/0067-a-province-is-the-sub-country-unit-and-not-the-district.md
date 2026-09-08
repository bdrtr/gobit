# ADR 0067 — A province is the sub-country unit, and not the district

**Summary:** `province` means what the tax schema already defined — the unit
under the country, an il in Turkey. The district a domestic carrier prices on is
a different thing and still has no field.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

The word carried two readings and nothing said which was right.

The tax schema defines a province region as "a country root, or a province under
that root", and names the class: a US state, a Canadian province, a TR province.
It has a uniqueness rule and a constraint keeping a province's country from
diverging from its root's, so the reading is load bearing.

The end-to-end shipping test filed `Province: "Kadikoy"` and said why in its own
assertion — *a domestic carrier prices on the district*. Kadikoy is an ilce.

Both compiled and both suites were green, because the two had never met: the
cart's tax request sends the province ALWAYS EMPTY, so nothing had ever compared
them. The cart's field carried no comment at all, and neither did ten others.
That is how the second reading arrived — not by an argument anybody lost, but by
a field appearing beside the others with nothing said.

Measurement: [measurements/0067](../measurements/0067-two-readings-of-province.md).

## Decision

**`province` is the sub-country unit under the country, and the district is not
it.** Every hand-written declaration of the word says so, and
`internal/arch/province_test.go` refuses a new one that says nothing.

## Consequences

- **The end-to-end address was wrong and is corrected.** It now files Kadikoy as
  the CITY, which is what a Turkish shopper writes, and Istanbul as the
  province.
- **The district still has no field**, and that is ADR 0065's open trigger
  rather than an omission this record closes. A fourth reading of the word is
  what adding one carelessly would produce.
- **Eleven declarations gained a comment they never had.** The count is the
  finding: the word was introduced in silence everywhere it appears, so the
  second reading was not an accident of one file.
- **The gate cannot read the comment.** It refuses the SILENT introduction and
  nothing more; somebody who writes "the district" in it gets past and is then
  arguing in the open, which is where this record answers them.
- **Generated files are out of scope.** sqlc rewrites them from the schema and a
  comment there would be erased on the next generation; the schema's own header
  is where their meaning lives.
- **`customer_address` still has no sub-country column at all**, so a shopper
  picking a saved address cannot bring a province to the cart — the cart's copy
  is made by the caller and the book never held one. Adding it now would be a
  column nothing reads, since the tax request sends the field empty. It arrives
  when the province is first READ.

## Rejected

- **Make `province` mean the district.** It would contradict a schema with a
  constraint behind it, and rename the thing tax matches on below the country.
- **Add a district field now.** It has no reader: the tax request sends the
  province empty and no carrier plugin exists. That is the field-with-no-reader
  this repository refused four times over in ADR 0048.
- **Add the column to `customer_address` for parity.** Same reason, one layer
  down — parity with a field nothing reads is not a reader.
- **Leave it ambiguous and let each module mean what it needs.** It is what the
  tree already did, and the cost was two readings that never met and a test
  asserting the wrong one while green.
