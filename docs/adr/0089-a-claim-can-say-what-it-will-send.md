# ADR 0089 — A claim can say what it will send

**Summary:** A claim to be settled with goods gets a record of WHAT to send —
which lines, how many, from where, by which carrier — and sends nothing.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

`internal/workflows/returns/claim.go` refuses to settle a claim of type
`replace`: "shipping a replacement against an existing order is not something
this framework can do yet". The sentence is true, and the first thing missing is
not the shipping.

Nothing said WHAT to send. `order_claims` carries a type and a refund amount;
`order_exchanges` carries a difference. Neither carries an item. The order
cannot carry it either — after an order is written its amounts and its lines do
not change (`models.Order`) — so an operator could open a claim promising goods
and no row in the database could name a single one of them.

## Decision

Two tables beside the order: `order_replacements` says how a claim will send
(carrier, warehouse, status, note) and `order_replacement_items` says which
lines and how many of each. The service records one, reads it, lists a claim's,
and withdraws one; the four admin endpoints do the same over HTTP.

Nothing ships. No stock moves, no parcel is opened, no other module is called —
the status vocabulary is therefore two words, `requested` and `canceled`, which
is exactly what the code can write.

## Consequences

The schema can now say what a `replace` claim will send, and the settle path can
be built against a record instead of against a guess. The rule that spans rows —
no more units promised than were bought — is counted under the order's lock and
excludes withdrawn promises, the same shape returns use. Withdrawing is
idempotent; a claim with an open promise cannot be withdrawn, so the promise
cannot outlive the record that made it.

Goods still do not move and `claim.go` still refuses to settle. That refusal
loses one of its two reasons here and keeps the other.

The rest of the journey (`held`, `dispatching`, `dispatched`, the parcel, the
reservation) arrives with the flow that writes it, as a second migration.
Shipping a status nothing can reach is what migration 000008 had to undo.

The order module's endpoint table gained a word it did not have: a plain
envelope carrying an ARRAY. The one endpoint already of that shape — the
timeline — was described as a single record, and no test could see it (D44).

Measurement: none. The decision is a shape, and nothing here was chosen by a
number.

## Rejected

**The lines on `order_claims` itself.** A claim is one row and the goods are
many; the second line would have nowhere to go.

**The whole status vocabulary now.** Every value but the first would name a
code path that does not exist — the shape migration 000008 took out of
`order_exchanges`, where a `completed` nothing could write sat beside a
`completed_at` nothing could stamp (D4).

**A nullable `order_exchange_id` beside the claim.** An exchange will own a
replacement, and no path here writes one; a column with no writer is D2 and D4.

**An idempotency key column.** The row IS the key: the retry that opens the
parcel names this record, and a second column holding the same fact could
disagree with it.

**A paged listing.** A claim's replacements are bounded by the lines of one
order; a cursor nobody advances is a contract to keep for nothing.
