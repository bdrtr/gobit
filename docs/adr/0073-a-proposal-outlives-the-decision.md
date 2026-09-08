# ADR 0073 — A proposal outlives the decision, and the queue's filter is indexed over the QUEUE

**Summary:** A model's proposal is not cleared when an operator decides, and the
listing that narrows the queue by it is served by an index bounded by the queue.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0071 stored a proposal beside a waiting review and ADR 0072 filled it. An
operator could then READ one at a time and nothing else: the queue could not be
narrowed by what the model said, so triaging with it meant paging everything.

Two questions were unanswered because nothing depended on them yet: what
happens to the proposal when the operator decides, and what narrowing the queue
by it costs.

Measurement: [measurements/0073](../measurements/0073-review-suggestion-filter.md).

## Decision

**A proposal is not cleared when a review is moderated.** Nothing did clear it,
and that stops being an accident here: a proposal beside a human decision is the
only corpus from which anybody could measure whether the model agrees with the
people. ADR 0072 claims no accuracy because measuring one needs exactly that
corpus; this is where it accumulates.

**The admin listing takes `?suggested=`**, whose value is a proposed status or
`none` for the reviews no model has been asked about. A value no proposal can
carry is REFUSED rather than answered with an empty page — the rule the status
filter already follows, because an empty page reads as "the model has flagged
nothing" and an operator acting on that leaves the flagged reviews where they
are.

**The word `none` lives in the API and nowhere below it.** The service takes a
value and a flag, so the SQL needs no sentinel that a status could collide with.

**The filter is served by `reviews_suggestion_idx`, partial on the QUEUE:**
`(suggested_status, created_at DESC, id DESC) WHERE status = 'submitted' AND
suggested_status IS NOT NULL`. Measured on 505,000 reviews: the page goes from
7.7 ms to 0.03 ms and the count from 14.5 ms to 1.0 ms, for 1,480 kB.

## Consequences

- **The rig grew a review family first**, because the module's existing timing
  sentences rest on a database that no longer exists. `gobit seed -reviews N`
  builds one, and this is the module's first decision measured on something
  anybody can rebuild.
- **A first measurement recommended the WRONG index, and the reason is worth
  more than the number.** On a rig where only the queue carried proposals, an
  index over every proposed row looked identical — same size, same page cost —
  and served one query this one does not. Modelling the archive the decision
  above creates moved it to 10,216 kB on the same rows and made the operator's
  count fifteen times slower. The measurement was taken on the shape an
  installation has in its first month, and it recommended the index that gets
  worse every month after.
- **Two queries get SLOWER as the job succeeds, and both are accepted.** Asking
  for the reviews with no proposal costs 0.03 ms with a backlog and 11.4 ms once
  the job has caught up: there is nothing left to find and the scan walks the
  queue. The job's own read is 7.7 ms and 12.1 ms the same way. An index would
  fix both and be empty exactly when the system is healthy.
- **One question is unserved on purpose**: the same filter with no status,
  which asks about the archive. 38 ms, measured and written down.
- **Nothing about the write path was measured**, and no claim is made about it.

## Rejected

- **Clearing the proposal on moderation.** It would keep the table smaller and
  destroy the only record of what the model said about a review a person then
  decided — the corpus ADR 0072 says an evaluation needs.
- **An index over every proposed row.** Measured: seven times larger on the same
  rows, growing with the archive, and slower on the query an operator actually
  runs.
- **A four-column index on `(status, suggested_status, …)`.** 33 MB against
  1,480 kB for the same page cost, because it indexes the archive too.
- **A sentinel value in the SQL.** The word would live in two places and have to
  be one no status could ever be — a promise about a CHECK in another file.
- **Requiring a status filter beside `?suggested=`.** It would make the cheap
  question awkward and the expensive one impossible to ask at all.
