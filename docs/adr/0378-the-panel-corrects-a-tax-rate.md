# ADR 0378 — The panel corrects a tax rate

**Summary:** Each rate on the Taxes screen corrects its name and its rate
through `tax.admin` under `tax:write`, from the ones it was drawn with,
refused with `tax_rate_revised` otherwise.

- **Status:** Accepted
- **Date:** 2026-10-03

## Context

ADR 0355 listed the tax regions and their rates and left writing them to
the admin API until the tax module's files were translated, which they now
are. A rate mistyped there is charged on every line it covers, and the
API's update writes the fields it is sent over whatever the rate has become.

## Decision

The tax module corrects a rate's name and rate together, in one UPDATE that
matches the ones the caller read on a live rate, and refuses with
`tax_rate_revised` otherwise. The panel's Taxes screen offers each rate that
correction, the rate typed as a percent, to an operator who may write the
taxes.

## Consequences

- An operator corrects a rate where they read it, and two operators
  correcting one rate at once write once; the second gets back what they
  typed for that rate alone.
- The name and rate are checked as the API's update checks them: a blank
  name, or a rate outside zero to a hundred percent, is refused.
- The code, the default, the stack and the rules are not corrected here;
  they stay the API's, as making or deleting a rate or a tax region does.
- `tax.admin` is the tax module's first panel surface; only the correction
  crosses it, its terms as JSON under the provider's field names (ADR 0001).
- The screen addresses a rate by the id the tax region entity already
  published with it.

## Rejected

- Writing through the API's partial update: it writes over a correction
  made meanwhile.
- Correcting the default flag here: making a rate the default has rules of
  its own (no rules on it, one per region) that the API's update enforces
  under a lock.
