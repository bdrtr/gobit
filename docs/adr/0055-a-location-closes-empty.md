# ADR 0055 — A stock location closes empty

**Summary:** A warehouse is retired by CLOSING it, and the close is refused
while it still holds stock or a live promise. The closed row stays readable and
takes no stock, so availability goes on summing levels with no join.

- **Status:** Accepted
- **Date:** 2026-09-08

## Context

`stock_locations.deleted_at` was the last column in this repository that nothing
writes, and the audit that found it measured why writing it was not the fix
(docs/gaps.md D18). A location had three verbs — create, get, list — so a
warehouse that closes could not be retired at all.

Availability is summed off `inventory_levels`, and no query in this module joins
anything. A location hidden behind a soft delete would therefore keep selling
its stock while vanishing from the operator's screen. A hard delete is worse:
both the level and the reservation rows CASCADE from it, and a reservation is
never deleted.

So the question was never "delete or status". It was what a closed location
owes: whether its levels move, are zeroed or stop counting, and what happens to
the promises standing there.

Measurement: [measurements/0055](../measurements/0055-a-location-closes-empty.md).

## Decision

**A location closes EMPTY, and the closed row stays readable.** The close is
refused while any living level at it holds units or any reservation there is
active, a closed location accepts no stock write, and `deleted_at` becomes
`closed_at` — a retired warehouse is answered for rather than hidden.

## Consequences

- **The levels neither move nor zero out.** Moving is a transfer this module has
  no primitive for, and zeroing would destroy a physical count silently. The
  operator empties the warehouse with the two calls they already use, and the
  close records that they did. Closing is a sequence, not a button.
- **A live promise refuses the close**, exactly as it refuses an item deletion.
  Release or confirm it first; closing over it would strand a quantity a sale is
  still waiting for.
- **The availability reads keep their shape.** They stay join-free because a
  closed location has nothing to add to them, which holds only as long as both
  halves do: refused while full, refused stock afterwards.
- **The location row becomes the first lock of the module's order** — shared by
  every stock write, exclusive for the close. That is what stops a write from
  committing between the count and the stamp; it costs one row lock per stock
  write, and the order stays fixed, so nothing deadlocks.
- **The operator sees a closed warehouse leave the list and stay readable.** The
  listing hides it unless `include_closed=true` is asked for, the single-row read
  always answers, and the admin stock form offers open locations only.
- **Closing is idempotent and FINAL.** A retried decommission finishes without
  moving the first closing moment. There is no reopen: a mis-close is undone
  today only by a new location, which the past does not name.
- **A location still cannot be renamed.** The measured gap had two halves and
  this record answers one. D18 keeps that, and the question the dropped reopen
  would have answered: whether a mis-close should be undoable.

## Rejected

- **Write `deleted_at` and hide the row.** The levels and reservations that name
  a location are read for years; hiding it makes that history unreadable.
- **Delete the row.** Both foreign keys CASCADE, so it destroys stock rows and
  reservation history that the module says must never be deleted.
- **Join the availability reads to `stock_locations`.** Three statements gain a
  join, one of them on the checkout path — and `FOR UPDATE` over a join locks the
  location row too, measured, so every reservation at one warehouse would
  serialize behind the last one.
- **Zero the stock, or move it, when closing.** The first destroys a count
  nobody agreed to lose; the second is a transfer decision, and a close is the
  wrong place to make it.
- **Refuse `Reserve` at a closed location.** The invariant already makes it
  unreachable, and it would cost a location read per checkout line.
- **A status column rather than a stamp.** Two states need no vocabulary, and
  the stamp answers "when" as well, which an operator asks first.
- **A reopen verb.** Nothing needs one, and clearing the stamp would make
  `closed_at` the schema's one mutable moment; D18 keeps the question open.
