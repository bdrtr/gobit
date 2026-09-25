// Package scheduledpublish publishes the drafts whose moment has come (ADR 0177).
//
// # Why a job, and why this one is allowed to change what a shopper sees
//
// Every other window in this repository is judged when it is READ — a campaign
// out of its dates is refused by the computation, a price list out of its
// dates by the price ladder — and the job package's own godoc says why that is
// usually better than a job: flipping a status nobody reads changes nothing. A
// product is the case where it is not better. Whether a product is visible is
// decided in several places by its status alone, one of them a plugin's search
// index that learns from events; a moment judged at read time would have to be
// taught to all of them, and none of them would hear the moment arrive. Changing the
// status at the moment reuses every one of those answers and gives the index and
// the webhooks the event a publication by hand gives.
//
// It changes what a shopper sees, and that is on the right side of the line ADR
// 0017 draws: what is refused there is running compensations — undoing work —
// on a schedule nobody watched. This does what an operator scheduled, at the
// moment they named, and nothing else.
package scheduledpublish

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/core/job"
)

// Name identifies the job; it is the lock's input and the history row's key.
const Name = "scheduled-publish"

// Every is how often the job asks for due drafts.
//
// It is the publication's precision: a draft scheduled for 09:00 goes live
// between 09:00 and 09:01. A minute is what the outbox relay already runs at, so
// the scheduler's own granularity costs nothing new.
const Every = time.Minute

// MaxRun bounds one pass. A pass is one statement and a burst of events.
const MaxRun = 45 * time.Second

// limit is how many drafts one pass publishes. A launch of more than that at one
// moment is finished by the following passes, a minute apart, oldest moment
// first.
const limit = 500

// codePublishFailed reports a pass that could not publish.
const codePublishFailed = "scheduledpublish_failed"

// publisher is the product service as this job needs it.
type publisher interface {
	PublishDue(ctx context.Context, limit int64) ([]string, error)
}

// Definition returns the job.
func Definition(p publisher, log *slog.Logger) job.Definition {
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

// run makes one pass.
func run(ctx context.Context, p publisher, log *slog.Logger) error {
	ids, err := p.PublishDue(ctx, limit)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codePublishFailed,
			"the scheduled products could not be published")
	}

	jobreport.Report(ctx, fmt.Sprintf("published %d scheduled products", len(ids)))
	if len(ids) == 0 {
		// DEBUG: an installation with nothing scheduled runs this every minute
		// forever, and a line that never changes is a line nobody reads.
		log.DebugContext(ctx, "no scheduled product was due")

		return nil
	}

	// INFO, one line per product: a product going live is a business event an
	// operator may be asked about ("when did it appear?"), and the answer is
	// this line.
	for _, id := range ids {
		log.InfoContext(ctx, "a scheduled product was published", "product_id", id)
	}
	if len(ids) == limit {
		log.InfoContext(ctx, "more scheduled products may be due; the next pass continues",
			"published", len(ids))
	}

	return nil
}
