# ADR 0149 — A carrier can be asked where a parcel is

**Summary:** `core/provider` publishes an optional `ShipmentTracker`, the provider
in the box answers it, and one admin read reports the carrier's view beside this
module's own. It costs an answer nobody may collapse — "nobody could ask" — and
buys the drift between the two ledgers, which was visible only through a method
the contract did not carry.

- **Status:** Accepted
- **Date:** 2026-09-12

## Context

A parcel's tracking number can be attached at dispatch and the timeline carries
five moments, so the MANUAL half is complete. The provider half was absent: no
contract method asked a carrier anything, and the provider's own view was
reachable only through `manual.Provider.GetShipment`, which is outside the core
contract and whose godoc says so — "a bug where the two ledgers have drifted
apart can only be seen that way".

That sentence is the gap. The module's record and the provider's ledger are
separate tables on purpose and can hold different things, and nothing published
could compare them. A carrier plugin (A4.10) had nowhere to put an answer either.

Measurement: [measurements/0149](../measurements/0149-what-nobody-could-ask.md)

## Decision

`coreprovider.ShipmentTracker` is an OPTIONAL capability — `Track`, a read, keyed
by the provider's own shipment id — and
`GET /admin/v1/fulfillments/{id}/tracking` reports its answer beside the module's
record without writing anything. A provider that cannot be asked produces a named
answer rather than an empty one.

## Consequences

Nothing is written, because neither side is authoritative in general. A real
carrier knows where a parcel is and this module does not; the provider in the
box is the SHOP, so there the module's status — an operator marking a parcel
handed over — is the true one and the provider's row is a stub nobody moves. A
write would pick a winner for both cases and be wrong in one, so both views are
reported and a human decides, which is the payment reconciliation's choice too.

The answer has FIVE shapes and a client branches on the name, not on emptiness: a
carrier answering "pending" with no number is byte-identical to a carrier nobody
could ask. `unaskable` (not registered, or no such capability),
`unknown_to_provider` (the carrier disowns the identifier we hold — what a label
opened against the wrong account looks like), `unreachable` (asked, no answer),
`not_opened` and `answered`. Collapsing any of them reports a parcel that has
not moved.

`tracking_numbers_agree` is false whenever the carrier did not answer: agreement
must not be readable out of a question nobody asked. A mutation proved the test
could not tell — with a number on the module's side the two differ anyway — so
the witness is a parcel whose BOTH sides are empty.

What the provider in the box can say is its own row: the status the label was
opened with and the number it carries. That number beside the operator's is
what makes a parcel recorded under the wrong one visible.

The read is admin-only and a sub-resource. A shopper's "where is my parcel"
would put a carrier's call on the busiest surface there is, and a field on the
fulfillment would make listing parcels cost one request per row; the shopper's
view stays the timeline's stored state (ADR 0100).

Seven mutations bit and one survived, and the survivor was a hole in a TEST rather
than in the code: the fixture that closes it is the one whose two sides are blank.

## Rejected

- **A method on `FulfillmentProvider`.** Every provider would change so that one
  of them can gain a capability, and a carrier that genuinely cannot answer would
  have to implement something that lies.
- **Write the carrier's answer onto the record.** It picks an authority the
  contract does not have, and ADR 0017's argument against an unwatched sweeper
  applies with more force where the subject is somebody else's report.
- **A checkpoint history.** Where a movement list is stored, how it is deduplicated
  and what a carrier reordering events means are a second decision.
- **Poll the carriers on a schedule.** It needs a carrier that answers, a rate
  budget per provider and a transition record; none is decidable yet.
