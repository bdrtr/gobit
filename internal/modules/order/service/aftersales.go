package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// This file is the SKELETON of the after-sales records (return, exchange,
// claim) (plan Section 6, Phase 6 scope).
//
// All three share the same pattern: the record is born in the "requested"
// status, it is listed and it is read one by one. The status transitions, the
// line-based return, taking the stock back and refunding the payment are the
// job of the NEXT PHASES; that is why there is no transition method here. The
// reason the skeleton is built now is that neither the order's schema nor the
// API envelope should be forced to change in that phase.
//
// # Why a record cannot be opened on a canceled order
//
// All three creations take the order's lock and reject a canceled order. On a
// canceled order there are no delivered goods: there is nothing to return, to
// exchange or to be damaged either. The lock makes the check RACE-FREE —
// between a lockless read and the write the order could be canceled and the
// record could end up attached to a canceled order.
//
// The EXISTENCE of the order is not checked separately; the lock already
// returns NotFound.
//
// # The ceiling of the refund amount
//
// The amount of the return/claim record cannot exceed the TOTAL of the order:
// the money of goods that were not sold cannot be given back. The check is done
// under the lock, against the read state of the order (see
// [Service.requireLiveOrder]).
//
// The ceiling not being the paid_total of the summary is deliberate: the record
// is a REQUEST and it has to be possible to open it while the collection has
// not been written yet; associating it with the payment is the job of the
// return flow (the next phase). The rule cannot be translated into a database
// constraint either — a CHECK stays within a single row,
// order_returns.refund_amount and orders.total are in DIFFERENT tables and the
// only way to enforce this would be a trigger.

// CreateReturnInput is the input of a new return record.
type CreateReturnInput struct {
	// OrderID is the order the return belongs to; it is REQUIRED.
	OrderID string
	// RefundAmount is the amount planned to be refunded (minor unit).
	RefundAmount int64
	// Reason is the reason for the return; it is optional.
	Reason string
	// Note is free text; it is optional.
	Note string
	// Metadata is the caller's free extra data.
	Metadata map[string]any
	// Lines are the order lines coming back.
	//
	// It may be empty, and that is not the same as "the whole order": an empty
	// return says WHICH lines are unknown, which is the state every return
	// record was in before lines existed. A flow cannot restock from it.
	Lines []ReturnLineInput
}

// ReturnLineInput is one line of the order coming back.
type ReturnLineInput struct {
	// OrderLineItemID is the line; it has to belong to the order.
	OrderLineItemID string
	// Quantity is how many units come back; it has to be positive and, added
	// to what earlier live returns already asked for, may not exceed what was
	// bought.
	Quantity int64
	// RefundAmount is the part of the refund falling on this line.
	RefundAmount int64
}

// CreateReturn opens a return record on the order.
//
// The record is always born in the [models.ReturnRequested] status: a return is
// a REQUEST and the received stamp is put on it when the goods are really taken
// back, in the workflow of the next phase.
func (s *Service) CreateReturn(ctx context.Context, in CreateReturnInput) (models.Return, error) {
	if err := requireID("order_id", in.OrderID); err != nil {
		return models.Return{}, err
	}
	if err := checkAmount("refund_amount", in.RefundAmount, models.MaxTotal); err != nil {
		return models.Return{}, err
	}
	if err := checkTextLen("reason", in.Reason); err != nil {
		return models.Return{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.Return{}, err
	}
	if err := checkReturnLines(in.Lines); err != nil {
		return models.Return{}, err
	}

	var created models.Return
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.requireLiveOrder(ctx, in.OrderID, "a return record")
		if err != nil {
			return err
		}
		if err := checkRefundWithinOrder(order, in.RefundAmount); err != nil {
			return err
		}
		lines, err := s.store.ListLineItems(ctx, in.OrderID)
		if err != nil {
			return err
		}

		// The rule is checked BEFORE the record is written, under the order's
		// lock taken by requireLiveOrder. Writing first and validating after
		// would leave a rejected return in the table for the length of the
		// transaction, and the sum the NEXT request reads is exactly that
		// table.
		returned, err := s.store.ReturnedQuantities(ctx, lineIDsOf(in.Lines))
		if err != nil {
			return err
		}
		if err := checkReturnQuantities(lines, returned, in.Lines); err != nil {
			return err
		}

		created, err = s.store.CreateReturn(ctx, models.Return{
			ID:           models.NewReturnID(),
			OrderID:      in.OrderID,
			Status:       models.ReturnRequested,
			RefundAmount: in.RefundAmount,
			Reason:       in.Reason,
			Note:         in.Note,
			Metadata:     in.Metadata,
		})
		if err != nil {
			return err
		}

		created.Items = make([]models.ReturnItem, 0, len(in.Lines))
		for i := range in.Lines {
			item, itemErr := s.store.CreateReturnItem(ctx, models.ReturnItem{
				ID:              models.NewReturnItemID(),
				ReturnID:        created.ID,
				OrderLineItemID: in.Lines[i].OrderLineItemID,
				Quantity:        in.Lines[i].Quantity,
				RefundAmount:    in.Lines[i].RefundAmount,
			})
			if itemErr != nil {
				return itemErr
			}
			created.Items = append(created.Items, item)
		}

		return nil
	})
	if err != nil {
		return models.Return{}, err
	}
	return created, nil
}

