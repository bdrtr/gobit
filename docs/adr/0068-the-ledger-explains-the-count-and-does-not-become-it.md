# ADR 0068 — The ledger explains the count, and does not become it

**Summary:** `inventory_movements` records every change to the physical count
and `stocked_quantity` stays authoritative; a reservation is not a movement, a
row carries a reason instead of an actor, and nothing deletes it.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

B7 was the last open row in `docs/gaps.md` and it said what it needed: not an
unblocking but four answers. `audit_log` does not cover the ledger — it records
the REQUEST, holds no delta and no item, and never sees a reservation the
checkout takes. Three write sites change the physical count
(`SetInventoryLevel`, `AdjustInventory`, `ConfirmReservation`); the fourth
candidate does not exist, since `gobit seed` writes no stock.

Measurement: [measurements/0068](../measurements/0068-the-movement-ledger.md).

## Decision

**A reservation is NOT a movement.** Reserving and releasing change what is
AVAILABLE, not what is present, and `inventory_reservations` is already that
record. Only the CONFIRM leaves a row, and it is the one row that names its
reservation — the fact B7 says `audit_log` can never hold.

**`stocked_quantity` does NOT become derived.** Deriving it puts an aggregate in
front of every availability read, the storefront listing's included, and answers
nothing about stock older than the table. Two things hold the pair together
instead: the movement is written in the SAME TRANSACTION as the column —
`AppendMovement` refuses to run outside one, as the Lock methods do — and every
row carries `stocked_after`, so a drift shows in ONE row rather than by summing.

**There is no actor; there is a REASON, and it says where the actor is.** Four
values, closed in the type and in a CHECK. `stock_count` and `adjustment` come
from an admin request, which `audit_log` already records with its caller; `sale`
and `return_restock` come from a flow with nobody behind it. A nullable actor
would be empty on half the table (ADR 0056), so the reader publishes the mapping
as `from_admin_request`.

**A row is KEPT, and retention is the operator's** — this repository's practice
rather than a policy invented here: `audit_log` has none by decision (ADR 0037).
No endpoint removes a movement and no window expires one.

**The reader ships with the table**: `GET /admin/v1/inventory-items/{id}/movements`,
keyset-paged newest first, under the existing `inventory:read`. Reading it is NOT
audited — ADR 0037's exception is an exact-path list and this listing shows the
story of numbers `GET .../levels` shows already.

**The ledger begins where the table does.** No opening balance is written, so the
deltas do not sum to the count — which costs nothing because the count is
authoritative, and the oldest row names the inherited balance.

## Consequences

- **A stock discrepancy is answerable**: what changed, when, how much, why.
- **One INSERT joins every physical stock write that goes through the store**,
  the checkout's included — an append with a monotonic leading key. Raw SQL and
  a migration are outside what the gate reads.
- **Two entry points where there was one.** `RestockInventory` splits off
  `AdjustInventory`: a positive delta cannot say if goods came back or a count
  was corrected.
- **A soft-deleted level leaves no movement**: nothing moved.
- **Two gates**: the choke point in `internal/arch`, the transaction in the repository.

## Rejected

- **Derive `stocked_quantity`.** An aggregate on the hottest read path.
- **One table for both records.** The ledger would explain no single column.
- **An `actor_id` column.** Empty on half the rows, unissued on the rest.
- **A `source` column beside the reason.** The two are a bijection over four
  values today, and a column with no independent fact is refused (ADR 0048). A
  SECOND producer of one reason reopens it — and moves the PUBLISHED field too:
  `from_admin_request` stops being true of those rows and is re-derived from
  `source`.
- **`ListOptionalCount`, the envelope the other keyset listings ship.** Here
  `offset` has no meaning and `count` is a scan of an append-only table nobody
  asked for; the page says `next_cursor` and stops.
- **An opening balance, or a retention window.** The first dates a shop's whole history to its upgrade; the second is the embedder's (ADR 0029).
