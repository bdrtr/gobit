# ADR 0205 — A catalog import is a record a job works through

**Summary:** `POST /admin/v1/products/imports` takes a CSV file in the export's
columns and keeps it, and a job applies its rows a minute at a time, updating
and creating products and variants, while `GET` reports what the rows did. It
costs a table holding the file until the import ends; prices follow in 0206.

- **Status:** Accepted
- **Date:** 2026-09-27
- **Amended by:** [0207](0207-an-import-writes-its-prices-through-pricing.md) for the price columns, which are now applied; the record this one calls 0206 is 0207, because [0206](0206-a-base-price-is-changed-at-one-unit.md) first corrected the write they use

Measurement: [measurements/0205](../measurements/0205-a-file-worked-through.md)

## Context

ADR 0204 sent the catalog out as CSV. Bringing it back needs what nothing had:
the job system runs scheduled jobs with no payload, the file module takes
images by default and gives other modules an upload's record rather than its
bytes, and a request applying tens of thousands of rows would outlive the
server's write timeout. The user chose an import that is kept and worked
through, and that updates as well as creates.

## Decision

The product module keeps an import — the file, its row count and what its rows
did — and a job applies its rows in order, recording each as it goes. A row
finds its product by id or handle and its variant by id, SKU or options,
writes its non-empty cells that differ, and creates what it does not find.

## Consequences

The file is the request's body, sent as text/csv, up to 32 MiB; the export of
52,004 products is 6.8 MB. A file that is not UTF-8 or not CSV, has a row of
another width, or names a column the export does not write or no way to a
product, is refused whole with 422. Any subset of the export's columns works.

A run applies rows for 40 seconds of its 45-second bound, and the next run
continues from the row it reached. A row is recorded as it is applied, and a
row recorded twice counts once. A run stopped between applying a row and
recording it applies it again and finds what it made, which is why a new
product needs a handle and a new variant a SKU or options. A row is not one
transaction: a product's change stands when its variant is refused, and the
refusal names the row by its line.

An empty cell leaves the value as it is, so an import cannot clear a field. The
apostrophe the export put before a formula-like cell is taken off. A row that
matches what is there counts as neither created nor updated; the first thousand
refused rows keep their reason. `is_giftcard` is read on creation only. The file
is dropped when the import ends.

`Idempotency-Key` buffers a body up to 1 MiB, as everywhere, and refuses a
larger one with 422 `body_too_large`. A larger file is sent without the key, and
sent twice it is two imports, the second finding what the first made.

The price columns are read and not applied: pricing owns the price, and a write
across the two modules is a flow's, which 0206 adds.

## Rejected

- **The file module.** It takes images by default, serves its files at a public
  URL, and gives other modules an upload's record rather than its bytes.
- **A general job queue with payloads.** The import is its only consumer; a
  record the job reads is how the scheduled publisher already works.
- **One transaction per row, with its progress.** The service's writes open
  their own transactions; matching by natural keys makes a replayed row safe
  without rewriting all of them.
