package returns

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
)

// RefundResult reports what refunding a return did.
type RefundResult struct {
	// ReturnID and OrderID locate the return.
	ReturnID string
	OrderID  string
	// CollectionID is the payment collection the money came out of.
	CollectionID string
	// RefundedAmount is what actually went back (minor unit).
	RefundedAmount int64
	// SummaryRecorded reports whether the order was told about it.
	//
	// FALSE does not mean the money stayed: it means the ORDER does not say it
	// left, and will keep reporting the customer as owing nothing back.
	SummaryRecorded bool
	// Warnings are the faults that did not stop the refund.
	Warnings []string
}

// RefundReturn sends money back for a return and records it on the order.
//
// # Why it is separate from receiving
//
// Receiving is a physical fact: the goods are in the building, so the stock is
// real whether anyone likes it or not. A refund is a DECISION — the goods may
// have come back damaged, incomplete, or outside the window — and a framework
// that paid automatically on receipt would be deciding what the shop has to
// decide.
//
// # The goods have to be here first
//
// A return that has not been received cannot be refunded. Money going back for
// goods nobody has seen is not a policy this flow can choose on the shop's
// behalf; a shop that wants to pay before inspection can receive first and
// refund immediately, which is the same two facts in the same order.
//
// # The order is told LAST, and a failure there does not undo the money
//
// The refund reaches a payment provider; the summary write is local. Doing the
// local write first would mean recording money that a provider then refused to
// send. Doing it after leaves the opposite risk — money sent and not recorded —
// and that one is REPORTED rather than hidden: the result says so, the log says
// so at ERROR, and the order's own reconciliation against its collection makes
// the difference visible (ADR 0020's argument, one level up).
func (w *Workflows) RefundReturn(
	ctx context.Context, returnID string, amount int64, reason string,
) (RefundResult, error) {
	if returnID == "" {
		return RefundResult{}, errors.Invalid(CodeInvalidInput, "the return id is required")
	}
	if amount < 0 {
		return RefundResult{}, errors.Invalid(CodeInvalidInput,
			"the refunded amount cannot be negative: %d", amount)
	}

	detail, err := w.readReturn(ctx, returnID)
	if err != nil {
		return RefundResult{}, err
	}
	if detail.Status != statusReceived {
		return RefundResult{}, errors.Conflict(CodeInvalidInput,
			"return %s has not been received; money is not sent back for goods nobody has seen",
			returnID)
	}

	collectionID, err := w.collectionOf(ctx, detail.OrderID)
	if err != nil {
		return RefundResult{}, err
	}

	refunded, err := w.payments.RefundCollection(ctx, collectionID, amount, reason)
	if err != nil && refunded == 0 {
		return RefundResult{}, errors.Wrap(err, errors.KindOf(err), CodeRefundFailed,
			"the refund for return %s could not be made", returnID)
	}

	result := RefundResult{
		ReturnID:       detail.ReturnID,
		OrderID:        detail.OrderID,
		CollectionID:   collectionID,
		RefundedAmount: refunded,
	}
	if err != nil {
		// Part of it went back. The order still has to learn about that part,
		// so the fault is carried as a warning rather than as a return value
		// that would make the caller think nothing moved.
		w.log.ErrorContext(ctx, "the refund was made only in part; a human has to finish it",
			"return_id", returnID, "collection_id", collectionID,
			"refunded", refunded, "error", err)
		result.Warnings = append(result.Warnings, "the refund was made only in part: "+err.Error())
	}

	w.recordRefund(ctx, collectionID, &result)

	return result, nil
}

// collectionOf finds the payment collection bound to the order.
func (w *Workflows) collectionOf(ctx context.Context, orderID string) (string, error) {
	linked, err := w.links.ListMany(ctx, LinkOrderPayment, []string{orderID})
	if err != nil {
		return "", errors.Wrap(err, errors.KindOf(err), CodeNoPayment,
			"the payment collection of order %s could not be read", orderID)
	}

	collections := linked[orderID]
	if len(collections) == 0 {
		return "", errors.Conflict(CodeNoPayment,
			"order %s has no payment collection bound to it, so there is nothing to refund from",
			orderID)
	}

	// The definition is one to one, so more than one is a data fault rather
	// than a choice. Taking the first keeps a broken link from stranding a
	// refund the operator already approved.
	return collections[0], nil
}

// recordRefund tells the order how much has now been refunded on it.
//
// # Why the figure comes from the COLLECTION and not from the refund
//
// The order's summary is CUMULATIVE — it is the lifetime refunded total, not
// the size of this refund — so adding the amount this call sent would be wrong
// the moment a second refund exists. The collection already keeps the running
// total, so it is read back and reported as it stands.
//
// That is also what makes the write safe to repeat: the merge on the order side
// keeps the larger value, and the value being reported is a total rather than a
// delta.
func (w *Workflows) recordRefund(ctx context.Context, collectionID string, result *RefundResult) {
	_, _, _, captured, refunded, err := w.payments.Collection(ctx, collectionID)
	if err != nil {
		w.log.ErrorContext(ctx,
			"the collection could not be read back after the refund; the money LEFT and the "+
				"order does not say so",
			"return_id", result.ReturnID, "order_id", result.OrderID,
			"collection_id", collectionID, "error", err)
		result.Warnings = append(result.Warnings,
			"the order was not told about the refund: "+err.Error())

		return
	}

	if err := w.orders.SetOrderSummaryTotals(ctx, result.OrderID, captured, refunded); err != nil {
		w.log.ErrorContext(ctx,
			"the refund could not be recorded on the order; the money LEFT and the order "+
				"still reports nothing refunded",
			"return_id", result.ReturnID, "order_id", result.OrderID,
			"collection_id", collectionID, "refunded", refunded, "error", err)
		result.Warnings = append(result.Warnings,
			"the order was not told about the refund: "+err.Error())

		return
	}

	result.SummaryRecorded = true
}
