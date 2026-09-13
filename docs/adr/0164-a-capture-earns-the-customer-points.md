# ADR 0164 — A capture earns the customer points

**Summary:** A named customer earns loyalty points when money is captured for
them and loses them when it is refunded; the append-only ledger lives in the
payment module and only the function that moves a collection's totals writes it.

- **Status:** Accepted
- **Date:** 2026-09-13

Measurement: [measurements/0164](../measurements/0164-a-name-that-was-already-taken.md)

## Context

The feature list asked for a `loyalty` module with a `loyalty_transaction` table
subscribing to `order.placed`. Measuring it against the tree refused all three
nouns.

The name is taken. `examples/starter/loyalty` is a module named `loyalty` that
binds `/store/v1/loyalty/balance`, and the registry refuses a repeated name
before it mounts a route — so the example gobit hands an embedder would stop
booting, and no lane boots it.

`order.placed` is the wrong moment: it is published before the money is
authorized or captured, a cancelled order publishes nothing at all, and on a
guest order its customer is empty. And an earn half alone is a table nothing
reads — ADR 0153 measured that slice order green and inverted it, because the
missing half is the one nothing can fake.

## Decision

A customer earns points when money is CAPTURED for them and loses them when it
is refunded. The append-only `payment_loyalty_entries` ledger lives in the
payment module, `writeCollectionTotals` — the one function that moves a
collection's totals — writes every row as the difference to a target computed
from that collection's own cumulative amounts, and an operator reads the balance
and the history under `payment:read`.

## Consequences

The write cannot double-count and needs no uniqueness index to say so. A row is
the difference between what a collection should have earned and what it has been
written, so a repeated call appends nothing and a refund appends a negative row.
It is the rule the module's own events already state — carry an identifier, read
the record — made general by ADR 0162.

A guest earns nothing — most collections name nobody, and a row keyed on an empty
customer would put every guest in the shop into one account — and a rate of zero
returns before the ledger is read, because otherwise closing a program would
take back what it gave.

Points are not spendable yet: the tender that spends them is a provider in this
module, where the published contract already says a person's funds are spent
from. And a customer cannot see their own balance, for the reason ADR 0152
recorded and this record does not reopen: there is no proven customer identity at
the storefront.

The rate is one number for every currency, refused above one point per minor unit
rather than reduced; the currency is on every row, so a rate per currency is a
later decision and not a migration. `payment:read` now also opens a customer's
points, and this module's scope enumerations were stale already (D111).

## Rejected

**A nineteenth module.** It cannot learn whose money was captured: this module's
published read surface carries no customer, so the owner would arrive by widening
a name promised until 1.0.0, or through the order link.

**The promotion module, which the feature list named.** It owns no customer, no
event and no money type, and its own word for a unitless rate already means
basis point.

**Subscribing to `payment.captured`.** The module publishes and does not
subscribe, a written sentence with a reason; the transaction that moved the
money is also exact rather than eventual.

**`order.placed` with a uniqueness key.** It fixes the double credit and none of
the rest: unpaid orders, no reversal, an empty guest key, and a rate on a gross
total whose parts the payload does not carry.

**A write endpoint for an operator.** In this module a write means money moved.
