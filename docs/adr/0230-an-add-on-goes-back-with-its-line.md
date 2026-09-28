# ADR 0230 — An add-on goes back with its line

**Summary:** After the sale an add-on line is returned and written off only with
the line it was sold with and at the same quantity: a return has to name both,
and a write-off of the line writes off its add-ons in the same act.

- **Status:** Accepted
- **Date:** 2026-09-29

Measurement: [measurements/0230](../measurements/0230-the-engraving-comes-back-too.md)

## Context

ADR 0229 binds an add-on line to its line on the cart and on the order, and left
the after-sales acts as they were: a return and a line write-off each named
lines one by one. A ring could come back and leave its engraving sold, or the
engraving be written off while the ring shipped, and the units spoken for on the
two lines would part. A return carries a refund per line the operator types; a
write-off moves no money and names one line.

## Decision

A return has to name every add-on of a line it names at that line's quantity,
and an add-on only beside its line at the same quantity, what each refunds
staying the operator's. A write-off of a line writes off the same quantity of
each of its add-ons in the same transaction, each with its own record and event,
and an add-on named alone is refused with `order_add_on_follows_its_line`.

## Consequences

The units spoken for on a line and on its add-ons stay equal, so the ceiling of
one is the ceiling of the others. A return lists the engraving that comes back
on the ring and may refund nothing for it; the storefront's return request
answers 422 until both are named. A write-off answers the line it was asked for,
and its add-ons' records are read with the order's other cancellations; each
publishes its own `order.line_canceled`, so the stock of a counted add-on is
put back as any line's is.

A replacement is not bound: a line replaced ships without a new add-on unless
the operator adds one. A return or write-off made before this decision is
untouched.

## Rejected

- **A return naming the line widened to its add-ons.** The refund is typed per
  line, and a widened line would carry an amount nobody wrote.
- **A write-off of the line refused until its add-ons are written off.** An
  add-on cannot be written off alone, so the two rules would lock each other.
- **Add-ons returned and written off freely.** A ring would come back with its
  engraving still sold, and nothing would say they belong together.
