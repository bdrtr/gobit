# The change that waited — measured 2026-09-29

The evidence behind [ADR 0241](../adr/0241-a-row-written-under-a-lock-is-stamped-when-written.md)
and D164.

## 1. The mechanism

`now()` is `transaction_timestamp()`: the moment the transaction began, the same
for every statement in it. A write that begins its transaction, waits for a row
lock another write holds, and then writes, is written after that other write
and stamped before it. Every reproduction below holds the FIRST lock asked for
in a store wrapper, after that caller's transaction has begun, runs a second
write to the end, and then releases the first.

## 2. The reproductions

| Test | Held lock | Before | After |
|---|---|---|---|
| `TestTheChangeMadeLastIsTheCurrentDelivery` (order) | `LockOrder` in `ChangeDelivery` | the waiting change listed first; `CurrentDeliveries` reported the other; its difference (-500) was reckoned from the other's amount | listed last, and the delivery |
| `TestTwoCorrectionsAtOnceLeaveTheLastOneCurrent` (order) | `LockOrder` in `CorrectShippingAddress` | `order_addresses_superseded_after_written` refused the waiting correction: SQLSTATE 23514, a 500 | both written, the waiting one current |
| `TestAnExchangeCompletedAfterItsFundingReadsCompleted` (order) | `LockExchange` in `CompleteExchange` | `completed_at` 67 µs before `funded_at`; the history, whose latest moment is the status, reads "funded" | `completed_at` after `funded_at` |
| `TestTheNewestMovementCarriesTheCount` (inventory) | `LockInventoryLevel` in `AdjustInventory` | the newest movement read `stocked_after` 8, the level 5 | 5 and 5, and the movement's moment is the level's |

`CurrentDeliveries` feeds `ShippingOptionOf`, which the fulfilling flow reads to
open the parcel: the parcel would have been opened on the delivery the customer
had changed away from.

## 3. The population

A read-only audit of every stamp written after a lock in the same transaction.

| Table | Stamp | Reader that depends on the order | Verdict |
|---|---|---|---|
| `order_delivery_changes` | `now()` default | `CurrentDeliveries`, last wins | fixed |
| `order_addresses` | `now()` default, `superseded_at = now()` | the CHECK `superseded_at >= created_at` | fixed |
| `order_exchanges` | `funded_at`, `completed_at`, `canceled_at = now()` | `appendRecord` in the order's history, latest wins | fixed |
| `inventory_movements`, `inventory_levels` | `now()` default, `updated_at = now()` | the newest movement's `stocked_after`; the inherited balance; the newest-first keyset | fixed |
| `price_set_history`, `price_list_history` | the process clock, read before the lock | the last snapshot at a moment is the standing price; the storefront reduction | D165 |
| `invoices` | `issued_at` from the process clock before the series lock | number order against date order | D165 |
| credit lines, line cancellations, replacements, refunds, store credit, loyalty and gift card entries, orders, cart rows | `now()` | sums, sets or status; a list, a timeline, an as-of cutoff or a journal window moves by the lock wait | cosmetic, kept |
| captures, fulfillments, product revisions | the process clock after the lock, or the insert first | — | fine |

Keyset listings over a stamp written after a lock can skip a row that commits
late with a moment the reader has passed; `clock_timestamp()` narrows that gap
without closing it, since the stamp is still taken before the commit. The
movement ledger is the most exposed and is among the fixed ones.

## 4. Other changes

- The movement's moment is passed to the insert; the repository refuses a zero
  one. Two integration tests that appended movements directly now pass one, and
  the paging test names its premise, two movements with one moment, instead of
  relying on the transaction's start.
- The inventory service's test double keeps a moment it is given.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| T1 | delivery changes stamped at the transaction's start | `TestTheChangeMadeLastIsTheCurrentDelivery` |
| T2 | address rows stamped at the transaction's start | `TestTwoCorrectionsAtOnceLeaveTheLastOneCurrent`, the history's order |
| T3 | an address superseded at the transaction's start | the same, the CHECK |
| T4 | an exchange completed at the transaction's start | `TestAnExchangeCompletedAfterItsFundingReadsCompleted` |
| T5 | a level written at the transaction's start | `TestTheNewestMovementCarriesTheCount` |
| T6 | the movement on the level's creation moment | the same, the shared moment; the service's unit tests do not see it |
| T7 | a movement with no moment written | `TestAMovementWithNoMomentIsRefusedByTheRepository` |
| G1 | 0053's row back to `current` | `TestTheADRIndexNamesEveryAmendment` |

Eight mutants, all killed. The first draft of the address test read only the
current row, which the supersede moment alone keeps right, so it was given the
history's newest row to read before the run; the moment's guard got its own
test then too.
