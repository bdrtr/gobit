// Package paymentrecon compares this installation's payment ledger against
// each provider's own, and reports what does not match.
//
// # The hole it closes, and why nothing else could
//
// The payment module calls the provider INSIDE its own database transaction.
// That trade buys "exactly one authorization" (see the module's package doc)
// and it costs one thing: if the transaction fails to commit AFTER the provider
// moved the money, the rollback leaves the money gone and no local trace of it.
// The session stays authorized here while the provider says captured.
//
// Nothing local can see that. Every record that would show it is the record
// that was rolled back. internal/workflows/checkout/doc.go names the
// consequence — the saga reads the collection, sees nothing captured, and
// compensates an order that was paid for — and names the only correct closure:
//
//	"The only correct way to close (2) is to ASK the provider — that is,
//	reconciliation: a periodic comparison against the provider's own ledger."
//
// ADR 0019 built the scheduler and recorded this as the repository's one
// unkept periodic promise. This is that promise.
//
// # It REPORTS and does not repair
//
// Nothing here writes anything. Recording a capture off the back of a
// comparison would be this job deciding, alone and unwatched, that money moved
// — and ADR 0017 refuses that reasoning for compensations, which are cheaper
// than money. The repair stays a human's, with both ledgers in front of them.
// What changes is that the human learns there is one.
//
// # What it does NOT close
//
// A session whose row was never committed at all is invisible to this job, and
// no local query can find it: authorization and the row are written in the same
// transaction, so a rollback takes both. Finding those would mean listing the
// provider's ledger from the other end — walking every charge the provider
// holds and looking for the ones with no session here. That is a different
// query against a different API, it is not the same job, and it is not built.
// The class this job covers is the one where a local row EXISTS and disagrees.
//
// Refund amounts are likewise out of scope. The suspect set is authorized
// sessions, where the divergence destroys an order; a refund that differs is an
// accounting difference on an order that already survived.
package paymentrecon

import (
	"context"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/job"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Name is the job's name. It is the advisory lock's input and the primary key
// of its history, so it is a contract.
const Name = "payment-reconcile"

// Every is how often the comparison runs.
//
// Hourly, and the interval is bounded from BOTH sides rather than picked.
//
// It cannot be much slower: what this finds is an order the saga has already
// compensated, so the goods are already back in stock and the customer is
// already looking at a failure while their card is short. Every hour that
// passes is an hour of a refund conversation getting worse.
//
// It cannot be much faster either, and that limit is the provider's. The pass
// makes ONE network call per unsettled session, and an installation with
// delayed capture keeps sessions authorized legitimately for as long as
// fulfillment takes — so those same sessions are re-asked about on every pass,
// forever. At [limit] that is a bounded, predictable load; at a one-minute
// interval it would be sixty times the same questions an hour.
const Every = time.Hour

// MaxRun bounds one pass.
//
// The database side is not the cost: the listing is a partial-index scan
// measured at 0.56 ms over a 200,000-session fixture (52 buffers, against
// 12.0 ms and 3,618 buffers for the same query with the index dropped). The
// cost is [limit] sequential provider round trips.
//
// When the deadline lands mid-pass the pass does NOT lose what it found. The
// context error surfaces from the provider call, which is counted as
// unreachable and moved past, so the divergences already collected are still
// reported and the unreachable count says the pass was cut short.
const MaxRun = 5 * time.Minute

// settleWithin is how long a session may sit authorized before this job is
// willing to call it a divergence.
//
// A capture in flight is in EXACTLY the suspect state — authorized here,
// possibly already captured there — for as long as the provider takes to
// answer. Asking during that gap would report every ordinary payment as a
// discrepancy, which is the one thing a money alert must never do.
//
// Fifteen minutes is far above any provider call that can complete inside an
// HTTP request, and it is not the detection latency: a session that stays
// authorized for a week is compared on every pass for a week, because a long
// authorization is normal and only a captured-there amount is a finding.
const settleWithin = 15 * time.Minute

// limit caps one pass, and the cap is a provider budget rather than a database
// one.
//
// It is deliberately small. A hit cap is REPORTED as hit, and it has a
// consequence worth stating: the listing is oldest-first, so a permanently
// oversized suspect set means the same oldest sessions are examined every pass
// and newer ones are never reached. An installation in that state needs its
// operator, not a bigger number chosen here.
const limit = 50

// codeReconcileFailed reports that the pass could not be made at all.
const codeReconcileFailed = "paymentrecon_failed"

// reconciler is the narrow surface this job needs.
//
// It is declared HERE rather than taken as the concrete service, so the job
// depends on the one method it calls and a test can supply it without a
// database or a provider.
type reconciler interface {
	Reconcile(
		ctx context.Context, unchangedFor time.Duration, limit int,
	) (paymentsvc.ReconciliationReport, error)
}

// Definition builds the job.
func Definition(r reconciler, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, r, log) },
	}
}

