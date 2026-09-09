# ADR 0087 — The limit groups are NAMED in the prose, so the names are checked and not only the count

**Summary:** The README sentence that prices `docs/known-limits.md` also names
its groups; the names are now compared against the file's headings.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0070's count gate prices both READMEs' sentence about `docs/known-limits.md`
— so many ITEMS in so many GROUPS — and counts both from the file. Both numbers
are real and both are held.

The same sentence goes on to NAME the groups, and nothing read the names.

**The hole bit on 2026-09-09.** A group was added, the count went from four to
five, the gate turned green, and the sentence still listed four names. It was
caught by a person reading the diff. The count and the list are updated by two
different edits and only one of them had a gate.

## Decision

**The English README's names are the file's headings, lower-cased and IN
ORDER.** Order is part of the claim: a group inserted in the middle of the
document and appended to the end of the sentence describes a different document
than the one it prices.

**The Turkish README is a translation, so only the LENGTH of its list is
compared**, and that limit is written into the gate rather than left for a
reader to discover. The count is what the defect took the shape of; a
mistranslated name is a different fault and this does not catch it.

## Consequences

- **Seven mutations, all fired**: the historical defect itself (the count right,
  one name missing), two names swapped, a heading renamed in the file, a name
  dropped from the Turkish list, a group added with neither README touched, a
  blinded heading reader, and the README row deleted outright.
- **The row is found by the document's PATH**, not by a line number or by the
  sentence's wording, so a rephrased sentence still prices the same file.
- **It does NOT hold the thing that prompted it.** The defect a reader spotted
  was a tax limit filed under the category tree heading — the names were right,
  the count was right, and the entry was in the wrong group. Whether an entry
  belongs under a heading is a judgment about meaning, not a property of the
  text, and no gate here reaches it. What this closes is the ADJACENT hole the
  same afternoon had already opened once.
- **A second unchecked half is now named rather than silent.** The Turkish
  names being uncompared is written in the failure message, which is how the
  next person gets to decide whether to close it.

## Rejected

- **A written English-to-Turkish map of the group names.** It would compare both
  READMEs properly, and it would put Turkish text inside an English gate file
  that ADR 0012's ratchet reads — solvable with escapes, at the price of a map
  nobody can read. It would also rot: a renamed group needs three edits instead
  of two.
- **Comparing counts only.** That is what already existed, and it is what let
  the names drift.
- **Gating which group an entry belongs to.** It is the defect that started
  this and it cannot be written as a rule over the text.
