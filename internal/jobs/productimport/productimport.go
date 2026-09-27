// Package productimport works through the catalog imports an operator sent
// (ADR 0205).
//
// # Why a job
//
// An import is a file of tens of thousands of rows, and a request that
// applied them would outlive the server's write timeout and the operator's
// patience. The endpoint keeps the file and answers; this job applies it, a
// row at a time, within its run, and the next run carries on from the row the
// last one reached. A minute apart is the cadence every other job here runs
// at, and an import is not in a hurry measured in seconds.
package productimport

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
const Name = "product-import"

// Every is how often the job runs.
const Every = time.Minute

// MaxRun bounds one run.
const MaxRun = 45 * time.Second

// budget is how long a run applies rows. It stops short of MaxRun so the row
// in hand and its record finish inside the run rather than being cut off.
const budget = 40 * time.Second

// codeImportFailed reports a run that could not apply its rows.
const codeImportFailed = "productimport_failed"

// importer is the product service as this job needs it.
type importer interface {
	ApplyImports(ctx context.Context, until time.Time) (int, error)
}

// Definition returns the job.
func Definition(i importer, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, i, log) },
	}
}

// run applies rows of the oldest open import until it ends or the budget does.
func run(ctx context.Context, i importer, log *slog.Logger) error {
	applied, err := i.ApplyImports(ctx, time.Now().Add(budget))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeImportFailed,
			"the catalog import could not be applied (%d rows applied first)", applied)
	}

	jobreport.Report(ctx, fmt.Sprintf("applied %d import rows", applied))
	if applied == 0 {
		log.DebugContext(ctx, "no catalog import had rows left")

		return nil
	}
	log.InfoContext(ctx, "catalog import rows were applied", "rows", applied)

	return nil
}
