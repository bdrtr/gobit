// Package cartretention deletes the open carts untouched for the shop's
// retention period (ADR 0301).
//
// # Why a job, and why it deletes
//
// A storefront opens a cart for every shopping session and most are never
// completed, so the carts outnumber the orders and nothing removed them: a
// guest's e-mail and address stayed for as long as the database did. The
// period is the shop's, as the controller of that data (ADR 0029), and with
// none set the job deletes nothing and says so. A completed cart is the
// record an order rests on and is never deleted.
//
// The rows are deleted for good, children with them, rather than stamped: a
// stamped row still holds what the shopper wrote.
package cartretention

import (
	"context"
	"fmt"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/core/job"
)

// Name is the job's name in the listing.
const Name = "cart-retention"

// Every is how often the job runs. A period is counted in days, so an hour
// late is a rounding of it.
const Every = time.Hour

// MaxRun bounds one run; what it leaves is the next run's.
const MaxRun = 5 * time.Minute

// batch is how many carts one delete removes, each batch its own transaction,
// so a run never holds the locks of thousands of rows at once.
const batch = 500

// codeRetentionFailed reports a run that could not delete.
const codeRetentionFailed = "cartretention_failed"

// carts is the cart service as this job needs it.
type carts interface {
	DeleteAbandonedCarts(ctx context.Context, now time.Time, limit int64) (int64, error)
	AbandonedCartsExpire() bool
}

// Definition builds the job.
func Definition(svc carts) job.Definition {
	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run: func(ctx context.Context) error {
			return run(ctx, svc, time.Now().UTC())
		},
	}
}

// run deletes batch after batch until one comes back short.
func run(ctx context.Context, svc carts, now time.Time) error {
	if !svc.AbandonedCartsExpire() {
		jobreport.Report(ctx, "no cart retention period is set; every cart is kept")
		return nil
	}

	var deleted int64
	defer func() {
		jobreport.Report(ctx, fmt.Sprintf("%d abandoned carts are deleted", deleted))
	}()
	for {
		n, err := svc.DeleteAbandonedCarts(ctx, now, batch)
		if err != nil {
			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeRetentionFailed,
				"the abandoned carts could not be deleted")
		}
		deleted += n
		if n < batch {
			return nil
		}
	}
}
