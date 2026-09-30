# ADR 0279 — The panel opens a return and a replacement with their detail

**Summary:** The order page's return form takes each line's part of the refund
beside its quantity, and its replacement form takes one variant the order never
sold with its quantity beside the order's lines.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

Since ADR 0272 the order page opens every kind of after-sales record, and the
known limits named what its forms still left to the API: a return line's own
part of the refund, a replacement that sends another variant than the line
sold, and a claim's evidence. The service takes the first two: a return
line's refund since return lines existed, a replacement's foreign variant since
ADR 0145. A replacement item names either an order
line or a variant, never both: a variant the order never sold is bounded by the
exchange's money guard, not by what a line bought.

## Decision

The return form carries a refund box beside each line's quantity and hands each
named line's refund to `order.admin` with it. The replacement form keeps a
quantity box per order line and adds one pair of a variant id and its units,
which `order.admin` turns into an item of its own naming no line.

## Consequences

- An operator splits a return's refund across its lines from the page, and a
  line left without a figure keeps none, as through the API.
- An operator sends a different size or colour from the page by its variant id;
  the page does not look the variant up, since an order screen reads only the
  order module (ADR 0260), and the service reads the catalog for its parts.
- A replacement of two or more foreign variants is still an API call.
- The panel surface's two openers take the new values as parallel lists, and a
  list out of step with its lines is refused before the service is asked.
- A refund that is not an amount is refused naming its line; a variant with no
  units, or units with no variant, is refused.

## Rejected

- A variant box beside each order line: it would say the variant replaces that
  line, which the record cannot keep; the item names the variant alone.
- A variant picker listing the product's other variants: the order screen would
  read the catalog, which its privilege does not own.
- Evidence uploads in the same change: a file form is its own surface, with the
  file module behind it.
