package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// SummaryTotalsInput are the CUMULATIVE amounts reported to the payment summary
// of the order.
//
// Their being cumulative rather than incremental is deliberate: payment events
// are delivered at least once (see core/eventbus) and an incremental write would
// add the amount TWICE on a repeated event. The values given here mean "so far a
// total of this much has been collected / refunded"; reporting the same value a
// second time is harmless.
//
// The values ARE NOT OVERWRITTEN onto the record, they are MERGED with it; for
// the rationale see [Service.SetOrderSummaryTotals].
type SummaryTotalsInput struct {
	// PaidTotal is the total amount COLLECTED against the order (minor unit).
	PaidTotal int64
	// RefundedTotal is the total amount PAID BACK to the customer (minor unit).
	RefundedTotal int64
}

// GetOrderSummary returns the payment/refund summary of the order.
//
// Because the summary is born together with the order it is found for every
// order that exists; NotFound is only returned when the order does not exist (or
// when the summary was deleted directly with SQL).
func (s *Service) GetOrderSummary(ctx context.Context, orderID string) (models.OrderSummary, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderSummary{}, err
	}
	return s.store.GetSummary(ctx, orderID)
}

// SetOrderSummaryTotals reports the paid and the refunded amounts of the order.
//
// # Who calls this surface
//
// NOT the payment module: the two modules do not know each other (Principle
// 2.1/2.4). The side that knows the result of the collection is the
// complete_cart workflow or a subscriber listening to the payment events, and it
// comes here through a narrow interface it resolved from the container.
//
// # Why the write is a MERGE and not an overwrite
//
// The reported values are not overwritten onto the record; for every field the
// LARGER of the recorded value and the reported value is kept. The reason is
// that the caller is fed from the event bus: delivery is AT LEAST ONCE and the
// order IS NOT GUARANTEED. On an overwriting endpoint the reprocessing of a
// late collection event would silently zero out a refund recorded after it — the
// call would return without an error and the recorded money would disappear.
//
// Both amounts are the LIFETIME total of the order and by their nature they only
// grow; that is why the merge loses no data and the call becomes both idempotent
// and ORDER INDEPENDENT. A report that shrinks the value DOES NOT return an
// error, it is ignored and logged at the DEBUG level: a late delivery is a fact,
// not a mistake of the caller, and returning an error would put the subscriber
// into an endless retry. Correcting an amount that was written wrongly is NOT
// the job of this surface.
//
// # Why under the lock of the order
//
// Every writing flow of the module starts by locking the order (see [Store]);
// this path, which writes money, is no exception either. The lock additionally
// verifies that the order REALLY exists: without a lock only the summary row
// would be looked up and the error of a missing order would look like "the
// summary was not found".
//
// # Why it is not compared with the order total
//
// PaidTotal EXCEEDING the order total is not rejected. Overcollection is a real
// fact (an exchange rate difference, a correction made on the provider's side)
// and rejecting it would mean not being able to record a collection that really
// happened. The difference shows up as a NEGATIVE through
// [models.OrderSummary.Outstanding]; it is not clipped to zero, because clipping
// would make the overcollection invisible.
//
// The only structural rule is that the refunded amount cannot exceed the
// collected one: an amount that was not collected cannot be refunded. The rule
// stands in the database as well (order_summaries_refund_within_paid) and the
// merge cannot break it.
func (s *Service) SetOrderSummaryTotals(ctx context.Context, orderID string, in SummaryTotalsInput) (models.OrderSummary, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderSummary{}, err
	}
	if err := checkAmount("paid_total", in.PaidTotal, models.MaxTotal); err != nil {
		return models.OrderSummary{}, err
	}
	if err := checkAmount("refunded_total", in.RefundedTotal, models.MaxTotal); err != nil {
		return models.OrderSummary{}, err
	}
	if in.RefundedTotal > in.PaidTotal {
		return models.OrderSummary{}, errors.Invalid(CodeSummaryInvalid,
			"the refunded amount cannot exceed the collected one: refunded_total=%d, paid_total=%d",
			in.RefundedTotal, in.PaidTotal)
	}

	var merged models.OrderSummary
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.store.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		merged, err = s.store.SetSummaryTotals(ctx, orderID, in.PaidTotal, in.RefundedTotal)
		if err != nil {
			return err
		}
		if merged.PaidTotal != in.PaidTotal || merged.RefundedTotal != in.RefundedTotal {
			s.log.DebugContext(ctx, "a late summary report was ignored, the recorded amounts were preserved",
				"order_id", orderID, "display_id", order.DisplayID,
				"reported_paid", in.PaidTotal, "reported_refunded", in.RefundedTotal,
				"recorded_paid", merged.PaidTotal, "recorded_refunded", merged.RefundedTotal)
		}
		return nil
	})
	if err != nil {
		return models.OrderSummary{}, err
	}
	return merged, nil
}
