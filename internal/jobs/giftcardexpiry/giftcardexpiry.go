// Package giftcardexpiry closes the gift cards whose moment has come
// (ADR 0214).
//
// # Why a job, when the moment is already enforced
//
// A card whose moment has come is refused as a payment from that moment: the
// gift-card provider reads the moment when a code is presented. What it cannot
// do is close the books: the card would go on holding a balance the shop no
// longer owes, and the journal would show a debt nobody can claim. This job
// closes the card the way an operator's close does (ADR 0213), which voids
// what it held and books it.
//
// It writes, and on the side of the line ADR 0017 draws that the scheduled
// publisher does: it carries out the term the card was made with, at the moment
// that term named, and undoes nothing a person decided.
package giftcardexpiry

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
const Name = "gift-card-expiry"

// Every is how often the job runs. The card stops paying at its moment
// whatever the job does, so the interval only decides how soon the books show
// it.
const Every = 15 * time.Minute

// MaxRun bounds one run.
const MaxRun = 5 * time.Minute

// batch is how many cards one run closes; more are left to the next run.
const batch = 100

// codeExpiryFailed reports a run that could not close its cards.
const codeExpiryFailed = "giftcardexpiry_failed"

// expirer is the payment service as this job needs it.
type expirer interface {
	ExpireGiftCards(ctx context.Context, limit int64) (closed, held int, err error)
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

// run closes one batch of expired cards.
func run(ctx context.Context, e expirer, log *slog.Logger) error {
	closed, held, err := e.ExpireGiftCards(ctx, batch)
	// What was closed before a failure is reported all the same: those cards
	// are closed and their balances voided.
	jobreport.Report(ctx, fmt.Sprintf("closed %d expired gift cards; %d wait for a payment that holds them",
		closed, held))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeExpiryFailed,
			"the expired gift cards could not all be closed")
	}
	if closed == batch {
		log.InfoContext(ctx, "more gift cards may have expired; the next run continues", "closed", closed)
	}

	return nil
}
