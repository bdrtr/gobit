package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// ReceiveReturn records that the returned goods arrived.
//
// # What it does NOT do
//
// It does not put the stock back and it does not refund anything. Both reach
// across modules — inventory and payment — and this module knows neither
// (Principle 2.1/2.4). What this method establishes is the FACT the flow will
// act on: these lines, in these quantities, are physically here.
//
// # Idempotent, and the second call keeps the FIRST moment
//
// A second receive is a no-op rather than a conflict: an operator clicking
// twice has already achieved what they wanted. It is deliberately not a
// re-write, because received_at is the moment the goods arrived and re-stamping
// it would make the record claim they arrived when somebody clicked.
//
// A canceled return cannot be received: the request was withdrawn, and goods
// arriving against a withdrawn request are a new request.
func (s *Service) ReceiveReturn(
	ctx context.Context, returnID, locationID string,
) (models.Return, error) {
	if err := requireID("location_id", locationID); err != nil {
		return models.Return{}, err
	}

	return s.transitionReturn(ctx, returnID, "receiving",
		models.ReturnStatus.ReceiveAction,
		func(ctx context.Context, id string) (models.Return, error) {
			return s.store.ReceiveReturn(ctx, id, locationID)
		})
}

// CancelReturn withdraws the return request.
//
// A RECEIVED return cannot be withdrawn, and that is the entry in the table
// that carries weight: the goods are physically in the warehouse, the record is
// the only thing that says where they came from, and canceling it would not
// un-receive them.
func (s *Service) CancelReturn(ctx context.Context, returnID string) (models.Return, error) {
	return s.transitionReturn(ctx, returnID, "canceling",
		models.ReturnStatus.CancelAction, s.store.CancelReturn)
}

// WHERE the goods arrived is required, and it is required HERE rather than
// derived, because nothing else in the system knows it.
//
// The order carries no location. The reservation knew one, but reservations are
// CONFIRMED at checkout — which consumes them — and their identifiers are not
// kept against the order. And deriving it from the warehouse that shipped would
// be wrong rather than merely unavailable: a customer may return to a different
// one, and the stock has to land where the goods actually are.

// transitionReturn applies one transition under the record's lock.
//
// The three transitions differ only in their table and their write, so the
// lock, the no-op branch and the conflict message live here once. Had each
// method carried its own copy, the "second call keeps the first moment" rule
// would have to be right in three places.
func (s *Service) transitionReturn(
	ctx context.Context,
	returnID, what string,
	action func(models.ReturnStatus) models.AfterSalesAction,
	write func(context.Context, string) (models.Return, error),
) (models.Return, error) {
	if err := requireID("return_id", returnID); err != nil {
		return models.Return{}, err
	}

	var out models.Return
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		current, err := s.store.LockReturn(ctx, returnID)
		if err != nil {
			return err
		}

		switch action(current.Status) {
		case models.AfterSalesNoop:
			s.log.DebugContext(ctx, "the return record is already in the target state, nothing was done",
				"return_id", returnID, "status", current.Status.String(), "action", what)
			out = current

			return nil
		case models.AfterSalesConflict:
			return errors.Conflict(CodeAfterSalesTransition,
				"%s is not possible on a return in status %q (%s)",
				what, current.Status.String(), returnID)
		case models.AfterSalesProceed:
			// Handled below.
		}

		out, err = write(ctx, returnID)

		return err
	})
	if err != nil {
		return models.Return{}, err
	}

	return out, nil
}

// checkReturnQuantities verifies that the requested lines belong to the order
// and that no line is asked back more times than it was bought.
//
// # Why the rule cannot live in the database
//
// It spans rows: what may be returned depends on every OTHER live return of the
// same line. A CHECK sees only its own row, so the sum is read here — under the
// order's lock, which is what makes reading it and writing against it atomic.
//
// # Why a canceled return does not count
//
// Withdrawing a request releases the units it was holding. A received one does
// not release them, and a still-requested one must count as well: two open
// requests could otherwise each claim a whole line and together ask back twice
// what was bought.
func checkReturnQuantities(
	lines []models.OrderLineItem,
	alreadyReturned map[string]int64,
	requested []ReturnLineInput,
) error {
	ordered := make(map[string]int64, len(lines))
	for i := range lines {
		ordered[lines[i].ID] = lines[i].Quantity
	}

	for i := range requested {
		bought, onOrder := ordered[requested[i].OrderLineItemID]
		if !onOrder {
			return errors.Invalid(CodeReturnLineUnknown,
				"line %s is not on this order", requested[i].OrderLineItemID)
		}

		total := alreadyReturned[requested[i].OrderLineItemID] + requested[i].Quantity
		if total > bought {
			return errors.Conflict(CodeReturnQuantityExceeded,
				"more of line %s was asked back than was bought: %d requested plus %d already, %d bought",
				requested[i].OrderLineItemID, requested[i].Quantity,
				alreadyReturned[requested[i].OrderLineItemID], bought)
		}
	}

	return nil
}

// checkReturnLines validates the shape of the requested lines before any read.
//
// The duplicate check is here rather than left to the unique index: the index
// would reject the SECOND insert, so the transaction would already have written
// the first and the error would name a constraint instead of the mistake.
func checkReturnLines(lines []ReturnLineInput) error {
	seen := make(map[string]bool, len(lines))
	for i := range lines {
		if err := requireID("order_line_item_id", lines[i].OrderLineItemID); err != nil {
			return err
		}
		if lines[i].Quantity <= 0 {
			return errors.Invalid(CodeInvalidInput,
				"the returned quantity has to be positive: line %s, quantity %d",
				lines[i].OrderLineItemID, lines[i].Quantity)
		}
		if err := checkAmount("refund_amount", lines[i].RefundAmount, models.MaxTotal); err != nil {
			return err
		}
		if seen[lines[i].OrderLineItemID] {
			return errors.Invalid(CodeInvalidInput,
				"line %s appears twice in the same return; the quantity carries the count",
				lines[i].OrderLineItemID)
		}
		seen[lines[i].OrderLineItemID] = true
	}

	return nil
}

// lineIDsOf collects the order line ids the request names.
func lineIDsOf(lines []ReturnLineInput) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		out = append(out, lines[i].OrderLineItemID)
	}

	return out
}
