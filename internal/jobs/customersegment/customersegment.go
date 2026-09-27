// Package customersegment writes the members of the customer segments on a
// schedule (ADR 0217).
//
// # Why a job
//
// A segment's rule reads facts that change without an event this module could
// hear: a customer's age grows by the hour, and a window of orders moves on
// every day whether or not anyone orders. So the flow evaluates every rule over
// every customer, every hour.
//
// It writes, and falls on the permitted side of ADR 0017's line: the operator
// set the rule, and the job keeps the group what the rule says it is.
package customersegment

import (
	"context"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/core/job"
)

// Name is the job's name in the listing.
const Name = "customer-segments"

// Every is how often the job runs: a segment follows its rule within the hour.
const Every = time.Hour

// MaxRun bounds one run.
const MaxRun = 50 * time.Minute

// codePassFailed reports a run that could not write every segment.
const codePassFailed = "customersegment_failed"

// passer is the segment flow as this job needs it.
type passer interface {
	PassReport(ctx context.Context) (report string, err error)
}

// Definition returns the job.
func Definition(p passer, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, p, log) },
	}
}

// run makes one pass over the segments.
func run(ctx context.Context, p passer, log *slog.Logger) error {
	report, err := p.PassReport(ctx)
	// What a failed pass wrote stays, so its line is the operator's too.
	jobreport.Report(ctx, report)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codePassFailed,
			"the customer segments could not all be written")
	}
	log.InfoContext(ctx, "customer segments were written", "report", report)

	return nil
}
