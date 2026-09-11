package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// Error codes of a partial cancellation.
const (
	// CodeCancelQuantityExceeded reports a cancellation of more units than the
	// line has left.
	CodeCancelQuantityExceeded = "order_cancel_quantity_exceeded"
	// CodeCancelLineUnknown reports a line that is not on the order.
	CodeCancelLineUnknown = "order_cancel_line_unknown"
)

// CancelOrderLineInput is the request to write off part of one line.
type CancelOrderLineInput struct {
	// OrderLineItemID is the line whose units will not be delivered; it has to
	// belong to the order.
	OrderLineItemID string
	// Quantity is how many units are written off; it has to be POSITIVE and may
	// not take the line past what is left of it.
	Quantity int64
	// Reason is the merchant's short word for why; it is REQUIRED.
	//
	// This module does not enumerate it — out of stock, damaged, the customer
	// asked — because a shop's vocabulary is the shop's. What it refuses is an
	// unexplained one: a line that vanished for no recorded reason is a question
	// nobody can answer six months later.
	Reason string
	// Note is free-form detail; it may be empty.
	Note string
}

// CancelOrderLine writes off units of one line without touching the rest of the
// order.
//
// # Why this is not CancelOrder
//
// [Service.CancelOrder] is the checkout saga's COMPENSATION: it takes the whole
// order and refuses one that has collected money, because at that point nothing
// has shipped and nothing has been charged. The act here is the opposite
// situation — a live order, one line of it out of stock or dropped at the
// customer's request, and the rest shipping as normal.
//
// # What it does NOT do
//
// It does not move money. A unit already paid for and now not coming is a refund
// or a credit ([Service.CreateCreditLine]), and which of the two it is depends on
// a policy this module does not hold. Recording the goods and settling the money
// are two acts because they have two authorizations, and a cancellation before
// payment owes nothing at all.
//
// It does not change the order's STATUS either. An order whose every line is
// written off is still an order somebody has to close, and closing it is
// [Service.CancelOrder]'s or [Service.CompleteOrder]'s answer rather than a side
// effect of the last line going.
//
// # The ceiling is checked under the ORDER's lock
//
// A unit is spoken for once it has been asked back OR written off, and the sum
// of the two may not exceed what was bought. The check reads two SUMs and then
// writes a row; under READ COMMITTED two concurrent cancellations would each
// read the sums before the other committed, both would pass, and the pair would
// exceed the line. The order row is locked first, which is what every
// state-changing flow in this module does.
func (s *Service) CancelOrderLine(
	ctx context.Context, orderID string, in CancelOrderLineInput,
) (models.OrderLineCancellation, error) {
	if err := requireID("order_id", orderID); err != nil {
		return models.OrderLineCancellation{}, err
	}
	if err := requireID("order_line_item_id", in.OrderLineItemID); err != nil {
		return models.OrderLineCancellation{}, err
	}
	if in.Quantity <= 0 {
		return models.OrderLineCancellation{}, errors.Invalid(CodeInvalidInput,
			"a cancellation has to be positive: %d. Canceling zero units is not an act, "+
				"and a negative one would be an order for goods nobody placed", in.Quantity)
	}
	if err := checkQuantity(in.Quantity); err != nil {
		return models.OrderLineCancellation{}, err
	}

	// TRIMMED FIRST, then required: requireText refuses only the empty string,
	// and a reason of three spaces is a reason somebody thought they gave.
	reason := strings.TrimSpace(in.Reason)
	if err := requireText("reason", reason); err != nil {
		return models.OrderLineCancellation{}, err
	}
	if err := checkTextLen("note", in.Note); err != nil {
		return models.OrderLineCancellation{}, err
	}

	var (
		created   models.OrderLineCancellation
		variantID string
		before    int64
		bought    int64
	)

	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, lockErr := s.requireLiveOrder(ctx, orderID, "a line cancellation"); lockErr != nil {
			return lockErr
		}

		lines, listErr := s.store.ListLineItems(ctx, orderID)
		if listErr != nil {
			return listErr
		}

		spokenFor, sumErr := s.unitsSpokenFor(ctx, []string{in.OrderLineItemID})
		if sumErr != nil {
			return sumErr
		}
		if err := checkCancelQuantity(lines, spokenFor, in); err != nil {
			return err
		}

		// The CANCELED sum alone, not [Service.unitsSpokenFor]'s total.
		//
		// The event carries where this row sits among the line's cancellations, so
		// that a second one does not put back units the first already put back. A
		// total that included RETURNS would be the wrong ruler: returned goods are
		// restocked by the returns flow when they physically arrive, and counting
		// them here would make every cancellation after a return give back too few.
		canceledBefore, sumErr := s.store.CanceledQuantities(ctx, []string{in.OrderLineItemID})
		if sumErr != nil {
			return sumErr
		}

		var createErr error
		created, createErr = s.store.CreateLineCancellation(ctx, models.OrderLineCancellation{
			ID:              models.NewLineCancellationID(),
			OrderLineItemID: in.OrderLineItemID,
			Quantity:        in.Quantity,
			Reason:          reason,
			Note:            in.Note,
		})
		if createErr != nil {
			return createErr
		}

		line, found := lineByID(lines, in.OrderLineItemID)
		if !found {
			// checkCancelQuantity has already refused a line that is not on the
			// order, so reaching here means the two disagree — which is a bug in
			// this function rather than a caller's mistake.
			return errors.Internal(CodeInconsistentState,
				"line %s passed the cancellation ceiling and is not on order %s",
				in.OrderLineItemID, orderID)
		}

		variantID = line.VariantID
		before = canceledBefore[in.OrderLineItemID]
		bought = line.Quantity

		// The outbox row is written INSIDE this transaction, so a cancellation
		// cannot commit without its promise to say so (ADR 0134). A failure here
		// fails the write-off, because a write-off with no event is stock that
		// stays deducted forever with nothing anywhere saying it should not be.
		return s.recordLineCanceled(ctx, orderID, created, variantID, before, bought)
	})
	if err != nil {
		return models.OrderLineCancellation{}, err
	}

	// Published AFTER the commit, so a subscriber cannot read a cancellation that
	// is not there yet. The outbox row covers a lost publish; this is the fast path.
	s.publishLineCanceled(ctx, orderID, created, variantID, before, bought)

	return created, nil
}

