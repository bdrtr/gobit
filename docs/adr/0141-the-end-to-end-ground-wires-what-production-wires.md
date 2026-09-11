# ADR 0141 — The end-to-end ground wires what production wires

**Summary:** A gate compares the flows `internal/app` imports with the flows
`internal/e2e` imports and refuses a difference. It costs a deliberate edit when
a flow is genuinely out of scope, and it closes the place two faults hid.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

The end-to-end ground wires the modules and flows by hand rather than calling the
production setup, deliberately: its own documentation says the order "FOLLOWS the
order in internal/app/app.go", and a ground that called the real setup would be
testing the setup. The cost of that copy is that it is a copy.

It had fallen behind by exactly one flow. `internal/app` wired seven and the
ground wired six, and the missing one was the cancellation flow — which is the
only flow in this repository driven entirely by the bus. Nothing resolves it and
nothing calls it, so an unsubscribed one does nothing, breaks no request and
reddens no test.

That is where D75 and D76 lived for weeks: a write-off that credited the shelf
with goods sitting in a box, and a parcel cancellation that released nothing.
Both were green against fakes, because a fake is written to the mechanism rather
than to what the endpoints can actually produce.

Measured: of the seven flows, the cancellation flow was the only one with no
appearance in the end-to-end suite at all.

Measurement: [measurements/0141](../measurements/0141-what-the-ground-was-missing.md)

## Decision

`internal/arch/e2e_ground_test.go` reads the flow packages imported by
`internal/app` and by `internal/e2e` and refuses one that production wires and the
ground does not. The cancellation flow is now wired on the ground, and a scenario
runs the whole write-off chain through the real endpoints.

## Consequences

The population is derived from IMPORTS rather than from constructor calls, because
a flow is wired by whatever name its package chose and a scan for `FromContainer`
would miss one that named its entry point anything else. An import cannot be
avoided.

The gate is about FLOWS and not modules, and the asymmetry is the argument: a
module that is not registered fails loudly — its routes are missing, its container
names do not resolve — so the first scenario that touches it goes red. A flow can
be silent, and silence is what needs a gate.

An exemption map exists and is empty. Adding a name to it is a deliberate,
reviewable admission that one flow's behavior is proven by nothing but its own
unit tests, which is better than the ground quietly falling behind again.

The scenario it enabled is worth its own sentence: three units bought, two put in
a parcel, all three written off, then the parcel canceled. It is the only test
that can see ADR 0134, 0135, 0139 and 0140 at once, and it fails in the two ways
that matter — with the binding removed the shelf reads ten instead of eight, which
is the shop crediting itself with goods that are about to ship; with the flow
unwired it reads seven, which is nothing coming back at all.

Its polling helper is written out at length because two drafts of it lied. A
condition function must not assert — testify runs it on its own goroutine and a
`require` there is `runtime.Goexit` on a goroutine that is not the test's, so the
tick dies silently. And the failure message must be built AFTER the poll, because
arguments are evaluated before the call: a live counter passed in reports the
value it had at zero seconds. Both drafts printed "last read: 0" for a shelf that
was not empty, which accuses the wrong mechanism.

## Rejected

- **Call the production setup from the ground.** It would make every scenario a
  test of the composition root, and a failure there would redden everything.
- **Compare the module lists too.** A missing module already fails loudly; a gate
  that also policed it would earn its keep only on the case that is already
  covered.
- **Require every flow to have a NAMED end-to-end scenario.** It would be a gate
  on test names rather than on wiring, and a scenario can exercise several flows
  without naming any of them.
