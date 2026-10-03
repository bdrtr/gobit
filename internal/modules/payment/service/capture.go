package service

import (
	"context"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// CapturePayment captures the held amount and produces a [models.Payment].
//
// If amount is given as ZERO, the whole of the session's held amount is taken;
// it is the same rule as in the provider contract. More than the held amount
// cannot be taken (errors.Conflict).
//
// # A capture CLOSES the hold
//
// The captured session's held amount is subtracted IN FULL from the
// collection's held total: the part taken has turned into a capture, the part
// not taken is released. The session's own held amount also drops to the
// amount actually taken.
//
// Under a partial capture, omitting this was an unrepairable fault: since the
// session is now "captured" it cannot be canceled, and the difference not
// taken could not be released — the collection would answer the question "how
// much is on hold on the customer" with too much, forever. Real providers also
// release the remaining hold on capture.
//
// For the transition table see [models.SessionStatus.CaptureAction]. A second
// call on an already captured session with the SAME amount (or with zero)
// returns no error but the existing capture — at most one capture comes out of
// a session, and the idempotency comes from there. If a different amount is
// asked for, errors.Conflict is returned; that is no longer a repeat but a new
// request.
//
// Lock order: collection -> session.
func (s *Service) CapturePayment(ctx context.Context, sessionID string, amount int64) (models.Payment, error) {
	if err := requireText(fieldSessionID, sessionID); err != nil {
		return models.Payment{}, err
	}
	if err := requireOptionalAmount("amount", amount); err != nil {
		return models.Payment{}, err
	}

	prov, err := s.providerForSession(ctx, sessionID)
	if err != nil {
		return models.Payment{}, err
	}

	var out models.Payment
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, sessionID)
		if err != nil {
			return err
		}

		switch ses.Status.CaptureAction() {
		case models.ActionNoop:
			existing, err := s.store.PaymentBySession(ctx, ses.ID)
			if err != nil {
				return err
			}
			if amount != 0 && amount != existing.Amount {
				return errors.Conflict(CodeInvalidTransition,
					"the session was captured with an amount of %d; it cannot be captured again with %d (%s)",
					existing.Amount, amount, ses.ID)
			}
			s.log.DebugContext(ctx, "the session is already captured, nothing was done",
				"session", ses.ID, "payment", existing.ID)
			out = existing
			return nil
		case models.ActionConflict:
			return conflictTransition("cannot be captured", ses)
		case models.ActionProceed:
			// Handled below.
		}

		captured := amount
		if captured == 0 {
			captured = ses.AuthorizedAmount
		}
		if captured > ses.AuthorizedAmount {
			return errors.Conflict(CodeInvalidTransition,
				"the capture amount cannot exceed the held amount: %d requested, %d held (%s)",
				captured, ses.AuthorizedAmount, ses.ID)
		}

		if err := prov.Capture(ctx, ses.ExternalID, captured); err != nil {
			return err
		}

		// The session's hold CLOSES: the part taken turns into a capture, the
		// part not taken is released. The collection's held total shows only
		// the amount that is STILL on hold; since the session is now
		// "captured", if it were not subtracted here the same money would count
		// as both held and captured and reconciliation would break. Under a
		// partial capture it is graver still: since the session can no longer
		// be canceled, the difference not taken could never be released.
		blocked := ses.AuthorizedAmount
		if blocked > col.AuthorizedAmount {
			return errors.Internal(CodeInconsistentState,
				"the collection's held amount (%d) is smaller than the session's (%d) (%s)",
				col.AuthorizedAmount, blocked, ses.ID)
		}

		if _, err := s.store.UpdatePaymentSessionState(ctx, ses.ID,
			models.SessionCaptured, captured, ses.Data, ses.DeclineReason); err != nil {
			return err
		}

		payment, err := s.store.CreatePayment(ctx, models.Payment{
			ID:                  models.NewPaymentID(),
			PaymentSessionID:    ses.ID,
			PaymentCollectionID: col.ID,
			Amount:              captured,
			CurrencyCode:        ses.CurrencyCode,
			CapturedAt:          time.Now().UTC(),
		})
		if err != nil {
			return err
		}

		if _, err := s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount-blocked, col.CapturedAmount+captured, col.RefundedAmount); err != nil {
			return err
		}

		// The event is written to the outbox in the SAME transaction: the money
		// movement and its announcement either commit together or not at all.
		// The id is derived from the CAPTURE row, not from the collection — a
		// collection moves money many times, and an id keyed on the collection
		// would make the second movement look like a repeat of the first.
		if err := s.recordMoneyMoved(ctx, EventPaymentCaptured,
			payment.ID, col.ID, payment.CapturedAt); err != nil {
			return err
		}

		out = payment
		return nil
	})
	if err != nil {
		return models.Payment{}, err
	}

	// AFTER the commit, the fast path. The outbox relay covers a loss.
	s.publishMoneyMoved(ctx, EventPaymentCaptured, out.ID, out.PaymentCollectionID, out.CapturedAt)

	return out, nil
}

