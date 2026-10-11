// Package paymentreports completes the carts whose payment a provider reported
// paid and no completion took yet (ADR 0438).
//
// # Why a job, and why it may write
//
// A provider whose customer pays on its own page reports the payment by
// posting back, and the report completes the cart at once. A completion that
// fails there — stock taken meanwhile, a fault on the way — waits in the
// payment module's report queue with the time of its next try, and a report
// the queue never took, because the process stopped between the provider's
// answer and the write, is found by asking the provider. This job does both,
// through the checkout's own door.
//
// It writes, on the side of the line ADR 0017 draws that the outbox relay and
// the gift card sweep do: it undoes nothing, and it does what a paid payment
// page already promised. An order is placed once, because the checkout's own
// key admits one completion of a cart and answers a session an order took with
// that order; a cart that can never be placed is given its money back.
package paymentreports

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/core/job"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// Name is the job's name in the listing.
const Name = "payment-reports"

// Every is how often the job runs: the first retry of a failed completion is
// due [checkoutwf.ReportRetryBase] after it, and a report the queue never took
// waits at most this long.
const Every = 5 * time.Minute

// MaxRun bounds one run.
const MaxRun = 4 * time.Minute

// codeSweepFailed reports a run that could not sweep.
const codeSweepFailed = "paymentreports_failed"

// Completer is the checkout as this job needs it; the application resolves
// it under the checkout's interop name.
type Completer interface {
	CompleteReportedPayments(ctx context.Context, now time.Time) (checkoutwf.ReportPass, error)
}

// Definition returns the job.
func Definition(c Completer, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, c, log, time.Now().UTC()) },
	}
}

// run sweeps the report queue once.
func run(ctx context.Context, c Completer, log *slog.Logger, now time.Time) error {
	pass, err := c.CompleteReportedPayments(ctx, now)
	jobreport.Report(ctx, summarize(pass))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSweepFailed,
			"the reported payments could not be swept")
	}
	if pass.Refunded > 0 || pass.GaveUp > 0 || pass.Left > 0 {
		// The checkout logged each one; this line says the pass found some,
		// for whoever reads the job's own lines.
		log.WarnContext(ctx, "reported payments were refunded, given up or left to a person",
			"refunded", pass.Refunded, "gave_up", pass.GaveUp, "left", pass.Left)
	}

	return nil
}

// summarize renders one pass as the single line `gobit jobs` prints, every
// count included so "nothing to do" and "never ran" do not look the same.
func summarize(pass checkoutwf.ReportPass) string {
	return fmt.Sprintf("completed %d, refunded %d, closed %d, left %d, retrying %d, gave up %d; found %d unreported",
		pass.Completed, pass.Refunded, pass.Closed, pass.Left, pass.Retrying, pass.GaveUp, pass.Discovered)
}
