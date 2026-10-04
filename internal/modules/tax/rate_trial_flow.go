package tax

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// CartFlowsName is the container name of the cart flows' surface, where the
// tax rate trial reads its orders (ADR 0387).
//
// The flows package declares it as InteropName; this module cannot import that
// package (ADR 0006), so the string is repeated here, as the cart, promotion
// and pricing modules repeat it. A typo does not stay silent: the trial fails
// closed on its first request.
const CartFlowsName = "workflows.cart.interop"

// rateTrial resolves the trial flow ON FIRST USE: the flows are built after
// every module has registered. The outcome of the first resolution is kept.
type rateTrial struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	flow service.RateTrialFlow
	err  error
}

var _ service.RateTrialFlow = (*rateTrial)(nil)

// TrialTaxRateJSON taxes the orders of a period with the rate as it is and as
// amended.
func (t *rateTrial) TrialTaxRateJSON(
	ctx context.Context, rateID string, from, to time.Time, change json.RawMessage,
) (json.RawMessage, error) {
	t.once.Do(func() { t.resolve(ctx) })
	if t.err != nil {
		return nil, t.err
	}
	return t.flow.TrialTaxRateJSON(ctx, rateID, from, to, change)
}

// resolve looks the flow up in the container and remembers the outcome; a name
// that does not resolve is a setup failure, whatever the container said.
func (t *rateTrial) resolve(ctx context.Context) {
	flow, err := container.Resolve[service.RateTrialFlow](t.c, CartFlowsName)
	if err != nil {
		t.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the tax rate trial flow (%q)", ModuleName, CartFlowsName)

		return
	}
	t.flow = flow
	t.log.InfoContext(ctx, "tax rate trial flow bound", "flow", CartFlowsName)
}
