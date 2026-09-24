# ADR 0167 — A price keeps its history

**Summary:** Every write to a price, a rule, a set or a list appends a snapshot
of what the price ladder reads, in the same transaction, and the ladder is run
over those snapshots to say what a set charged at any past moment. A storefront
sale price carries its list's type, when the reduction began, and the lowest
price that applied in the thirty days before it.

- **Status:** Accepted
- **Date:** 2026-09-24

Measurement: [measurements/0167](../measurements/0167-what-a-price-was.md)

## Context

ADR 0047 deletes a replaced price and keeps nothing, because the rows the old
code left behind had no reader; it leaves a price history to whoever needs one.
A shop that announces a reduction needs one: the reference it has to show is
the lowest price it applied in the thirty days before the reduction began. The
catalog could not say it — a replaced amount was gone, a list's status and
window were overwritten in place, and a sale price reached the storefront as an
unlabeled second amount beside the price it reduced.

## Decision

The pricing module appends a snapshot of a set's live prices and rules, or of a
list's type, status and window, in the transaction of every write that changes
them, and never updates or deletes one. The price a set charged at a past
moment is the unchanged ladder run over the snapshots that stood then, at
quantity one and with no rule context.

## Consequences

What is kept is the ladder's input rather than its answer, because the answer
changes with no write: a sale window opens at its starts_at whether anybody
touches the catalog or not. The moments a price can change are the writes and
the window edges, and the timeline is evaluated at exactly those.

The history starts at the migration, which records every live set and list
once. Nothing before it is known, so a window that reaches further back is
answered from that moment and says it is not covered; a reduction that began
before it has no stated start, and a reference whose thirty days the history
does not hold is absent rather than approximated.

A sale price at the storefront names its list's type, and the one charged now
carries `reduced_since` and `lowest_prior_amount`, on the product listing and
the price set endpoint alike, from one code path. A reduction is a run of sale
prices; an override is a different price, not a reduction. A reference is not
announced when the history's last answer is not the price charged now.

An admin endpoint answers what a set charged over a window and which price and
list each stretch came from. ADR 0047 is amended: a replaced price is still
deleted from `price`, and what it was is kept in the snapshot before.

## Rejected

- **Storing the ladder's answer at each write.** A window opens without a write.
- **Keeping replaced rows, as before ADR 0047.** List headers change in place too.
- **Triggers writing the history.** Procedural code in the schema; a gate holds it.
- **A snapshot only of rule-less prices.** The admin timeline would lose the rest.
- **A reference over the covered part of the thirty days.** It is not the rule's.
- **A setting for the thirty days.** It is a rule a shop has to prove.
