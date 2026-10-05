# ADR 0398 — The end-to-end ground opens the installation the server serves

**Summary:** `internal/e2e` brings its ground up through `app.Open`, the assembly the server and `App.InProcess` share, and adds only what an embedder adds.
It costs one failure in the composition root reddening the whole suite, and production's password cost in every run.

- **Status:** Accepted
- **Date:** 2026-10-05
- **Supersedes:** [0141](0141-the-end-to-end-ground-wires-what-production-wires.md)
- **Amends:** [0150](0150-an-embedder-can-bring-gobit-up-in-a-test.md), whose one assembly now has this repository's ground as a caller
- **Amends:** [0210](0210-a-sold-gift-card-is-issued-when-its-order-is-paid.md), whose gate holding production to the ground's flows is retired

Measurement: [measurements/0398](../measurements/0398-the-ground-and-the-assembly.md)

## Context

The ground built its own container, registry, guard stack and router, and gates
held the copy's flows, modules and describe loop to the root. Nothing held its
guard stack, and it had drifted: accepting an invitation was not exempt, there
was no audit, panel or callback ring, and the personal-data and audit-log
endpoints were never mounted (D254). The gate holding production to the
ground's flows counted imports, and the gift card sweep's import kept it green
while production could drop the flow's subscription (D255).

ADR 0141 rejected calling the production setup because a failure there would
redden every scenario. A failure there stops every server too, and a ground that
stays green while the root fails proves a build nobody ships.

## Decision

`internal/e2e` brings its ground up through `app.Open`, which `App.InProcess`
and the server's assembly share, and sets only what an embedder sets: a cleared
and then set environment under the shared profile, a module providing the
storefront identity, and two plugins carrying the spies and event logs.
`app.Open` returns the router, container, modules, served schema and plugin
jobs, and only the facade and `internal/e2e` may import `internal/app`.

## Consequences

- There is one composition. The gates ADR 0141, ADR 0210 and ADR 0036 added
  are deleted with their maps; nothing compares two lists.
- The ground cannot stand in for production. It provides no name outside a
  module's Register, subscribes nowhere but a plugin's Setup and refers to no
  function of a flow that subscribes when built, so a bus-driven flow
  `registerWorkflows` stops wiring stops in its scenarios.
  `TestTheEndToEndGroundIsTheProductionAssembly` holds those rules;
  `TestOnlyTheFacadeAndTheGroundOpenTheCompositionRoot` holds the import rule,
  contrib's modules included.
- The ground proves production's guard stack. An invitation is accepted without
  an identity, an admin write is read back from the audit log, the panel tree
  enters the authorization matrix, and the personal-data and audit-log
  endpoints enter the walk and the privilege test. The callback ring runs, and
  no scenario registers a callback.
- A failure in the composition root reddens every scenario at once, with the
  error the server would print at startup. That is the cost ADR 0141 named.
- The environment is the ground's. TestMain clears every variable the
  configuration reads before setting its own, once, so scenarios stay parallel
  and a shell's exports cannot change the ground.
- The profile is staging. It is shared and not production, so the manual
  payment provider stays and the first administrator comes from the seed step.
- It costs time. Passwords are hashed at production's cost because no setting
  lowers it; a cost setting is a separate decision.
- The facade does not change. `InProcess` is `Open` keeping only the router,
  and ADR 0150's refusal to publish the container stands.
- Not yet the server's: the panel scenarios' own router, and the stock alert
  and segment passes the ground builds itself.

## Rejected

- The facade's `InProcess` as it is: it returns a handler, and the ground reads
  services, flows, jobs and the schema.
- Publishing the container: ADR 0150 refused it, and the ground needs no
  published name.
- Keeping the copy and gating its guard stack too: another comparison gate for
  a copy that already drifted where no gate looked.
- Test fields on `app.Options`: a second configuration path, which ADR 0150
  refused.
- `APP_ENV=development`: it hides every shared-profile branch from the suite.
- Every scenario over HTTP: the fixtures beyond the seeded administrator are
  read and written through services, as they were.
