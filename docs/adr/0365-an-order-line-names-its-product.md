# ADR 0365 — An order line names its product

**Summary:** The checkout copies the title of each line's product onto the
order line beside the variant's, and an invoice row prints the two together,
so an order and its documents say what was sold after the catalog moves on.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

An order line's title is its variant's, copied from the catalog when the
order is placed. A variant's title names its options — "1 kg / Filtre" —
and not its product, so the order, the panel's order page and the invoice
printed "1 kg / Filtre" for a bag of coffee. The order kept a variant id
and nothing else that named the product, and a product renamed or deleted
later left no record of what had been sold.

## Decision

An order line carries `product_title`, the title of its variant's product,
copied by the checkout from the product read it already makes for the gift
card flag. An invoice row prints the product and the variant as
"product — variant", the variant alone where the line kept no product title.

## Consequences

- The order is the record of the sale: renaming the product afterwards
  changes nothing on it, as for the variant's title.
- The read costs nothing new: one more field in a query every checkout
  already runs.
- The admin and store order records, the order line entity and the panel's
  order page publish the product's title; a line written before this
  migration carries the empty string, and its rows print the variant alone.
- A missing product title does not stop a sale, as a missing gift card flag
  does: the order still says what it sold by the variant's title.
- A cart line is unchanged: it is not a record, and a storefront that shows
  its product reads the catalog.

## Rejected

- Writing the product's title into the line's title: a shop whose variant
  titles already name the product would print it twice, and the variant's
  own title would be lost to every reader.
- Reading the product's title when the invoice is issued: the document
  would print what the catalog says that day, or nothing for a product
  deleted since.
- Copying the product's id beside its title: no reader needs to follow it,
  and the variant id already leads there while the product exists.