// run makes one comparison and reports it.
func run(ctx context.Context, r reconciler, log *slog.Logger) error {
	report, err := r.Reconcile(ctx, settleWithin, limit)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeReconcileFailed,
			"the payment ledgers could not be compared")
	}

	if report.Clean() {
		// DEBUG, not INFO. A healthy installation runs this every hour forever,
		// and a line that never changes is a line nobody reads — which is how
		// the one that differs gets missed.
		log.DebugContext(ctx, "the payment ledgers agree",
			"examined", report.Examined, "agreed", report.Agreed)

		return nil
	}

	// The three findings are logged SEPARATELY rather than as one summary line,
	// because they need three different people. A divergence needs accounting.
	// An unreachable provider needs whoever owns the integration. An unaskable
	// one is a deployment decision somebody made. Folding them into a single
	// line would send all three to whoever reads it first.
	if len(report.Divergences) > 0 {
		reportDivergences(ctx, report, log)
	}

	if report.Unreachable > 0 {
		log.WarnContext(ctx, "some providers could not be asked, so their sessions were NOT compared; "+
			"this pass proves nothing about them",
			"unreachable", report.Unreachable, "examined", report.Examined)
	}

	if report.Unknown > 0 {
		// ERROR alongside a divergence, not below it. A provider that disowns a
		// session this module holds means an authorization went somewhere this
		// installation cannot see, and no amount of local repair reaches it.
		log.ErrorContext(ctx, "some sessions are unknown to the provider that supposedly opened them; "+
			"an authorization may be sitting on an account this installation does not read",
			"unknown", report.Unknown, "examined", report.Examined)
	}

	if report.Unaskable > 0 {
		// INFO: an installation running a provider that cannot be inspected is
		// a legitimate configuration, and it is a standing condition rather
		// than an event. What it must not do is look like agreement.
		log.InfoContext(ctx, "some sessions belong to a provider that cannot be inspected and were "+
			"NOT compared; their ledger is unverified by anything",
			"unaskable", report.Unaskable, "examined", report.Examined)
	}

	if report.Truncated {
		log.WarnContext(ctx, "the reconciliation pass filled its limit, so the newest sessions were "+
			"not reached; the listing is oldest-first and this repeats every pass until the backlog clears",
			"limit", limit, "examined", report.Examined)
	}

	return nil
}

// reportDivergences logs the sessions where the money and the record disagree.
func reportDivergences(ctx context.Context, report paymentsvc.ReconciliationReport, log *slog.Logger) {
	// ERROR, and the severity is the point: every one of these is money that
	// moved with nothing here to show for it, and an order that the saga has
	// most likely already rolled back.
	log.ErrorContext(ctx, "the provider reports money captured that this installation has no record of; "+
		"each of these needs a human with both ledgers open",
		"divergent", len(report.Divergences), "examined", report.Examined)

	for i := range report.Divergences {
		d := report.Divergences[i]
		// One line each, and the external id is on it: that is the value an
		// operator pastes into the provider's own dashboard, which is the
		// difference between a report and an alarm.
		log.ErrorContext(ctx, "a payment session disagrees with its provider",
			"session", d.SessionID,
			"collection", d.CollectionID,
			"provider", d.ProviderID,
			"external_id", d.ExternalID,
			"local_status", string(d.LocalStatus),
			"local_authorized", d.LocalAuthorized,
			"provider_status", string(d.ProviderStatus),
			"provider_captured", d.ProviderCaptured,
			"currency", d.CurrencyCode)
	}
}
