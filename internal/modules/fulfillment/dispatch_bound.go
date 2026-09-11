package fulfillment

import (
	"context"
	"log/slog"
	"sync"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// fulfillingFlowName is the container name of the flow that answers how many units
// of an order line a parcel may still hold.
//
// It is re-spelled as a literal rather than imported, which is what a module has to
// do for a name that lives above it: importing the flow would make this module
// depend on the layer that depends on it (Principle 2.4).
const fulfillingFlowName = "workflows.fulfilling.interop"

// dispatchBound resolves the fulfilling flow on FIRST USE.
//
// # Why it is lazy
//
// The flow is built after every module has registered, and this module's service is
// built during registration. Resolving at request time is how that circle is
// broken, and it is the shape the cart module already uses for its pricing flow —
// the same problem with the same answer.
//
// # Why a failure is remembered
//
// The resolution is attempted once and the outcome is kept, error included. A
// missing flow is a composition fault rather than a transient one: retrying it per
// request would turn one startup mistake into a stream of identical log lines and
// would answer differently from one request to the next for no reason a reader
// could find.
type dispatchBound struct {
	c   *container.Container
	log *slog.Logger

	once sync.Once
	svc  service.DispatchBound
	err  error
}

// newDispatchBound holds the container until the first question.
func newDispatchBound(c *container.Container, log *slog.Logger) *dispatchBound {
	return &dispatchBound{c: c, log: log}
}

// DispatchableQuantities answers what the order still owes, per line.
//
// An unresolvable flow answers an ERROR rather than an empty map, and the
// difference is the whole point: the service treats a line missing from the map as
// "not on this order" and refuses it, so an empty map would look like a refusal of
// every line and read to an operator as a data problem. The error says what is
// actually wrong — nobody is answering.
func (b *dispatchBound) DispatchableQuantities(
	ctx context.Context, orderID string, lineItemIDs []string,
) (map[string]int64, error) {
	b.once.Do(func() {
		b.svc, b.err = container.Resolve[service.DispatchBound](b.c, fulfillingFlowName)
		if b.err != nil {
			b.err = errors.Wrap(b.err, errors.KindInternal, codeSetupFailed,
				"the %s module could not resolve the fulfilling flow (%q); a parcel "+
					"cannot be opened without knowing what the order still owes",
				ModuleName, fulfillingFlowName)

			return
		}
		b.log.InfoContext(ctx, "dispatch bound flow bound", "flow", fulfillingFlowName)
	})
	if b.err != nil {
		return nil, b.err
	}

	return b.svc.DispatchableQuantities(ctx, orderID, lineItemIDs)
}
