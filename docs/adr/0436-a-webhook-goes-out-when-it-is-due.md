# ADR 0436 — A webhook goes out when it is due

**Summary:** `plugins/webhookout` claims a delivery when it is queued or redriven and a retry when its step of the ladder ends, at once or when the pass in flight ends, through a worker `core/plugin` lets a plugin run while the server serves; claims take receivers in turn and one lock, a receiver's cap counts its leases per clock minute, and deliveries keep no order.
It costs four published names, a migration, up to eight requests in flight per process rather than per installation, and a job listing that mostly reads "nothing due".

- **Status:** Accepted
- **Date:** 2026-10-10
- **Amends:** [0275](0275-a-webhook-receiver-sets-its-rate.md), whose cap counts the leases of every claim per clock minute rather than per pass
- **Extends:** [0026](0026-the-published-surface-is-fourteen-packages.md), whose `core/plugin` gains `Worker`

Measurement: [measurements/0436](../measurements/0436-when-a-webhook-goes-out.md)

## Context

A marketplace running beside gobit saw its first webhook delivery arrive up to
a minute after the event, and its retries a pass later than the ladder says.
The subscriber only queues, and the one sender was the minute job, so a
delivery waited for the scheduler's next tick. A failure's backoff was timed
from after its request while the next pass came one tick after the pass that
made it, so a step of the ladder `retry.go` documents took a pass longer
whenever the request outlasted the pass's drift (D273). A pass said the rows it
had no budget for were left for the next pass while their lease kept them from
it (D274). Two claims running at once leased the same rows (D275). Nothing let
a plugin run a loop for the life of the server.

## Decision

A worker each serving process runs beside its jobs claims a delivery when its
row is queued or redriven, and a failed one when its step of the ladder ends,
at once or when its pass in flight ends; every claim takes receivers in turn,
takes one lock, measures what is due, its lease and its minute from the instant
it holds it, and counts a receiver's `max_per_minute` leases per clock minute,
and the minute job stays the backstop and the alarm. Deliveries keep no order
and arrive at least once, and a receiver reads the record the event names and
drops a repeat by its delivery id.

## Consequences

- A delivery is attempted by the first claim after it is queued, and a retry by
  the first after its step ends: at once, or when the pass in flight ends, at
  most forty-five seconds later. Receivers take turns in a claim, a delivery
  never tried first in its turn, so a receiver whose requests hang holds no
  other back longer (known limits). The ladder's window is `deliveryWindow`
  plus those waits.
- `core/plugin` gains `Worker`, `Worker.PluginName`, `Host.RegisterWorker` and
  `Host.Workers`; the server starts workers beside its jobs, cancels them first
  on shutdown and waits up to `SHUTDOWN_TIMEOUT` before the pool closes, and a
  worker with no name, no Run or a taken name stops startup. The subcommands,
  the MCP surface and `InProcess` start none.
- Claims take advisory lock class 9 one at a time and give up after five
  seconds; a claim never leases a row twice, and a claim's minute never moves a
  receiver's window back. Claims over a large due backlog hold the lock for
  their numbering, so many processes waking together after an outage queue on
  it and the last of them time out and try again within a minute.
- `max_per_minute` counts leases in `webhook_endpoint.rate_window` and
  `rate_claimed`, a lease released unattempted stays counted, and a lease is
  sent within its pass, so a receiver can be sent up to twice its cap within
  the sixty seconds around a minute's start.
- A process has at most eight attempts in flight, shared by its worker and the
  job's pass, so N processes can send a receiver 8N. An attempt in flight at
  shutdown finishes, in either pass, every outcome is written on a context of
  its own, and rows a pass did not attempt are released when it ends.
- The worker sleeps until the first row due after its last claim, a minute at
  most, and logs a successful pass at DEBUG. The minute job sends what no
  worker took and fails on the pile; its listing line mostly reads "nothing due".
- Migration 000004 of the plugin adds the two columns, a check and a due index;
  rolling it back returns the cap to counting per claim.

## Rejected

- Sending from the subscriber: the bus's retries last a second and a quarter and the Redis consumer is one loop.
- A job every few seconds: the runner ticks once a minute for every job, and a run cannot outlast its interval.
- Running the job on demand: it takes the job's lock, queues behind a pass to a dead receiver and records nothing.
- Kicking a pass from the subscriber without a worker: no plugin hook stops it before the pool closes or wakes it when a step ends.
- Claiming again while a pass is sending: overlapping passes of one worker for a wait bounded by one pass.
- A cap per receiver on each claim: a lone busy receiver would be held under the claim's limit for nothing.
- LISTEN/NOTIFY: a connection outside the pool per process, to wake a process the queuing one already is.
- One elected sender polling every second: a connection held for life and a poll forever, to keep eight in flight per installation.
- Leaving capped receivers to the minute pass: any cap would add up to a minute.
- A per-aggregate sequence number: only the publisher's transaction knows the order, and a lost or discarded delivery leaves a gap no receiver can wait out.
