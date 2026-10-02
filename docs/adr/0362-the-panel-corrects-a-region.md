# ADR 0362 — The panel corrects a region

**Summary:** Each row of the Regions screen corrects its region's name,
whether taxes are computed for it and its tax rate through `region.admin`
under `region:write`, from the ones the row was drawn with, refused with
`region_revised` otherwise.

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

ADR 0354 listed the regions and left writing them to the admin API until
the region module's files were translated, which they now are. A tax rate
mistyped in a region is charged on every order sold in it, and the API's
update writes the fields it is sent over whatever the region has become.

## Decision

The region module corrects a region's name, automatic taxes and tax rate
together, in one UPDATE that matches the ones the caller read on a live
region, and refuses with `region_revised` otherwise. The panel's Regions
screen offers each row that correction, the rate typed as a percent, to an
operator who may write the regions.

## Consequences

- An operator corrects a rate where they read it, and two operators
  correcting one region at once write once; the second gets back what they
  typed in that row alone.
- The name and rate are checked as the API's update checks them: a blank
  name, or a rate outside zero to a hundred percent, is refused.
- The currency is not corrected here; changing it stays the API's.
- The countries a region covers, and making or deleting a region, stay the
  API's.
- `region.admin` is the region module's first panel surface; only the
  correction crosses it, its terms as JSON under the provider's field
  names (ADR 0001).

## Rejected

- Writing through the API's partial update: it writes over a correction
  made meanwhile.
- Matching on the moment the region was last written: a currency changed
  through the API, which this correction leaves as it is, would refuse it.
