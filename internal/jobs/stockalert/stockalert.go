// Package stockalert runs the wishlist alert flow on a schedule (ADR 0215): the
// stock marks, and the price marks (ADR 0216).
//
// # Why a job
//
// Nothing announces that a variant is back: the inventory module publishes no
// event (ADR 0063), and whether the storefront shows a variant in stock is an
// answer computed from several modules (ADR 0040). So the flow asks, for every
// marked wishlist item, what the storefront would answer now.
//
// It writes, and falls on the permitted side of ADR 0017's line: it sends the
// mail a proven customer asked for, to the address on their own record, once,
// and undoes nothing.
package stockalert

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/core/job"
)

// Name is the job's name in the listing.
const Name = "stock-alert"

// Every is how often the job runs: a shopper waiting for a variant hears of it
// within minutes of the storefront showing it.
const Every = 5 * time.Minute

// MaxRun bounds one run.
const MaxRun = 4 * time.Minute

// codePassFailed reports a run that could not handle its marks.
const codePassFailed = "stockalert_failed"

// passer is the wishlist alert flow as this job needs it.
type passer interface {
	Pass(ctx context.Context) (armed, recorded, mailed int, err error)
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

// run makes one pass over the marks.
func run(ctx context.Context, p passer, log *slog.Logger) error {
	armed, recorded, mailed, err := p.Pass(ctx)
	// What was mailed before a failure is reported all the same: those mails
	// went and their marks are cleared.
	jobreport.Report(ctx, fmt.Sprintf("mailed %d wishlist alerts; armed %d marks that ran out; "+
		"recorded %d prices at their mark", mailed, armed, recorded))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codePassFailed,
			"the stock alerts could not all be handled")
	}
	if mailed > 0 {
		log.InfoContext(ctx, "wishlist alerts were mailed", "mailed", mailed)
	}

	return nil
}
