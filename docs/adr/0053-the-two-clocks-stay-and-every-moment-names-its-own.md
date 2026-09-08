# ADR 0053 — The two clocks stay, and every moment names the one that stamped it

**Summary:** gobit does not put its timestamps on one clock. Six columns are
stamped by the process and the rest by the database, and each moment a reader
sees says which — because unifying them would make two of the six less truthful,
not more.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

Six columns are stamped by whichever process wrote the row: `payments.captured_at`,
the four `fulfillments` transition stamps, and `invoices.issued_at`. Everything
on the order itself comes from the database's `now()`. On one machine they agree;
across machines a capture can be printed before the order it paid for.

The gap ledger carried this as an open decision — move them to the database
clock, and lose the injectable clock the tests use. Measurement removed the cost
it named (two tests) and then removed the decision itself, in the other
direction: for two of the six, the database clock is WORSE.

`now()` is transaction START. The capture's transaction wraps the provider call,
so a database stamp would record when gobit began trying rather than when the
processor took the money — and that column is a reconciliation input, read as
`min(captured_at)`. The invoice's single `now` feeds both the series year and
the stamp, so splitting them would let a document be numbered 2027 and dated
2026.

Measurement: [measurements/0053](../measurements/0053-two-clocks.md).

## Decision

**The two clocks stay, and every moment gobit reports names the clock that
stamped it.** The order timeline already does this; `returned_at` is published
so the fourth parcel stamp can be named too.

## Consequences

- **A reader can see why two lines look out of order**, which a single sorted
  list would present as a fact rather than a guess.
- **The skew is not removed.** Across machines the moments can still disagree by
  more than the gap between two events. This decision makes it legible, not
  absent, and an operator wanting them close has to keep the clocks close.
- **`returned_at` reaches the cross-module read layer**, so a parcel that came
  back is on the timeline. It was on the column, the model and the admin body,
  and missing from the one map that decides what another module may read — an
  omission nothing could report, because that map REFUSES an unknown field
  rather than answering zero (ADR 0004).
- **The fulfillment module keeps its injectable clock.** Two tests depend on it,
  and the stamps are a stamp SELECTION rather than a time read: which column is
  set is decided with the status, in one statement, under CHECK constraints that
  pair them.
- **Nothing gains a `DEFAULT now()`.** A column the database supplies leaves the
  column audit's scope, which this repository has argued against twice in
  migrations and once in the gate's own message.

## Rejected

- **Move all six to the database clock.** It makes the capture and the invoice
  less truthful, which is the opposite of the row's intent.
- **Move only the fulfillment stamps.** It splits one module's four moments from
  the two beside them for no gain, and costs the fake store's mirror of four
  schema CHECKs — the only place they hold without a database.
- **Add `DEFAULT now()` and drop the column from the INSERT.** Argued against in
  `order` migration 000009 and in `invoice` migration 000003, which adds a
  default and then drops it for this reason.
- **Hide the skew behind one sorted list.** It presents a guess as a fact, and
  the guess is wrong exactly when it matters.
