# The order that nothing enforced

Evidence for [ADR 0142](../adr/0142-both-acts-compute-the-same-target.md).

Measured 2026-09-11, hours after ADR 0139 shipped.

## How it was found

Not by a failing test. The formula was written in several places — two godocs,
two ADRs, the changelog — and they were being read to check whether the copies
agreed. They did. What the reading showed instead was that the two acts' terms
refer to each other: the parcel act subtracts "what the write-off already put
back", which is a number it does not read but assumes.

That is a claim about ORDER, and the bus does not offer one.

## The reproduction

A probe, run against the flow with its fakes, delivering the two events in the
order nothing forbids:

```
PROBE: after the PARCEL handler, the shelf received 3
PROBE: after the LINE handler too, the shelf received 8 (five units were canceled)
PROBE: references = [ful_01CANCELSTOCKPARCEL0:oli_01CANCELSTOCKLINE0000
                     olc_01CANCELSTOCKROW0000]
```

Five bought, three in a parcel, all five written off.

| Step | What it computed | Shelf |
|---|---|---|
| the parcel is canceled first | `after = min(5, 5−0) = 5`, `before = min(5, 5−3) = 2`, so 3 | +3 |
| the write-off arrives | window `5 − 0 = 5`, so `min(5,5) − min(0,5) = 5` | +5 |
| | | **8 for a cancellation of 5** |

The parcel act's `before` term subtracted the two units it expected the write-off
to have returned. The write-off had not run, and when it did, it found the parcel
already gone and returned everything.

The two references are different — one names the parcel and the line, the other
names the cancellation row — so the unique index that made a redelivery harmless
had nothing to compare.

## Why the ordering is reachable, not theoretical

The write-off's event has two deliveries by design: a direct publish after the
transaction commits, and an outbox row the relay sends if that publish was lost.
The relay runs every minute. So:

1. an operator writes off the line; the direct publish fails (a bus hiccup, a
   restart, a Redis blip) and the outbox row waits;
2. the operator, looking at a parcel that still holds the units, cancels it —
   which is exactly what ADR 0135 told them to do;
3. that event is published directly and handled at once;
4. the relay delivers the write-off up to a minute later.

Nothing in that sequence is unusual and nothing in it is wrong.

The in-memory bus gives a second route: it hands each handler its own goroutine,
so two events in flight are two handlers running at once, in whatever order the
scheduler picks.

## The shape of the fix, checked against the cases

`target = min(canceledTotal, bought − committed)`, computed by both acts from the
state each finds; the module moves `target − already returned` under the level's
lock.

| Sequence | First act | Second act | Total |
|---|---|---|---|
| write-off, then parcel | target `min(5, 5−3) = 2`, moves 2 | target `min(5, 5−0) = 5`, returned 2, moves 3 | **5** |
| parcel, then write-off | target `min(5, 5−0) = 5`, moves 5 | target 5, returned 5, moves 0 | **5** |
| either, then a redelivery | — | target unchanged, returned equal, moves 0 | **5** |
| two parcels, either order | `min(5, 5−3) = 2` | `min(5, 5−0) = 5`, returned 2, moves 3 | **5** |
| nothing written off | target `min(0, …) = 0` | — | **0** |

The last row is the distinction the design has to keep: a canceled parcel makes
its units DISPATCHABLE again, not sellable again. They are still sold.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 19 | the parcel act put back on the difference of two windows | **bit** — 5 tests, including both orders |
| 20 | the module adds the target without reading the sum | **bit** — the redelivery test |

Mutation 19 is the reversion: it restores exactly the code that shipped this
morning, and the test that fails first is the one that delivers the two events in
the order nothing forbids.

## What the fixtures had been hiding

Five existing tests went red on the new arithmetic, and every one of them for the
same reason: they asserted the amount ONE act moves, on a fixture that implied a
prior state without producing it. A parcel-only test with `canceled = 5` expected
three units because the write-off "would have" returned two — the same assumption
the code was making, written into the test.

They now either deliver the earlier act first, or assert the TOTAL rather than
the delta. Changing the unit of the assertion is most of the correction: the
amount a single act moves depends on what has already been moved, and the
invariant does not.

## A second finding, from the suite rather than from the test

The per-line sum was first written as `WHERE reason = 'cancellation' AND
line_item_id = $1`, with no item. Two integration tests reusing the same line id
on different items then shared a total: the second one passed when run with
`-run` and failed when run beside its neighbour.

The fixture was the trigger and the query was the fault. The caller holds the
level of ONE (item, location) pair, so a sum reaching across items reads rows its
transaction does not protect — the item belongs in the predicate because it is in
the LOCK. In production a line sells one variant and a variant tracks one item, so
it narrows nothing; what it does is make the read match the lock.

The comment written above the shared constant claimed the sharing was safe
"because each test gets a fresh item". That was false when written and true after
the query changed, which is worth recording as its own small lesson: a fixture
comment can describe the behavior the author wanted rather than the one the code
had.

## What was not measured

Whether any installation has over-returned this way. It would show as a
cancellation movement whose delta exceeds what the line could owe, which is
reconstructable from the ledger — every cancellation row now carries its line, so
the query exists going forward, and for rows written before this migration the
line is null.
