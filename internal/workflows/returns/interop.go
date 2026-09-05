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
// A claim settled with a REPLACEMENT is refused rather than stamped: shipping
// goods against an existing order is not a capability this framework has.
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
