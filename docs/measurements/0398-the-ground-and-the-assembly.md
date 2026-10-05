# The ground and the assembly — measured 2026-10-05

Evidence for [ADR 0398](../adr/0398-the-end-to-end-ground-opens-the-installation-the-server-serves.md)
and gaps D254 and D255. Read on a tree at 21f452f2, every Go command as
`GOTOOLCHAIN=go1.26.6 GOMAXPROCS=2 nice -n 19`, every run with `-count=1` and
the whole `internal/e2e` package, never `-run`. No run used `-race`.

## 1. What the hand-built ground differed in

The ground in `internal/e2e/e2e_test.go` built its own container, registry,
guard stack and router. Against `internal/app` it differed in:

| Ground at 21f452f2 | Production |
|---|---|
| `AdminExempt` held the sign-in alone | `guards.go` exempts the sign-in and `authapi.AcceptInvitationPath` |
| no `Audit`, `AuditedReads` or `OpenPrefixes` | all three set in `guardStack` |
| `LimitKey: corehttp.ClientIPKey` | `clientKey(cfg)`, the trusted-proxy key |
| no API session, security headers, origin check, `panel.Protect` or callback ring | `withPanelRing` |
| panel not mounted | `registerPanel` |
| erasure and audit-log routes not mounted | `registerErasure`, `registerAuditLog` |
| core migrations: pgstore and outbox | every source in `coreMigrationSources` |
| `bcrypt.MinCost` | the auth module's default cost; no setting changes it |
| no provider checks, no first-administrator seed | `verifyNotificationProvider`, `verifyFileProvider`, `seedAdmin` |

`docs/operating.md` said the ground ran "the same guard stack" as production.
No gate compared guard options: the three gates compared flow imports, module
registrations and the describe loop.

## 2. The gift card subscription (D255)

Mutation at 21f452f2: the `giftcardsalewf.FromContainer` call deleted from
`registerWorkflows` in `internal/app/setup.go`, with that file's import of the
package (the import is per file; `jobs.go` keeps its own for the sweep, ADR
0212). The mutant compiles.

| Lane | Result |
|---|---|
| `go test -tags integration ./internal/e2e/` | ok, 41.3s |
| `go test ./internal/app/` | ok |
| `go test ./internal/arch/` | the flow-import gate and the workflow setup gate pass; the only failures were the git-dependent gates, because the copy under test was not a git checkout |

The ground subscribed its own copy of the flow, the import gate read the
sweep's import in `jobs.go`, and the setup gate accepted
`SweeperFromContainer` as the package's constructor.

## 3. The two new tests on the old ground

Both written first, then run on the ground at 21f452f2:

| Test | Old ground | New ground |
|---|---|---|
| `TestAnInviteeSetsAPasswordWithoutAnIdentity` | 401 `unauthenticated` on the accept request | pass |
| `TestAnAdminWriteIsReadBackFromTheAuditLog` | 404 on `/admin/v1/audit-log` | pass |

## 4. Duration

| Ground | `go test` reported | wall |
|---|---|---|
| hand-built, 21f452f2 | 43.1s | 1:09 |
| `app.Open` | 62.1s | 1:15 |

The difference is the production password cost and the extra start-up steps.
The package held 339 `func Test` at 21f452f2 (TestMain included) and 341 after.

## 5. Mutants on the new ground

Each mutant was run against the whole package after a green base run.

| Mutant | Killed by |
|---|---|
| `giftcardsalewf.FromContainer` deleted from `registerWorkflows` | `TestABoughtGiftCardIsMailedAndSpent`, `TestABoughtCardIsClosedAndNotReturned` |
| `ordercancelwf.FromContainer` call replaced with `var _ = ordercancelwf.FromContainer` | `TestAWriteOffAndThenACanceledParcelPutEveryUnitBack` and six more |
| `Open` takes `app.router` without `assemble` | TestMain (no administrator was seeded); the root package's `TestAnEmbedderCanBringAnInstallationUpInItsOwnProcess` |
| `authapi.AcceptInvitationPath` dropped from `AdminExempt` | `TestAnInviteeSetsAPasswordWithoutAnIdentity` |
| `Audit: nil` in `guardStack` | `TestAnAdminWriteIsReadBackFromTheAuditLog` |
| `RequireScope` dropped from the audit-log route | `TestUnauthorizedIdentityCanDoNoWorkOnAnyAdminEndpoint`, `TestEveryOperationNamesThePrivilegeItsRouteRefuses` |
| `panel.Protect` dropped from `withPanelRing` | `TestTheAuthorizationMatrixHoldsForEveryEndpoint` (the panel row) |
| `panel.CheckOrigin` dropped from `withPanelRing` | the panel row, 78 write rows: with no Origin they answer 401 where 403 is owed |
| `UI.Protect` passes every method but GET | the panel row, 73 write rows: a same-origin write reaches the handler (72 answer 403, one 303) where 401 is owed |
| the `seedAdmin` call skipped | TestMain: "the seed step created no administrator" |
| `describeInstallation` without `describeAuditLog` | `TestEveryRealRouteIsDescribed` |
| `ManualProvider: cfg.IsProduction()` | 119 tests |
| `PersonBoundTenders: cfg.StorefrontTrustsUnverifiedCustomerClaim` | 10 tests, the store credit and points scenarios |

The narrow `audit:read` key does not kill the `RequireScope` mutant: removing
the scope widens the route, and the narrow key still reads it.

The panel row first asserted only that an anonymous write was refused with some
status of 400 or more. The origin check stands before identity and refuses a
write that carries no Origin, so both panel mutants above left the matrix test
green under it (run alone, on a fresh container). The row now sends a write twice: with no Origin it owes
403, and with the panel's own origin it owes 401.

## 6. Mutants on the two gates

| Mutant | Killed by |
|---|---|
| a test in `internal/e2e` calls `ctr.Provide` | `TestTheEndToEndGroundIsTheProductionAssembly` |
| a function in `internal/e2e` calls `Subscribe` on a bus | the same |
| `internal/e2e` calls `ordercancelwf.New` | the same |
| a second `app.Open` call in `internal/e2e` | the same |
| the subscribing-flow scan looks for `Subscribes` | the same (the floor: `giftcardsale`, `ordercancel`, `returns`) |
| `returns` subscribes through a `w.listen(bus)` helper, and `internal/e2e` refers to `returnswf.FromContainer` | the same; the first version, which read `FromContainer`'s body alone, stayed green |
| `returns` subscribes through a function value, which the scan cannot see | the same, by the floor; the first version dropped the flow from the set and stayed green |
| the scan counts `Provides` instead of `Provide` | the same (no Provide seen) |
| `internal/smoke` test file imports `internal/app` | `TestOnlyTheFacadeAndTheGroundOpenTheCompositionRoot` |
| `contrib/identity-session` test file imports `internal/app` | the same, parsed rather than compiled |
| the sibling-module prefix made to match no module | the same (no nested file read) |

The subscribing flows the scan finds are `giftcardsale`, `ordercancel` and
`returns`. A package counts when any of its non-test functions or methods
calls `Subscribe`, and the three are pinned as a floor under the derived set.
