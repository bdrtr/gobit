package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// CodeCollectionNothingToRefund reports that the collection has no refundable
// amount left.
const CodeCollectionNothingToRefund = "payment_collection_nothing_to_refund"

// CodeRefundExceedsCause reports a refund that would take what the refunds
// naming its cause gave back past the ceiling the caller named (ADR 0433).
const CodeRefundExceedsCause = "payment_refund_exceeds_cause"

// RefundCollection refunds an amount against a COLLECTION rather than against
// one capture, for a cause and up to a ceiling.
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
// Captures are drawn NEWEST FIRST, each up to what is left refundable on it,
// until the requested amount is covered. The order is the one
// `ListPaymentsByCollection` returns and [planRefund] walks that list as it
// comes.
//
// This paragraph claimed the opposite until ADR 0118 — "oldest first is
// deliberate rather than arbitrary" — with a reason built on top of it. The
// query has ordered by `created_at DESC` since it was written, so the reason
// described a behavior that never shipped. Which order is BETTER is a separate
// question this record does not answer: a provider that only refunds within a
// window of the capture would prefer the oldest drawn first, and changing the
// order is a behavior change nobody has asked for. The trigger is the first
// provider that reports a closed refund window.
//
// # The reference names the cause, and the ceiling bounds it
//
// Every refund row it writes carries the reference, in the transaction that
// writes the row (ADR 0187). The caller passes the id of its own record that
// caused the refund — a return, a claim, an exchange — so the order's books can
// tell a return's money from a claim's. The reference is required: this module
// does not read what it names, it sums by it (ADR 0433).
//
// The ceiling is the most the refunds naming that cause may give back between
// them, in every collection: what a return's units were sold for, the amount
// a claim's settle asks, an exchange's difference. The caller names the
// figure, because what the cause is worth is the caller's to know. An amount
// past what the cause has left under it is refused with
// [CodeRefundExceedsCause] before money moves, and a ZERO amount asks for what
// the cause has left, as far as the collection holds.
//
// # It is NOT idempotent, and the ceiling is what holds it
//
// For [Service.RefundPayment]'s reason, which applies unchanged: two calls for
// ten units are a real refund of twenty, and the record must show two lines.
// What holds a cause to its figure is the ceiling, and the sum is read twice:
// here, so that a refund past it moves nothing, and again by each part under
// the collection's lock before the provider is called, so that two refunds of
// one cause at once give back at most the ceiling between them. The first read
// alone would let both pass: a refund in flight is not yet a refund.
func (s *Service) RefundCollection(
	ctx context.Context, collectionID string, amount, ceiling int64, reason, reference string,
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
	if err := requireText("reference", reference); err != nil {
		return nil, err
	}
	if err := checkReference(reference); err != nil {
		return nil, err
	}
	if ceiling <= 0 {
		return nil, errors.Invalid(CodeInvalidInput,
			"the refunds naming %s are held to a ceiling, and it has to be positive: %d", reference, ceiling)
	}

	payments, err := s.store.ListPaymentsByCollection(ctx, collectionID)
	if err != nil {
		return nil, err
	}

	given, err := s.store.RefundedForReference(ctx, reference)
	if err != nil {
		return nil, err
	}
	left := ceiling - given
	if left <= 0 {
		return nil, errors.Conflict(CodeRefundExceedsCause,
			"the refunds naming %s gave back %d of the %d it may give back, so nothing is left",
			reference, given, ceiling)
	}
	if amount == 0 {
		amount = min(left, refundableOf(payments))
	}
	if amount > left {
		return nil, errors.Conflict(CodeRefundExceedsCause,
			"%d was asked back for %s, whose refunds gave back %d of the %d it may give back, so %d is left",
			amount, reference, given, ceiling, left)
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
		refund, refundErr := s.refundPayment(ctx, part.paymentID, part.amount, reason, reference, ceiling)
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

// refundableOf is what the captures still hold.
func refundableOf(payments []models.Payment) int64 {
	var refundable int64
	for i := range payments {
		refundable += payments[i].Amount - payments[i].RefundedAmount
	}
	return refundable
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
	refundable := refundableOf(payments)
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
