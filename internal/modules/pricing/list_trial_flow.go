package pricing

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// CartFlowsName is the container name of the cart flows' surface, where the
// price list trial runs (ADR 0220).
//
// The flows package declares it as InteropName; this module cannot import that
// package (ADR 0006), so the string is repeated here, as the cart and promotion
// modules repeat it. A typo does not stay silent: the trial fails closed on its
// first request.
const CartFlowsName = "workflows.cart.interop"

// listTrial resolves the trial flow ON FIRST USE: the flows are built after
// every module has registered. The outcome of the first resolution is kept.
type listTrial struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	flow api.PriceListTrial
	err  error
}

var (
	_ api.PriceListTrial    = (*listTrial)(nil)
	_ service.ListTrialFlow = (*listTrial)(nil)
)

// TrialPriceListJSON prices the list against the orders of a period.
func (t *listTrial) TrialPriceListJSON(
	ctx context.Context, listID string, from, to time.Time,
) (json.RawMessage, error) {
	t.once.Do(func() { t.resolve(ctx) })
	if t.err != nil {
		return nil, t.err
	}
	return t.flow.TrialPriceListJSON(ctx, listID, from, to)
}

// resolve looks the flow up in the container and remembers the outcome; a name
// that does not resolve is a setup failure, whatever the container said.
func (t *listTrial) resolve(ctx context.Context) {
	flow, err := container.Resolve[api.PriceListTrial](t.c, CartFlowsName)
	if err != nil {
		t.err = errors.Wrap(err, errors.KindInternal, "pricing_setup_failed",
			"the %s module could not resolve the price list trial flow (%q)", Name, CartFlowsName)

		return
	}
	t.flow = flow
	t.log.InfoContext(ctx, "price list trial flow bound", "flow", CartFlowsName)
}
