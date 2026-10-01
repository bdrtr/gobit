# ADR 0280 — A price and a count name what they were drawn with

**Summary:** The panel's price and stock forms send the amount and the physical
count they were drawn with, and the write is refused when the price or the
level holds another value by the time it takes the lock.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The known limits said concurrent editing of a price or a stock level is
last-writer-wins: the forms carried no version and nothing checked one under
them. For stock it is worse than two operators: the form writes an absolute
physical count, so a count typed against ten and saved after a sale took one
unit writes the unit back. Reading the price write for this limit found D193,
where two writes to different currencies of one set lost one of them; that is
fixed underneath, and the same currency twice remained. The product form has
carried its version since ADR 0222. Neither a price nor a level has a version
column, and both writes already read the row under its lock.

## Decision

The price form sends the amount at one unit it showed and the stock form the
physical count it showed, as hidden fields, and the panel surfaces of pricing
and inventory compare them with the price or the level under the lock and
refuse a mismatch with `pricing_price_moved` or `inventory_stock_moved`. A
location with no level holds zero, and the value itself is the version.

## Consequences

- An operator whose save would overwrite a change they did not see is shown
  the page again, with the refusal naming the value as it is now.
- A count over a level that moved by a sale is refused, so a typed count can no
  longer write back a unit that left.
- A value that moved away and back between the read and the save passes; the
  write then sets what the operator meant over what they saw.
- A form without the value it was drawn with is refused before the module is
  asked.
- The panel surface no longer adds a base price in a currency the set has none
  in: the form never draws one. A catalog import still does, through
  `SetUnitBasePrices`.
- The admin API's price and level writes are unchanged.

## Rejected

- A version column on the price set and the level: a second number to keep
  true beside the value that already says whether the row moved.
- Writing the difference instead of the count: the operator counts what is
  on the shelf, and the form is the count.
- An `If-Match` on the admin API in the same change: its callers are
  integrations with their own retries, and the forms are the case named.
