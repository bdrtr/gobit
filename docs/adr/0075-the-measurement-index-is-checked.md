# ADR 0075 — The measurements index is checked, and the argument for leaving it alone was the argument for gating it

**Summary:** Every row of `docs/measurements/README.md` must state the report's
real length, and every report must have a row.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0070 built a gate over the counts the prose states and named one number it
did NOT gate, handing it to the owner instead: the per-file `Lines` column of
the measurements index, wrong in nineteen rows. Its reason was that the column
"prices a document's LENGTH rather than a population, so it goes stale on the
same commit that corrects any report".

Every word of that is true. On re-reading, it is an argument FOR a gate rather
than against one: a number that goes stale on the next commit is exactly the
number that needs something to notice. What the sentence actually establishes is
that the column does not belong in the count vocabulary — that gate is built on
populations the tree sizes for itself, and a file's length is not one — which is
a fact about that gate's shape and not about whether this number should be true.

Re-measured 2026-09-09: eighteen of thirty-four rows were wrong. Most by exactly
four lines, the header block added when the reports moved out of `docs/gaps.md`
and never carried back; one by 189.

## Decision

**The `Lines` column must equal the report's length**, checked by
`TestTheMeasurementIndexLineCountsAreTrue`. All eighteen rows were corrected in
the same change.

**Every report must have a row**, checked by
`TestTheMeasurementIndexNamesEveryReport`. This is the direction that matters
more: a wrong number is a small lie, and evidence nobody indexed is evidence
that exists and cannot be found by somebody who does not already know its name.

**The reader has a positive control of its own.** Both checks rest on one
regular expression, and a reader matching nothing would report a perfect index —
no row wrong, and every report present in a set that was never narrowed.

## Consequences

- **A report and its row move together.** That is the whole cost: one line of
  the index in the commit that grew the file. The alternative measured out at
  eighteen wrong rows in a document whose only job is to tell a reader what they
  are about to open.
- **ADR 0070's Rejected section is now half wrong, and it is not edited.** The
  records are the historical record and are amended by adding; this record is
  the amendment. What that section got right — that the column is not a
  population and does not belong in the count vocabulary — still holds, and this
  gate is a separate one for that reason.
- **The line count is `wc -l`**: the number of newlines, so a file whose last
  line has no terminator counts one fewer. It is the answer anybody checking by
  hand would get, and it is written down because the alternative definition is
  equally defensible and would differ by one.
- **Nothing checks that the DESCRIPTION beside a row is true.** It is prose about
  the report, no gate reads it, and this record does not pretend otherwise.

## Rejected

- **Dropping the column.** It exists for a real reason — a reader deciding
  whether to open a 210-line report or a 40-line one — and the measurements are
  deliberately unbounded where the records are capped at eighty lines, so the
  length is the one thing the index can say that the title cannot.
- **Adding it to the count vocabulary of ADR 0070.** The vocabulary is built on
  populations the tree sizes for itself and admits a sentence by the path it
  names. A file's length is neither, and forcing it in would widen the anchor
  rule for one case.
- **Generating the index.** A generated index would carry a generated
  description, and the description is the part a person writes.
