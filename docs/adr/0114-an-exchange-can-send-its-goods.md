# ADR 0114 — An exchange can send its goods

**Summary:** A replacement is sourced from a claim OR an exchange, and
dispatching it settles that source — an exchange only when it owes nothing. It
costs a wire schema both sides had to change together and buys the exchange the
completion migration 000008 took away.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

An exchange has been a request that can only be withdrawn. Migration 000008
removed its completion and wrote down exactly what would bring it back:
completing one needs goods shipped OUT against an existing order and, when the
difference is not zero, money moved against that same order — "and the framework
has neither".

One of the two arrived. ADR 0090 built the goods-out flow and
`order_replacements` is the record it sends against. That record could only be
fed by a CLAIM, because on the day it was written a claim was the only thing
that could ask for goods — so an exchange, which IS goods out against goods
back, had no way to say what it was sending.

The other has not arrived: the order-to-payment link is still one-to-one.

## Decision

A replacement names exactly one source, a claim or an exchange, and dispatching
it settles that source. An exchange is settled only when its `difference_due` is
zero, and the database holds that bound.

## Consequences

An exchange that owes money STAYS OPEN after its goods leave. That is the honest
state rather than a gap: the goods half is recorded by the replacement that sent
them, and the money half happened somewhere this framework cannot see. The
trigger for the rest is the one the payment module's link definition already
names — the day the order-to-payment link becomes one-to-many.

The bound is a CHECK and not a service rule. It needs only the row, which is the
whole reason it can be one, and a rule in the schema cannot be skipped by a
second writer.

The cross-module wire lost `claim_id` and `claim_status` and gained
`source_kind`, `source_id` and `source_status`. Two pairs, one always empty,
would make every reader ask which was set. The two ends cannot import each other
(ADR 0006), so the compiler sees nothing and the integration lane is the proof.

The exchange has TWO transitions now, so it gets the frame
`CancelExchange`'s godoc said was not worth building for one: with two, the rule
that a second call keeps the FIRST moment would otherwise be written twice.

Five admin routes are repeated under the exchange's path. Three of the handlers
are shared verbatim, because they read only the replacement's own id.

## Rejected

**A `source_kind` column beside the two identifiers.** A stored discriminator is
a third thing that can disagree with the pair that already says it.

**Completing any exchange and leaving the difference to the operator.** It would
let a record say a balance was handled when nothing handled it, which is the
fault 000008 removed the status to avoid.

**Refusing the dispatch when the exchange owes money.** The goods really do
leave; refusing to send them because the money cannot be moved would make a
recordable act unrecordable.

**Moving the replacement routes under the order.** It would address the record
without its source and break a published surface to avoid repeating five lines.
