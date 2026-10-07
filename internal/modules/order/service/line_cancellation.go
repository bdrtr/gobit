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
	// ReadSpokenFor, when given, is how many of the line's units were asked
	// back or written off when the caller read the line; the cancellation is
	// refused with [CodeLineMoved] if that has changed, so a form sent twice
	// writes off once (ADR 0341).
	ReadSpokenFor *int64
}

// CodeLineMoved refuses a line cancellation whose line had more or fewer of
// its units asked back or written off than the caller read (ADR 0341).
const CodeLineMoved = "order_line_moved"

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

	// done are the cancellations the transaction made, kept to publish after
	// the commit: the line asked for and, since ADR 0230, each of its add-ons.
	var done []writtenOff

	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		if _, lockErr := s.requireLiveOrder(ctx, orderID, "a line cancellation"); lockErr != nil {
			return lockErr
		}

		lines, listErr := s.store.ListLineItems(ctx, orderID)
		if listErr != nil {
			return listErr
		}
		// An add-on is written off with its line and never alone (ADR 0230):
		// an engraving for a ring that is not coming is not coming either.
		targets, err := cancellationTargets(lines, in.OrderLineItemID)
		if err != nil {
			return err
		}
		if err := refuseGiftCardLines(lines, targets, "written off"); err != nil {
			return err
		}

		spokenFor, sumErr := s.unitsSpokenFor(ctx, targets)
		if sumErr != nil {
			return sumErr
		}
		if in.ReadSpokenFor != nil && spokenFor[in.OrderLineItemID] != *in.ReadSpokenFor {
			return errors.Conflict(CodeLineMoved,
				"order line %s has %d units asked back or written off now, not %d; draw the page again",
				in.OrderLineItemID, spokenFor[in.OrderLineItemID], *in.ReadSpokenFor)
		}
		for _, id := range targets {
			if err := checkCancelQuantity(lines, spokenFor, CancelOrderLineInput{
				OrderLineItemID: id, Quantity: in.Quantity,
			}); err != nil {
				return err
			}
		}

		// The CANCELED sum alone, not [Service.unitsSpokenFor]'s total.
		//
		// The event carries where this row sits among the line's cancellations, so
		// that a second one does not put back units the first already put back. A
		// total that included RETURNS would be the wrong ruler: returned goods are
		// restocked by the returns flow when they physically arrive, and counting
		// them here would make every cancellation after a return give back too few.
		canceledBefore, sumErr := s.store.CanceledQuantities(ctx, targets)
		if sumErr != nil {
			return sumErr
		}

		for _, id := range targets {
			created, createErr := s.store.CreateLineCancellation(ctx, models.OrderLineCancellation{
				ID:              models.NewLineCancellationID(),
				OrderLineItemID: id,
				Quantity:        in.Quantity,
				Reason:          reason,
				Note:            in.Note,
			})
			if createErr != nil {
				return createErr
			}

			line, found := lineByID(lines, id)
			if !found {
				// checkCancelQuantity has already refused a line that is not on
				// the order, so reaching here means the two disagree — which is a
				// bug in this function rather than a caller's mistake.
				return errors.Internal(CodeInconsistentState,
					"line %s passed the cancellation ceiling and is not on order %s", id, orderID)
			}
			w := writtenOff{
				cancellation: created, variantID: line.VariantID,
				before: canceledBefore[id], bought: line.Quantity,
			}

			// The outbox row is written INSIDE this transaction, so a cancellation
			// cannot commit without its promise to say so (ADR 0134). A failure
			// here fails the write-off, because a write-off with no event is stock
			// that stays deducted forever with nothing anywhere saying it should
			// not be.
			if err := s.recordLineCanceled(ctx, orderID, w.cancellation, w.variantID, w.before, w.bought); err != nil {
				return err
			}
			done = append(done, w)
		}
		return nil
	})
	if err != nil {
		return models.OrderLineCancellation{}, err
	}

	// Published AFTER the commit, so a subscriber cannot read a cancellation that
	// is not there yet. The outbox row covers a lost publish; this is the fast path.
	for i := range done {
		s.publishLineCanceled(ctx, orderID, done[i].cancellation, done[i].variantID, done[i].before, done[i].bought)
	}

	return done[0].cancellation, nil
}

// orderCanceledReason is the reason a whole order's write-off records when the
// cancel gave none; a line cancellation always carries one.
const orderCanceledReason = "order canceled"

// writtenOff is one line cancellation a transaction made, kept to publish after
// the commit.
type writtenOff struct {
	cancellation models.OrderLineCancellation
	variantID    string
	before       int64
	bought       int64
}