// lineByID finds a line on the order.
func lineByID(lines []models.OrderLineItem, id string) (models.OrderLineItem, bool) {
	for i := range lines {
		if lines[i].ID == id {
			return lines[i], true
		}
	}

	return models.OrderLineItem{}, false
}

// ListLineCancellations returns the order's line cancellations, oldest first.
func (s *Service) ListLineCancellations(
	ctx context.Context, orderID string,
) ([]models.OrderLineCancellation, error) {
	if err := requireID("order_id", orderID); err != nil {
		return nil, err
	}

	return s.store.ListLineCancellations(ctx, orderID)
}

// unitsSpokenFor reports how many units of each line are no longer available to
// be claimed — asked back or written off, added together.
//
// The two reads are added HERE rather than compared separately because they are
// one ceiling. Two checks against the same bought quantity would each pass on
// their own while the pair went over it: three units bought, two asked back and
// two canceled is four units of a line that has three.
func (s *Service) unitsSpokenFor(
	ctx context.Context, lineItemIDs []string,
) (map[string]int64, error) {
	returned, err := s.store.ReturnedQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, err
	}

	canceled, err := s.store.CanceledQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(returned)+len(canceled))
	for id, quantity := range returned {
		out[id] = quantity
	}
	for id, quantity := range canceled {
		out[id] += quantity
	}

	return out, nil
}

// checkCancelQuantity refuses a cancellation the line cannot carry.
//
// It is [checkReturnQuantities] seen from the other side and it reads the same
// map: what is left of a line is what was bought minus what is already spoken
// for, whichever of the two acts spoke for it.
func checkCancelQuantity(
	lines []models.OrderLineItem, spokenFor map[string]int64, in CancelOrderLineInput,
) error {
	bought, onOrder := int64(0), false
	for i := range lines {
		if lines[i].ID == in.OrderLineItemID {
			bought, onOrder = lines[i].Quantity, true

			break
		}
	}
	if !onOrder {
		return errors.Invalid(CodeCancelLineUnknown,
			"line %s is not on this order", in.OrderLineItemID)
	}

	total := spokenFor[in.OrderLineItemID] + in.Quantity
	if total > bought {
		return errors.Conflict(CodeCancelQuantityExceeded,
			"more of line %s was canceled than is left: %d requested plus %d already "+
				"returned or canceled, %d bought",
			in.OrderLineItemID, in.Quantity, spokenFor[in.OrderLineItemID], bought)
	}

	return nil
}
