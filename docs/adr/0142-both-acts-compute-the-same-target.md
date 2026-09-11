# ADR 0142 — Both acts compute the same target

**Summary:** A write-off and a canceled parcel each compute where a line's total
on the shelf should BE, and the inventory module moves the difference under its
own lock. It costs a column and the loss of a unique index, and it removes an
ordering assumption that credited the shelf with units nobody canceled.

- **Status:** Accepted
- **Date:** 2026-09-11

## Context

Two acts put a line's written-off units back. ADR 0134's write-off returns
`min(canceled, bought − committed)`, and ADR 0139's parcel cancellation returns
the difference between that window evaluated twice — once with the parcel's units
counted and once without.

Each is right on its own. The pair assumes an ORDER: the parcel act's "what was
already owed" term is the write-off's answer, so it subtracts work it assumes
happened. Nothing enforces that — the bus is asynchronous, so a write-off whose
direct publish was lost arrives up to a minute later through the outbox relay,
after an operator who saw the units still in a parcel cancelled it.

Run that way: five units bought, three in a parcel, all five written off. The
parcel act put back three, anticipating the write-off. The write-off then found a
full window and put back five. **The shelf was credited with eight units for a
cancellation of five**, and the two acts carry different references, so the
ledger's uniqueness could not see it. Gap D82.

Measurement: [measurements/0142](../measurements/0142-the-order-that-nothing-enforced.md)

## Decision

Both acts compute the same target — `min(canceledTotal, bought − committed)` from
the state each finds — and `ReturnCanceledInventory` brings the line's total UP TO
it: it reads what is already back, under the level's lock, and moves the
difference. Neither act computes a delta from a state it does not hold.

## Consequences

Order stops mattering, because a target carries no assumption about who ran
first. Whichever act arrives first does the work; the other recomputes the target
and finds it met. A redelivered event finds it met too — which is what replaces
the unique index the old design leaned on, and it is stronger rather than weaker:
uniqueness refused a second WRITE, a met target refuses a second UNIT.

The index had to go regardless, and that is worth stating rather than hiding: the
same act legitimately writes twice now. A write-off that reached two units while a
parcel was live tops up by three when that parcel is cancelled, under the same
reference both times, and the index would have refused the correct second write.

`inventory_movements` gains `line_item_id`, set on a cancellation and on nothing
else, because "how much of this line is already back" has to be a question the
ledger can answer. It is another module's identifier and not a foreign key, which
is the rule the `reference` column already follows.

The read and the write are one transaction under `LockInventoryLevel`, so two
acts arriving together cannot both see the old sum. That lock already existed;
the sum is now read inside it rather than a delta trusted from outside.

The mutation that matters is the reversion: putting the parcel act back on the
difference-of-windows reddens five tests, including the fixture that delivers the
two events in the order nothing forbids.

The fakes moved with it: the flow's inventory fake keeps a per-line total, because
one that simply added the number it was handed would prove order-independence
against a producer that does not behave like the producer.

## Rejected

- **Keep the difference and enforce the order.** The bus does not offer ordering,
  and a flow that needed it would be asking for a guarantee no backend gives.
- **Track what was returned in the flow's own table.** The flow would own schema,
  which `internal/workflows` does not, and the count would be a second copy of
  something the ledger already holds.
- **Encode the line in the reference and sum by prefix.** It makes a string
  convention load-bearing, which this repository refuses elsewhere for the same
  reason a free-text reference is not read as an order id.
- **Let the parcel act do nothing and re-deliver the write-off.** Redelivering
  somebody else's event puts one module's correctness inside another's retry.
