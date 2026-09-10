# ADR 0113 — A line can be written off in part

**Summary:** `order_line_cancellations` records units of one line that will not
be delivered, under the order's lock and against a ceiling shared with returns.
It costs a second act for the money and buys a live order the ordinary case its
all-or-nothing cancellation could not express.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

Canceling was all or nothing. `CancelOrder` takes the whole order and refuses
one that has collected money, and that is right for what it is: the checkout
saga's compensation, run when nothing has shipped and nothing has been charged.

The case it cannot express is the ordinary one. A live order's line goes out of
stock, arrives damaged at the warehouse, or the customer drops it while the rest
of the order ships. Nothing in the module could say so, and the only ways to
record it were both wrong — cancel the whole order, or edit the quantity of a
line that is the cart's snapshot.

The return ceiling had the same blind spot from the other side: it counted
returns and nothing else.

## Decision

`order_line_cancellations` records how many units of a line will not be
delivered, with a reason, and the ceiling — bought minus returned minus already
canceled — is checked under the ORDER's lock. The return path reads the same
sum, so a unit is spoken for once whichever act spoke for it.

## Consequences

The order's TOTAL does not move and neither does the line's quantity. Both are
the cart's snapshot, the total is pinned to the lines by a CHECK, and lowering
either would erase the sale instead of recording the cancellation.

The STATUS does not move either. An order whose every line is written off is
still an order somebody has to close, and closing it stays a separate verb.

The MONEY is a second act. A unit already paid for and now not coming is a refund
or a credit (ADR 0105), and which of the two depends on a policy this module does
not hold; a cancellation made before payment owes nothing at all.

The STOCK is not put back, and this is the limit worth naming. The order module
cannot reach inventory (ADR 0006), so releasing a reservation or restocking a
canceled unit belongs to a flow above it — and no flow asks yet. The trigger is
the first one that does.

The two ceilings are ONE arithmetic and are read together. Counted separately,
three bought units could be asked back twice and canceled once, and the goods
arriving at the warehouse would disagree with the record by a quantity nobody
could account for.

The record carries no `order_id`. The line already names its order, a second copy
is a second thing to keep true, and the listing joins one row.

## Rejected

**Lowering the line's quantity.** It erases what was sold and breaks the totals
constraint; the order stops being the permanent answer to what was bought.

**A status on the cancellation.** A withdrawn return releases its units because a
request is a promise; a cancellation is a fact about goods. Giving it a lifecycle
would invite "un-canceling" a unit that was never going to arrive.

**Closing the order when its last line is written off.** It would make a
bookkeeping act into a state transition, and an order with nothing left to send
is still one a merchant may want to keep open for a replacement.

**Writing the credit automatically.** It settles money nobody authorized, and it
is wrong outright for a cancellation made before payment.

**Enforcing the ceiling with a CHECK.** The rule spans rows; a CHECK sees only
its own.
