# ADR 0258 — Store credit can expire

**Summary:** An issue of store credit may name the moment it expires; from then
the balance stops counting what it still holds, and a job writes an expire row
that takes it back, spending taken to draw on the soonest-expiring credit first.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

Store credit (ADR 0152) was spendable forever. The known limits said so and
named the shape of the fix: a scheduled job writing negative rows, which must
not race a checkout holding the same money. The ledger is a sum of rows, and
nothing records which grant a hold drew on, so what an expired grant still
holds has to be derived from the sums rather than read from a lot.

## Decision

An issue may carry `expires_at`, and what the balance holds beyond the
unexpired issues and the refunds is expired credit, up to what the expired
issues gave and no expire row has taken back. The balance a tender spends leaves
that out from the moment it expires, and a job writes the expire row under the
balance's lock.

## Consequences

- Spending is taken to draw on the soonest-expiring credit first, the order
  most generous to the customer, and no row has to say which grant it drew on.
- The amount is a target computed from the sums, not a count kept anywhere:
  a settled balance is written nothing, and money a canceled session gives
  back after its credit expired is taken on the next run.
- Money a session holds is not taken while it is held; a capture spends it, a
  release gives it back to the next run.
- A refund into credit never expires, even when the credit it repays did.
- The job takes the lock the tenders take to spend, so a checkout either held
  its money before the expiry read the sums or finds the expiry written.
- The rule is written once in Go, and the balance is read through it; the
  query that lists the balances due carries its own copy to select with, and
  a test holds the two to agree at every step of a scenario.
- The journal books an expiry as the debt going and the grant's cost coming
  back, as a closed gift card's void is booked (ADR 0213).
- The listing of balances due re-sums every balance that ever held an expired
  issue on each run; the partial index keeps the others out of it.
- Rolling migration 000013 back is refused while an issue names a moment or an
  expire row exists.
- Credit still names no cause beyond free text; the known limit keeps that half.

## Rejected

- Recording on each hold which grant it drew on: every tender's write would
  have to change, for an order a rule on the sums already gives.
- Taking back the whole of an expired grant: it would take money the customer
  spent before the moment, and push the balance below zero.
- Expiring only when the job runs: credit would keep paying for up to the job's
  interval after its moment, which a gift card does not.
