# ADR 0385 — A workflow step has no observer

**Summary:** The saga engine takes no hook, observer or step callback; a step's
timing and attempts are kept by its Store and read by SQL until a second saga.

- **Status:** Accepted
- **Date:** 2026-10-05

## Context

Hook points on the workflow engine, a `StepObserver` called around each step,
were refused on 2026-09-12 in the feature list alone, a file git does not track.

What an observer would carry is already kept. After every step the executor
hands `Store.AppendStep` a `StepRecord` with name, status, output, failure,
attempts, start and end, and the Postgres store writes it to
`workflow_execution_steps`. `gobit stuck` (ADR 0016) prints a held saga's steps
by name and status; nothing in the tree reads a step's attempts or timing.

One saga runs on the engine, the checkout. The engine is internal and is built
once in the composition root, so no program outside this repository could
register an observer. The engine's package comment asked that
compensation_failed be the first thing monitoring counts; the ERROR line written
with it, the error collector and `gobit stuck` are what count it. Metrics in
this tree are OpenTelemetry instruments read by scrape (ADR 0046), and none is a
workflow's.

Measurement: [measurements/0385](../measurements/0385-workflow-step-observers.md)

## Decision

**The saga engine takes no hook, observer or step callback, and a saga that
wants a span or a timer opens it inside its own steps.** The decision reopens
when a second saga package runs on the engine, because that is when the same
watching would be written twice.

## Consequences

- **Nothing moves for an installation.** No route, scope, migration, setting or
  published name changes.
- **How long a step took, and how often it was tried, is a query.** The step
  rows hold both, and no command prints them; that is the operator's cost. A
  compensated step's row ends at its compensation and counts Invoke's attempts
  only; compensation retries are in the log.
- **A failed step is logged at ERROR** with its workflow, execution, step and
  attempt. The error collector receives the workflow and the step; the
  execution and the attempt stay in the log.
- **A step's own span nests under the request's trace.** The checkout runs its
  saga on a context that keeps the request's values and drops its cancellation,
  so such a span may end after the request's.
- **Decorating the Store is not a seam.** A wrapper embedding `Store` hides
  `ClaimingStore` (ADR 0017), and recovery stops being exclusive.
- **Two gates hold this record.** `TestASecondSagaReopensTheStepObserverRefusal`
  fails the day a second saga lands, in any package.
  `TestTheWorkflowEngineOffersNoObserver` pins the engine's imports, the places
  it accepts code it does not own, and the Store's implementations. It reads
  syntax: a new member of a type the engine already names, the logger among
  them, passes it. Either failure is the cue to supersede this record, not to
  edit the gate.

## Rejected

- **A `StepObserver` passed through a run option.** It carries what
  `AppendStep` already writes, to a caller nobody has written.
- **An observer set on the executor in the composition root.** The same
  payload, and the root would hold a seam with no consumer.
- **A provider slot for an outside observer.** A plugin cannot name an internal
  record, and no plugin asks.
- **A bus event per step.** A topic needs a subscriber that chose it, a
  forwarder is not one (ADR 0063), and the topic gates already refuse it.
- **Engine-level spans or metrics per step, now.** One saga can instrument its
  own steps; the engine's version waits for a second.
- **Leaving the refusal in the feature list.** That file is not tracked, and a
  refusal nobody can read is reopened by the next person who counts the row.
