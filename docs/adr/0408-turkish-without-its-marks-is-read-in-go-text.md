# ADR 0408 — Turkish without its marks is read in Go text

**Summary:** The language gate reads a Go comment or string literal for the stems it reads in names, and five more suffixed words.
It costs an exemption with a reason for every Turkish word kept as data, and it still reads no Markdown or SQL text for stems.

- **Status:** Accepted
- **Date:** 2026-10-06
- **Amends:** [0012](0012-repository-language-and-solid.md), whose ratchet read prose for ten words only

Measurement: [measurements/0408](../measurements/0408-turkish-without-its-marks.md)

## Context

ADR 0012's gate reads Turkish letters in every file, ten function words in
prose, and 187 stems in Go names. Turkish typed without its marks in a Go
comment or string literal passed every lane unless it used one of the ten
words. The ledger emptied on 2026-10-03, so every file was taken to be English,
and on 2026-10-06 the manual fulfillment provider still answered API callers
with a Turkish message while 24 Go files kept Turkish test data and comments
(D263).

## Decision

A fourth lane reads every Go comment and string literal, as written with its
escapes blanked, for the stems the identifier lane reads, whole word by word and
part by part, skipping parts of two letters and the file names the path ledger
lists. The word lane takes five suffixed words, each absent from the standard
library, and both prose lanes subtract a file's exempt text as the letter lane
does.

## Consequences

- The provider's message is "the reference is required"; its code is unchanged.
  Promotion codes, b2b addresses, inventory, search and guard fixtures are
  English, each test asserting what it did.
- Turkish kept as data needs an entry in `diacriticDataExemptions` with its
  reason: the rig's titles, which reproduce a measured catalog; PayTR's API
  paths; the incident and the records the reference audit quotes; one slug
  test's expected handle. The map keeps its name because ADR 0009 names it.
- An escaped literal stays outside the lane, as the letter lane's policy has
  it, so a case-folding test spells its Turkish input in escapes.
- The stem list is a floor: a suffixed form outside it, or a Turkish word that
  is no stem (a warehouse name in three tests was one), still passes, and the five new words were
  added because they were found, not because the list is whole.
- Markdown, SQL and templates are read for letters and the word list only. A
  historical record's file name and a dated measurement's search term would
  each need an exemption there.

## Rejected

- Matching stems as prefixes with a suffix list: "para" and "parade", "ara" and "arable", and a list that grows by false positive.
- Reading Markdown and SQL for stems: dozens of exemptions for links to records whose file names stay Turkish by rule.
- Translating the rig's titles: its package records that a rebuilt rig must diff clean and that one figure was measured against one title.
- Cleaning the tree without a lane: the same blind spot let this debt in after the ledger was declared empty.