// GetReturn returns the return record by its identifier.
func (s *Service) GetReturn(ctx context.Context, returnID string) (models.Return, error) {
	if err := requireID("return_id", returnID); err != nil {
		return models.Return{}, err
	}
	return s.store.GetReturn(ctx, returnID)
}

// ListReturns returns the return records of the order in pages; the second
// value is the total count.
func (s *Service) ListReturns(ctx context.Context, orderID string, page Page) ([]models.Return, int64, error) {
	filter, err := childFilter(orderID, page)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListReturns(ctx, filter)
}

// CreateExchangeInput is the input of a new exchange record.
type CreateExchangeInput struct {
	// OrderID is the order the exchange belongs to; it is REQUIRED.
	OrderID string
	// DifferenceDue is the difference of the exchange (minor unit) and IT MAY BE
	// NEGATIVE: when positive the difference is collected from the customer,
	// when negative it is paid to the customer.
	DifferenceDue int64
	// Note is free text; it is optional.
	Note string
	// Metadata is the caller's free extra data.
	Metadata map[string]any
}

// CreateExchange opens an exchange record on the order.
func (s *Service) CreateExchange(ctx context.Context, in CreateExchangeInput) (models.Exchange, error) {
	if err := requireID("order_id", in.OrderID); err != nil {
		return models.Exchange{}, err
	}
	if err := checkSignedAmount("difference_due", in.DifferenceDue, models.MaxTotal); err != nil {
		return models.Exchange{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.Exchange{}, err
	}

	var created models.Exchange
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, err := s.requireLiveOrder(ctx, in.OrderID, "an exchange record"); err != nil {
			return err
		}
		var err error
		created, err = s.store.CreateExchange(ctx, models.Exchange{
			ID:            models.NewExchangeID(),
			OrderID:       in.OrderID,
			Status:        models.ExchangeRequested,
			DifferenceDue: in.DifferenceDue,
			Note:          in.Note,
			Metadata:      in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.Exchange{}, err
	}
	return created, nil
}

// GetExchange returns the exchange record by its identifier.
func (s *Service) GetExchange(ctx context.Context, exchangeID string) (models.Exchange, error) {
	if err := requireID("exchange_id", exchangeID); err != nil {
		return models.Exchange{}, err
	}
	return s.store.GetExchange(ctx, exchangeID)
}

// ListExchanges returns the exchange records of the order in pages; the second
// value is the total count.
func (s *Service) ListExchanges(ctx context.Context, orderID string, page Page) ([]models.Exchange, int64, error) {
	filter, err := childFilter(orderID, page)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListExchanges(ctx, filter)
}

// CreateClaimInput is the input of a new claim record.
type CreateClaimInput struct {
	// OrderID is the order the claim belongs to; it is REQUIRED.
	OrderID string
	// Type is how the claim will be settled; it is REQUIRED.
	Type models.ClaimType
	// RefundAmount is the amount to be refunded while Type is
	// [models.ClaimRefund].
	RefundAmount int64
	// Reason is the reason for the claim; it is optional.
	Reason string
	// Note is free text; it is optional.
	Note string
	// Metadata is the caller's free extra data.
	Metadata map[string]any
}

// CreateClaim opens a damage/shortage record on the order.
//
// For a claim of the [models.ClaimReplace] type the amount has to be ZERO: on a
// claim whose goods are sent again there is no money to refund, and a filled-in
// amount would mean a silent double payment where the customer receives both
// the goods and the money.
func (s *Service) CreateClaim(ctx context.Context, in CreateClaimInput) (models.Claim, error) {
	if err := requireID("order_id", in.OrderID); err != nil {
		return models.Claim{}, err
	}
	if !in.Type.Valid() {
		return models.Claim{}, errors.Invalid(CodeInvalidInput,
			"undefined claim record type: %q (valid: %q, %q)",
			in.Type, models.ClaimRefund, models.ClaimReplace)
	}
	if err := checkAmount("refund_amount", in.RefundAmount, models.MaxTotal); err != nil {
		return models.Claim{}, err
	}
	if in.Type == models.ClaimReplace && in.RefundAmount != 0 {
		return models.Claim{}, errors.Invalid(CodeInvalidInput,
			"on a claim of the %q type refund_amount has to be zero: %d", models.ClaimReplace, in.RefundAmount)
	}
	if err := checkTextLen("reason", in.Reason); err != nil {
		return models.Claim{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.Claim{}, err
	}

	var created models.Claim
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, err := s.requireLiveOrder(ctx, in.OrderID, "a claim record")
		if err != nil {
			return err
		}
		if err := checkRefundWithinOrder(order, in.RefundAmount); err != nil {
			return err
		}
		created, err = s.store.CreateClaim(ctx, models.Claim{
			ID:           models.NewClaimID(),
			OrderID:      in.OrderID,
			Type:         in.Type,
			Status:       models.ClaimRequested,
			RefundAmount: in.RefundAmount,
			Reason:       in.Reason,
			Note:         in.Note,
			Metadata:     in.Metadata,
		})
		return err
	})
	if err != nil {
		return models.Claim{}, err
	}
	return created, nil
}

// GetClaim returns the claim record by its identifier.
func (s *Service) GetClaim(ctx context.Context, claimID string) (models.Claim, error) {
	if err := requireID("claim_id", claimID); err != nil {
		return models.Claim{}, err
	}
	return s.store.GetClaim(ctx, claimID)
}

// ListClaims returns the claim records of the order in pages; the second value
// is the total count.
func (s *Service) ListClaims(ctx context.Context, orderID string, page Page) ([]models.Claim, int64, error) {
	filter, err := childFilter(orderID, page)
	if err != nil {
		return nil, 0, err
	}
	return s.store.ListClaims(ctx, filter)
}

// requireLiveOrder LOCKS the order, verifies that it is not canceled and
// returns its state under the lock.
//
// The lock prevents a cancellation from slipping in between the check and the
// writing of the record; a lockless check would only be true "at that moment".
//
// The order is RETURNED so that the amount checks can be done under the same
// lock and on the same read state; a second read could be stale.
func (s *Service) requireLiveOrder(ctx context.Context, orderID, what string) (models.Order, error) {
	order, err := s.store.LockOrder(ctx, orderID)
	if err != nil {
		return models.Order{}, err
	}
	if order.Canceled() {
		return models.Order{}, errors.Conflict(CodeNotPending,
			"%s cannot be opened on a canceled order: %s", what, orderID)
	}
	return order, nil
}

// checkRefundWithinOrder verifies that the refund amount does not exceed the
// total of the order.
//
// A zero amount is always valid: on a claim record whose goods are sent again
// ([models.ClaimReplace]) there is no money to refund.
func checkRefundWithinOrder(order models.Order, refundAmount int64) error {
	if refundAmount > order.Total {
		return errors.Invalid(CodeRefundExceedsOrder,
			"the refund amount cannot exceed the total of the order: refund_amount=%d, order total=%d (%s)",
			refundAmount, order.Total, order.ID)
	}
	return nil
}

// childFilter validates and builds the criteria of the return/exchange/claim
// listing.
func childFilter(orderID string, page Page) (models.ChildFilter, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.ChildFilter{}, err
	}
	normalized, err := page.normalize()
	if err != nil {
		return models.ChildFilter{}, err
	}
	return models.ChildFilter{
		OrderID: orderID,
		Limit:   normalized.Limit,
		Offset:  normalized.Offset,
	}, nil
}

// checkSignedAmount validates the magnitude of a SIGNED amount.
//
// It is separate from [checkAmount] because it DOES NOT REJECT a negative
// value: the difference of an exchange can arise in both directions (see
// [models.Exchange.DifferenceDue]). What is validated is that the magnitude
// stays within the bound; because the absolute value of the smallest int64
// cannot be taken, the comparison is done separately at the two ends.
func checkSignedAmount(label string, value, upper int64) error {
	if value > upper || value < -upper {
		return errors.Invalid(CodeInvalidInput,
			"%s has to be between -%d and %d: %d", label, upper, upper, value)
	}
	return nil
}
