# ADR 0223 — A cart line carries what the shopper wrote

**Summary:** A cart line takes the shopper's own words, an engraving or a gift
message, as properties that make it a line of its own, and the order line keeps
them with the line's note.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0223](../measurements/0223-the-words-on-a-line.md)

## Context

The storefront's add-line body took a free-form `metadata` described as a gift
note or a personalization. The same variant added again raised the first line's
quantity and dropped the second note, the cart holding one line per variant by a
unique index. The checkout's order snapshot had no field for it, so the order
never received a note its own schema took (D152). The order module asks that a
key a consumer needs be promoted to a column where it can be validated.

## Decision

A cart line carries `properties`, up to ten names with the texts the shopper
wrote, taken on the storefront and admin add-line bodies, and the same variant
with other properties is another line. The checkout hands each line's properties
and its metadata to the order line, which keeps them and publishes the
properties on its bodies and its read layer.

## Consequences

A name is 1 to 64 characters and a text 1 to 500, both trimmed and without a
control character; anything else is refused with 422
`cart_line_properties_invalid` before the cart is written. The same words raise
the line already there, whatever order their names came in: the unique index
keys on a hash of the JSONB's canonical text. A merge (ADR 0107) sums lines of
the same variant and properties and keeps others apart.

A variant split across lines by its words is priced at each line's quantity and
reserved line by line; the checkout already reserved per line and refuses when
the lines together do not fit the stock. Properties never change a price: a
modifier would need a price row the ladder picks (ADR 0168) and a decision of
its own under ADR 0021. A line's words cannot be edited in place; the shopper
removes the line and adds another.

Both modules declare the column as open personal data, kept on erasure as their
other free-form columns are (ADR 0029, 0033), and disclose it. The metadata now
reaches the order line and stays out of its read layer, as before. Rolling back
the cart's migration refuses while a cart holds one variant on two lines.

## Rejected

- **The metadata as the line's identity.** It is free-form and may carry what
  must not split a line, and the order module asked for a column.
- **One line per variant with a list of words.** How many of each engraving was
  bought would be lost.
- **An option tree with price modifiers now.** Pricing has no row for a
  modifier, and the server deciding the price needs its own design.
