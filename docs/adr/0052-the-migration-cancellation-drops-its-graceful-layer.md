# ADR 0052 — The migration cancellation drops its graceful layer

**Summary:** gobit no longer sends on golang-migrate's `GracefulStop` channel;
closing the connection is the whole of the cancellation. The send bought nothing
measurable and armed a data race in a dependency.

- **Status:** Accepted
- **Date:** 2026-09-08
- **Amends:** ADR 0003, whose first layer this removes

## Context

ADR 0003 cancels a migration in layers, and the first was a send on the
migrator's `GracefulStop` channel to stop the NEXT migration from starting. The
second — closing the connection — severs the in-flight statement.

golang-migrate v4.19.1 guards the migrator's `isLocked` field with a mutex and
leaves `isGracefulStop`, declared on the next line, guarded by nothing. Its
`stop` helper both reads and writes that field, and its `Up` entry point calls
`stop` from both goroutines it runs, with no happens-before edge between them.
The write executes only when something sends on the channel, and in this whole
repository one line did.

It surfaced as a red integration lane on 2026-09-08 that never reproduced: a
data race fails the run rather than an assertion, so it arrives as noise rather
than as a finding. v4.19.1 is the newest release; there is no version to upgrade
to.

Measurement: [measurements/0052](../measurements/0052-migration-cancellation-race.md).

## Decision

**gobit does not send on `GracefulStop`.** Closing the connection is the whole
of the cancellation, and `internal/arch/migration_cancel_test.go` refuses a send
that comes back.

## Consequences

- **The race is dormant rather than fixed.** The field is unexported, so it
  cannot be synchronized from outside; not arming it is the only remedy
  available to a consumer.
- **Nothing observable changed.** Measured both ways: the same error code, the
  same version, the same dirty flag, and the same regression test passing. That
  is the evidence the layer was redundant, and it is also the reason the removal
  is safe to make without a deprecation.
- **A cancelled run still leaves the version DIRTY**, and it always did. The
  graceful layer never prevented that — the measurement is the first time
  anybody looked.
- **ADR 0003's remaining layers stand.** The connection close, the bounded wait
  and the release on return are untouched, and its regression test pins the
  property end to end.
- **The gate has an expiry condition, written into its godoc.** When
  golang-migrate guards the field, the send becomes a free belt-and-braces layer
  again, ADR 0003's original argument stands on its own, and this gate should go.
- **One less layer is one less thing to reason about, and also one less thing
  between a cancelled migration and a wrong outcome.** The remaining defence is
  a single mechanism; if closing the connection ever stops severing a statement,
  nothing else is standing behind it.

## Rejected

- **Keep the send and document the race.** It leaves a known-red lane armed for
  a layer that was measured to change nothing.
- **Carry an upstream patch.** Vendoring or forking a dependency for one
  unguarded bool is out of proportion to what the field does.
- **Wait for upstream.** v4.19.1 is current; waiting means keeping the fault
  for an unbounded time with no owner.
- **Send only outside tests.** Behaviour that differs between the test lane and
  production makes the lane evidence about a program nobody runs.
