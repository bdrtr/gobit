# ADR 0162 — A message a dead consumer was holding comes back

**Summary:** The Redis bus takes over a message left unacknowledged under
another consumer's name once it has been idle past a threshold, which is what
makes at-least-once true for the case durability exists for.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0162](../measurements/0162-what-a-killed-consumer-leaves-behind.md)

## Context

XREADGROUP hands a message to one consumer and remembers that it did. If that
process dies before the ACK, the message stays in ITS pending list — and a
restarted process comes back as `<hostname>-<pid>`, a name that never existed
before, so it reads an empty list of its own and the ">" marker never offers the
message again. Nothing in the bus read a list belonging to a name that will not
come back.

Measured against a real Redis before anything was written: a consumer took one
message and stopped, a second consumer came up, and after ten seconds it had
received nothing while the entry sat there with its idle time growing (D105).

The package comment has promised at-least-once delivery since the backend was
written. It held for every message except the ones in flight when a process
dies, which is the case a durable bus is bought for.

## Decision

A consume loop sweeps its own stream between two reads: messages pending under
ANOTHER consumer's name, idle longer than `RedisConfig.ClaimMinIdle` (a minute
by default), are claimed with XCLAIM and dispatched through the same path as a
new message. A message already delivered three times without an ACK is
acknowledged and logged at error level instead of being handed on.

## Consequences

At-least-once now covers the death of a consumer, which is the only thing that
ever made a second delivery happen here: a handler's error and a handler's panic
are both ACKed, deliberately and unchanged. Handlers in this system already
compute a TARGET rather than a difference, which is what makes a second delivery
harmless; that rule predates this record and is why the takeover is safe to
build at all.

A consumer that is merely slow leaves the same pending entry as a dead one. The
threshold is the only thing that separates them, so it must be longer than the
slowest handler — otherwise a live consumer's message is taken and its work is
done twice. The claim asks Redis to check the idle time a second time, so a
message the owner finishes between the two commands is not taken at all.

The poison pill stays bounded. Without a dead letter queue an unbounded
redelivery is the endless loop this bus has always refused; three deliveries is
where it stops, and the log line is the dead letter. That line is the only
notice an operator gets, and nothing reads it but a human.

The takeover is a phase of the existing loop rather than a goroutine of its own,
so "a stream's messages are processed in order in a single consumer loop" stays
true and shutdown gains no new path to wait for. The price is cadence: the sweep
runs between reads, so a stranded message comes back within roughly twice the
threshold.

## Rejected

**A stable default consumer name** (the hostname alone, no pid). It would let a
restart read its own pending list immediately, and it would also give two
processes on one host the SAME list — which the configuration has always refused
by name, because sharing a pending list means processing every message twice.

**XAUTOCLAIM in one round trip.** It does not report how many times a message
has been delivered, and that count is what bounds the poison pill. XPENDING
first costs one more command per sweep and buys the bound.

**A goroutine per stream doing the sweep.** A second dispatcher into the same
handlers would break the ordering sentence the package comment makes, precisely
for the messages that were already handled once by a process that died.

**An environment variable for the threshold.** `RedisConfig` is published and an
embedder can set it; nothing in this installation needs another value, and
ADR 0063 refuses a setting with no consumer.
