// Package storecreditexpiry takes back what expired store credit still holds
// (ADR 0258).
//
// # Why a job, when the moment is already enforced
//
// Credit whose moment has come stops paying at that moment: the balance a
// tender spends is read less what expired credit still holds. What that read
// cannot do is close the books: the ledger would go on summing to money the
// shop no longer owes, and the journal would show a debt nobody can claim. This
// job writes the expire row that makes the sum say what the balance already
// says, the way the gift card expiry closes a card (ADR 0214).
//
// It carries out the term the credit was issued with, at the moment that term
// named, and undoes nothing a person decided.
package storecreditexpiry

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
const Name = "store-credit-expiry"

// Every is how often the job runs. The credit stops paying at its moment
// whatever the job does, so the interval only decides how soon the books show
// it.
const Every = 15 * time.Minute

// MaxRun bounds one run.
const MaxRun = 5 * time.Minute

// batch is how many balances one run settles; more are left to the next run.
const batch = 100

// codeExpiryFailed reports a run that could not settle its balances.
const codeExpiryFailed = "storecreditexpiry_failed"

// expirer is the payment service as this job needs it.
type expirer interface {
	ExpireStoreCredit(ctx context.Context, limit int64) (int, error)
}

// Definition returns the job.
func Definition(e expirer, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, e, log) },
	}
}

// run settles one batch of balances.
func run(ctx context.Context, e expirer, log *slog.Logger) error {
	written, err := e.ExpireStoreCredit(ctx, batch)
	// What was written before a failure is reported all the same: those rows
	// are committed.
	jobreport.Report(ctx, fmt.Sprintf("took back expired credit from %d balances", written))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeExpiryFailed,
			"the expired store credit could not all be taken back")
	}
	if written == batch {
		log.InfoContext(ctx, "more store credit may have expired; the next run continues", "written", written)
	}

	return nil
}
