# ADR 0160 — A command does not take the server's events

**Summary:** A process that opens the application to run a verb publishes to the
event bus and consumes nothing; only the two paths that answer requests take
their share.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0160](../measurements/0160-what-seed-was-eating.md)

## Context

Every verb in the dispatch opens the whole application — deliberately, and it is
why `seed` gets its schema from the modules themselves and `recover` reaches the
services it repairs. The alternative was a second composition root, which is the
copy this repository refuses.

Opening the application registers the modules, and registering a module
subscribes it to the event bus. On the in-memory bus that is harmless: the bus
is the process's own. On Redis it is not. A subscription there creates the
consumer group if it is missing and starts a goroutine reading from it, and
consumers in one group receive each message ONCE — which is how the bus scales
and how a command steals.

So `gobit seed` against a Redis installation joined the server's group and took
`order.placed`, `payment.captured` and every other topic the modules listen for,
for as long as it ran. What it took, it RAN — the notification module's
subscriber is registered in the same `Register` that subscribed, so a seed
command sent order confirmations. And what it had not acknowledged when the
process exited stayed in the pending list under a consumer name that never comes
back; the bus has no reclaim and no pending sweep.

## Decision

The assembly takes an event ROLE: the two paths that answer requests — the server
and the facade's in-process harness — consume, and every verb publishes only,
through a wrapper whose `Subscribe` accepts the registration, takes no message and
says so once in the log.

## Consequences

Publishing is untouched. A command that writes through a service writes an
outbox row in the same transaction and publishes directly after the commit, and
that direct half still reaches the real bus. Replacing the bus with an in-memory
one would have dropped it silently — the outbox would have carried the event,
which is a repair rather than a reason.

`Subscribe` succeeds and does nothing, a shape this repository normally refuses.
Refusing instead would fail `Register`, and a module that cannot be registered is
a command that cannot run. What makes the silence honest is the log line: a
process that quietly declines what it was asked is the failure this rule exists
against. The role is a parameter rather than a guess, because nothing in the
assembly can tell the two apart — a command and the server register the same
modules — and a call site that names no role does not compile.

The in-process harness still consumes, a deliberate exception with a cost:
bus is the in-memory one by default and its purpose is to let a test see a
subscriber fire; the limit is written down rather than removed.

Nothing changes on the in-memory bus, the default: the wrapper is applied there
too, so what a command’s subscribers do with an event they were never meant to
see does not depend on the backend.

## Rejected

**Giving a command the in-memory bus.** Its publishes would go nowhere: the
direct half of the house pattern dropped by the backend rather than by a
decision.

**Refusing Subscribe in a command.** `Register` would fail and the verb would not
run; the module cannot ask whether this process serves.

**A separate consumer group per command.** Fan-out runs the command's handlers IN
ADDITION to the server's — two order confirmations rather than none.

**Reclaiming the pending list instead.** A real gap and a different one: it makes
the loss recoverable rather than stopping it, and it belongs to the bus.
