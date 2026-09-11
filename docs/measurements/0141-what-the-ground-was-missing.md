# What the ground was missing

Evidence for [ADR 0141](../adr/0141-the-end-to-end-ground-wires-what-production-wires.md).

Measured 2026-09-11, right after ADR 0140.

## The two composition roots, side by side

```
$ grep -ohE '"github.com/bdrtr/gobit/internal/workflows/[a-z]+"' internal/app/*.go \
      | sed 's|.*workflows/||; s|"||' | sort -u
cart  checkout  datasubject  fulfilling  invoicing  ordercancel  returns

$ grep -ohE '"github.com/bdrtr/gobit/internal/workflows/[a-z]+"' internal/e2e/*.go \
      | sed 's|.*workflows/||; s|"||' | sort -u
cart  checkout  datasubject  fulfilling  invoicing  returns
```

Seven against six. One flow — `ordercancel` — was wired in production and not on
the ground.

The same answer from the other direction, counting how often each flow's name
appears anywhere in the end-to-end suite:

| Flow | unit test files | files with an integration/e2e tag | mentions in internal/e2e |
|---|---|---|---|
| cart | 17 | 0 | 23 |
| checkout | 10 | 1 | 16 |
| datasubject | 1 | 0 | 1 |
| fulfilling | 2 | 0 | 2 |
| invoicing | 3 | 0 | 5 |
| returns | 5 | 0 | 34 |
| **ordercancel** | **2** | **0** | **0** |

A single zero in a column of seven. It is the flow whose two faults had just been
found, and the coincidence is not one: the ground is where a fake stops being
able to hide a mechanism that cannot be fed.

## Why this flow and not another

Every other flow is RESOLVED by something. The order module's shipment endpoints
resolve the fulfilling flow by name and fail closed without it; the cart module's
pricing resolves the cart flow; the return endpoints resolve the return flow. An
unwired one of those reddens the first scenario that touches it.

The cancellation flow is resolved by nothing. It is the repository's only flow
driven entirely by the bus: `FromContainer` subscribes it and that is the whole
of its wiring. Unwired, it subscribes to nothing, does nothing, and every request
still succeeds — the stock figure is simply short.

## The scenario the ground can now run

Three units bought, two placed in a parcel through the admin endpoint (the only
one that takes an item breakdown), all three written off, then the parcel
canceled.

| Step | Shelf | Why |
|---|---|---|
| 10 on hand, 3 sold | 7 | the checkout CONFIRMS the reservation, so the units are deducted |
| 2 of them put in a parcel | 7 | putting goods in a box is not a second deduction |
| all 3 written off | **8** | only the unit outside the parcel can come back |
| the parcel canceled | **10** | the two it held are released |

Every figure is distinct, which is the point of buying three and parcelling two:
had the parcel held all three, "what the write-off could reach" and "what the
parcel released" would be zero and three, and a flow that put everything back at
the first step would look correct.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 13 | ADR 0140's binding write removed | **bit** — the shelf reads **10** instead of 8 |
| 14 | the flow unwired on the ground | **bit** — the shelf reads **7**, nothing comes back |
| 15 | the flow's import removed from the ground | **bit** — the gate names `[ordercancel]` |
| 16 | the gate's scanner blinded (`.go` → `.xx`) | **bit** — "only 0 flow(s) were found" |

Mutations 13 and 14 are the two halves of what was wrong, and they fail
differently on purpose: ten is the shop crediting itself with goods that are about
to ship, seven is nothing happening at all. A test that reported the same number
for both could not tell a wiring fault from an arithmetic one.

## The helper lied twice before it told the truth

This is recorded because both mistakes produce a diagnostic that accuses the wrong
mechanism, which is worse than no diagnostic.

**First draft — the poll asserted.** It called the shared `stockLevel` helper,
which uses `require`. Testify runs the condition on its own goroutine, and a
`require` there is `runtime.Goexit` on a goroutine that is not the test's: the
tick dies silently, no value is ever recorded. The read is now plain and its error
is carried and reported.

**Second draft — the message was passed IN.** `require.Eventuallyf(t, cond, wait,
tick, "... %d ...", last.Load())` evaluates `last.Load()` before the poll begins,
like any argument. It reported `last read: 0` for a shelf holding seven. The
message is now built after the poll, from an `assert.Eventually` whose result is
checked.

Both drafts printed the same wrong number — zero — for two different reasons, and
the second one survived the fix to the first. That is the part worth remembering:
the wrong output looked the same, so the first fix appeared not to have worked.

**Third time, in the neighboring test.** The fix was applied to the polling
helper and NOT to the `require.Never` beside it, which called the same asserting
helper from its own condition goroutine. That one passed when run with `-run` and
brought the entire e2e package down when run in the suite: the condition goroutine
outlives the assertion, the test's context is canceled under it, the read fails,
and the `require` inside it is a `FailNow` on a test that has finished. A whole
package's worth of red, from a helper that was correct everywhere it was looked
at. It is the rule this repository already writes down — running a new test
with `-run` is not running it — arriving from the other side: the fault was not
isolation between tests but a goroutine that outlived one.

## What was not measured

Whether any other hand-kept mirror of production has fallen behind in the same
way. The module lists of the two roots were not compared, because a missing module
fails loudly; the plugin lists, the migration sets and the guard exemptions were
not compared at all.
