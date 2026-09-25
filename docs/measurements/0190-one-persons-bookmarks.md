# One person's bookmarks — measured 2026-09-26

The evidence behind [ADR 0190](../adr/0190-a-customer-keeps-a-wishlist.md).

## 1. The index

A scratch PostgreSQL 16 with the customer module's migrations, 50,000 customers,
one in ten with a full wishlist of 200 and the rest with five: 1,225,000 rows.
`EXPLAIN ANALYZE` of one full list:

| Read | Key and a listing index `(customer_id, created_at DESC, variant_id)` | Key only |
|---|---|---|
| the list, newest first | Index Only Scan on the listing index, 0.117 ms | Index Scan on the key and a quicksort of 200 rows in 45 kB, 0.147 ms |
| the count under the lock | Index Only Scan, 0.089 ms | Index Only Scan on the key, 0.053 ms |
| the disclosure, two customers | not measured | Index Scan on the key and an incremental sort, 0.171 ms |
| the erasure's delete | not measured | Index Scan on the key, 0.141 ms |
| one item | not measured | Index Only Scan on the key, 0.067 ms |

| Relation | Size |
|---|---|
| the table | 128 MB |
| the listing index | 123 MB |
| the key | 112 MB |

The listing index was dropped from the migration before it shipped.

## 2. The lock

The first concurrency test ran sixteen saves of different variants against a
list with one place left and asserted one winner. It passed with the customer
row's lock removed from the save: the window between the count and the insert
did not open between goroutines.

`TestASaveWaitsForTheLockBeforeItCounts` opens it. A rival transaction locks
the customer row and inserts the last place without committing; the save under
test waits, and after the commit it has to be refused as full with the table at
200. The insert's foreign key takes KEY SHARE on the same row, so a save without
the lock waits too, at its insert, having counted one place free. The answer
after waking is what tells the two apart.

## 3. Mutations

| Mutation | Result |
|---|---|
| the save reads the customer without the lock | survived the racers; red: `TestASaveWaitsForTheLockBeforeItCounts` |
| the cap compared with `>` | red: the full-list and the lock tests |
| a saved variant not returned before the count | red: `TestAVariantSavedTwiceIsOneRow` |
| the erasure keeps the wishlist | red: `TestAnErasureDeletesTheWishlist` |
| the disclosure leaves the wishlist out | red: `TestADisclosureListsTheWishlist` |
| the service passes the cap plus one | red: `TestTheWishlistCapIsTheModelsConstant` |
| a removal naming no customer | red: `TestAnotherCustomersWishlistIsNotRead` |
| a removal scoped by the variant alone | red: `TestAnotherCustomersWishlistIsNotRead` |
| the list read oldest first | red: `TestTheWishlistIsReadNewestFirstFromTheTable` |

The last three mutated the query constants sqlc generated. The same edits to
the `.sql` source changed nothing that ran and survived.

## 4. The gates the table moved

| Gate | What it asked |
|---|---|
| `TestNoStorefrontWriteStoresAnUnjudgedIdentityClaim` | the path's customer id recorded as a confined claim |
| `TestPersonalDataCoversEveryPersonalColumn` | the new table named; `variant_id` declared, the rest exempt |
| `TestEveryDeclaredTableIsDisclosedOrExempt` | the table disclosed |
| `TestTheAuthorizationMatrixHoldsForEveryEndpoint` | two more patterns that a publishable key may not reach |
| `TestTheMigrationCanBeRolledBack` | the table in the list; the version now read from the migration set |

The column audit first read `CHECK` as a column, from a `CONSTRAINT` written
over two lines. The constraint is on one line now, as the other migrations
write theirs.
