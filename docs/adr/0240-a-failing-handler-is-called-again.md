# ADR 0240 — A failing handler is called again

**Summary:** The event bus calls a handler that returns an error twice more,
a quarter of a second and then a second later, before it logs the error and
counts the event as processed; an invalid event and a panic are not repeated.

- **Status:** Accepted; amended by [0420](0420-every-parcel-waits-for-its-orders-lock.md), whose Redis bus leaves a message pending when its handler still fails during the bus's shutdown
- **Date:** 2026-09-29
- **Amends:** [0239](0239-a-canceled-parcel-recalls-its-replacement.md), whose recall the bus did not repeat

Measurement: [measurements/0240](../measurements/0240-thirteen-handlers-and-one-try.md)

## Context

The bus logged a handler's error and counted the event as processed, in both
backends, so that a broken event could not lock a consumer in a loop. Four
handlers said the bus tries again and returned errors for faults that may pass,
expecting it (D163). An outbox topic reaches its handlers a second time when the
relay publishes the row a minute later, whatever the first call returned; a
fault during both calls, or during the one call a directly published product
event gets, lost what the handler was for: written-off units off the shelf, a
payment summary, a confirmation e-mail, a search index row, a webhook. Only the
gift card sale has a sweep behind it.

## Decision

A handler that returns an error is called again, at most twice, after a
quarter of a second and then a second, and only then is its error logged and the
event counted as processed. An error of `errors.KindInvalid` and a panic are not
repeated.

## Consequences

- The retry is the failing handler's alone; the other handlers of the event run
  once. Each call gets its own copy of the event.
- A handler returns an error for a fault that may pass and nil for one that
  never will, which is what the four handlers already did. It has to be
  idempotent, as the bus already required.
- On the Redis backend a stream's next message waits for the retries, at most a
  second and a quarter per failing handler.
- An outage longer than that is still logged and lost, and there is still no
  dead letter for a handler; the outbox relay's second delivery of an outbox
  topic, a minute later, remains the only other chance.
- Every comment that described the old policy, and the four that promised a
  retry, now say what the bus does.

## Rejected

- **Leaving the message un-ACKed on Redis.** The bus takes over only another
  consumer's pending message, so one process would never see it again, and the
  in-memory bus has no redelivery at all.
- **A retry wrapper each handler opts into.** Every handler that returns an
  error already means "this may pass"; opting in would leave the next one as
  the four were.
- **A longer schedule.** Minutes of waiting would stall a Redis stream behind
  one handler, and an outage that long needs a sweep, not a wait.
