# ADR 0222 — A product write names the version it read

**Summary:** A write that revises a product can name the version it was read
at, and is refused with 412 when the product has moved on; the panel's edit form
always names it.

- **Status:** Accepted
- **Date:** 2026-09-28

Measurement: [measurements/0222](../measurements/0222-two-operators-one-product.md)

## Context

ADR 0013 records that two operators saving one product overwrite each other,
the last write winning. ADR 0221 gave a product a `version` counting its
revisions, stamped under the product's row lock by every write that revises it,
and left it unchecked. HTTP answers this with an `ETag`, an `If-Match` and 412;
`core/errors` had no class that maps to 412.

## Decision

Every admin route whose write revises a product takes an `If-Match` naming one
quoted version, and the write is refused with 412 `product_version_mismatch`
when the product is at another version once its row lock is taken; a successful
write, a read and a creation answer the version as the `ETag`. The panel's edit
form carries the version it was read at, and a refused save comes back with what
was typed and the version now stored.

## Consequences

`core/errors` gains `KindPreconditionFailed`, answered as 412 with its message,
and `core/http` gains `HeaderOnSuccess`, a writer that sets a header only on a
2xx answer, since a module may not keep the response writer in a value.
Without the header, or with `*`, a write asks nothing and behaves as before, so
the check is the client's to opt into; the panel always opts in. A weak tag, a
list or a bare number is refused with 422 before anything is written.

The version is the product's. A variant, option, image or attribute write is
asked on it and answers it, so a client editing several parts carries the ETag
from one write to the next; a write that changed nothing answers the version it
found. The comparison is made under the row lock that orders the writers, so the
second of two writes asked on one version is refused rather than overwriting
the first. The schedule, relations, channels, price and stock links and a
deletion are not revisions and read no `If-Match`.

A panel save with no readable version is refused with 400; one refused as stale
shows the form again at the stored version, and saving it again writes the
operator's values over the other save knowingly. An arch gate derives the routes
from the service methods that reach `revise` and holds each to the router group
that reads the header. ADR 0013's concurrent-edit limit and ADR 0221's unchecked
version are closed by this record.

## Rejected

- **409 for a stale version.** A failed `If-Match` is 412 in HTTP, and 409
  already answers a handle taken.
- **The version in the body.** Every body would carry a field that is not the
  resource's, and a DELETE has none.
- **Requiring the header (428).** Every client and script that writes today would
  stop.
- **`updated_at` as the precondition.** A timestamp ties at its resolution and
  moves on a write that changed nothing.
