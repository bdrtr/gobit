# ADR 0275 — A webhook receiver sets its rate

**Summary:** A receiver registered with `plugins/webhookout` can set
`max_per_minute`, and each delivery pass sends it at most that many of its due
deliveries, oldest first, leaving the rest for the next pass.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The known limits said the webhook plugin forwards every published topic and
has no rate limit: a receiver of `cart.created` is owed one delivery per opened
cart, and a pass of up to a hundred deliveries, eight at a time, sends them as
fast as they come. A receiver behind a gateway that throttles, or a small
service that falls over under a burst, answered with failures that climbed the
retry ladder toward the dead letter for load the sender caused. The delivery
job runs once a minute and claims the due rows oldest first.

## Decision

A receiver's `max_per_minute`, set at registration or on a change and lifted
with zero, caps how many of its due deliveries one pass claims. The claim
numbers each receiver's due rows oldest first and leases only those inside its
cap, so the rest wait for the next pass with no attempt counted.

## Consequences

- A receiver is sent no more than its cap in a minute and the backlog drains
  at that pace, oldest first; waiting is not an attempt, so it moves nothing
  toward the dead letter.
- A capped receiver's backlog no longer takes a whole pass, so the others are
  sent theirs in the same minute.
- The cap is per pass, and the pass is once a minute; a rate below one a
  minute is not expressible.
- A row another pass holds is skipped as before, which can leave a receiver
  under its cap for a pass and never over it.
- The claim numbers every due row once a pass; a backlog of ten thousand rows
  is numbered each minute.
- Migration 000003 of the plugin adds the column; rolling it back lifts every
  cap.

## Rejected

- A token bucket per receiver: a second clock beside the pass's for a cadence
  the pass already has.
- A rate per second: the pass cannot send on a schedule finer than itself.
- A default cap for every receiver: an installation that never set one would
  find its receivers slowed by a number nobody chose.
