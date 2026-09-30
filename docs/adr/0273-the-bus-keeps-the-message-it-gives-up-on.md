# ADR 0273 — The bus keeps the message it gives up on

**Summary:** A message that emptied every consumer that took it is copied to
the Redis bus's dead-letter stream before it is acknowledged; the outbox
relay's alarm stands on that pile too, and `gobit deadletters` lists, redrives
and discards it.

- **Status:** Accepted
- **Date:** 2026-10-01
- **Amends:** [0162](0162-a-message-a-dead-consumer-was-holding-comes-back.md), whose dead letter was a log line

## Context

ADR 0162 bounded the poison pill: a message delivered three times without an
acknowledgement is acknowledged instead of handed to a fourth consumer. The log
line was the dead letter, and the known limits said nothing stored it, nothing
counted it and nothing listed it. The outbox already had the whole shape for
the events it could not publish: a pile that keeps the payload, the relay job
that fails while the pile is not empty, and `gobit deadletters` to redrive or
discard one letter at a time with its id repeated. A message the bus
published and its consumers died on is the other half of the same question.

## Decision

The bus copies such a message, with where it came from, who held it last, how
often it was handed over and when it was given up on, to a dead-letter stream
beside its events, and acknowledges it only once the copy is written.
`core/eventbus` publishes the stream's name and the reads and writes on it, the
outbox relay job fails while that pile is not empty, and `gobit deadletters`
lists it after the outbox's pile and acts on a letter named by its stream id.

## Consequences

- A message that kills its consumers is kept, counted, alarmed on and
  redriven or discarded like an event the relay gave up on; a redrive puts it
  back on its stream whole, as a new entry the group reads as any other.
- A copy that fails leaves the message pending, so the next sweep tries again.
  An acknowledgement that fails after the copy can keep a second copy; the pile
  then shows the same message twice.
- The stream hangs off the prefix with a hyphen where an event's stream has a
  colon, so no event name lands on it and a namespace keeps its own.
- `core/eventbus` publishes eight new names (ADR 0026).
- The report leaves the payload out, as the outbox's does; the stream keeps it.
- A handler's error still counts the event processed after three calls
  (ADR 0240), and a message trimmed by `MaxLen` while pending is not kept.
- The in-memory bus keeps no pile: its handlers run in the publishing process.

## Rejected

- A second table in PostgreSQL: the bus would need a database, and a pile
  written by the bus beside the message it came from is one write.
- Trimming the pile: a trimmed dead letter is a lost one; the pile is emptied
  by a human.
- A job of its own for the bus's pile: a second listing to correlate with the
  relay's, the argument the relay makes for reporting its own pile.
