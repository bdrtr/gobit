# What a closed stock location owes — measured 2026-09-08

Evidence for [ADR 0055](../adr/0055-a-location-closes-empty.md).

## The surface a location had: three verbs

At 3c912f3 the inventory module's location surface was `CreateStockLocation`,
`GetStockLocation`, `ListStockLocations` and nothing else — no update path, no
delete path. `queries/stock_locations.sql` held four statements (the fourth is
the pagination count).

| | before | after |
|---|---|---|
| statements in `stock_locations.sql` | 4 | 7 |
| service verbs | 3 | 4 (`CloseStockLocation`) |
| admin routes | 3 | 4 (`POST .../{id}/close`) |

A reopen verb was drafted alongside the close and then dropped. Nothing in
keeping a closed location empty needs it, and it would have made `closed_at` the
only stamp in the schema that can be cleared. The question it answered — should
a mis-close be undoable — is recorded as open in `docs/gaps.md` rather than
settled by a surface nobody asked for.

## The reads do not join. Not "not to locations" — not at all

```
$ git grep -c -i "join" HEAD -- internal/modules/inventory/queries
(no output: zero matches in the whole directory)
```

So the three statements that decide what can be sold —
`AvailableQuantityByItemIDs`, `ListInventoryLevels` and `LockInventoryLevel` —
read `inventory_levels` alone. A location marked deleted would have been invisible
to every one of them, which is the whole finding: `deleted_at` on this table
could be written and would change nothing about what the shop sells.

## And a delete destroys the history

```
$ git grep -n "ON DELETE CASCADE" HEAD -- internal/modules/inventory/migrations
000001:67  inventory_item_id ... REFERENCES inventory_items (id) ON DELETE CASCADE
000001:68  location_id       ... REFERENCES stock_locations (id) ON DELETE CASCADE
000001:100 inventory_item_id ... REFERENCES inventory_items (id) ON DELETE CASCADE
000001:101 location_id       ... REFERENCES stock_locations (id) ON DELETE CASCADE
```

Lines 68 and 101 are the two that matter: deleting a location takes its stock
levels AND its reservations with it. Migration 000002 argues at length that a
reservation is never deleted, because a released reservation and one that never
existed have to stay distinguishable for the checkout compensation to be
idempotent. A row that can be deleted through a warehouse is not kept.

## Why not join the reads instead: `FOR UPDATE` locks what it joins

The obvious alternative to an invariant is to filter at read time — join
`inventory_levels` to `stock_locations` and drop the closed ones. One of the
three statements is `LockInventoryLevel`, which is `SELECT … FOR UPDATE` on the
checkout path. Probe on the repository's PostgreSQL 16, in a scratch database:

```sql
CREATE TABLE loc (id text PRIMARY KEY, closed_at timestamptz);
CREATE TABLE lvl (id text PRIMARY KEY, loc_id text NOT NULL REFERENCES loc (id));
INSERT INTO loc (id) VALUES ('L1');
INSERT INTO lvl (id, loc_id) VALUES ('V1', 'L1');
```

Session 1 holds the joined lock the way a reservation would:

```sql
BEGIN;
SELECT l.id FROM lvl l JOIN loc ON loc.id = l.loc_id WHERE l.id = 'V1' FOR UPDATE;
```

Session 2, asking only for the LOCATION row:

```sql
SELECT id FROM loc WHERE id = 'L1' FOR UPDATE NOWAIT;
ERROR:  could not obtain lock on row in relation "loc"
```

Control, same probe with `FOR UPDATE OF l` in session 1: session 2 returns `L1`
and no error. So the join is not free — written plainly it takes an exclusive
lock on the warehouse row for every reservation, and every checkout at one
warehouse serializes behind the last one. `OF l` avoids it, and then the filter
is a per-read detail that three statements have to remember forever.

The chosen shape pays none of that: the reads are untouched, and the location
row is locked SHARED by the two flows that put stock in.

## The index really does go with the column

