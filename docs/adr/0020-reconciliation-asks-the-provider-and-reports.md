# ADR 0020 — Reconciliation ASKS the provider, and only reports what it hears

- **Status:** Accepted
- **Date:** 2026-09-04
- **Phase:** after the roadmap

## Context

The payment module calls the provider INSIDE its own database transaction.
That is a deliberate trade, argued in the module's package doc: it buys
"exactly one authorization" under concurrency, at the price of holding a row
lock for the duration of a network call.

It costs one more thing, and that cost is the subject here. If the transaction
fails to commit AFTER the provider moved the money, the rollback takes the
money's only local trace with it. The session stays `authorized` here while the
provider says `captured`.

**No local query can find that.** Every record that would show the divergence is
a record that was rolled back. `internal/workflows/checkout/doc.go` has carried
both the consequence and the remedy since Phase 7:

> The only correct way to close (2) is to ASK the provider — that is,
> reconciliation: a periodic comparison against the provider's own ledger. The
> plan does not put that in this phase; it is Phase 7+ work.

The consequence is not abstract. The checkout saga reads the local collection,
sees nothing captured, and compensates — releasing stock and cancelling an order
that was paid for. The customer's card is short and the order is gone.

ADR 0019 built the scheduler and recorded this as the repository's one unkept
periodic promise, explicitly not built there because that decision was about the
mechanism. This is the promise.

## Decision

**Reconciliation is a scheduled comparison that asks each provider about the
sessions where the two ledgers can silently disagree, and REPORTS what it
hears. It writes nothing.**

It arrives as four pieces, and the split is the decision:

1. `provider.SessionInspector` — an OPTIONAL interface in core, reached by type
   assertion, carrying one method: `InspectSession`.
2. `payment/service.Reconcile` — the comparison, returning a report of counts
   and both sides of every difference.
3. `payment_sessions_reconcile_idx` — a partial index on the predicate the
   listing carries.
4. `internal/jobs/paymentrecon` — the schedule and the severity.

**The suspect set is authorized-here, aged past a settling window.** A capture in
flight is in exactly that state for as long as the provider takes to answer, so
without the window every ordinary payment would be reported as a discrepancy.

**Agreement is narrow: the provider reporting nothing captured.** Every session
in the set is locally authorized, which means this module believes nothing was
drawn. What the provider calls the status does not matter; the captured amount
does.

## Rejected options

**A. `InspectSession` as a method on `PaymentProvider`.** It would make every
provider in existence change for one provider's capability, and — worse — it
would force each of them to answer something. The cheapest something to answer
is zero, and zero is indistinguishable from "nothing was captured". The one
guarantee this decision rests on is that *"the two ledgers agree"* and *"nobody
could ask"* never look the same; a mandatory method deletes it. Optional means a
provider that cannot be inspected is COUNTED as uninspectable, in its own field,
and said out loud on every pass.

**B. Repairing what it finds.** Recording a capture off the back of a comparison
would be this module deciding, alone and unwatched, that money moved. ADR 0017
refuses that reasoning for compensations — which are cheaper than money — in
four places. The same line holds here with more force. A human repairs, with
both ledgers in front of them; what changes is that the human learns there is
something to repair, which until now nothing told them.

**C. Walking the provider's ledger from the other end.** That would also catch
the harder class: an authorization whose session row was never committed at all,
which is invisible from here because the row and the authorization are written
in one transaction. It is a different query against a different API, it needs
per-provider paging, and it is not this. **The class this closes is the one
where a local row EXISTS and disagrees**, and the package doc says so rather
than letting the gap be inferred.

**D. Counting a disowned session as a failure.** `SessionInspector`'s contract
requires NotFound rather than a zero inspection when the provider has never
heard of a session, precisely so the two stay distinguishable. A provider that
was reached and disowned a session this module holds is reporting a fact about
this installation — an authorization sitting on an account nobody here reads —
not a network blip. It gets its own count and its own ERROR line.

**E. Failing the pass on the first unreachable provider.** The sessions a failing
provider would have covered are precisely the ones nobody else is looking at. A
provider error is counted and stepped past. This also gives the deadline a
useful shape: when `MaxRun` lands mid-pass the context error surfaces from the
provider call, is counted as unreachable, and the divergences already found are
still reported.

**F. A boolean "clean" result.** A pass that asked forty providers and could not
reach three of them is not clean, and one boolean would say it was. The report
carries `Examined`, `Agreed`, `Divergences`, `Unaskable`, `Unreachable`,
`Unknown` and `Truncated`; `Clean()` is false if ANY of the last four is
non-zero.

## Consequences

**The interval is bounded from both sides, and by different things.** It cannot
be much slower than hourly: what this finds is an order the saga has already
compensated, and every hour is an hour of a refund conversation getting worse.
It cannot be much faster either, and that limit is the provider's — the pass
makes one network call per unsettled session, and an installation with delayed
capture keeps sessions legitimately authorized for as long as fulfillment takes,
so the same sessions are re-asked about on every pass forever.

**A filled limit is a backlog, not a sample, and it is reported as one.** The
listing is oldest-first, so a suspect set permanently larger than the limit
means the same oldest sessions are examined every pass and newer ones are never
reached. Truncation is computed by asking for one row MORE than the limit and
never examining it, because a warning derived from "the page came back full"
fires on a healthy installation exactly when the set ends on the limit — and a
warning that fires when it should not is one an operator learns to skip.

**The index is partial and it was measured, not assumed.** Sessions leave the
suspect set for good once captured, canceled or declined, so the index carries
only `updated_at` for live authorized rows. On a 200,000-session fixture the
listing is an index scan at 0.56 ms over 52 buffers; with the index dropped the
same query is a parallel sequential scan at 12.0 ms over 3,618 buffers, and that
side grows with every payment the installation ever takes. An integration test
reads the plan, because this repository has already shipped a godoc claiming an
index was used where the planner disagreed.

**`gobit jobs` now opens the whole application.** A job whose dependency is a
module can only be built from a container that holds the modules, and the
listing is built by the SAME `registerJobs` the runner calls. A listing built
from a thinner container would describe a different set of jobs than the one
that runs, which is the one thing that command exists not to do.

**Three findings, three severities, three lines.** A divergence needs
accounting; an unreachable provider needs whoever owns the integration; an
uninspectable one is a deployment decision somebody made. Folding them into one
summary line would send all three to whoever read it first. A clean pass logs at
DEBUG, because a line that never changes is a line nobody reads.

## Reopening

The refusal to repair reopens only if the repair can be made from evidence
rather than inference — a provider that returns its own capture reference, a
module that can write a payment attributable to it. Nothing about the schedule
changes then; what changes is what a human is asked to approve.

The other-end walk (rejected option C) reopens with the first provider whose API
can list charges by date. It is additive: a second job, a second interface, and
the same rule that nothing writes.

## Related

- ADR 0009 — a capability with no consumer is an error class. The consumer here
  was named in `checkout/doc.go` before the capability existed, which is the
  order this rule wants.
- ADR 0016 — the operator's read surface, and the alerting half it left open.
- ADR 0017 — recovery is replayed from the record; a scheduled sweeper is
  refused. Nothing here acts, for the same reason and a bigger stake.
- ADR 0019 — the scheduler, and the promise it recorded as unkept.
