# ADR 0124 — The dispatch asks whether the money is still there

**Summary:** A dispatch sourced from an exchange asks the payment module whether
the difference is still held, before the stock moves and again before the record
is closed. It costs one read on each of those two paths and buys the end of a
shop giving its goods away against money that went back.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

ADR 0120 lets an exchange take its difference, and the row keeps the MOMENT it
was funded because a moment is all an order row may keep about a figure the
payment module owns (ADR 0119). A moment is not a balance. The collection stays
reachable by the payment module's own refund route, with no flow anywhere on that
path — the same absence gap D55 records one module over — so the money can leave
and the stamp does not move.

It was measured rather than feared. Funding an exchange, refunding its collection
through the published route and dispatching sent the units, opened a real parcel
and marked the exchange completed with the collection holding nothing. Gap D61.

Three guards stand on that path and each is right about its own question: two ask
the ROW whether it may be closed, and the third asked the payment module once, at
funding time. Nothing asked again.

## Decision

A dispatch whose source is an exchange asks the payment module whether that
exchange's collection still holds its difference, and refuses to send when it
does not. The same question is asked again before the source is marked settled,
because that step also runs on the retry path.

## Consequences

The refusal is placed BEFORE the stock is held, and the placement is the decision
rather than an ordering detail: below that line the units are in a box and no
answer can put them back. It answers CONFLICT, which is honest while nothing has
happened yet — the goods stay on the shelf and the operator is told the figure.

On the retry path the goods are already with the customer and refusing is not
available. The exchange stays open and the loss is logged at ERROR with what is
held and what is owed, which leaves a human both facts. Writing `completed` there
would be the state ADR 0119 exists to forbid.

Asking twice is not a duplicated read. It is that record's rule applied: the
deciding flow asks payment at the moment it decides, and a dispatch decides twice
— once about the goods and once about the record.

It costs one payment read per exchange-sourced dispatch on each path. A
claim-sourced dispatch asks nothing, because a claim is settled by the goods
alone; an exchange owing nothing asks nothing, because it names no collection.

This discharges one half of the question ADR 0119 left open on purpose. The other
half — a withdrawal refusing while the exchange holds the customer's money — is
already answered by ADR 0120's exit, which refunds what is held and withdraws in
one act.

Measurement: [measurements/0124](../measurements/0124-a-stamp-is-not-a-balance.md)

## Rejected

**Keeping the balance on the exchange row.** It is a second copy of a figure the
payment module owns, which is what ADR 0119 refuses, and it would go stale by the
same route that produced this defect.

**Asking only before the parcel.** The settle runs on the retry path with nothing
asked, and the record is the half still repairable once the goods have gone.

**Subscribing to the refund event instead.** The order module already listens
(ADR 0121) and correctly hears nothing here: an exchange's collection is bound to
no order, which ADR 0117 makes structural.

**Refusing on the retry path.** The goods are with the customer; an error there
asks the caller to retry what has already happened.
