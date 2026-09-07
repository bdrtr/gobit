package checkout

// This file holds the saga's SECOND step: placing the order from the cart's
// snapshot.
//
// The step's quartet (Name/Restore/Invoke/Compensate) stands here together with
// cancelReason, the only helper it has. What all five steps share stays in
// steps.go.

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// createOrderStep places an order from the cart's snapshot.
type createOrderStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// createOrderOutput is the order step's output written to the execution record.
type createOrderOutput struct {
	// OrderID is the identifier of the order that was placed.
	OrderID string `json:"order_id"`
}

// Name returns the step's name.
func (s *createOrderStep) Name() string { return StepCreateOrder }

// Restore rebuilds the identifier of the placed order FROM THE RECORD.
func (s *createOrderStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	var out createOrderOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepCreateOrder)
	}
	if out.OrderID == "" {
		return errors.Internal(CodeSharedStateInvalid,
			"the record of step %q holds no order identifier", StepCreateOrder)
	}

	sc.Shared[sharedOrderID] = out.OrderID

	return nil
}

// Invoke places the order and writes its identifier into the shared map.
//
// The EXECUTION identifier is put into the snapshot as the idempotency key: a
// call repeated within the same execution does not place a new order. The
// "order.placed" event is published by the order module itself; this step
// publishes no event at all (see the package comment).
//
// # An EMPTY order identifier does not count as success
//
// If the order module returns an empty identifier without an error, the order
// WAS placed but we do NOT have its trace. Accepting the identifier silently
// would produce two lies: compensation would think "no order was ever placed"
// and do nothing (an ORPHAN order stays standing), and the result would report a
// "successful" order whose order_id is empty. The case is reported with
// [workflow.ErrUncompensated]; the stock is still released, but the execution is
// written compensation_failed instead of "rolled back".
func (s *createOrderStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	payload, err := s.plan.orderSnapshotJSON(sc.ExecutionID)
	if err != nil {
		return nil, err
	}

	orderID, err := s.w.orders.PlaceOrderJSON(ctx, payload)
	if err != nil {
		return nil, err
	}
	if orderID == "" {
		return nil, errors.Wrap(errors.Join(
			errors.Internal(CodeEmptyIdentifier,
				"the order module returned an EMPTY order identifier; an order that may have been placed cannot be canceled"),
			workflow.ErrUncompensated),
			errors.KindInternal, CodeEmptyIdentifier,
			"cart %s may have an order without an identifier left dangling; MANUAL INTERVENTION is required", s.plan.CartID)
	}
	sc.Shared[sharedOrderID] = orderID

	s.w.log.InfoContext(ctx, "order placed",
		"cart_id", s.plan.CartID, "order_id", orderID, "amount", s.plan.Amount)
	return createOrderOutput{OrderID: orderID}, nil
}

// Compensate cancels the order; it is IDEMPOTENT.
//
// If no order was ever placed (there is no identifier) the call is a no-op:
// compensation does not go looking for a record that does not exist.
//
// If a capture was made the order is NOT CANCELED (see
// [Workflows.skipAfterCapture]): canceling an order whose money has been taken
// would cost the customer both their money and their order. The order stays
// standing and the manual-intervention signal is read from the execution's
// status.
func (s *createOrderStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepCreateOrder, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	orderID, err := sharedText(sc, sharedOrderID)
	if err != nil {
		return err
	}
	if orderID == "" {
		return nil
	}

	if cancelErr := s.w.orders.CancelOrder(ctx, orderID, cancelReason(sc)); cancelErr != nil {
		return cancelErr
	}

	s.w.log.InfoContext(ctx, "compensation: order canceled",
		"cart_id", s.plan.CartID, "order_id", orderID)
	return nil
}

// cancelReason produces the order's cancellation reason.
//
// The reason carries the execution identifier: whoever looks at a canceled order
// must be able to find in the record which flow and which execution rolled it
// back.
func cancelReason(sc *workflow.StepContext) string {
	return "complete_cart compensation (execution: " + sc.ExecutionID + ")"
}
