# The inventory movement ledger — measured 2026-09-08

The evidence behind
[ADR 0068](../adr/0068-the-ledger-explains-the-count-and-does-not-become-it.md):
which code actually changes the physical count, what the four candidate reasons
turn out to be, what `audit_log` can and cannot say about a stock change, and
what the repository does about retention everywhere else.

## 1. Who changes `stocked_quantity` — the census

Two statements write the column, both in
`internal/modules/inventory/repository`: `CreateInventoryLevel` and
`UpdateInventoryLevelQuantities`. Nothing else in the module's `queries/`
directory names it, and no other module may (Principle 2.2, held by
`TestModuleSQLNamesOnlyItsOwnTables`).

Walking the service's callers of those two gives the whole population:

| Service flow | stocked | reserved | Reached from |
|---|---|---|---|
| `SetInventoryLevel` (level absent) | 0 → n | — | admin API, admin panel surface |
| `SetInventoryLevel` (level present) | n → m | unchanged | admin API, admin panel surface |
| `AdjustInventory` | n → n+d | unchanged | admin API |
| `Interop.Restock` → `AdjustInventory` | n → n+q | unchanged | the returns flow |
| `Reserve` | **unchanged** | r → r+q | checkout saga |
| `ReleaseReservation` | **unchanged** | r → r−q | checkout compensation |
| `ConfirmReservation` | n → n−q | r → r−q | checkout saga, last step |

Three of the seven move goods. The two reservation flows move a promise and
touch no physical unit, which is question one answered by the arithmetic rather
than by preference.

**The row's fourth candidate does not exist.** The leaning named "a seed" as a
source of movements. `internal/app/seed.go` was read end to end: it mentions
inventory three times, all of them in prose, and writes no `inventory_items`,
`inventory_levels` or `stock_locations` row. The component that DOES insert
stock directly is `internal/rig/catalog.go`, the load-test fixture, and it is
the one component in the tree allowed to name a table it does not own — per
table, listed, and audited (docs/gaps.md D13). `inventory_movements` is not on
its list, so the rig writes none and the gate stays green.

## 2. The reason ↔ source bijection, which is why there is one column

The leaning asked for a REASON and a SOURCE. Enumerated over the census above,
today's set is:

| Reason | Produced by | Any other producer? |
|---|---|---|
| `stock_count` | `SetInventoryLevel`, from the admin API or the panel | none |
| `adjustment` | `AdjustInventory`, from the admin API | none |
| `sale` | `ConfirmReservation`, from the checkout saga | none |
| `return_restock` | `RestockInventory`, from the returns flow | none |

