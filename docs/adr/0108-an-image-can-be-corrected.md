# ADR 0108 — An image can be corrected, and its address cannot

**Summary:** A product's images can be added, patched and removed one at a time;
the patch reaches the alt text, the rank and the metadata, and never the address.
It costs three endpoints and buys a wrong alt text that can be fixed.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

A product's images were fixed at creation. `CreateProduct` took them, nothing
else ever wrote `product_image`, and `UpdateProductInput` carried no image field
— so a picture added later, or a caption typed wrongly, meant deleting the
product and writing it again.

ADR 0104 made that visible. It published `alt_text` on the storefront and in the
GraphQL type, which is the point of an accessibility attribute; a text that is
published and cannot be corrected is worse than one that is merely missing,
because a screen reader now reads out the mistake.

The open question was the ADDRESS. A patch that could move `url` is the obvious
shape, and `product_image` carries two halves of one fact: the address and the
upload binding, written in the same call.

## Decision

Three admin endpoints — add, patch, remove — each addressed by the product's id
AND the image's. The patch reaches `alt_text`, `rank` and `metadata`.

`url` is not patchable. Replacing a picture is a new image and the removal of the
old one.

## Consequences

Both identifiers are in the WHERE clause of every query. Addressing an image by
its own id alone would let a caller name a product of their own and reach
somebody else's picture, and the answer would look like a success.

A patch that moved the address would leave the row's `upload_id` and the link
record pointing at a file the image no longer shows. The module already refuses
to open a "bind this image to that upload" endpoint for exactly that reason;
this is the same refusal from the other side.

`alt_text` gained a length limit, and it gained it on BOTH write paths. A limit
on one of two ways into a column is not a limit.

An added image lands LAST: a zero rank means "not given", as it does in the
create body, and it resolves to one past the highest rank the product carries.
Zero itself would put the new picture first, which is the one position nobody
means by "add an image".

Removing an image removes its upload binding and NOT the file. The file belongs
to the file module and may back another product's image; what the cleanup
protects is the reverse read, so that a picture no storefront shows stops
answering "this file is in use".

No event is published. The module's event names are the product's OWN fields,
and its own rule says an image write that needs an event gets a NEW name rather
than borrowing `product.updated`. Nothing subscribes to such a name today, and a
capability with no consumer is refused (ADR 0009).

## Rejected

**An `images` field on `UpdateProductInput`.** A whole-list PATCH cannot say
"fix this one caption" — it says "these are the images now", so a client that
read the product, edited one entry and sent the list back would delete every
image added since it read.

**A patchable `url`.** It buys replacing a picture in one call and pays with a
record whose two halves disagree.

**Answering the removal with 204.** Every other delete in this module says what
it deleted; one endpoint answering differently makes the client generator
produce two shapes for one intent.
