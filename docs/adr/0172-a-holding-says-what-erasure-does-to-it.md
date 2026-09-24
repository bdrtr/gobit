# ADR 0172 — A holding says what erasure does to it

**Summary:** Every declared place personal data is kept now says what a
successful erasure does to it, emptied or kept. What an erasure reports as
kept is read off that declaration instead of a list of its own. The
declaration endpoint publishes it, so a privacy notice can say which data goes
on request and which stays.

- **Status:** Accepted
- **Date:** 2026-09-25

Measurement: [measurements/0172](../measurements/0172-what-an-erasure-keeps.md)

## Context

The feature list's C5 row asks for a classification in the schema that
erasure derives from. Every holder already declared its columns
(`personaldata.Holding`, ADR 0029), and an erasure reports the columns it kept.
Those were two lists, written separately in six holders. One had drifted: the
link holder reported `from_id` as kept, and its declaration never named it
(D128). The order and cart modules had put both facts in one private row, a
holding plus an `erased` flag, which is the shape this record publishes.

## Decision

`personaldata.Holding` gains `OnErasure`, `Emptied` or `Kept`, and every holder
declares it for every column. A holder's report of what it kept is derived from
its declaration: the kept holdings after anonymizing, every holding after a
refusal.

## Consequences

The privacy notice and the erasure report come from one list and cannot
disagree about a column. `GET /admin/v1/personal-data` publishes `on_erasure`
on every holding.

A source gate requires every `Holding` literal in `internal`, `plugins` and
`contrib` to name its `OnErasure`. It also refuses `Emptied` in a unit that has
no `Erase` method, since nothing there could empty anything. An end-to-end
check runs the production sweep and requires every answer's kept list to name
only declared columns. An anonymizing answer that touched rows has to keep
exactly the declaration's kept holdings.

Whether the declared value matches the SQL is still proven per module. The
order and customer integration tests re-read the emptied columns, and the cart
test reads its statements. A holding flipped against its SQL fails there.

The invoice keeps its own kept list. It refuses erasure and declares the
seller's columns as well as the buyer's, and a refusal about a buyer does not
report the shop's own name as kept about them.

The label says what an erasure does, not for how long anything is kept. A
retention period is the controller's to set (ADR 0029). gobit has no such
figure to declare, and so it declares none.

## Rejected

- **Generating each holder's erasure from the tags.** Which rows a person owns
  and when they may be touched (settled orders, issued invoices) are the
  modules' rules.
- **Tagging in SQL comments.** Only the migrations could read them, and the
  declaration and the report are Go.
- **Every open column is kept.** A holder that deletes rows removes them too.
- **A retention period per column.** It is the controller's judgement.
