# ADR 0088 — An idempotency key outlives its shipment, so a repeated key can name a canceled one

**Summary:** Opening a shipment with a key that names a CANCELED parcel is
refused, instead of being reported as a parcel that is already open.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

Found while designing the replacement-shipment capability, and the chain was
verified end to end before anything was changed:

1. `OpenForOrder` opens a parcel and writes the order-to-shipment binding.
2. The parcel is canceled. **The binding is not removed** — nothing in
   `internal/workflows/fulfilling` deletes one.
3. The same idempotency key is used again. The module returns the SAME
   shipment: migration 000003 made the key unique over every shipment rather
   than the live ones, and the repeated-key branch compares the reference, the
   option and the item set and never looks at the status.
4. The flow answered `AlreadyOpen: true`, computed from a link that had
   outlived the parcel.

So the caller was told goods were on their way, nothing was going to ship, and
nothing said so.

**The module is not wrong.** Its contract is "the same key returns the same
shipment" and it says nothing about status; returning what the key produced is
idempotency working. What was wrong was a CALLER reading that answer as "a
parcel is open".

## Decision

**A canceled shipment is REFUSED rather than reported.** The caller asked to
OPEN one; the honest answer is that this key cannot open anything any more. The
message names the shipment and says a NEW key is what would work, so the next
step is an informed choice rather than a guess.

**The status is read through the surface that already exists.**
`FulfillmentStatus` was already on the flow's narrow interface, so nothing about
the cross-module boundary widens. It costs one call on every open, which is
accepted: opening a parcel is an operator action rather than a request path, and
the alternative was to widen a compiler-invisible surface (ADR 0006) for a field
one caller needs.

**A status that cannot be read is an error, not an open parcel.** The status is
what separates a live parcel from a canceled one; an unanswered question about
whether goods will move is not a yes. The error carries the shipment id for the
same reason the binding failure does — pressing the button again with a fresh
key opens a second parcel.

## Consequences

- **The lie is gone and the ordinary path is untouched**: a pending shipment,
  opened for the first time or returned for a repeated key, is answered exactly
  as before.
- **The module's behaviour is now PINNED by an integration test.** The whole
  refusal rests on "a repeated key resolves to the canceled parcel", which had
  been read out of the code and never run. A fake that answered differently
  would have made the flow's test agree with itself — a class this repository
  was bitten by earlier the same day.
- **Two mutations fired**: a condition that never matches, and a swallowed
  status error. The first had to be rewritten to COMPILE — deleting the branch
  left the status unused, and a mutation that does not build proves nothing.
- **The binding is still not removed on cancel.** This record does not change
  that; it stops the binding from being read as a promise. Whether a canceled
  parcel should keep its binding is a separate question, and the answer today is
  that the binding is a historical fact rather than a live one.

## Rejected

- **Widening `CreateFulfillment` to return the status.** It saves one call and
  costs a cross-module surface change for a field one caller needs.
- **Reporting the cancellation in `OpenResult` instead of refusing.** Every
  caller would then have to remember to look, and the one that forgot would be
  back where this started.
- **Making the idempotency key partial on live shipments again.** Migration
  000003 made it global deliberately, so that soft-deleting could not free a key
  and print a second label. Undoing that to fix a caller's reading would trade a
  real guarantee for a convenience.
