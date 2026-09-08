# ADR 0070 — A count is audited against a closed vocabulary, and a universal negation is not audited at all

**Summary:** A number the prose states for a population in a closed vocabulary
must equal the number the tree computes; a universal negation gets no gate,
because its object is a predicate no vocabulary can close.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

`docs/gaps.md` D29 left three classes of claim without a standing check —
counts, universal negations, cross-references — and a line saying the route gate
reads 393 of 491 addresses.

Cross-references have a gate per reference kind the prose writes — twelve, six
with a blindness control; the one kind none covers is a `table.column` in a
document, resolved against the schema by nothing. The 393 of 491 is the route
gate's own dated-record exclusion, price included, and both numbers had gone
stale — the count class biting the sentence reporting it. Counts bite hardest:
on one day, every suite green, five count claims here were false. "Every number
in every document" is no population: "a number and a plural noun" occurs 185
times for modules, almost every one a subset.

Measurement: [measurements/0070](../measurements/0070-count-claims.md).

## Decision

**A count claim is a claim about a population in a CLOSED VOCABULARY.** Eight,
each sized by the tree from a source that is not the sentence: the modules, the
packages under `core/`, the plugins, the workflows, the ADR records, the
measurement reports, and the entries and groups of the limits document.

**A sentence enters by naming the population BY ITS PATH on the same line.**
That is the shape a total is written in and a subset is not; it takes the
modules from 185 occurrences to two, both totals. A single-segment anchor holds
only in a README's layout column, since `core` and `plugins` are ordinary words.

**The number is read in both languages and every form** — digits, English words
including hyphenated compounds, Turkish words including spaced compounds. What
cannot be composed is left alone: the Turkish ten, which is also the English
preposition, and anything in front of a scale word. Silence, not invention.

**Every ADR record is out of scope, at any number, and so is the changelog.**
Wider than the route gate's frozen line on purpose, and for one reason: a route
address in a new record should still be true tomorrow, while a count is stale
the day the tree grows — and `CLAUDE.md` forbids editing a dated record.

**The universal negation gets no gate.** Its object is a predicate — "nothing
depends on `lower()` folding non-ASCII", "nothing emits `Vary`" — each needing a
different oracle, so no population exists. **The trigger is a negation about a
population an existing gate enumerates**: tables, columns, routes, modules,
plugins, migrations. One already qualifies — measurement 0066's "no table named
for a suggestion exists in any migration" — and the repair is an assertion in the
gate owning that population, never a scanner over the prose.

## Consequences

- **Six false counts, found the day the gate shipped**, all in the two READMEs:
  the published packages, the decision records and the limits entries, each
  wrong in both languages — and the two files disagreed with each other too.
- **Two more were repaired by hand**, in the route gate's own godoc; no path
  names the route population, which is this gate's largest named blind spot.
- **The exclusion's price was measured, not assumed.** With it off the ADR
  records state no count in the audited shape at all, and the changelog states
  exactly ONE: the entry announcing `docs/measurements/`, pricing at fifteen the
  reports moved out of `gaps.md` that day against thirty-two now. It is
  HISTORICAL — the argument for the exclusion, not a cost of it.

## Rejected

- **Every number in every document.** Noise, and its exemption list would carry
  its own false positives — the shape D16 records.
- **An exemption list for counts.** It would say a document may state a number
  the tree contradicts, and none may.
- **The route gate's frozen line, reused.** A count goes stale where an address
  does not, and a dated record may not be edited to keep one true.
- **The measurements index's per-file Lines column**, wrong in 19 rows: it
  prices LENGTH, not a population. Reported to the owner instead.
- **A universal-negation gate over a lexicon.** 91 sentences, none settled.
