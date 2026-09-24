# ADR 0165 — A customer can pay with their points

**Summary:** A `loyalty_points` provider spends a customer's points through the
slot store credit is spent through, a point is worth one minor unit of the
currency it was earned in, and money captured through that tender earns nothing.
The state machine the two tenders share is written once.

- **Status:** Accepted
- **Date:** 2026-09-13

Measurement: [measurements/0165](../measurements/0165-what-a-point-buys.md)

## Context

ADR 0164 earns points and leaves them unspendable, naming the tender that will
spend them: a provider in this module. Measuring that sentence against the tree
found four things a copy of the store-credit provider would get wrong.

The provider contract speaks money in minor units and the ledger holds points
that no number in the installation converts. The earn rule is provider-blind, so
a capture paid with points would earn points — unbounded at the ceiling rate,
where a point earns itself back. The ledger's gate admits exactly one writer,
while the credit ledger has two writers and no gate. And the manual and
store-credit providers are two hand copies of one state machine, of which one
answers reconciliation and the other does not.

## Decision

A `loyalty_points` provider spends a customer's points through the slot store
credit is spent through, and a point is worth one minor unit of the currency it
was earned in, so the earn rate is the cashback in basis points. The state
machine is written once, in `balancetender`, both tenders are that machine over
their own ledger, and money captured through the points tender earns nothing.

## Consequences

Authorize writes a hold, cancel a release, refund a refund, and capture no
spend, only the release of what it did not take; every spend row references the
provider's own session and never a collection, and the earn target now sums only
earn and reverse rows. The balance is locked before it is summed, with an
advisory lock keyed on the customer and the currency, because a lock on the rows
took nothing from a customer who had none yet (D118); the transaction names READ
COMMITTED, the level that lock rests on (D119).

The earn excludes money captured through the points tender: the target is the
net of the collection's captures whose session is at another provider. Credit
that is spent still earns, because credit is money the shop owes at face value
and points are the program's own currency.

A balance can fall below zero. A refund reverses points the customer may have
spent already, it is never refused for that, the next earn fills the hole first
and the tender declines against it. The reverse takes no lock: the interleaving
a lock would prevent produces a state a legal order also produces.

Both tenders now answer reconciliation, and a guest who chooses either gets a
conflict rather than a server error. They are registered under one option named
for its reason, `PersonBoundTenders`, where the customer claim is proven. The
ledger gate gains a second door derived from the tender's identity and is
pointed at the credit ledger too.

At the storefront points pay for an order they cover entirely, as credit does;
splitting is the admin surface's. A customer still cannot read their balance.
ADR 0164 is amended: its totals writer writes every earn row, not every row.
ADR 0152 is amended: its lock on the customer's rows is the lock on the balance.

## Rejected

- **A redeem value setting.** Two numbers for one quantity, rounding up in one
  direction and down in the other, and a rate frozen per session; it can be added
  later with a default of one and revalues balances the day a shop changes it.
- **Converting points into store credit.** Only an operator could trigger it, so
  every redemption is a ticket.
- **A discount at checkout.** The promotion module owns no customer and no money
  type, and a refund would have no way back to the ledger.
- **Letting a points-paid capture earn.** A point earns itself at the ceiling.
- **A third hand copy of the state machine.** Three copies compared by nothing.
- **A lock on the earn path's reverse.** It prevents nothing a legal order allows.
- **A lock on the customer's rows.** It takes nothing from a customer who has none.
- **SERIALIZABLE for the spend.** It fails one of the two, and every caller retries.
- **A storefront balance endpoint.** Still ADR 0152's identity bill.
