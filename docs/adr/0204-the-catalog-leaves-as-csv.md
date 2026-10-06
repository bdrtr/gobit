# ADR 0204 — The catalog leaves as CSV

**Summary:** `GET /admin/v1/products/export` streams the catalog as CSV, a row
per variant with its base price in a column per currency a region sells in, so
a shop can take its catalog to a spreadsheet or to another system. It is the
first half of B2.6; the columns it writes are the ones an import will read.

- **Status:** Accepted; amended by [0424](0424-the-catalogs-file-carries-its-costs.md), whose export also carries the costs
- **Date:** 2026-09-27

Measurement: [measurements/0204](../measurements/0204-fifty-thousand-rows.md)

## Context

B2.6 asks for a catalog import and export with row errors, partial success
and a job's status. Measured, the import needs parts that do not exist: the job
system runs only scheduled jobs with no payload, the file module refuses a CSV
upload and no module can read an uploaded file back. The export needs none of
them. The user chose the export as the first slice.

## Decision

The product module serves the export, reading its products by cursor with
their relations and each page's base prices from pricing through the read
layer, and streaming a page at a time. It takes both `product:read` and
`pricing:read`, because it carries prices.

## Consequences

A row is a variant, with its product's columns repeated; a product with none is
one row with the variant columns empty. Tag and category ids are joined with a
bar, and metadata and a variant's options are JSON. A text cell that starts
with `=`, `+`, `-` or `@` is prefixed with an apostrophe so a spreadsheet does
not run it, which the import will take off again.

The price is the variant's base price at one unit by ADR 0041's definition,
the one the catalog's price filter compares, in minor units. Its columns are
the regions' currencies: a price in a currency no region sells in is not one a
cart can charge, and it is not exported. The export reads a region's
`currency_code`, a field the catalog did not read before, and the audit of the
catalog's foreign fields lists it.

Nothing is sent until the currencies and the first page are read, so a failure
there is an ordinary error. A failure later drops the connection, so a partial
file cannot pass for a whole one. Each page moves the write deadline thirty
seconds forward, so the server's write timeout no longer bounds the whole file.

Reading by cursor, newest first, a product created during the export is left
out and none is written twice. A product changed during it is written as it
was when its page was read.

An installation without the region or the pricing module exports its
catalog without prices, as its storefront shows it without them.

## Rejected

- **A workflow reading everything through the read layer.** Its product
  provider pages by offset, newest first, so a product created during the
  export would shift every page after it and repeat a row.
- **The prices as one cell.** A spreadsheet user edits a column per currency;
  a cell of pairs would be parsed by hand.
- **Every currency any price is in.** The header is written before the rows,
  and the only list known in advance is the regions'.
