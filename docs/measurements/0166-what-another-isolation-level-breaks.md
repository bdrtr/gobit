# What another isolation level breaks — measured 2026-09-24

The evidence behind [ADR 0166](../adr/0166-a-connection-at-another-isolation-level-is-refused.md).

[Measurement 0165](0165-what-a-point-buys.md) §5.2 found that the balance lock
is correct only at READ COMMITTED, named the level in the payment repository
(D119), and left the rest of the tree with one sentence: the other modules'
locks argue from the same level in their own godocs, and every one of their
transactions began at the server's default. This record measures what that
sentence was worth.

## 1. How a whole lane was run at another level

pgx reads `PGOPTIONS` from the environment when a DSN does not set `options`,
and every harness in the tree builds its DSN from a testcontainers connection
string that does not. Before anything was believed, a probe with the tree's
pgx version against `postgres:16-alpine` showed the variable reaching the
session:

```
default: read committed | plain Begin runs at: read committed
default: repeatable read | plain Begin runs at: repeatable read     (PGOPTIONS set)
```

The integration lane was then run on `b6400c9` with

```
PGOPTIONS='-c default_transaction_isolation=repeatable\ read' make test-integration
```

so every connection every test opened — the harnesses', the services', the
competitors' — started at REPEATABLE READ, the way it would on a database or a
role an operator had set.

## 2. What failed

Forty tests in fifteen packages. The same lane at the server's default was
green on the same tree the same day.

| Package | Test | What happened |
|---|---|---|
| `core/link` | `TestDefineIsSafeUnderConcurrency` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/e2e` | `TestEmptyLocationReservesLinesFromDifferentWarehouses` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/auth/repository` | `TestSetPasswordWaitsForAnInFlightDeleteAndThenRefuses` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/auth/repository` | `TestLinkWaitsForAnInFlightChannelDeleteAndThenRefuses` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/auth/repository` | `TestTheFailedAttemptCounterLosesNoIncrement` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/auth/repository` | `TestAnInvitationIsSpentByONEStatement` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/cart` | `TestConcurrentAddLineItemDoesNotCorruptLines` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/cart` | `TestConcurrentDifferentVariantAdditions` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/cart` | `TestTwoMergesRunningOppositeWaysDoNotDeadlock` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/customer` | `TestConcurrentDefaultAddressAssignmentsLeaveOneDefault` | a unique violation where the second assignment was meant to wait and win |
| `internal/modules/customer` | `TestACustomerBeingDeletedCannotTakeANewAddress` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/fulfillment` | `TestConcurrentCancelMakesOneProviderCall` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/fulfillment` | `TestConcurrentPolicyWritesLeaveNoTornRecord` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/inventory` | `TestACloseAndAStockWriteCannotBothWin` | **silent** — a close and a stock write both succeeded |
| `internal/modules/inventory` | `TestEszamanliReserveSonAdediTekKazanir` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/inventory` | `TestEszamanliReserveStoguAsmaz` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/inventory` | `TestReserveIleSeviyeYazmaKilitlenmez` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/inventory` | `TestReserveIleKalemSilmeKilitlenmez` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/inventory` | `TestEszamanliReleaseTekSeferDuser` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/invoice` | `TestConcurrentIssuesTakeDistinctConsecutiveNumbers` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/order` | `TestConcurrentCreditsCannotPassTheCeiling` | **silent** — 10,000 credited on a 6,100 order, every round |
| `internal/modules/order` | `TestConcurrentLineCancellationsCannotExceedTheLine` | **silent** — 16 cancellations won on a 3-unit line |
| `internal/modules/order` | `TestConcurrentOrdersCannotExceedTheLimitTogether` | **silent** — 8 of 8 orders through a limit that covers 1 |
| `internal/modules/order` | `TestWithdrawingAnExchangeTwiceUnderTheLockIsSafe` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/payment` | `TestConcurrentProviderCallsOnOneKeyOpenOneSession` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/pricing` | `TestConcurrentSetPricesDoesNotMerge` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/product` | `TestCreateVariantLosesRaceWithProductDeletion` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/product` | `TestTwoConcurrentReparentsCannotCloseARingBetweenThem` | **silent** — both moves applied: a ring |
| `internal/modules/promotion` | `TestEszamanliRedeemKullanimSinirindaTamOlarakSinirKadarKazanir` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/promotion` | `TestEszamanliRedeemAyniReferansIcinTekKayitYazar` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/promotion` | `TestEszamanliRedeemKampanyaButcesiniAsmaz` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/promotion` | `TestEszamanliReleaseSayaciBirKezDusurur` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/promotion` | `TestKuralEklemeSilinenPromosyonaYazmaz` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/promotion` | `TestYontemYazmaSilinenPromosyonaYazmaz` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/region` | `TestBolgeGuncellemeKilitAltindaOkur` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/region` | `TestUlkeAtamasiKilitAltindaOkur` | refused as Internal (500) where Conflict (409) was meant |
| `internal/modules/region` | `TestSilinmekteOlanBolgeyeUlkeEklenemez` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/region` | `TestEszamanliUlkeAtamasiTekKazanan` | serialization failure (40001) where a waiter was meant to proceed |
| `internal/modules/tax` | `TestAProvinceCannotJoinARootBeingDeleted` | refused as Internal (500) where NotFound (404) was meant |
| `internal/modules/tax` | `TestARateCannotJoinARegionBeingDeleted` | refused as Internal (500) where NotFound (404) was meant |

