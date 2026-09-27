// Package giftcardsweep issues the gift cards a paid order bought and no
// capture delivery issued (ADR 0212).
//
// # Why a job, and why it may write
//
// The gift card sale flow runs on payment.captured, and a handler's error is
// logged rather than delivered again; a process that stops while a handler
// runs loses the event as well. An order whose flow failed before its cards
// were made would then never have them. This job walks the gift card lines of
// the orders placed in its window and hands the ones missing a card to the
// same door the capture uses.
//
// It writes, and it falls on the side of the line ADR 0017 draws that the
// outbox relay does: it undoes nothing, and it does what a paid order already
// promised. A card is named by its sale, so a card the capture made is never
// made twice, and a code is mailed by whichever of the two made the card.
package giftcardsweep

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
const Name = "gift-card-sweep"

// Every is how often the job runs. A buyer who paid for a card is waiting for
// its code, so a lost delivery is made good within minutes.
const Every = 5 * time.Minute

// MaxRun bounds one run.
const MaxRun = 4 * time.Minute

// Window is how far back a run looks: the orders placed in the last week. An
// order whose card no run could issue in a week of runs has a fault the logs
// have reported every five minutes, and it is left to a person.
const Window = 7 * 24 * time.Hour

// codeSweepFailed reports a run that could not sweep.
const codeSweepFailed = "giftcardsweep_failed"

// sweeper is the gift card sale flow as this job needs it.
type sweeper interface {
	Sweep(ctx context.Context, since time.Time) (orders, issued, waiting int, err error)
}

// Definition returns the job.
func Definition(s sweeper, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run:    func(ctx context.Context) error { return run(ctx, s, log, time.Now()) },
	}
}

// run sweeps the orders placed in the window ending now.
func run(ctx context.Context, s sweeper, log *slog.Logger, now time.Time) error {
	orders, issued, waiting, err := s.Sweep(ctx, now.Add(-Window))
	// What was issued before a failure is reported all the same: those cards
	// exist and their codes were mailed.
	jobreport.Report(ctx, fmt.Sprintf("issued %d gift cards on %d orders; %d orders wait for their capture",
		issued, orders, waiting))
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSweepFailed,
			"the sold gift cards could not all be issued")
	}
	if issued > 0 {
		// WARN: a card the capture should have issued was issued late, which
		// means a delivery failed and an operator may want to know why.
		log.WarnContext(ctx, "gift cards a capture did not issue were issued by the sweep",
			"cards", issued, "orders", orders)
	}

	return nil
}
