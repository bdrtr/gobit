# Eleven shared rollbacks — measured 2026-09-26

The evidence behind [ADR 0201](../adr/0201-a-rollback-runs-in-a-database-of-its-own.md).

## 1. The tests that rolled back against the shared address

`TestARollbackRunsInADatabaseOfItsOwn`, run on the tree before this change,
reported fifteen calls in eleven files: D141's list, found by the call rather
than by memory.

| File | Calls | Data in place before the rollback | Asserted afterwards |
|---|---|---|---|
| `core/db/db_integration_test.go` | 5 | the test's own `alpha` and `rollback*` owners | versions |
| b2b | 1 | a company and an employee | the version, no `b2b_company` row left |
| cart | 1 | — | the tables, the version |
| customer | 1 | — | the tables, the version |
| fulfillment | 1 | an option, a rule, a parcel, a location policy | the tables, the version |
| inventory | 1 | — | the tables, the version |
| promotion | 1 | — | the tables, the version |
| region | 1 | a live and a soft-deleted region | the seed, no `region` row left |
| tax | 1 | a region tree, rates, a rule, a deleted rate | the version, no `tax_region` row left |
| analytics plugin | 1 | an event | the table |
| searchpg plugin | 1 | — | a search on the reapplied schema |

In the shared database the three "no row left" assertions were statements
about every test's rows, all of which the rollback had just dropped with the
schema. `core/db`'s owners
are its own and nothing else writes them; it moved for the rule to have no
exception.

## 2. The helpers that already existed

| File | Helper | Dropped its database |
|---|---|---|
| `internal/core/workflow/pgstore` | `newDatabase` | yes |
| `internal/core/job/jobpg` | `freshDatabase` | yes |
| `internal/modules/product` | `newDatabase` (two files call it) | yes |
| `plugins/webpush` | `freshDatabase` | yes |
| `plugins/paymentpaytr` | `freshDatabase` | yes |
| `plugins/webhookout` | `freshDatabase` | yes |
| `core/eventbus/outbox` | `freshDatabase` | yes |
| `internal/modules/payment` | `isolatedDatabase` | no |
| `internal/modules/order` | `isolatedDatabase` | no |
| `internal/modules/pricing`, migrations | inline | no |
| `internal/modules/pricing`, history seed | inline, a fixed name dropped before the next run | no |

All eleven call `internal/testdb` now. Two creators stay as they were, since
neither rolls back: `core/db`'s isolation test, which runs `ALTER DATABASE` on
its database by name, and the smoke lane's per-scenario databases.

## 3. Mutations

| # | Mutation | Killed by |
|---|---|---|
| T1 | `testdb.New` returning the shared address | the helper's test; cart's rollback test still passed |
| T2 | the database never dropped | the helper's test |
| T3 | `TableExists` always true | the helper's test, cart's rollback test |
| T4 | cart's rollback back on `testDSN` | the gate |
| T5 | region's | the gate |
| T6 | `core/db`'s | the gate |
| T7 | searchpg's | the gate |

T1 is the reason the helper has a test of its own: a helper that handed back
the shared address would put every moved test back where it was, and each of
them would stay green.
