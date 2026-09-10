# ADR 0118 — A collection's gate reads what is left to take

**Summary:** Opening a payment session stops asking whether the collection has
ever taken anything and asks what is left to take. It costs one arithmetic
change and buys back a partial capture's remainder, which nothing could collect.

- **Status:** Accepted
- **Date:** 2026-09-10

## Context

A collection refused every new session once its captured total rose above zero.
The rule read a counter rather than a balance — "has this ever taken anything",
not "does this still want anything" — and the calculation beneath it never read
the captured total at all: the remainder was the amount less what live sessions
reserved, and nothing else.

A partial capture is not a corner. The admin capture endpoint takes an optional
amount and bounds it only from above, so an operator can take part of a hold;
a provider that authorizes in part reaches the same place with nobody choosing
anything; and the derived status vocabulary has carried a name for it since it
was written. After one, the rest of that collection could never be collected by
any route.

The module also promised more than the rule allowed. Its own test says a
canceled session must not lock a collection for ever, because the customer has
to be able to try another payment method — a promise that held only while
nothing had been captured. ADR 0042 named this blockage and the very
calculation that would have to learn to do the sum.

## Decision

Opening a session asks what is LEFT: the amount, less the captured total, less
what live sessions reserve. A session is refused only when that remainder is
zero, and the flag that asked whether anything had ever been captured is gone.

## Consequences

The bar against a double charge moves from a flag to arithmetic, and it moves
to where the second wall already stood. The database refuses a captured total
above the amount, and until now that constraint could only fire AFTER the
provider had taken the money; the sum that opens a session is now that sum.

A partially captured collection can be finished. The status the schema already
named stops being a dead end.

A fully refunded collection does NOT become payable again. A refund raises the
refunded total and leaves the captured one where it is, so the remaining
capacity stays zero. That is ADR 0117's named trigger and it is deliberately
not met here: the two production callers of a collection refund only send money
back, neither collects afterwards, and a capability nobody reads is not
published.

ADR 0117 said the blocker for an exchange difference was the collection. The
measured answer is narrower: the existing one could not take that difference
even if it were reusable, because its amount never changes — so a second
collection is its own question rather than this one.

ADR 0042 describes the old rule in a sentence that no gate holds to the code.

The refund plan's prose claimed captures are drawn oldest first and gave a
reason; the query has ordered newest first since it was written. The prose is
corrected to the shipped behavior and the order is left alone. Gap D54.

Measurement: [measurements/0118](../measurements/0118-a-counter-in-place-of-a-balance.md)

## Rejected

**Reading the net capture instead.** A fully refunded collection would open for
its whole amount, and the capture would fail on the constraint after the
provider had moved the money.

**A delete or a patch on a collection.** Money records are not removed, and its
status is derived rather than stored.

**A closing stamp, the shape a stock location retires with.** A withdrawable
collection has no reader today.

**Deciding here what a second collection would be bound by.** ADR 0117 deferred
that name for the same reason, and it is still true.
