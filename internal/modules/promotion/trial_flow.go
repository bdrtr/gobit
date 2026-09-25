package promotion

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
)

// CartFlowsName is the container name of the cart flows' cross-module surface,
// where the promotion trial runs (ADR 0176).
//
// The flows package declares it as InteropName; this module cannot import that
// package (ADR 0006), so the string is repeated here, as the cart module repeats
// it. A typo does not stay silent: the trial fails closed on its first request.
const CartFlowsName = "workflows.cart.interop"

// promotionTrial resolves the trial flow ON FIRST USE.
//
// The flows are built after every module has registered, so the flow cannot be
// resolved in Register. The outcome of the first resolution is kept: a name that
// did not resolve then will not resolve on a later request either.
type promotionTrial struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	flow api.PromotionTrial
	err  error
}

// That the wrapper satisfies the surface the handler expects is pinned at
// compile time.
var _ api.PromotionTrial = (*promotionTrial)(nil)

// TrialPromotionJSON prices the promotion against the orders of a period.
func (p *promotionTrial) TrialPromotionJSON(
	ctx context.Context, promotionID string, from, to time.Time,
) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}

	return p.flow.TrialPromotionJSON(ctx, promotionID, from, to)
}

// resolve looks the flow up in the container and remembers the outcome.
//
// The error's kind is internal whatever the container said: a name that does not
// resolve is a setup failure, and a 404 would tell the operator the promotion
// does not exist.
func (p *promotionTrial) resolve(ctx context.Context) {
	flow, err := container.Resolve[api.PromotionTrial](p.c, CartFlowsName)
	if err != nil {
		p.err = errors.Wrap(err, errors.KindInternal, codeSetupFailed,
			"the %s module could not resolve the trial flow (%q); no order can be read",
			ModuleName, CartFlowsName)

		return
	}
	p.flow = flow
	p.log.InfoContext(ctx, "promotion trial flow bound", "flow", CartFlowsName)
}
