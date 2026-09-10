package returns

import "context"

// InteropName is the name of the return flow in the container (ADR 0006).
//
// The order module's API resolves it BY NAME at request time: the flow is born
// after every module has registered, while the handler is built during
// registration, and deferring the resolution is how that circle is broken.
const InteropName = "workflows.returns.interop"

// Interop is the flow's cross-module surface.
//
// It carries only PRIMITIVE and stdlib types, so a consumer can declare the
// interface on its own side without importing this package (ADR 0001/0006).
type Interop struct {
	w *Workflows
}

// NewInterop builds the surface over the given flow.
func NewInterop(w *Workflows) *Interop { return &Interop{w: w} }

// RefundReturn sends money back for a received return and records it on the
// order.
//
// The result crosses as primitives for ADR 0006's reason. summaryRecorded being
// false does not mean the money stayed — it means the ORDER does not say it
// left, which is a fact the caller has to be able to show an operator.
func (i *Interop) RefundReturn(
	ctx context.Context, returnID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	out, err := i.w.RefundReturn(ctx, returnID, amount, reason)
	if err != nil {
		return 0, false, nil, err
	}

	return out.RefundedAmount, out.SummaryRecorded, out.Warnings, nil
}

// SettleClaim settles a damage or shortage claim by refunding it.
//
// A claim settled with a REPLACEMENT is refused rather than stamped and sent to
// [Interop.DispatchReplacement], which is the verb that settles it by sending
// goods.
func (i *Interop) SettleClaim(
	ctx context.Context, claimID string, amount int64, reason string,
) (refunded int64, summaryRecorded bool, warnings []string, err error) {
	out, err := i.w.SettleClaim(ctx, claimID, amount, reason)
	if err != nil {
		return 0, false, nil, err
	}

	return out.RefundedAmount, out.SummaryRecorded, out.Warnings, nil
}

// ReceiveReturn records that the returned goods arrived and puts their stock
// back.
//
// The result is reported as three primitives rather than a struct, for the
// reason ADR 0006 gives: a consumer that cannot import this package cannot name
// a shared type. warnings is non-empty when the record is right and the
// WAREHOUSE COUNT is not — every entry needs a human.
func (i *Interop) ReceiveReturn(
	ctx context.Context, returnID, locationID string,
) (restockedLines int, restockedUnits int64, warnings []string, err error) {
	out, err := i.w.ReceiveReturn(ctx, returnID, locationID)
	if err != nil {
		return 0, 0, nil, err
	}

	return out.RestockedLines, out.RestockedUnits, out.Warnings, nil
}

// DispatchReplacement sends what a claim promised: it sets the units aside,
// opens a parcel, takes the units out of the count and records all three.
//
// alreadySent being true means the goods had already gone and nothing moved
// this time. It crosses as its own value rather than being inferred, for the
// reason the fulfilling flow reports alreadyOpen: an operator who pressed the
// button twice has to be told the second press sent nothing.
func (i *Interop) DispatchReplacement(
	ctx context.Context, replacementID string,
) (fulfillmentID string, sentUnits int64, alreadySent bool, err error) {
	out, err := i.w.DispatchReplacement(ctx, replacementID)
	if err != nil {
		return "", 0, false, err
	}

	return out.FulfillmentID, out.SentUnits, out.AlreadySent, nil
}

// FundExchangeDifference records WHICH payment collection answers an exchange's
// difference.
//
// The verb is the flow's because deciding it needs both modules: the exchange
// says what it owes and the payment module says what the collection holds, and
// neither may ask the other (ADR 0006).
func (i *Interop) FundExchangeDifference(ctx context.Context, exchangeID, collectionID string) error {
	return i.w.FundExchangeDifference(ctx, exchangeID, collectionID)
}

// RefundExchangeDifference sends a funded exchange's money back and takes the
// request back with it.
//
// It is the EXIT from a funded exchange, which refuses the ordinary withdrawal.
// The two halves are one call so they cannot be done in the wrong order.
func (i *Interop) RefundExchangeDifference(ctx context.Context, exchangeID, reason string) error {
	return i.w.RefundExchangeDifference(ctx, exchangeID, reason)
}
