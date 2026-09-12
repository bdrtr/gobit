# What nobody could ask

Evidence for [ADR 0149](../adr/0149-a-carrier-can-be-asked-where-a-parcel-is.md).

Measured 2026-09-12, taking the first slice the feature list's A4.9 row names.

## The row's own claim, checked

> The MANUAL half is there: a tracking number and URL can be attached at dispatch
> and the timeline carries five statuses with their moments. **The PROVIDER half is
> absent** — no `Track`, no return label.

(The row is in Turkish; this is its translation, and the file stays English because
ADR 0012 makes language a property of the FILE.)

Both halves hold. The manual side is complete: `MarkShipped` takes a tracking
number and URL, the five moments are columns (`shipped_at`, `delivered_at`,
`canceled_at`, `returned_at` plus the record's own `created_at`), and the order
timeline reads them.

The provider side had nothing. `core/provider`'s shipping contract is three
methods — `Quote`, `Create`, `Cancel` — and none of them asks a carrier anything
after the label is printed.

## The sentence that named the gap before this slice existed

`manual.Provider.GetShipment`, in the provider that ships in the box:

> It is NOT part of the core contract and the fulfillment service does NOT call
> it. It exists only for integration tests and for diagnosis: a shipment's status
> on the provider's side has to be verifiable without looking at the module's own
> record — a bug where the two ledgers have drifted apart can only be seen that
> way.

That is the whole finding. The two ledgers are separate tables on purpose
(`fulfillments` is the module's, `fulfillment_manual_shipments` is the provider's,
and the provider may not read the module's), they CAN hold different things, and
the only way to compare them was a method outside the contract that no plugin's
carrier would offer.

## Do they actually drift? Measured

Yes, and by design rather than by accident:

| Act | The module's row | The provider's row |
|---|---|---|
| `CreateFulfillment` | `pending`, the provider's tracking details copied in | `pending`, its own tracking details |
| `MarkShipped(number)` | `shipped`, the OPERATOR's number | untouched |
| `MarkDelivered` | `delivered` | untouched |
| `CancelFulfillment` | `canceled` | `canceled` (the provider is told) |

So after a dispatch the module says `shipped` and the provider says `pending`, and
the tracking number an operator typed sits beside the one the label was opened
with. With a real carrier the direction reverses — the carrier's status moves and
nobody here is told — which is exactly why the endpoint reports both and judges
neither.

The drift on the NUMBER is the one worth reading today: it is a parcel recorded
under a different number than the label stuck on it, and nothing in the system
could show it.

## Why five answers and not a boolean

Asking has four ways of not answering, and they are not interchangeable:

| Answer | What it means | What an operator does |
|---|---|---|
| `answered` | the carrier's view is filled in | read it |
| `unknown_to_provider` | the carrier disowns the identifier we hold | look at the account the label was opened against |
| `unaskable` | not registered here, or no tracking capability | nothing; install the plugin or accept it |
| `unreachable` | asked, no answer | retry |
| `not_opened` | no label was ever opened | look at why the parcel has no provider identity |

A carrier answering "pending" with no tracking number produces the same empty
fields as a carrier nobody could ask. That is why the client branches on the NAME:
the payment reconciliation's `Unaskable` count exists for the identical reason and
its godoc says it — "the two ledgers agree" and "nobody could ask" must not look
the same.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 61 | a silence is reported as an answer | **bit** (1 unit) |
| 62 | the carrier is asked by the MODULE's id | **bit** (1 unit, 1 e2e) |
| 63 | a disowned label is filed as unreachable | **bit** (1 unit) |
| 64 | agreement is claimed when nobody answered | **survived**, then bit |
| 65 | the carrier's status is written onto the module's row | **bit** (1 unit) |
| 66 | the provider answers a zero update for a label it never opened | **bit** (1 unit) |
| 67 | the response carries a carrier object for a silence | **bit** (2 unit) |

Mutation 64 is the one worth the space, because the defect it found was in a TEST.
Removing the "only when answered" guard from `TrackingNumbersAgree` broke nothing:
every non-answer fixture had a tracking number on the module's side, so the two
differed anyway and the comparison returned false for the wrong reason. The
fixture that can tell is the one whose BOTH sides are empty — a parcel opened at a
carrier that reports its waybill later, which is an ordinary state rather than an
exotic one. With that test in place the mutation bites.

Mutation 62 is the one a reviewer would not think to look for: asking the carrier
by the module's own identifier makes every parcel come back `unknown_to_provider`,
which reads as a shop whose labels are all opened against the wrong account. Its
witness asserts WHICH id reached the provider.

## What is NOT closed

No carrier plugin exists, so the only provider that answers is the shop itself
(A4.10). What this slice buys for that day is that the plugin needs no core change:
the capability is published, the module asks for it with a type assertion, and a
carrier that offers no tracking is a named answer rather than a silence.

Return labels are still absent, which is the other half of the A4.9 row:
`order_returns` carries no carrier, option or tracking column and the provider
contract has no "open a return label" verb. It is a separate decision and it needs
a carrier that can print one.

No checkpoint history, no polling, no webhook: all three need a provider whose
status moves on its own, and the reasons are in the ADR's Rejected list.

## What was not measured

Whether any installation has a parcel whose two ledgers disagree on the tracking
number today. It is now READABLE — that is the point of the slice — but the
existing rows were not swept, because a sweep would need to call every provider
once per parcel and the endpoint is the deliberate, human-paced way to do that.
