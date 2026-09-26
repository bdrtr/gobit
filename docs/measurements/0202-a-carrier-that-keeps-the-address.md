# A carrier that keeps the address — measured 2026-09-26

The evidence behind [ADR 0202](../adr/0202-the-kit-checks-a-shipping-provider.md).

## 1. What was checked before

| Where | What |
|---|---|
| `core/providertest` | `Identity`, `UniqueIdentities`, `Classifier` |
| the box provider's compliance test | `Identity` only |
| `TestEveryProviderRunsTheComplianceSuite` | that a provider package's tests contain `providertest.` |
| ADR 0194 | "Nothing checks a plugin for that; the conformance kit does not yet cover shipping." |

The shipment rules were written in two godocs, `FulfillmentProvider`'s and
`CreateFulfillmentInput.Destination`'s, and in the box provider's own tests,
which an outside carrier does not run.

## 2. The providers the new gate matches

`TestAProviderRunsItsContractsSuite` reads the provider packages
`TestEveryProviderRunsTheComplianceSuite` finds and matches each by the input
its methods take:

| Input | Suite | Packages |
|---|---|---|
| `CreateFulfillmentInput` | `Fulfillment` | `internal/modules/fulfillment/manual` |
| `ClassifyInput` | `Classifier` | `plugins/aianthropic` |

With the box provider's compliance test as it was, the gate fails on it (G1).

## 3. Mutations

| # | Mutation | Killed by |
|---|---|---|
| F1 | the returned data not searched for markers | the kit's own test |
| F2 | a second shipment not compared with the first | the kit's own test |
| F3 | a failed second cancel ignored | the kit's own test |
| F4 | the metadata's marker not searched for | the kit's own test |
| F5 | the caller's idempotency key kept | the box provider's test: it refuses an empty key |
| F6 | the box provider returning the destination in its data | the box provider's test |
| F7 | the box provider refusing a second cancel | the box provider's test |
| F8 | the box provider opening a second shipment for a key | the box provider's test |
| G1 | the box provider running `Identity` alone | the gate |

F1 to F3 pass the box provider's test, which is correct: it obeys the rules the
mutants stop checking, and only the kit's own test holds a provider that breaks
them.
