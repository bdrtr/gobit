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
