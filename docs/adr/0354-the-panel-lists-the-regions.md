# ADR 0354 — The panel lists the regions

**Summary:** A Regions screen lists the region module's regions through its
region entity under `region:read`, each with its currency, its tax rate as
a percent, whether taxes are computed for it and the countries it covers;
nothing is written there.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

The panel reads a region's currency for every amount it prints and offers
the regions on the shipping option form (ADR 0334), but which regions the
shop sells in, at what tax rate and to which countries, was read only
through the admin API. An operator asked why a price was taxed as it was
had no screen to look at.

## Decision

The panel's Regions section reads the regions a page at a time through the
region entity the region module already publishes, and shows them to an
operator who may read the regions.

## Consequences

- No module changes: the entity publishes every field the screen prints,
  the countries and the currency read with the regions in one call.
- The tax rate shown is the region's own; a rate the tax module sets for a
  country inside it is not on this screen.
- Writing a region stays the admin API's: the region module's files are
  in the Turkish ledger, and a write surface waits for them to be
  translated.

## Rejected

- A surface method listing the regions: the region entity already pages
  them, and the panel reads every module's data through the read layer
  where a provider exists (ADR 0011).
