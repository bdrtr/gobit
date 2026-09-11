package fulfilling

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// OpenResult reports what opening a shipment did.
type OpenResult struct {
	// OrderID and FulfillmentID locate the two records.
	OrderID       string
	FulfillmentID string
	// AlreadyOpen reports that the idempotency key had already opened this
	// shipment and nothing new was created.
	//
	// It is REPORTED rather than inferred: an operator who pressed the button
	// twice has to be told the second press did nothing, and the alternative —
	// answering the same way either way — is how a shop ends up printing two
	// labels for one parcel and finding out at the carrier.
	AlreadyOpen bool
}

// OpenForOrder opens a shipment for an order and binds the two.
//
// # The order is read FIRST, and only to be refused
//
// The fulfillment module never validates the reference it is handed, by
// decision (Principle 2.2), so an unknown order id would open a real parcel
// bound to nothing. This flow is the only place that holds both sides, so it is
// the only place that can refuse. What it reads is the contact block, for no
// reason other than that reading it fails with a not-found.
//
// # The binding is written AFTER the shipment exists
//
// The other order is impossible: there is nothing to bind until the shipment
// has an id. So the failure this leaves is the one it can leave — a parcel that
// exists with no binding — and it is REPORTED rather than hidden: the error
// carries its own code, and the operator is told the shipment id so the link
// can be repaired instead of the parcel being opened a second time.
//
// Retrying is safe in both directions: the same idempotency key returns the
// same shipment, and binding a pair that is already bound is a no-op.
func (w *Workflows) OpenForOrder(
	ctx context.Context, orderID, optionID, idempotencyKey string,
) (OpenResult, error) {
	switch {
	case strings.TrimSpace(orderID) == "":
		return OpenResult{}, errors.Invalid(CodeInvalidInput, "the order id is required")
	case strings.TrimSpace(optionID) == "":
		return OpenResult{}, errors.Invalid(CodeInvalidInput, "the shipping option id is required")
	case strings.TrimSpace(idempotencyKey) == "":
		return OpenResult{}, errors.Invalid(CodeInvalidInput,
			"an idempotency key is required; without one a retried request opens a SECOND "+
				"parcel for the same order and the shop finds out at the carrier")
	}

	if _, err := w.orders.OrderContactJSON(ctx, orderID); err != nil {
		return OpenResult{}, errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
			"order %s could not be read, so no shipment was opened for it", orderID)
	}

	// What is bound before the call is what tells an "already open" apart from
	// a new one. Reading it after the call could not: the shipment would be
	// bound either way by then.
	bound, err := w.boundFulfillments(ctx, orderID)
	if err != nil {
		return OpenResult{}, err
	}

	fulfillmentID, err := w.fulfillments.CreateFulfillment(ctx, orderID, optionID, idempotencyKey)
	if err != nil {
		return OpenResult{}, errors.Wrap(err, errors.KindOf(err), CodeCreateFailed,
			"a shipment could not be opened for order %s", orderID)
	}

	// An idempotency key OUTLIVES the shipment it opened, so a key whose
	// shipment was later canceled still resolves to it.
	//
	// The module is right to return it — its contract is "the same key returns
	// the same shipment" and it says nothing about status. What was wrong was
	// this flow's answer: the binding written on the first open is NOT removed
	// by a cancel, so AlreadyOpen was computed from a link that outlived the
	// parcel and reported a canceled shipment as an open one. The caller then
	// believed goods were on their way and nothing was ever going to ship.
	//
	// So the status is read and a canceled shipment is REFUSED rather than
	// reported. The caller asked to OPEN one; the honest answer is that this
	// key cannot open anything any more, and the message says which shipment it
	// names so a fresh key is an informed choice rather than a guess.
	//
	// The read costs one call on every open. That is accepted: opening a parcel
	// is an operator action rather than a request path, and the alternative was
	// to widen a cross-module surface (ADR 0006) for a field one caller needs.
	status, err := w.fulfillments.FulfillmentStatus(ctx, fulfillmentID)
	if err != nil {
		return OpenResult{}, errors.Wrap(err, errors.KindOf(err), CodeCreateFailed,
			"shipment %s was opened for order %s but its status could not be read",
			fulfillmentID, orderID)
	}
	if status == statusCanceled {
		return OpenResult{}, errors.Conflict(CodeShipmentCanceled,
			"the idempotency key names shipment %s, which was canceled; nothing was opened "+
				"for order %s. A canceled shipment cannot be reopened — repeat the request "+
				"with a NEW key to open another one",
			fulfillmentID, orderID)
	}

	result := OpenResult{
		OrderID:       orderID,
		FulfillmentID: fulfillmentID,
		AlreadyOpen:   bound[fulfillmentID],
	}

	// The binding is NOT written here any more. The fulfillment module owns the
	// "order_fulfillment" definition and writes it inside CreateFulfillment, which
	// is the call above — so both ways of opening a parcel bind, where before only
	// this one did and the admin endpoint's item-carrying parcels were attributable
	// to no order at all (ADR 0140). A second write here would be the same rule in
	// two places.

	return result, nil
}

// Shipment is one shipment of an order, as the order's side sees it.
type Shipment struct {
	// FulfillmentID is the shipment's identifier.
	FulfillmentID string
	// Status is what the fulfillment module says about it. It is EMPTY when the
	// module could not be asked — see [Workflows.ShipmentsOfOrder].
	Status string
}

// ShipmentsOfOrder lists the shipments bound to an order.
//
// # Why a status that could not be read is empty rather than an error
//
// The binding is the fact this flow owns; the status belongs to the other
// module. An order with three parcels, one of whose statuses cannot be read,
// still has three parcels — answering the whole request with an error would
// hide the two that are fine, and the caller asked "what shipments does this
// order have".
func (w *Workflows) ShipmentsOfOrder(ctx context.Context, orderID string) ([]Shipment, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the order id is required")
	}

	bound, err := w.boundFulfillments(ctx, orderID)
	if err != nil {
		return nil, err
	}

	shipments := make([]Shipment, 0, len(bound))
	for fulfillmentID := range bound {
		status, statusErr := w.fulfillments.FulfillmentStatus(ctx, fulfillmentID)
		if statusErr != nil {
			w.log.ErrorContext(ctx, "the status of a bound shipment could not be read",
				"order_id", orderID, "fulfillment_id", fulfillmentID, "error", statusErr)
		}
		shipments = append(shipments, Shipment{FulfillmentID: fulfillmentID, Status: status})
	}

	return shipments, nil
}

// boundFulfillments is the set of shipments bound to the order.
func (w *Workflows) boundFulfillments(ctx context.Context, orderID string) (map[string]bool, error) {
	linked, err := w.links.ListMany(ctx, LinkOrderFulfillment, []string{orderID})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeLinkUnreadable,
			"the shipments of order %s could not be read", orderID)
	}

	bound := make(map[string]bool, len(linked[orderID]))
	for _, id := range linked[orderID] {
		bound[id] = true
	}

	return bound, nil
}
