# ADR 0343 — The panel lists the invoices

**Summary:** An Invoices screen lists the invoice module's documents under
`invoice:read` through `invoice.admin`, the latest first, in one status or
in all of them, each with its number, kind, buyer, total, status and why,
and when it was issued.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The order page issues an order's invoice and names it (ADR 0335), but the
documents themselves were reachable only through the admin API: an
accountant asked which invoices were rejected, or what was issued this
week, had no screen to look at.

## Decision

The invoice module's panel surface lists a page of its documents in a
status, or in every status, with how many there are, through the listing
the admin API pages with. The panel's Invoices section shows them to an
operator who may read the invoices, a tab per status, every status first.

## Consequences

- The documents are read where the shop's identity they are issued under is
  written (ADR 0336), the section sitting beside it.
- A rejected or canceled document says why on its row.
- The total is printed in its currency's decimals, or marked as minor units
  when the shop has no region in that currency.
- The buyer's name crosses the surface; an operator who may read the
  invoices reads it through the API already.
- A document is not yet opened from its row, and its status is not moved
  here.

## Rejected

- A query-layer entity for the documents: nothing else reads them, and the
  module's listing already pages them in the order the screen wants.
- Paging by the listing's cursor: the panel's other lists page by number,
  and a page number survives in the address an operator shares.
- Opening on the issued documents: an accountant's first question is about
  every document, and the tabs are one click away.