// RefundPayment refunds all or part of a captured amount.
//
// If amount is given as ZERO, the whole of the amount REMAINING on the capture
// is refunded. A request that exceeds the remaining amount returns
// errors.Conflict; if nothing is left to refund, errors.Conflict
// ([CodeNothingToRefund]) is returned as well.
//
// # Idempotency
//
// This method IS NOT IDEMPOTENT, and it would not be right for it to be: a
// 10-unit refund called twice is a real 20-unit refund, and the record has to
// show it as two rows. The place for repeat protection is not here but the
// request itself; calls coming from outside are protected by the idempotency
// middleware of Phase 9. Nor does the saga USE this step as a compensation —
// the compensation is [Service.CancelPayment], and that one is idempotent.
//
// Lock order: collection -> session -> capture.
func (s *Service) RefundPayment(
	ctx context.Context,
	paymentID string,
	amount int64,
	reason string,
) (models.Refund, error) {
	return s.refundPayment(ctx, paymentID, amount, reason, "")
}

// refundPayment is [Service.RefundPayment] with the reference of the record
// that caused the refund, which is written on the refund row in the same
// transaction (ADR 0187). An operator's refund has none.
func (s *Service) refundPayment(
	ctx context.Context,
	paymentID string,
	amount int64,
	reason, reference string,
) (models.Refund, error) {
	if err := requireText("payment_id", paymentID); err != nil {
		return models.Refund{}, err
	}
	if err := requireOptionalAmount("amount", amount); err != nil {
		return models.Refund{}, err
	}
	if err := checkTextLen("reason", reason); err != nil {
		return models.Refund{}, err
	}
	if err := checkReference(reference); err != nil {
		return models.Refund{}, err
	}

	preview, err := s.store.GetPayment(ctx, paymentID)
	if err != nil {
		return models.Refund{}, err
	}
	prov, err := s.providerForSession(ctx, preview.PaymentSessionID)
	if err != nil {
		return models.Refund{}, err
	}

	var refundedCollectionID string
	var out models.Refund
	err = s.store.WithTx(ctx, func(ctx context.Context) error {
		col, ses, err := s.lockCollectionAndSession(ctx, preview.PaymentSessionID)
		if err != nil {
			return err
		}
		payment, err := s.store.LockPayment(ctx, paymentID)
		if err != nil {
			return err
		}

		remaining := payment.RefundableAmount()
		if remaining == 0 {
			return errors.Conflict(CodeNothingToRefund,
				"the whole capture has already been refunded: %s", payment.ID)
		}
		refund := amount
		if refund == 0 {
			refund = remaining
		}
		if refund > remaining {
			return errors.Conflict(CodeInvalidTransition,
				"the refund amount cannot exceed the remaining amount: %d requested, %d remaining (%s)",
				refund, remaining, payment.ID)
		}

		if err := prov.Refund(ctx, ses.ExternalID, refund); err != nil {
			return err
		}

		if _, err := s.store.UpdatePaymentRefundedAmount(ctx, payment.ID,
			payment.RefundedAmount+refund); err != nil {
			return err
		}

		created, err := s.store.CreateRefund(ctx, models.Refund{
			ID:        models.NewRefundID(),
			PaymentID: payment.ID,
			Amount:    refund,
			Reason:    strings.TrimSpace(reason),
			Reference: reference,
		})
		if err != nil {
			return err
		}

		if _, err := s.writeCollectionTotals(ctx, col,
			col.AuthorizedAmount, col.CapturedAmount, col.RefundedAmount+refund); err != nil {
			return err
		}

		// The id is derived from the REFUND row. This method is deliberately
		// not idempotent — a ten-unit refund called twice is a real
		// twenty-unit refund — and an id keyed on the collection would silently
		// swallow the second refund, because the outbox row is written with
		// ON CONFLICT (id) DO NOTHING.
		if err := s.recordMoneyMoved(ctx, EventPaymentRefunded,
			created.ID, col.ID, created.CreatedAt); err != nil {
			return err
		}
		refundedCollectionID = col.ID

		out = created
		return nil
	})
	if err != nil {
		return models.Refund{}, err
	}

	s.publishMoneyMoved(ctx, EventPaymentRefunded, out.ID, refundedCollectionID, out.CreatedAt)

	return out, nil
}
