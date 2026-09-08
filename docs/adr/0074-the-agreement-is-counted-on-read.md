# ADR 0074 — The agreement between a model and the operators is counted on read, and carries no rate

**Summary:** One admin read counts, per model, how many decided reviews carry a
proposal and how many of those proposals name the status the review ended in.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0072 claims no accuracy for the model and says exactly why: measuring one
needs a set of reviews an operator has already decided about, and gobit cannot
invent it. ADR 0073 then decided that a proposal is not cleared when the
decision is made — so the set accumulates on its own, in the table, on every
installation running the job.

It was unreadable. Nothing in the tree could say whether the model a shop is
paying for agrees with the people it is meant to be helping, which left the
whole feature unaccountable: an operator could see one proposal at a time and
never the record.

Measurement: [measurements/0074](../measurements/0074-suggestion-agreement.md).

## Decision

**`GET /admin/v1/reviews/suggestion-agreement` returns one row per model**: how
many moderated reviews carry a proposal from it, and how many of those name the
status the review ended in.

**It carries TWO COUNTS and no rate.** A percentage is what a reader wants and it
is the one thing this refuses to compute. "67%" over three decided reviews reads
exactly like "67%" over three thousand, and gobit is not the party that should be
asserting how good somebody else's model is. The denominator is in the response,
so a client that wants a rate can compute one knowing what it rests on.

**It is grouped by MODEL**, because that is the cut a shop acts on: the question
is not "is the machine good" but "is the one I am paying for better than the one
I replaced", which a single number over every model that ever ran cannot answer.

**"Decided" is `moderated_at IS NOT NULL`**, not a list of statuses. That column
is what records the decision, and the mirror ties it to the status beside it, so
a status added later lands on the right side without anybody editing this query.

**It is computed on READ and nothing is stored.** Measured at 43 ms over 505,000
reviews — a parallel scan of the whole table — and left there.

## Consequences

- **A shop can now answer the question gobit refuses to answer for them.** The
  framework ships no threshold and no claim; the installation gets the counts
  and decides.
- **The cost is LINEAR in the table and it is written down.** At ten times this
  rig the report is most of a second. A shop that puts it on a dashboard
  refresh is the case that wants something stored, and nothing else is — the
  same sentence the review summary already carries.
- **A disagreement is not a mistake.** The operator decides; this counts how
  often the machine would have said the same thing. A low number means the
  model is not helping these people, which is the conclusion the data supports.
- **A proposal about a review still WAITING is not evidence** and is excluded.
  Counting it would put the model's own opinion in the denominator of its score.
- **The report is one more reason the proposal columns are never cleared**,
  which makes ADR 0073's decision load-bearing rather than incidental.

## Rejected

- **A percentage.** It reads as a measurement at any denominator, and the
  denominator is the whole question.
- **An index over the archive.** It would grow without bound to serve a report
  read occasionally — the shape ADR 0073 rejected where the argument was weaker.
- **A stored counter kept in the moderation transaction.** It would owe a
  correctness obligation on every write to buy 43 ms on a read nobody holds
  open, which is the trade ADR 0041 records against denormalizing a price.
- **Putting it on the queue's listing.** Two questions asked at different
  moments; the second would price a whole-table aggregate into the page an
  operator opens all day.
- **A per-product cut.** Nobody chooses a model per product, and the group would
  be a row per product for a question asked about the shop.
