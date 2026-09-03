# ADR 0017 — An abandoned saga's compensation is replayed FROM THE RECORD

- **Status:** Accepted
- **Date:** 2026-09-03
- **Phase:** after the roadmap

## Context

When the process dies in the middle of a saga — a deploy, an OOM, a pod
eviction — the compensation chain never runs: the inventory reserved up to that
point, the order that was opened and the payment session all stay in the world.

v0.7.0 stopped that from being silent. An execution whose lease expires is
closed, one that had done work becomes `compensation_failed`, an ERROR is
logged, and `gobit stuck` lists it. What nothing did was **clean it up**.
README said so plainly: there is no automatic recovery.

The blocker was written down too: the engine persists step outputs but not
`StepContext.Shared`, and a compensation reads "which reservation do I cancel"
out of that map, which dies with the process.

This round measured it, and the answer is that **the data is not lost**. Every
step's Invoke output is persisted, and the compensation record does NOT erase it
— `StepRecord.Output`'s godoc already states that as a decision: "the only data
an operator doing manual repair needs — which reservation, which payment — is
here". The one missing piece was turning that JSON back into the typed value in
`Shared`, and only the step itself knows how.

## Decision

**An abandoned execution's compensation chain runs with state rebuilt from the
steps' own persisted outputs.**

1. A step may implement the optional `workflow.Recoverable` interface:
   `Restore(sc *StepContext, output json.RawMessage) error`. A chain containing
   a step that does not implement it keeps today's behavior (manual
   intervention), so the interface adds a capability without breaking a
   contract.
2. When recovery completes, the execution becomes `failed` and **releases its
   idempotency key**. In this engine `failed` already means "compensated
   completely", so the customer can pay for the same cart again.
3. Recovery is REFUSED — and the record stays `compensation_failed` — in four
   cases: the recorded step name does not match the definition (the workflow
   changed between deploys), a step that did work is not `Recoverable`,
   `Restore` returns an error, and the boundary below.

### The boundary: a step with no record cannot be assumed not to have run

The engine writes a step's record **after Invoke returns**. A process that dies
inside Invoke leaves no trace of that step, so recovery treats it as never
having run. For most steps that is true. For the capture step it is not: if the
card was charged and the process died before the record was written, recovery
would release the stock, cancel the order and free the key — and the customer
would pay again and be **charged twice**.

Such a step is marked with `workflow.RecoveryBlocker`. While it has no record —
that is, while it might genuinely have been in flight — it blocks recovery of
the steps before it as well. Once it HAS a record its outcome is known and the
chain compensates normally.

The reasoning is the same asymmetry the capture step's own compensation already
documents: undoing something that was never done costs money that was taken and
has nothing behind it; failing to undo costs a pending order and reserved stock,
both visible and both reversible. In doubt, take the cheap error.

## Consequences

**Positive.** For `complete_cart`, recovery covers three of the four places the
process can die and deliberately stops at the fourth:

| where the process died | next step | outcome |
|---|---|---|
| after inventory was reserved | create order | recovered, stock released |
| after the order was opened | authorize | recovered |
| after authorization | **capture** | **manual** (the money may be gone) |
| after capture was recorded | clear cart | recovered, the refund runs |

**Negative.** If the process dies again during recovery, the same compensations
are called once more. That is not a new requirement: `Compensate` being
idempotent is already the engine's contract, because a failed compensation does
not stop the chain.

**Recovery has to be EXCLUSIVE, and the claim is what makes it so.** An
abandoned execution belongs to nobody, so every caller that arrives with the same
key finds it. Measured before the claim existed, with four concurrent callers on
one abandoned record: the chain ran **four times**, concurrently. Until then the
engine could only call a `Compensate` twice in SEQUENCE (a retry, or the
best-effort compensation of a step that blew up); the recovery path is the first
one that can call it twice at the same INSTANT, across processes — and
`Compensate` was only ever promised to be idempotent in sequence.

For this repository's own steps the cost would have been duplicate work and
duplicate provider calls rather than corruption: every compensation undoes by
identity (release the reservation with this id, cancel the order with this id,
void this payment session) and the database serializes those writes. The
exposure is a step author who implements a read-modify-write compensation — and
the contract never told them not to.

So the engine claims the record before recovering it: one conditional UPDATE that
matches only while the row is still `running` AND its `updated_at` is still the
value the "this is abandoned" judgement was based on. Winning stamps `updated_at`,
which does double duty — it excludes the other callers and it renews the lease
for as long as the recovery runs. A caller that LOSES the claim is not told
"still going" but sent around the loop once more: the winner may release the key
at any instant, and the second turn answers both endings correctly (the key is
free → open a new execution; the winner is still working → the record it finds
is fresh, so "still going" is the truth).

The capability is OPTIONAL (`workflow.ClaimingStore`, a type assertion, the same
shape as `Recoverable` on steps) rather than a new method on `Store`. A port
method would break every Store written outside this repository for a guarantee
they may not need; an optional one leaves them exactly as they were and upgrades
the two Stores shipped here. The cost is that the guarantee is not visible in the
`Store` type — and that a decorator embedding `Store` HIDES the capability
silently, because an embedded interface promotes only its own methods.

Measured after: the same four concurrent callers, one compensation
(`TestKurtarmaEsZamanliCagiranlarlaTEKKEZKosar`; removing the claim puts it back
to four).

`StepContext.Input` is NOT typed on the recovery path — the original Go value
died with the process and what remains is the record's JSON. No compensation
reads `Input` today; one that starts to must know the field carries two
different types on the two paths.

Recovery is **not triggered, it is arrived at**: it runs when a caller comes
back with the same idempotency key. If nobody comes back the record stays
`running` and `gobit stuck` keeps listing it. A scheduled sweeper was
deliberately NOT added — recovery runs compensations, which are side effects,
and handing that to a background job nobody watches is exactly the "decide
silently" class this repository keeps refusing.

## Rejected options

**Persisting `Shared`.** The obvious idea, and it needs a schema change. It also
needs more: `Shared` is a `map[string]any`, so a JSON round trip turns
`[]reservationRef` into `[]any` and every reader's type assertion fails. That is
a read-contract change on top of a migration — for data that is already in the
step's output.

**Recovering unconditionally.** Measured and rejected: treating an unrecorded
capture as "did not run" is a double-charge door. That is why the boundary
exists.

**Writing the step record BEFORE Invoke.** It would remove the boundary — there
would be no "might have been in flight" state — at the cost of another write per
step, and it would not actually settle the question: a process dying inside
Invoke *after* the record was written is still indistinguishable. The gain does
not pay for the cost.
