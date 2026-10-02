# ADR 0336 — The panel writes the store profile

**Summary:** A Store profile screen shows who the shop is under
`settings:read` and writes it under `settings:write` through a new
`settings.admin` surface, from the moment the profile the page was drawn
with was last written.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

Every invoice is issued under the shop's store profile, and issuing before
it is written is refused (ADR 0115). The panel issues an order's invoice
(ADR 0335) but could not write the profile, so a new shop's first invoice
sent the operator to the admin API, whose write replaces the profile with
whatever it is sent.

## Decision

The settings module writes the profile only while it is still the one the
caller read, named by the moment it was last written, or, read as never
written, while there still is none, refusing with
`settings_store_profile_revised` otherwise. The Store profile screen shows
the profile and offers the form drawn from it, carrying that moment.

## Consequences

- A new shop says who it is where it issues its invoices.
- Two operators writing the profile at once write it once; the second is
  told to draw the page again and gets back what they typed.
- The form writes every field of the profile, so the moment it was last
  written is exactly what the operator read; no field-by-field comparison
  is needed, unlike a form that writes only some of a record's fields.
- The profile travels as JSON, its printed fields named rather than placed.
- The admin API's replacing write stays as it is.

## Rejected

- Comparing every field read: the moment says the same thing about a
  record the form writes whole, in one column.
- Writing through the API's replacing write: a profile written in between
  would be lost without a word.
