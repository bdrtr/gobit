# ADR 0256 — An empty text on an update clears the field

**Summary:** On a product's, a variant's and a category's update, an optional
text left out is kept, one given empty clears the field to NULL, and any other
is trimmed and written.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

The catalog's updates write each field with `COALESCE`: a NULL parameter keeps
the column. The service turned an empty optional text into that NULL, so a
product's subtitle, description or thumbnail and a category's description
could not be emptied; the known limits said so. The same updates wrote a
product's material and origin country and a variant's SKU, barcode, EAN and UPC
as given, untrimmed and with an empty string as a value. A create trims all of
them and stores an empty one as NULL. The SKU index is unique among non-NULL
SKUs, so a second variant whose SKU was emptied was refused as a duplicate
(D177).

## Decision

On those three updates an optional text left out keeps the field, one that is
empty once trimmed clears it to NULL, and any other is trimmed and held to its
length. Each such column is written through a flag beside its value, so NULL
can be written and not only skipped.

## Consequences

- The field set is the product's subtitle, description, thumbnail, material
  and origin country, a variant's four codes, and a category's description:
  every optional text the three updates write.
- A client that sent an empty string meaning "no change" now clears the field.
  That was silently ignored before, or written as an empty string; the
  CHANGELOG says so for integrators.
- A create and an update now store the same thing for the same input, so an
  empty SKU is never a SKU and " ABC " never sits beside "ABC".
- Leaving a field out and sending it as JSON null both keep it; only an empty
  string clears.
- The other updates of the module (titles, handles, ids, numbers) keep the
  `COALESCE` rule: none of them may be empty, or an id names a record.
- The in-memory store the service tests run on applies the same rule, so the
  unit tests and the schema agree.

## Rejected

- A `clear_` flag per field, as `clear_parent` is: ten more request fields for
  what an empty string already says.
- Refusing an empty string with 422: it would close the silent loss without
  giving the operator the clearing they were asking for.
- Clearing on JSON null: the handlers decode a null and an absent field alike,
  and telling them apart needs a wrapper type on every request field.
