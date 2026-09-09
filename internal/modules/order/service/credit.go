package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// CodeCreditExceedsOrder reports a credit that would write off more than the
// order is worth.
const CodeCreditExceedsOrder = "order_credit_exceeds_order" //nolint:gosec // G101: not a credential, a constant error CODE returned to the client

// CreateCreditLineInput is the request to write off part of what an order owes.
type CreateCreditLineInput struct {
	// Amount is the credited amount (minor unit); it has to be POSITIVE.
	Amount int64
	// Reason is the merchant's short word for why; it is REQUIRED.
	//
	// This module does not enumerate it — a shop's vocabulary for a concession
	// is the shop's — but it refuses an unexplained one. A credit with no reason
	// is a number nobody can answer a question about six months later.
	Reason string
	// Note is free-form detail; it may be empty.
	Note string
}

// CreateCreditLine writes off part of what the order owes.
//
// # What it does NOT touch
//
// The order's total. That figure is the cart's snapshot — what was sold and at
// what price — and it is pinned to the order's own lines by a CHECK constraint.
// A concession agreed after the sale changes what the customer has left to pay,
// not what they bought; lowering the total would make the order disagree with
// its lines and would erase the fact that a concession was made at all.
//
// # The ceiling is checked under the ORDER's lock
//
// A credit may not take the credited total past the order's total. The check
// reads a SUM and then writes a row, and under READ COMMITTED two concurrent
// credits would each read the sum before the other committed — both would pass
// and the pair would exceed the ceiling. The order row is therefore locked
// first, which is what every state-changing flow in this module does.
//
// # Why the ceiling is the TOTAL and not the outstanding amount
//
// A credit granted after the customer has already paid is legitimate: it makes
// the outstanding amount negative, which is this module's word for "the shop
// owes the customer" and is exactly what a refund then settles. What is not
// legitimate is writing off more than the order was ever worth, which is a
// data-entry error rather than a concession.
func (s *Service) CreateCreditLine(
	ctx context.Context, orderID string, in CreateCreditLineInput,
) (models.OrderCreditLine, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderCreditLine{}, err
	}
	if in.Amount <= 0 {
		return models.OrderCreditLine{}, errors.Invalid(CodeInvalidInput,
			"a credit has to be positive: %d. A negative credit is a CHARGE, which is a "+
				"different act and does not belong on this endpoint", in.Amount)
	}
	if err := checkAmount("amount", in.Amount, models.MaxTotal); err != nil {
		return models.OrderCreditLine{}, err
	}

	// TRIMMED FIRST, then required. requireText refuses only the empty string,
	// and a reason of three spaces is not a reason — it is a reason somebody
	// thought they gave, which is the state this field exists to prevent.
	reason := strings.TrimSpace(in.Reason)
	if err := requireText("reason", reason); err != nil {
		return models.OrderCreditLine{}, err
	}

	var created models.OrderCreditLine

	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		order, lockErr := s.store.LockOrder(ctx, orderID)
		if lockErr != nil {
			return lockErr
		}

		credited, sumErr := s.store.CreditedTotal(ctx, orderID)
		if sumErr != nil {
			return sumErr
		}

		next, addErr := addAmount(credited, in.Amount)
		if addErr != nil {
			return addErr
		}
		if next > order.Total {
			return errors.Invalid(CodeCreditExceedsOrder,
				"the credit would write off more than the order is worth: %d already "+
					"credited plus %d is %d, and the order total is %d",
				credited, in.Amount, next, order.Total)
		}

		var createErr error
		created, createErr = s.store.CreateCreditLine(ctx, models.OrderCreditLine{
			ID:      models.NewCreditLineID(),
			OrderID: orderID,
			Amount:  in.Amount,
			Reason:  reason,
			Note:    in.Note,
		})

		return createErr
	})
	if err != nil {
		return models.OrderCreditLine{}, err
	}

	s.log.InfoContext(ctx, "an amount was written off the order",
		"order_id", orderID, "amount", created.Amount, "reason", created.Reason)

	return created, nil
}

// ListCreditLines returns the order's credit lines, oldest first.
func (s *Service) ListCreditLines(
	ctx context.Context, orderID string,
) ([]models.OrderCreditLine, error) {
	if err := requireID("order_id", orderID); err != nil {
		return nil, err
	}

	return s.store.ListCreditLines(ctx, orderID)
}
