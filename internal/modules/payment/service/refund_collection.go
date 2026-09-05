package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// CodeCollectionNothingToRefund reports that the collection has no refundable
// amount left.
const CodeCollectionNothingToRefund = "payment_collection_nothing_to_refund"

// RefundCollection refunds an amount against a COLLECTION rather than against
// one capture.
//
// # Why the caller should not have to name a capture
//
// [Service.RefundPayment] needs a payment id, and a caller outside this module
// has no way to get one: nothing on the cross-module surface maps a collection
// to its captures. Worse, it should not have to — how a collected amount is
// split across captures is this module's bookkeeping, and a caller that had to
// know it would be re-deriving that split every time it wanted its money back.
//
// The order module's return flow wants to say "give this much back for this
// order". This is that sentence.
//
// # How the amount is spread
//
// Captures are drawn in the order they were made, each up to what is left
// refundable on it, until the requested amount is covered. Oldest first is
// deliberate rather than arbitrary: it keeps the refundable remainder
// concentrated on the most recent captures, which are the ones a later partial
// refund is most likely to be about.
//
// A zero amount refunds EVERYTHING that is left, which is what "give the
// customer their money back" means when nobody named a figure.
//
// # It is NOT idempotent
//
// For [Service.RefundPayment]'s reason, which applies unchanged: two calls for
// ten units are a real refund of twenty, and the record must show two lines.
// The caller is responsible for calling it once — in the return flow that
// guarantee comes from a return being refundable only once.
func (s *Service) RefundCollection(
	ctx context.Context, collectionID string, amount int64, reason string,
) ([]models.Refund, error) {
	if err := requireText("collection_id", collectionID); err != nil {
		return nil, err
	}
	if err := requireOptionalAmount("amount", amount); err != nil {
		return nil, err
	}
	if err := checkTextLen("reason", reason); err != nil {
		return nil, err
	}

	payments, err := s.store.ListPaymentsByCollection(ctx, collectionID)
	if err != nil {
		return nil, err
	}

	plan, err := planRefund(collectionID, payments, amount)
	if err != nil {
		return nil, err
	}

	// The refunds are made one at a time and NOT in a single transaction, and
	// that is not an oversight: each one calls a payment provider, and holding
	// a transaction open across several network calls is the trade the module's
	// package doc argues about for a single one. A failure part-way therefore
	// leaves the refunds already made STANDING — which is correct, because the
	// money really did go back.
	made := make([]models.Refund, 0, len(plan))
	for _, part := range plan {
		refund, refundErr := s.RefundPayment(ctx, part.paymentID, part.amount, reason)
		if refundErr != nil {
			if len(made) == 0 {
				return nil, refundErr
			}

			// Some of it went back. Reporting a plain error would tell the
			// caller nothing moved, and the caller would be right to retry the
			// WHOLE amount.
			return made, errors.Wrap(refundErr, errors.KindOf(refundErr), CodeCollectionNothingToRefund,
				"the refund of collection %s was made only in part: %d of %d refunds succeeded",
				collectionID, len(made), len(plan))
		}
		made = append(made, refund)
	}

	return made, nil
}

// refundPart is one capture's share of a collection-level refund.
type refundPart struct {
	paymentID string
	amount    int64
}

// planRefund decides which captures the amount comes out of.
//
// The plan is built BEFORE anything is refunded so that an amount larger than
// the collection can give back is rejected without having moved money. Doing it
// the other way round would refund what it could and then fail, leaving the
// caller with a partial refund it did not ask for.
func planRefund(
	collectionID string, payments []models.Payment, amount int64,
) ([]refundPart, error) {
	var refundable int64
	for i := range payments {
		refundable += payments[i].Amount - payments[i].RefundedAmount
	}
	if refundable <= 0 {
		return nil, errors.Conflict(CodeCollectionNothingToRefund,
			"collection %s has nothing left to refund", collectionID)
	}

	if amount == 0 {
		amount = refundable
	}
	if amount > refundable {
		return nil, errors.Conflict(CodeCollectionNothingToRefund,
			"more was asked back than collection %s holds: %d requested, %d refundable",
			collectionID, amount, refundable)
	}

	plan := make([]refundPart, 0, len(payments))
	left := amount
	for i := range payments {
		if left == 0 {
			break
		}

		available := payments[i].Amount - payments[i].RefundedAmount
		if available <= 0 {
			continue
		}

		part := available
		if part > left {
			part = left
		}
		plan = append(plan, refundPart{paymentID: payments[i].ID, amount: part})
		left -= part
	}

	return plan, nil
}