// writeOffRemaining writes off every unit of the order's lines that is not yet
// returned or written off, and records each write-off's event; the caller holds
// the order's lock and publishes after the commit (ADR 0285).
//
// A line that sold gift cards is left out: it moved no stock, and a card is
// closed in the payment module ([refuseGiftCardLines]).
func (s *Service) writeOffRemaining(ctx context.Context, orderID, reason string) ([]writtenOff, error) {
	if reason == "" {
		reason = orderCanceledReason
	}
	lines, err := s.store.ListLineItems(ctx, orderID)
	if err != nil {
		return nil, err
	}
	targets := make([]string, 0, len(lines))
	for i := range lines {
		if !lines[i].IsGiftcard {
			targets = append(targets, lines[i].ID)
		}
	}
	if len(targets) == 0 {
		return nil, nil
	}

	spokenFor, err := s.unitsSpokenFor(ctx, targets)
	if err != nil {
		return nil, err
	}
	// The CANCELED sum alone is where each new row sits, for
	// [Service.CancelOrderLine]'s reason.
	canceledBefore, err := s.store.CanceledQuantities(ctx, targets)
	if err != nil {
		return nil, err
	}

	var out []writtenOff
	for i := range lines {
		line := lines[i]
		left := line.Quantity - spokenFor[line.ID]
		if line.IsGiftcard || left <= 0 {
			continue
		}
		created, err := s.store.CreateLineCancellation(ctx, models.OrderLineCancellation{
			ID:              models.NewLineCancellationID(),
			OrderLineItemID: line.ID,
			Quantity:        left,
			Reason:          reason,
		})
		if err != nil {
			return nil, err
		}
		w := writtenOff{
			cancellation: created, variantID: line.VariantID,
			before: canceledBefore[line.ID], bought: line.Quantity,
		}
		if err := s.recordLineCanceled(ctx, orderID, w.cancellation, w.variantID, w.before, w.bought); err != nil {
			return nil, err
		}
		out = append(out, w)
	}

	return out, nil
}

// cancellationTargets is the line a write-off names followed by its add-ons,
// which go with it (ADR 0230); an add-on named alone is refused.
func cancellationTargets(lines []models.OrderLineItem, lineID string) ([]string, error) {
	if line, found := lineByID(lines, lineID); found && line.ParentLineItemID != nil {
		return nil, addOnFollows(lineID, *line.ParentLineItemID)
	}
	targets := []string{lineID}
	addOns := addOnsOf(lines, lineID)
	for i := range addOns {
		targets = append(targets, addOns[i].ID)
	}
	return targets, nil
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

// CanceledUnits answers, per line, how many units were written off.
//
// It is the CANCELED sum alone rather than [Service.unitsSpokenFor]'s total,
// because the two answer different questions: the ceiling asks what is spoken for
// by either act, and a dispatch asks what will not be delivered. A returned unit
// shipped and came back, so counting it here would bound a parcel by goods that
// already left once.
func (s *Service) CanceledUnits(ctx context.Context, lineItemIDs []string) (map[string]int64, error) {
	if len(lineItemIDs) == 0 {
		return map[string]int64{}, nil
	}

	return s.store.CanceledQuantities(ctx, lineItemIDs)
}

// SpokenForUnits answers, per line, how many of its units a return or a
// replacement speaks for: what the line's requested and received returns ask
// back plus what its requested and dispatched replacements send again. A
// withdrawn return or replacement speaks for nothing, and a replacement item
// naming a variant rather than a line is no line's (ADR 0423).
//
// An exchange that names its return takes a line's units back through that
// return and sends the line's units again through its replacements: the same
// goods named twice. It speaks for each of the line's units once, the more of
// the two, so the fewer is taken off the sum (ADR 0432). A return no exchange
// names, and a claim's or an exchange's replacement beside a return it does
// not name, count as they are.
//
// It is not [Service.CanceledUnits]'s partner in a bound: a dispatch is held to
// what was sold less what was written off. It is what a parcel that came back
// undelivered holds its units to, read with the ceiling. A claim settled with
// money names no line, so what it paid for is not here.
func (s *Service) SpokenForUnits(ctx context.Context, lineItemIDs []string) (map[string]int64, error) {
	if len(lineItemIDs) == 0 {
		return map[string]int64{}, nil
	}

	returned, err := s.store.ReturnedQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, err
	}
	replaced, err := s.store.ReplacedQuantities(ctx, lineItemIDs)
	if err != nil {
		return nil, err
	}
	twice, err := s.store.ExchangeOverlapUnits(ctx, lineItemIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]int64, len(returned)+len(replaced))
	for line, units := range returned {
		out[line] += units
	}
	for line, units := range replaced {
		out[line] += units
	}
	for line, units := range twice {
		out[line] -= units
	}

	return out, nil
}