000002 measured the silent index drop on a probe table. Here it is on this
schema, applying 000001 → 000002 → 000003 to a scratch database on the same
PostgreSQL 16:

```
SELECT indexname FROM pg_indexes WHERE tablename = 'stock_locations';
 stock_locations_pkey
 stock_locations_open_idx     -- WHERE (closed_at IS NULL)
```

`stock_locations_alive_idx` is not there and nothing dropped it by name: its
predicate named `deleted_at`, so the DROP COLUMN took it. Rolling 000003 back on
the same database brings back `stock_locations_alive_idx` and `deleted_at`, and
leaves `closed_at` gone — the down statement is the same shape in reverse.

## The invariant, and why one number decides the stock half

`closed ⟹ the location holds nothing` is maintained at two points:

1. `CloseStockLocation` refuses while `SUM(stocked_quantity) > 0` over living
   levels. The reserved total is read alongside it and appears in the message,
   but it cannot decide anything on its own: `CHECK (reserved_quantity <=
   stocked_quantity)` means zero stocked forces zero reserved.
2. `SetInventoryLevel` and `AdjustInventory` refuse at a closed location. Without
   this half, "closed" is true for one instant and the un-joined sums start
   lying again the moment somebody restocks.

Soft-deleted levels are excluded from the sum deliberately. They are the residue
of a deleted item, no read sums them, and no path brings them back; counting
them would make a location impossible to close over stock that cannot be sold,
moved or written down.

## Why the reservation count is a SECOND check and not a duplicate

An active reservation raises its level's reserved quantity, so in the running
shop it also raises the stocked sum, and the check above would refuse. That
implication holds only because `DeleteInventoryItem` refuses while a reservation
is active — which is what keeps a promise from outliving the level row that
carries it. That rule lives in a different flow. A close that refused on stock
alone would be depending on a sentence it does not contain, so it counts the
promises itself.

This is not the case `AdjustInventory` argues against in its own comment. There
the second condition is implied by a CHECK constraint in the same table and no
input can reach it; here the implication runs through another method's guard.

## The interleaving the location lock exists for

Without a lock on the location row, under READ COMMITTED:

| t | close | stock write |
|---|---|---|
| 1 | | `BEGIN`; writes 100 units at L1 |
| 2 | reads levels at L1 → sees 0 (T1 uncommitted) | |
| 3 | stamps `closed_at`, `COMMIT` | |
| 4 | | `COMMIT` |

L1 is closed and holds 100 units that no availability read joins away. Locking
the level rows instead does not fix it — the write may be CREATING the level,
and a row that does not exist yet cannot be locked. The location row is the only
rendezvous point both flows are guaranteed to touch, which is why it became the
first lock of the order: LOCATION → ITEM → LEVEL.

`TestACloseAndAStockWriteCannotBothWin` runs that race against a real database
and asserts the two possible outcomes and no third one.

## The close's second count has no index to seek

`StockHeldAtLocation` filters `inventory_levels` on `location_id`, and
`inventory_levels_location_idx` is exactly that index — a seek.

`CountActiveReservationsByLocation` has no such index. After 000002
`inventory_reservations` carries two: `inventory_reservations_item_idx` on
`(inventory_item_id, location_id) WHERE status = 'active'`, and
`inventory_reservations_line_item_idx` on `line_item_id`. `location_id` is not
the leading column of either, so the count cannot seek.

What it does instead is NOT measured here and this file does not claim it. The
partial index's predicate matches the query's `status = 'active'`, so the
planner may scan that index — which holds only the active rows — rather than
the table; it may also choose a sequential scan. Either way it is a scan and not
a seek, on the module's fastest-growing, never-deleted table.

It is left as it is. The close is a rare administrative call, one per warehouse
retirement, and the alternative is a third index on a hot write path — every
reservation insert pays for it, so that the decommission of a warehouse can be
fast. If a shop with a large reservation history finds the close slow, the index
is `(location_id) WHERE status = 'active'` and this paragraph is the reason it
was not built on the day.

