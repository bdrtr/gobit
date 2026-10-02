# ADR 0335 — The order page issues the order's invoice

**Summary:** An order's page names its invoice under `order:read` and issues
one under `order:write` through the invoicing flow the API calls, on a series
the invoice module lists for an operator holding `invoice:read`, or on a new
series named apart.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The admin API issues an order's invoice through the invoicing flow and
names the one it has (ADR 0115, ADR 0193), and the panel did neither, so the
operator who shipped an order left the panel to bill it. A series is opened
by the first invoice numbered on it, so a prefix typed with a slip opens a
series nobody meant.

## Decision

The order module's surface names the order's document and issues one through
the invoicing flow, and the invoice module registers a surface listing the
numbering series. The order's page offers the series the shop has to an
operator who may read the invoices and takes a new series in a field of its
own, the buyer's fields defaulting to the order's billing address.

## Consequences

- An order is billed where it is read, and the page names the invoice with
  its number and status once it is issued.
- Issuing twice issues once: the flow returns the order's invoice, and the
  page says nothing new was issued.
- A series is chosen from the ones the shop numbers on, so a new one is
  opened only by being named as new.
- An operator who may write orders but not read the invoices names the
  series as the API's caller does.
- The buyer travels as the flow's JSON party, its printed fields named
  rather than placed, so a name cannot land in the address.

## Rejected

- One free field for the series: the slip it invites opens a series, and a
  number once taken is spent for good.
- Six string arguments for the buyer: the flow's own surface refuses that
  shape for the reason a caller gets it wrong silently.
