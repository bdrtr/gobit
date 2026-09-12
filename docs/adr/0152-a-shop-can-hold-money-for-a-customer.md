# ADR 0152 — A shop can hold money for a customer

**Summary:** The payment module gains an append-only store credit ledger and a
`store_credit` provider that spends it, and a payment collection now names whose
money is being collected. It costs one more table pair and a tender that a guest
cannot use, and it gives a shop something to offer instead of a refund.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

A shop that wants to keep a customer after a late delivery has two things it can
do: send the money back, or put it on the customer's account. This repository
could only do the first. No table in any module held a balance, so "we credited
you 200 lira" was a promise kept in a spreadsheet and settled by hand at the next
order.

What made it a decision rather than a table is the SPENDING half. Money that
belongs to a person can only be spent by that person, and the party naming the
payer was the client: a payment collection carried a reference and an amount and
named nobody. A tender that reads its owner out of the request body is a tender
anybody can spend by typing somebody else's id.

Measurement: [measurements/0152](../measurements/0152-what-a-shop-could-not-hold.md)

## Decision

Store credit is an append-only LEDGER owned by the payment module — a signed
amount per row, per customer, per currency — and a `store_credit` PROVIDER that
spends it through the same slot a card is spent through. The payment collection
carries the customer, so the session knows whose money it is, and the provider is
registered only where the customer claim is proven.

## Consequences

The balance is the sum of the rows and is stored nowhere. An authorization writes
a NEGATIVE hold, a capture writes nothing, and a cancellation writes the release;
so what a customer can spend already has their open holds subtracted, and a
correction is a new row rather than an edited one.

The lock is the correctness argument. The provider reads the balance and acts on
it, so it takes the customer's rows `FOR UPDATE` first; without it two
authorizations both see enough money, both write a hold, and the customer spends
the same money twice. It is proven against a real server with a competing
transaction, because a unit test can only see that the lock was ASKED for.

The customer now travels cart → plan → collection → session, and a guest cart
cannot pay with credit: the provider refuses a session naming nobody, which is
the same sentence as "there is no balance to spend". `CreateSessionInput` gains
the field on the published contract, so every provider sees it.

The dangerous configuration is not configurable. An installation that trusts an
unproven customer claim (`STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM`, ADR 0125)
does not get the provider registered at all — the tender disappears rather than
becoming a way to spend somebody else's balance.

There is no storefront endpoint, and the three new `customer_id` columns are
recorded CONFINED (ADR 0051): they are written by admin acts and by the checkout's
own plan, never by a storefront body.

Fourteen mutations, twelve of which bit. The two that did not are the finding:
the first concurrency test counted outcomes across eight goroutines and passed
with the lock REMOVED, because a local server finished each transaction before
the next began — it produces the interleaving now instead of hoping for it. And
the checkout dropping the customer broke only the end-to-end proof; the
workflow's own suite never read the field, so it has a witness.

## Rejected

- **A balance column on the customer.** It answers "how much" and never "why",
  and the answer to why is the half a shop actually needs when a customer asks.
- **Store credit in the customer module.** That module would become a party to
  money movements and the checkout saga would have two modules to compensate.
- **Taking the owner from the client's data.** It is the defect the decision
  exists to avoid; the flow that opens the collection is holding the cart already.
- **Letting a customer read their own balance.** It needs the customer claim
  proven on the storefront, which is a surface this module is not wired to.
- **A negative amount to take credit back.** Withdrawal is its own decision, and
  as a sign it would leave nothing between an operator and a balance below zero.
