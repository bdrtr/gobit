# ADR 0239 — A canceled parcel recalls its replacement

**Summary:** Canceling the parcel a replacement left in puts its units back on
the shelf, sends the replacement back to waiting and reopens the claim or
exchange it had settled, so it can be sent again or withdrawn.

- **Status:** Accepted
- **Date:** 2026-09-29
- **Amends:** [0090](0090-a-replacement-is-sent.md), whose dispatch had no way back from a canceled parcel

Measurement: [measurements/0239](../measurements/0239-a-box-that-never-left.md)

## Context

A dispatch confirms a replacement's promises, opens a parcel and marks the
record dispatched, and the goods settle its claim or exchange (ADR 0090). A
parcel can be canceled while pending or shipped, and the cancellation flow puts
back only a sale's written-off units, which a replacement's parcel does not
hold: its units stayed out of the count, the record said they left, and the
source stayed settled. The dispatch could not be repeated, since the record was
dispatched and its key resolves to the canceled parcel (ADR 0088).

## Decision

The returns flow hears a canceled parcel and, when it carried a replacement,
puts back every confirmed promise's units at the shelf they left, and then sends
the record back to 'requested' with its promises cleared and its recall counted.
The claim or exchange the goods settled goes back to where it stood unless
another of its replacements left.

## Consequences

- The inventory module's `RecallReplacement` writes a cancellation naming the
  promise, under the promise's lock, once. A sale's promise and one never
  confirmed are refused.
- The order interop gains `ReplacementOfParcel` and `RecallReplacement`; order
  migration 000036 adds `order_replacements.recalls`, which the admin read and
  the flow's document carry.
- The next parcel's key is `replacement-<id>-<recalls>`, so dispatching again
  opens a new parcel. The record forgets its first dispatch; the canceled parcel
  and the two movements keep it.
- A claim goes back to 'requested', an exchange to 'funded' when its difference
  was collected and to 'requested' otherwise.
- The units go back before the record is written, and both steps repeat
  safely, so the event's second delivery by the outbox relay finishes a recall
  that failed half way. Nothing else retries it: the bus logs a handler's error.
- The returns flow logs where the application logs (D162).

## Rejected

- **Refusing to cancel a replacement's parcel.** The fulfillment module knows
  no replacement, and a shop stopping a parcel is its own call to make.
- **Withdrawing the replacement instead of reopening it.** The shop would have
  to write the same promise again to send the goods it still owes.
- **Keeping the source settled.** A claim would say goods met it that never
  left, and a replacement withdrawn afterwards would leave it closed on nothing.
