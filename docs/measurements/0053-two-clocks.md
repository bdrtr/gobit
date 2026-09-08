# Two clocks on one axis — measured 2026-09-08

Evidence for [ADR 0053](../adr/0053-the-two-clocks-stay-and-every-moment-names-its-own.md).

## The columns: six, not five

| column | migration | default |
|---|---|---|
| `payments.captured_at` | payment 000001 | `NOT NULL DEFAULT now()`, overridden by the INSERT |
| `fulfillments.shipped_at` | fulfillment 000001 | none, CHECK pairs it with the status |
| `fulfillments.delivered_at` | fulfillment 000001 | none, CHECK |
| `fulfillments.canceled_at` | fulfillment 000001 | none, CHECK |
| `fulfillments.returned_at` | fulfillment 000004 | none, full-mirror CHECK |
| `invoices.issued_at` | invoice 000001 | `NOT NULL`, no default |

`returned_at` was added after the ledger row was written, which is why the row
said three fulfillment stamps.

## "Every other moment comes from the database" — false

Also process-stamped: `api_key.revoked_at`, `api_key.last_used_at`,
`auth_identity.last_login_at`, `promotion_redemption.released_at`, and every
`updated_at`/`deleted_at` written as a parameter in pricing, promotion, region,
auth, customer and b2b. Separately, every identifier is minted from the process
clock and is lexicographically time-sortable, so an id-tiebroken cursor carries
the same skew whatever the columns do.

The ORDER side of the claim is true: `orders.placed_at` and the return, claim
and exchange stamps are `now()`.

## What the injectable clock costs — and why the number misleads

Overlaying `Clock: time.Now` onto the fulfillment fake and running the package
fails EXACTLY TWO tests: `TestCancellationIsIdempotent` and
`TestMarkShippedIsIdempotent`. The invoice equivalent fails none.

Two is a floor, not the cost. It prices non-determinism while holding the store
signature fixed, and the move it is offered as evidence for would also delete
`stampFor`, four parameters from `UpdateFulfillmentStatus`, three from
`UpdateFulfillmentProviderResult`, and the fake's full-mirror stamp check —
the only place the four schema CHECKs hold without a database. A fake that
derived the stamp from the status could not violate the constraint, so that
check would lose its subject.

It also turns an assertion into a tautology: `assert.Equal(t, testNow,
*first.ShippedAt)` proves today that `MarkShipped` selected a stamp and which
one; after the move its only producer would be the fake emitting the value the
test asserts.

## Why the database clock is WORSE for two of the six

**`payments.captured_at`.** `now()` is transaction start. The capture opens its
transaction and then calls the provider inside it, so a database stamp would
predate the actual capture by the provider round-trip plus any retry. The Go
stamp is taken AFTER the provider returns. The column is read as
`min(captured_at) AS first_captured_at` — a reconciliation input, where "when we
began trying" is a different fact from "when the processor took it".

**`invoices.issued_at`.** One value feeds both the series year and the stamp, so
the printed number's year and the recorded date cannot disagree. Splitting them
puts a fiscal document's number and date on opposite clocks: across a year
boundary, `GBT2027…0001` stamped 2026-12-31.

**And a `DEFAULT now()` would buy silence.** A column the database supplies is
out of `TestEveryColumnIsWrittenBySomething`'s scope by that gate's own rule.
`order` migration 000009 says so; `invoice` migration 000003 adds a default and
then DROPS it, citing 000009 by name, in the same table.

## What was already decided and not written down

The order timeline declares `ClockDatabase` / `ClockApplication`, marks the
capture and the shipment transitions as application-clocked, carries the field
in its DTO and its OpenAPI text, and is gated. The ledger's C2 row records it;
section G did not, which is why this read as open.
