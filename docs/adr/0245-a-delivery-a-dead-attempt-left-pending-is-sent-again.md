# ADR 0245 — A delivery a dead attempt left pending is sent again

**Summary:** An operator's resend takes a notification left pending for longer
than an attempt can live, thirty seconds, as well as a failed one; a younger
pending one is refused, since its attempt may still be sending.

- **Status:** Accepted
- **Date:** 2026-09-30
- **Amends:** [0243](0243-a-failed-confirmation-is-sent-again-by-an-operator.md), which sent a failed record again and no other

Measurement: [measurements/0245](../measurements/0245-the-record-that-never-finished.md)

## Context

An attempt claims its record as pending, calls the provider and writes the
outcome. When the outcome cannot be written, or the process dies between the
call and the write, the record stays pending for good, and the module's own
words for it were "has to be examined by hand" (D168). ADR 0243 gave the
operator a resend for a failed record and refused a pending one, so an operator
who examined it still had nothing to act with.

## Decision

The resend takes a failed record, or a pending one whose last write is older
than twice the send timeout, the most an attempt's call and its outcome's write
can take. The reopen holds the rule on the database's clock, which stamped the
record.

## Consequences

- A pending record older than thirty seconds is an attempt that died, and its
  mail may or may not have gone, as with a failed one; the operator decides.
- A younger pending record answers 409 `notification_not_resendable`: its
  attempt may still be sending, and a second send would race it.
- The service reads the same rule to refuse before it reads the order, and
  the reopen on its own clock is what decides.

## Rejected

- **Marking an old pending record failed by a sweep.** It would say the
  provider refused a message nobody knows the fate of, and the operator would
  still have to press resend.
- **Letting the operator resend a pending record of any age.** A resend while
  the attempt is still in flight sends two e-mails for one decision.
