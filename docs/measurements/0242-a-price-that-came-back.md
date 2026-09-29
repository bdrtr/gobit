# A price that came back — measured 2026-09-29

The evidence behind [ADR 0242](../adr/0242-a-moment-the-process-reads-is-read-after-the-lock.md)
and D165.

## 1. The writers

| Writer | Clock read | Lock | Reader that depends on the order |
|---|---|---|---|
| `SetPrices` → `ReplacePrices` | `now := s.clock()` in the service, before the repository's transaction | `GetPriceSetForUpdate` | the price set history, `recorded_at, seq`; `standingSet` takes the last snapshot at a moment; `reductions` compares the storefront's live price with the timeline's |
| `UpdatePriceList` | `s.clock()` in the service | the `UPDATE`'s own row lock | the price list history, `standingList` |
| `Issue` (invoice) | `now` before the transaction; it chose the series' year and stamped `issued_at` | `TakeNextNumber`'s upsert on the series row | the series: number order and date order |

## 2. The reproductions

Two services share one clock that moves forward on every reading, as one wall
clock does. The waiting writer is held at the reading its stamp comes from
until released: the first for the list and the invoice, the second for the
price set, whose service reads the clock first for the prices' ids. The other
writer runs meanwhile, bounded by half a second so that a writer which reads
its clock under the lock cannot hang the test.

| Test | Before | After |
|---|---|---|
| `TestThePricesWrittenLastAreTheStandingOnes` (pricing) | the set held 9,000, written last; the latest snapshot said 8,000 | the latest snapshot holds the set's prices |
| `TestTheListUpdateWrittenLastIsTheStandingOne` (pricing) | written with the fix; M2 below is its before | the latest snapshot's status is the list's |
| `TestANumberIsNeverDatedBeforeTheOneBeforeIt` (invoice) | `DTS2026000000002` dated 1 ms before `DTS2026000000001` | not before |
| `TestAYearThatTurnsDuringTheWaitIsIssuedInTheNewYear` (invoice service) | the path did not exist | a reading of 23:59:59.9 on the 31st and one after midnight: numbered `GBT2027000000001`, dated 2027, four readings |

After the change the list test takes about half a second: the waiting update
reads its clock under the list's lock, so the other waits for it and the bound
elapses.

## 3. What did not change

- The price's id is made from the service's reading before the lock; ids are
  not an order anything reads.
- The pricing repository's two signatures take `func() time.Time`; the two test
  doubles call it at once, so their hooks keep their `time.Time`.
- `ReplacePrices`' and `UpdatePriceList`' godocs and the one inline comment in
  them were Turkish and were translated where they were touched.

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| M1 | the set's clock read in the repository before the lock | `TestThePricesWrittenLastAreTheStandingOnes` |
| M2 | the list not locked before its clock is read | `TestTheListUpdateWrittenLastIsTheStandingOne` |
| M3 | the invoice dated with the reading that chose its year | `TestANumberIsNeverDatedBeforeTheOneBeforeIt` |
| M4 | the two readings' years not compared | `TestAYearThatTurnsDuringTheWaitIsIssuedInTheNewYear` |
| M5 | no second issue after the year turned | the same |
| G1 | 0053's row without 0242 | `TestTheADRIndexNamesEveryAmendment` |

Six mutants, all killed. M1 survived the first run: the price test held the
writer's FIRST reading, the service's, so a repository that read its clock
before the lock still read it after the other write had finished. The test now
holds the second reading, and fails outright when the writer reads only once,
as the code before this record did.