## What the column audit says now

`inventory.stock_locations.deleted_at` was the last entry in `unwrittenColumns`
outside the order and payment blocks. The column is gone; `closed_at` is written
by `CloseStockLocation`, so it never enters the audit's unwritten set. Removing the exemption was not optional either — the gate asserts
that every exemption is still needed, and it failed with "is exempt in
unwrittenColumns but is NOT unwritten any more" until the entry was deleted.

## Mutations

Each mutation was applied to the tree, the test run with `-count=1`, and the
file restored from a copy taken beforehand — never with a checkout.

**Replace the shared location lock in `requireOpenLocation` with an unlocked
read.** This is the one that matters, because it is the only mutation at the
site the concurrency claim rests on. It leaves `TestAClosedLocationTakesNoStock`
green: the refusal still works, and what disappears is the rendezvous that makes
it hold under a race.

```
--- FAIL: TestTheLocationLockComesFirst/SetInventoryLevel (0.00s)
      Error: []string{"item", "level"} does not contain "location"
      Messages: the flow never took the location lock: [item level]
--- FAIL: TestTheLocationLockComesFirst/AdjustInventory (0.00s)
      Error: []string{"item", "level"} does not contain "location"
```

**Make `Reserve` take the location lock.** The gate holds both halves of the
sentence, so the omission is asserted as well as the order.

```
--- FAIL: TestTheLocationLockComesFirst/Reserve (0.00s)
      Error: []string{"location", "item", "level"} should not contain "location"
      Messages: a reservation flow takes no location lock; the omission is the decision
```

**Drop the `requireOpenLocation` call from `SetInventoryLevel`.**

```
--- FAIL: TestAClosedLocationTakesNoStock (0.00s)
      Error: An error is expected but got nil.
```

**Drop the already-closed branch, so the close stamps unconditionally.**

```
--- FAIL: TestClosingTwiceKeepsTheFirstMoment (0.00s)
      Error: Not equal:
        expected: time.Date(2026, time.September, 8, 13, 47, 9, 457024206, time.UTC)
        actual  : time.Date(2026, time.September, 8, 13, 47, 9, 457025669, time.UTC)
      Messages: the second close must not overwrite the first moment
```

**Move the close route off the write-scoped router.** It stays reachable and
answers 200 to a caller carrying read scope only. This is the mutation the
module's authorization table exists for, and until the close was written into
that table the same mutation left the whole package green.

```
--- FAIL: TestYazmaUcuDarYetkiliCagiraniReddeder/closing_a_location (0.00s)
      Error: Not equal: expected 403, actual 200
```

**Remove `ALTER TABLE stock_locations ADD COLUMN … closed_at` from migration
000003.**

```
--- FAIL: TestTheSchemaScannerFollowedEveryMigration (0.00s)
      Error: []string{"id", "name", …, "created_at", "updated_at"} does not contain "closed_at"
      Messages: 000003 adds the column that replaces it; without it the drop
                above reads as a table the scanner stopped following
--- FAIL: TestTheDeclarationCoversEveryPersonalColumn (0.00s)
      Messages: stock_locations.closed_at is exempted from the declaration and is
                not in the schema
```

**Put the `stock_locations.deleted_at` exemption back into `unwrittenColumns`.**

```
--- FAIL: TestEveryColumnIsWrittenBySomething (0.07s)
      Messages: inventory.stock_locations.deleted_at is exempt in unwrittenColumns
                but is NOT unwritten any more.
```

### One gate is NOT mutation-proved

`TestACloseAndAStockWriteCannotBothWin` runs against a testcontainer, and the
integration lane was not run in the round that finished this record. The gate is
therefore unproved by mutation — known to compile, and to have passed once,
which is not the standard this repository holds. The mutation to run against it
is the same one as above, the unlocked read in `requireOpenLocation`, and the
expected result is that the close and the write both succeed, failing the
either-or assertion.
