# A promise nobody could name — measured 2026-09-29

The evidence behind [ADR 0237](../adr/0237-a-withdrawn-replacement-gives-back-what-it-held.md)
and D159.

## 1. What there was

| Part | State |
|---|---|
| `holdStock` (returns flow) | per line: reserve, then write the promise on the line; a later line refused returns the error and leaves the earlier promises standing for the retry |
| `CancelReplacement` (order module) | flips the status; the module cannot reach the inventory |
| the admin cancel endpoint | called the order service directly |
| releasing a reservation | the flow's `releaseHeldStock`, only when the promise could not be recorded; no endpoint releases one by id |
| a reservation's lifetime | no expiry, no reaper in the inventory module |
| the flow's godoc | "a promise that no parcel ever carried is released by hand or expires with the record" |

## 2. The reproduction

`TestAWithdrawnReplacementGivesBackTheUnitsItHeld` (end to end): a replacement
names the order's line and a variant nothing stocks. The dispatch sets the
line's unit aside, is refused on the variant with
`returns_workflow_no_inventory_item`, and the sellable quantity is one lower.
The endpoint writing the record alone (mutant W5, the old behavior) withdraws it
with the unit still held; the test fails there. Through the flow, the unit is
sellable again and a second withdrawal answers the first.

## 3. The flow

| Test | Holds |
|---|---|
| `TestAWithdrawnReplacementGivesBackWhatItHeld` | the one held promise released, and only then the record withdrawn |
| `TestTheUnitsGoBackBeforeTheRecordIsWithdrawn` | a release that fails leaves the record open, `returns_workflow_stock_not_released` |
| `TestUnitsThatLeftWithAParcelAreNotWithdrawn` | a promise the inventory refuses as confirmed refuses the withdrawal and names the retry |
| `TestASentReplacementIsNotWithdrawn` | a dispatched record touches no promise |
| `TestAWithdrawalIsFinishedByAskingAgain` | a withdrawn record is released again and withdrawn again, both answered as done |
| `TestAdminWithdrawReplacementFailsClosedWithoutTheFlow` | no flow, no withdrawal |
| `TestAdminWithdrawReplacementReportsTheFlowsRefusal` | the flow's 409 reaches the caller |

## 4. Mutations

| # | Mutation | Killed by |
|---|---|---|
| W1 | no promise released | the flow's tests; the reproduction |
| W2 | the record withdrawn before the promises | the flow's tests |
| W3 | a confirmed promise read as released | `TestUnitsThatLeftWithAParcelAreNotWithdrawn` |
| W4 | a sent replacement taken into the flow | `TestASentReplacementIsNotWithdrawn` |
| W5 | the endpoint writing the record alone | the route tests; the reproduction |

Five mutants, all killed on the first run.