Four reasons, four sources, one-to-one. A `source` column would hold a value
computable from `reason` by a four-entry table on every row of the module's
hottest write path — which is the shape ADR 0048 refuses four times over and
ADR 0056 refuses again ("one surface would write it; every other row would leave
it empty").

What the pair WOULD buy is the day the map stops being one-to-one: a warehouse
integration that counts stock produces `stock_count` and is not an operator.
That is the ADR's named trigger, and it is checkable — a second production caller
of `SetInventoryLevel` or `AdjustInventory` reopens the question.

## 3. What `audit_log` holds about a stock change, and what it cannot

`audit_log` columns: `id, actor_id, actor_kind, method, path, status,
request_id, created_at`. For `POST /admin/v1/inventory-items/{id}/levels` a row
therefore says: this user called this path and got a 200.

| Question an operator asks | `audit_log` | `inventory_movements` |
|---|---|---|
| Who did it | **yes** (admin paths) | no, by decision |
| Which item | no — the id is in the path, unparsed | yes |
| Which location | no — it is in the request BODY, unrecorded | yes |
| How much did it change | no | yes |
| What is it now | no | yes |
| Why | no | yes |
| Sale that took the units | **impossible** — the checkout is storefront | yes |

The last row is the decisive one and B7 states it: the checkout runs under
`POST /store/v1/carts/{id}/complete`, and storefront requests are not audited at
all, because what authenticates one is a publishable key that names a sales
channel rather than a person. So no widening of `audit_log` could ever put a
confirmed reservation in it.

## 4. Retention: what this repository actually does

Searched for a `DELETE`, a `TRUNCATE`, a partition, a scheduled job or a
configuration knob touching any ledger. Findings:

| Ledger | Retention in the tree | Where it is decided |
|---|---|---|
| `audit_log` | none | ADR 0037, "No retention, no deletion" |
| `outbox` | delivered rows kept | — |
| callback outcomes | a log line, no table | ADR 0056 parked it, ADR 0062 answered |
| order / payment | rows kept, columns dropped | ADR 0054 |

There is no retention mechanism anywhere in gobit for any record of this kind,
and ADR 0029 puts the window on the embedder. "Kept, and retention is the
operator's" is therefore a description of the repository rather than a policy
this record invents — which is what B7 asked for.

## 5. The boundary this ledger deliberately does not cover

`DeleteInventoryItem` soft-deletes the item's levels
(`SoftDeleteInventoryLevelsByItem`). Availability drops, and no movement is
written. That is consistent with the answer to question one rather than a hole:
the ledger explains `stocked_quantity`, and a soft delete does not change it —
the units are still on the shelf, the row is hidden. The deletion has its own
record in `deleted_at`, and the flow is refused while a reservation is active.

Had `stocked_quantity` been made derived, this case would have been a defect
instead of a boundary: the sum of the deltas would go on reporting stock for a
level no read can see.

## 6. The gates, and what mutation proved

Two gates in `internal/arch` and one refusal inside the module.

`TestEveryPhysicalStockWriteGoesThroughTheLedger` walks the CALL GRAPH of the
whole production tree — the same scan the consumer audits use — and requires
every call to `CreateInventoryLevel`, `UpdateInventoryLevelQuantities` and
`AppendMovement` to sit inside the one function named for it. A directory scan
over the inventory service was the first draft and was rejected: it would be
checking that a package obeys itself, and the failure worth catching is a caller
appearing somewhere new.

Population: every call site in `internal`, `core`, `cmd`, `plugins` and the root
— not derived from the property. The repository's own same-name delegation
(`func (r *Repository) AppendMovement` calling the generated
`AppendMovement`) is skipped as the implementation. Floors, both of which fail
loudly: at least one real consumer per entry, and every choke point has to exist
as a function in the source, so renaming one turns the audit red rather than off.

`TestTheStockLedgerIsAppendOnlyInSQL` reads the module's query files and refuses
an `UPDATE` or a `DELETE` against the table, with the same shape of floor: the
scan has to find the table at all.

Mutations, each run with `-count=1` and reverted from a copy taken aside:

| Mutation | Expected | Result |
|---|---|---|
| Call `UpdateInventoryLevelQuantities` straight from `Reserve` | arch gate red | **red** — named the file, the line and the function |
| Delete the `requireTx` guard from `AppendMovement` | repository test red | **red** — a nil-pool panic, which the test's own godoc predicts |
| The same, against a live database | integration test red | **red** — the append succeeded outside a transaction |
| Skip the append when the reason is `sale` | service tests red | **red**, two of them |
| Drop the `sale ⇔ reservation_id` CHECK from the migration | integration test red | **red**, on both directions of the equivalence |
| Replace the listing index with one on `id` | plan test red | **red** — Seq Scan over 5,000 rows plus a Sort |
| Add a `DELETE FROM inventory_movements` query | append-only gate red | **red** |

The last two are the ones worth reading twice. The plan test seeds 5,000 rows
and runs `ANALYZE` before it reads the plan, and the seeding is what makes it a
measurement: on an empty relation every plan costs nothing and the planner takes
an index scan whatever the predicate looks like, so the assertion would have held
against a wrong index. With the rows in place the wrong index produces exactly
the failure the ledger's reader exists to avoid — a full scan and a sort, at
every page.

And the second mutation is a reminder about what a guard costs when it is
removed rather than weakened: deleting the transaction check does not make the
test fail politely, it makes the process crash on a nil pool. That is still a
failure, and the test says so in its own documentation rather than pretending
the refusal is what the driver would do anyway.

## 7. One rule was measured wrong, and the suite caught it

The first draft of `recordMovement` refused a reason offered for a write that
changed nothing, on the grounds that a reason with no delta is a movement of
zero units. It was wrong about a real path, and the module's own integration
suite found it: `TestReserveIleSeviyeYazmaKilitlenmez` sets a level to the count
it already holds, and the call started returning
`inventory_inconsistent_state`.

An operator who counts a shelf and writes the number that was already there has
done something real — they confirmed it — and it moved nothing. The rule is now
one-directional: a delta of zero records nothing whatever the caller meant, and
a delta with no reason is still `errors.Internal`. The case has its own unit test
so the strictness cannot come back by accident.
