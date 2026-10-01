// Package offlineexpiry cancels the orders whose offline payment did not
// arrive within the wait its method was given (ADR 0289).
//
// # Why a job, and why it acts
//
// An order placed with a bank transfer owes its total and holds its stock until
// the money arrives or the shop cancels it (ADR 0284, ADR 0285). A shop that
// names a wait for the method has decided ahead what happens when the transfer
// never comes; this job carries that decision out at the moment it named, the
// way the gift card expiry carries out a card's term (ADR 0214). A method the
// shop gave no wait is never read, and cash on delivery, paid at the door after
// the parcel has left, is the method that should stay so.
//
// The cancel is the shop's own (order.CancelPlacedOrder): the stock comes back
// through the written-off lines, and the payment session is closed by the
// payment module when it hears the order canceled (ADR 0288).
package offlineexpiry

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/jobreport"
	"github.com/bdrtr/gobit/internal/core/job"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Name is the job's name in the listing.
const Name = "offline-order-expiry"

// Every is how often the job runs. A wait is counted in days, so a quarter of
// an hour late is a rounding of the term, not a breach of it.
const Every = 15 * time.Minute

// MaxRun bounds one run.
const MaxRun = 5 * time.Minute

// page is how many overdue sessions one read returns. A run reads page after
// page, so an order the job cannot cancel does not hold back the ones after it.
const page = 100

// Reason is what the canceled order records as its cancel reason.
const Reason = "the offline payment did not arrive within its wait"

// codeExpiryFailed reports a run that could not read or cancel.
const codeExpiryFailed = "offlineexpiry_failed"

// linkOrderPayment binds an order to its collection. The name is repeated as a
// literal, as every flow repeats it: the payment module declares the link.
const linkOrderPayment = "order_payment"

// overdue is the payment service as this job needs it.
type overdue interface {
	ListOverdueOffline(
		ctx context.Context, now time.Time, after paymentsvc.OverdueKey, limit int32,
	) ([]models.PaymentSession, error)
}

// links reads the order a collection was opened for.
type links interface {
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)
}

// canceler is the order service as this job needs it: the shop's cancel.
type canceler interface {
	CancelPlacedOrder(ctx context.Context, orderID, reason string) error
}

// Definition builds the job.
func Definition(payments overdue, bindings links, orders canceler, log *slog.Logger) job.Definition {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return job.Definition{
		Name:   Name,
		Every:  Every,
		MaxRun: MaxRun,
		Run: func(ctx context.Context) error {
			return run(ctx, payments, bindings, orders, time.Now().UTC(), log)
		},
	}
}

// tally is what one run did, for the operator's line.
//
// The line says the orders ARE canceled rather than that the run canceled them:
// a second cancel is not an error, so an order canceled before its session was
// closed is counted again until the payment module closes it (ADR 0288).
type tally struct {
	canceled, refused int
}

func (t tally) String() string {
	return fmt.Sprintf("%d orders whose offline payment did not arrive are canceled; %d could not be",
		t.canceled, t.refused)
}

// run cancels the order of every overdue session, page by page.
//
// An order the cancel refuses — completed, or with money collected after the
// listing — is the shop's to decide; it is counted and logged, and the run
// goes on. Any other failure ends the run, and the next one starts over.
func run(
	ctx context.Context, payments overdue, bindings links, orders canceler, now time.Time, log *slog.Logger,
) error {
	var done tally
	defer func() { jobreport.Report(ctx, done.String()) }()

	seen := map[string]bool{}
	key := paymentsvc.OverdueKey{}
	for {
		sessions, err := payments.ListOverdueOffline(ctx, now, key, page)
		if err != nil {
			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeExpiryFailed,
				"the overdue offline payments could not be read")
		}
		if len(sessions) == 0 {
			return nil
		}

		collections := make([]string, 0, len(sessions))
		for i := range sessions {
			if !slices.Contains(collections, sessions[i].PaymentCollectionID) {
				collections = append(collections, sessions[i].PaymentCollectionID)
			}
		}
		bound, err := bindings.ListManyByTo(ctx, linkOrderPayment, collections)
		if err != nil {
			return coreerrors.Wrap(err, coreerrors.KindOf(err), codeExpiryFailed,
				"the orders of the overdue offline payments could not be read")
		}

		for _, collectionID := range collections {
			for _, orderID := range bound[collectionID] {
				if seen[orderID] {
					continue
				}
				seen[orderID] = true
				if err := cancel(ctx, orders, orderID, log, &done); err != nil {
					return err
				}
			}
		}

		if len(sessions) < page {
			return nil
		}
		last := sessions[len(sessions)-1]
		key = paymentsvc.OverdueKey{OpenedAt: last.CreatedAt, SessionID: last.ID}
	}
}

// cancel cancels one order and counts what happened; only a failure that is
// not a refusal is returned.
func cancel(ctx context.Context, orders canceler, orderID string, log *slog.Logger, done *tally) error {
	err := orders.CancelPlacedOrder(ctx, orderID, Reason)
	switch {
	case err == nil:
		done.canceled++
	case coreerrors.IsConflict(err):
		done.refused++
		log.InfoContext(ctx, "an overdue offline order was not canceled; the shop decides it",
			"order_id", orderID, "error", err)
	default:
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeExpiryFailed,
			"the overdue order %s could not be canceled", orderID)
	}

	return nil
}
