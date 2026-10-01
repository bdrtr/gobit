# ADR 0302 — The customer list finds a customer by e-mail

**Summary:** The panel's customer list takes an e-mail and lists the customer
records holding it, the account and the guest records alike, through the
customer provider's exact filter. Without one it pages every customer as
before.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The panel's customer list paged every record, newest first, and searched
nothing; an operator looking for a person who wrote in had to page until
they found them. ADR 0297 had read the customer provider's e-mail filter for
the telephone order, so the panel already used it under `customer:read`. The
filter matches an address exactly, as the customer module normalizes it, and
refuses one it cannot normalize.

## Decision

The customer list takes `email`, trimmed, and reads the records the provider's
e-mail filter returns, keeping the address in the box and in the paging. An
address the provider refuses is said on the list, and one no record holds is
said as well.

## Consequences

- An operator finds a person by the address they wrote from, in any case, and
  sees their account and their guest records together.
- A part of an address finds nothing; the search matches whole addresses.
- An invalid read with no search is still the screen's fault, answered as
  before.

## Rejected

- A partial or name search: the provider matches an address exactly, and a
  wider search would be the customer module's new query, not the panel's.
- Sending the operator from the search to a single record: an address can
  hold an account and several guest records, and the operator chooses.
