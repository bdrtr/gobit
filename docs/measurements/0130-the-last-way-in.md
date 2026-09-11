# The last way in — measured 2026-09-11

Serves [ADR 0130](../adr/0130-a-person-can-see-their-passkeys-and-remove-one.md).

## The concurrency question

Four candidate designs argued this from PostgreSQL's documented snapshot rules
and all four listed it as unverified. It was executed against a real PostgreSQL
16: two transactions, each removing a DIFFERENT passkey of the SAME customer,
each guarded by "leave at least one".

| Shape | Keys left, 6 runs |
|---|---|
| the guard inside the DELETE (a subquery counting the customer's rows) | 0, every run |
| `SELECT … WHERE customer_id = $1 FOR UPDATE` before the guard | 1, every run |
| `pg_advisory_xact_lock(hashtext(customer_id))` before the guard | 1, every run |

Zero keys left is the lockout, and the guarded single statement produces it every
time. Both locks fix it; the row lock was chosen because the advisory lock costs a
lock CLASS from a registry that lives in the main module — order spending is class
1, category reparent class 2 — and a contrib module claiming one would be
coordinating across a module boundary for what a row lock does locally. The row
lock also says what it means: the rows being counted are the rows being locked.

### The first probe was a FALSE NEGATIVE, and that is the finding under the finding

The first version started both goroutines from one closed channel and reported 1
key left, 5 runs out of 5 — "the race does not happen". It was measuring the
scheduler: each goroutine opened its transaction after the barrier, so the two
never overlapped. With a two-phase barrier — each transaction opens, runs a
statement so its snapshot is really taken, signals ready, and only then waits for
the gate — the answer flips to 0 every run.

A race test that does not prove the overlap happened is a test of the scheduler.
This repository already records that lesson as D46, about a one-round test that
was green half the time.

### How the SHIPPED test forces the overlap

The probe could open the transactions itself. The shipped test cannot: it drives
the module's own `Remove`, which opens and commits internally, so there is no
seam to put a barrier in — and adding one to production code for a test would put
the barrier in the shipping path.

A THIRD transaction holds the gate instead. It locks the customer's rows, both
removals queue behind it, and the test waits for PostgreSQL itself to report two
backends waiting on a lock:

```sql
SELECT count(*) FROM pg_stat_activity
WHERE datname = current_database() AND state = 'active' AND wait_event_type = 'Lock'
```

That is a condition rather than a duration, and it is the same condition in both
worlds — which is what makes it a fair barrier. With the row lock the two waiters
are the `SELECT … FOR UPDATE`s; without it they are the DELETEs, because a plain
SELECT never blocks and both removals fall straight through to their delete. Once
the gate commits, the fixed shape serialises and the second removal counts one row
and refuses; the mutated shape deletes both.

## Where the cross-module question is asked, and why the order was changed

The first draft asked "is there another way in" INSIDE the transaction, right
after the lock, on the grounds that the freshest answer is the best one. That is
wrong in a way the race table does not show: the question can reach another module
and another module's query takes a connection from the same pool. A removal
holding a row lock while waiting for a second connection is a removal that can
deadlock the pool against itself under enough concurrent load — the lock is held
for the duration of an unrelated query's queueing.

It is asked before the transaction now. The count it decides on can be stale and
both directions were checked:

| Stale direction | What happens |
|---|---|
| the count says MORE rows than there are | the question is skipped, `allowLast` is false, and the locked guard refuses |
| the count says FEWER rows than there are | the question is asked needlessly, and the locked count permits regardless of the answer |

The first row is a unit test rather than a sentence: the memory store is made to
report two rows while holding one, and the removal is refused. Reasoning about it
in a comment is what the FOR UPDATE table shows to be unreliable.

## The two 500s are different faults, and only the log can say which

"Nobody could answer whether this account has another way in" and "the answerer
itself broke" are both `KindInternal`, so `core/http` masks both messages — on
purpose, so a DSN or a query cannot leave the process. The bodies are byte-for-byte
identical and that is correct.

So the distinction lives in the log, and the test asserts it there. The first
version of that test asserted on the response BODY, passed nothing useful, and was
found by writing the second case: an operator reading one sentence looks at their
credential store and an operator reading the other looks at the network.

The fake that produced the first case was also wrong in a way that would have
passed: it returned an error whose TEXT mentioned `ErrPasswordUnknown` instead of
wrapping it. Both answer 500, so the test was green while the branch it was
written for never ran.

## One key had two names

Decoding with `base64.RawURLEncoding` does not require the trailing bits of an
unpadded group to be zero, so `b25sea` and `b25seQ` both decode to the four bytes
`only`:

```
only         -> "b25seQ"
b25sea       -> "only" err=<nil>
bm90LW1pbmU  -> "not-mine" err=<nil>
```

Found by accident. A fixture for the last-way-in status test used the first
spelling, the removal worked, and the id had been mistyped — so the test was
passing for a reason its author had not chosen. The listing only ever issues the
canonical spelling; the removal now refuses anything else, as "no such key".

Nothing in the tree keys on the string form today, which is the argument for
closing it now rather than later: an audit line, a cache key or a rate limit
written against the path segment would count two names as two subjects, and that
defect would surface nowhere near this function. Gap D67.

## The mutation table

Fifteen mutations, each run with `-count=1`, each restored from a scratchpad copy
rather than with `git checkout`.

| Mutation | Bitten by |
|---|---|
| listing: every key reported removable | `TestTheLastKeyIsNotRemovableWithoutAnotherWayIn` |
| listing: no key reported removable | `TestTheLastKeyGoesWhenTheInstallationSaysThereIsAnotherWayIn` |
| listing: the other-way-in question never asked | `TestTheLastKeyIsNotRemovableWithoutAnotherWayIn` |
| listing: the reason omitted | `TestTheLastKeyIsNotRemovableWithoutAnotherWayIn` |
| removal: the question asked even with two keys | `TestARemovalWithTwoKeysAsksNobody` |
| removal: the last key always permitted | `TestAStaleCountDoesNotWidenTheRule` |
| removal: both 500s folded into one sentence | `TestNobodyCouldAnswerAndTheAnswererIsBrokenAreDIFFERENTSENTENCES` |
| removal: a malformed id answered 500 | `TestOneCodeForEveryIdThatIsNotTheCallersKey` |
| removal: the remaining count not reported | `TestTheLastKeyIsNotRemovableWithoutAnotherWayIn` |
| seam: the named unknown not recognised | `TestARemovalThatCannotBeCheckedChangesNothing` |
| store: `FOR UPDATE` dropped | `TestTwoRemovalsCannotBOTHTakeTheLastKey` |
| store: the ownership check dropped | `TestARemovalCannotReachAnotherPersonsKey` |
| store: the last-key guard dropped | `TestTheRealStoreRefusesTheLastKey` |
| store: canonicality unchecked | `TestOneKeyHasONEName` |
| gate: the document's method not lower-cased | `TestEveryDocumentedRefusalIsTheOneTheRouteAnswers` |

Six of the fifteen first failed to COMPILE rather than to fail, which is not a
bite: a mutation that removes the last read of a variable is refused by the
compiler and proves only that Go noticed. Each was rewritten to compile — `true ||
otherWayIn` rather than `true`, `len(keys) > 99` rather than `if false` — and then
re-run.

### Two mutations survived, and the fixtures were the fault

**The last key always permitted.** Nothing failed, because no unit test held a
world where the unlocked count and the locked count DIFFER — and `allowLast` only
matters there. The memory store gained the ability to report more rows than it
holds, which is the concurrency window as a fixture, and the mutation then bit.

**The ownership check dropped.** Nothing failed, because the fixture gave the
caller two keys and permission to remove their last: the DELETE is itself scoped by
`customer_id`, so it affected no rows and answered "not found" anyway. Two rules,
one answer, nothing isolated. With the caller holding exactly ONE key and not
permitted to remove it, the mutated store answers "that is the only way into this
account" — about a key that is not theirs, which is both the wrong answer and a
disclosure. It is the lesson this repository already records about chain tests:
a fixture has to separate the rules, or whichever fires first answers for all of
them.

## The gate that would have failed on its first non-POST

`documented_status_test.go` spelled the document's operation key by special-casing
POST and returning every other method unchanged. An OpenAPI document keys
operations in lower case, so the first GET added to that table looked for an
operation named `GET` and found none. It had been correct for exactly as long as
the table held only POSTs, and it was found by adding one.
