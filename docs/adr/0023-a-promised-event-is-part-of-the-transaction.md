# ADR 0023 — A promised event is part of the transaction that promised it

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap

## Context

A module commits its work and then publishes. `order/service/order.go` states
the ordering plainly — the order and everything belonging to it commit in a
single transaction, and only then is `order.placed` published, because *"a
publishing failure does not drop the order"*.

The event bus is equally honest about its own guarantee
(`core/eventbus/eventbus.go`): the in-memory backend is at-most-once
and loses events when the process dies; the Redis backend is at-least-once and
resumes where it left off.

**Neither statement covers the window between the two.** If the process dies
after the commit and before the publish, the order exists and the event never
happened: no confirmation mail is sent, and nothing anywhere records that one is
owed.

This is the same shape as the hole ADR 0020 closed one level down, about money —
a committed local fact whose downstream effect silently did not occur — and the
measured gap inventory named it as the framework's remaining correctness defect.
The word "outbox" appeared exactly once in the repository, as a hypothetical in
a comment.

## Decision

**The event is written INSIDE the transaction that promised it, and a scheduled
relay publishes it.**

- `core/eventbus/outbox` owns the `event_outbox` table and the writing
  rule.
- The module writes the row through its OWN repository, which is the only side
  inside its transaction.
- `internal/jobs/outboxrelay` sends the pending rows every minute and marks
  them.
- The direct publish STAYS, as the fast path.

## Rejected options

**A. Reading the transaction from the context in core.** The obvious design, and
it is not available: every module keeps its transaction under its own
UNEXPORTED context key. The core cannot see it, and that is deliberate — a
module's transaction is its own. Making it shared would mean a context key every
module has to agree on, which is a coupling far larger than this problem.

So the writer takes the executor as an ARGUMENT. The core owns the table and the
rule; the module's repository is the hand that reaches into the transaction.

**B. Publishing only through the outbox and dropping the direct publish.** It
would make one delivery path instead of two, which is simpler to reason about.
Rejected because it makes every subscriber up to a minute late for no gain in
correctness: the row is the guarantee, the direct publish is the speed, and a
confirmation mail that arrives a minute after checkout is a worse product for an
identical promise.

The two cannot become two events, and the ID is what makes that true: both
carry the same one, derived from the order rather than random. A subscriber
idempotent on the event id — which the bus's at-least-once contract ALREADY
requires — cannot tell them apart.

**C. A goroutine instead of a job.** It would restart with the process and would
have to decide on its own how often to look and what to do about an event it
could not send. The scheduler answers both, and it records the run so
`gobit jobs` can say whether the relay happened at all. A relay nobody can see
the last run of is a relay nobody can trust.

**D. A status column instead of `published_at`.** There are two states and the
timestamp carries both, plus WHEN. A status would need the timestamp anyway and
could then disagree with it.

**E. Generating the event id in the outbox.** Then a retried write would insert
a SECOND row instead of landing on the same one, which is the duplicate this
table exists to prevent. The caller supplies the id because the caller is the
only side that can make a retry idempotent.

## Consequences

**A failure to write the event now FAILS the order.** Unlike the publish, the
outbox write returns its error and the transaction rolls back. An order written
without its promised event is exactly the state this decision exists to prevent,
and accepting it silently would leave the guarantee looking present while it was
not.

**The relay runs every minute, and that is the one short interval in this
repository.** `sagawatch` and `paymentrecon` report things that have already
been wrong for a while, so finding them sooner changes nothing. Here the delay
IS the damage: what waits is a message somebody is expecting about an order they
have already paid for.

**More than one instance can relay at once.** The rows are taken with
`FOR UPDATE SKIP LOCKED`, so a second relay steps over what the first is
sending. The lock IS the claim, held until the transaction ends — there is
nothing to expire and nothing to reap, which is the same reasoning ADR 0019 used
to choose an advisory lock over a lease.

**This job ACTS, and that does not contradict ADR 0017.** That decision refuses
SCHEDULED COMPENSATION — undoing work unwatched. This undoes nothing. It
delivers a message a person's request already decided to send; all that was
missing was the delivery.

**Only the order module writes through it today.** Every other publisher still
has the window. That is deliberate rather than unfinished: `order.placed` is the
event with a real subscriber (notification), and converting publishers with no
subscriber would be the error class ADR 0009 names. The mechanism is there for
the next one.

**A permanently failing event is visible rather than merely slow.** The row
keeps an attempt count and the last error, and the relay logs failures at ERROR.
Without the count, a row written a second ago and a row that has failed two
hundred times look identical.

## Reopening

The direct publish becomes removable the day every subscriber is provably
idempotent on the event id and the extra minute is acceptable. Nothing else
changes then; the relay already sends everything.

The `Execer` argument becomes unnecessary if the repository ever grows a shared
transaction context across modules. That would be a larger decision than this
one and should be made for its own reasons, not for this.

## Related

- ADR 0009 — a capability with no consumer. `order.placed` is why this is built
  now and why nothing else was converted.
- ADR 0017 — nothing scheduled undoes work. This delivers rather than undoes.
- ADR 0019 — the scheduler this relay runs on, and the lock-versus-lease
  argument reused here.
- ADR 0020 — the same defect one level down: a committed fact whose downstream
  effect silently did not happen.