- serialization failure (40001): 25
- refused as Internal (500) where a 404 or a 409 was meant: 9
- silent: the invariant broken, every call answered with success: 5
- a unique violation: 1

The five silent ones are the class that matters. Each is a lock that is taken
and then a total read, and each read after the wait saw the world from before
the other commit — so the lock serialized the transactions and both decided on
the same stale total. Nothing failed: every call answered with success, and
only the invariant, read afterwards, said otherwise.

The loud ones are loud for the reason REPEATABLE READ gives: an UPDATE that
meets a row another transaction changed after the snapshot fails with 40001
instead of re-reading it. None of the code retries, because none of it was
written for a level that asks it to, so a waiter that was meant to proceed —
or to be refused with a 404 or a 409 — becomes a 500.

## 3. Why naming the level at each BEGIN would not have been enough

The payment module's failure is the argument. Its repository had named READ
COMMITTED on every transaction since D119, and it still failed one test:
`TestConcurrentProviderCallsOnOneKeyOpenOneSession`, three times
`could not serialize access due to concurrent update` from the manual
provider's `INSERT … ON CONFLICT DO NOTHING RETURNING *`. That statement runs
outside any transaction, as its own, and a statement of its own starts at the
session's default. Sixteen BEGINs began at the default — thirteen module
repositories' write transactions, the outbox relay, the link declaration and
the passkey store in contrib — besides the payment repository's, which names
its level, and the order and cart read views, which name theirs. Pinning each
would have left every statement outside them to the server, and a gate over
BEGIN calls would have been green while it did.

## 4. Where the guard stands, and what it does not cover

`core/db.New` is the only place the production tree builds a pool — searched
for `pgxpool.New`, `pgxpool.NewWithConfig` and `pgx.Connect` outside tests —
so an `AfterConnect` there sees every session the process opens. It reads
`SHOW default_transaction_isolation`, which works through a connection pooler,
and refuses anything but `read committed`. The error it returns is not wrapped
as "unreachable": the database answered, at the wrong level.

What it does not see: a pool built without core/db. The tests that build one
on purpose are the payment lock test's REPEATABLE READ case, which is how it
still proves the repository's own pin, and the guard's own tests. golang-migrate
opens its own connection for DDL, which no level changes.

## 5. The same lane, with the guard

The lane was run again on the guarded tree with the same `PGOPTIONS`. Every one
of the thirty-three packages in it that opens a database — every module, the
core packages with a store, the workflows, the plugins with a schema, the
root facade and the e2e harness — stopped at its first pool with
`db_isolation_unsupported`. None ran a test at REPEATABLE READ, silently or
otherwise. The lane's one other red package was `internal/arch`, whose
document gates ran while this record's index rows were still being written;
they are green on the committed tree.

Two things are not in that lane: the two separate Go modules under `contrib`,
which run in their own lane and build their pools through core/db like
everything else, and the smoke lane, which has its own case
and is the one an operator meets — `TestAProcessRefusesADatabaseThatStartsTransactionsAtAnotherLevel`
sets the level on the database, not in the DSN, and the binary does not start.

## 6. The mutations

| # | Mutation | What went red |
|---|---|---|
| G1 | the pool's `AfterConnect` not set | the startup refusal (both levels) and the drift test |
| G2 | the refusal reported as "unreachable" again | the startup refusal, on its code |
| G3 | SERIALIZABLE accepted | the startup refusal's serializable case only |
| G4 | the guard removed, through the real binary | `TestAProcessRefusesADatabaseThatStartsTransactionsAtAnotherLevel`: the process came up on a database defaulting to REPEATABLE READ and served |
| G5 | the payment repository's own pin removed | the payment lock test's two REPEATABLE READ shapes, on a pool built without core/db |
