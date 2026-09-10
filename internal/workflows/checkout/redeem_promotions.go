package checkout

// This file holds the saga's step that SPENDS the coupons: the discount round is
// side-effect free, and until this step existed nothing ever incremented a
// promotion's usage counter or a campaign's budget.
//
// The step's quartet (Name/Restore/Invoke/Compensate) stands here. What all
// steps share stays in steps.go.

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// redeemPromotionsStep spends the promotions that produced the cart's discount.
type redeemPromotionsStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// redeemOutput is the step's output written to the execution record.
type redeemOutput struct {
	// Redeemed names the promotions that were spent, in the order they were.
	//
	// It is written for the COMPENSATION and for the operator: releasing a use
	// takes the promotion and the reference, and a record that only counted them
	// would leave a half-finished step impossible to unwind by hand.
	Redeemed []redeemedRef `json:"redeemed,omitempty"`
}

// redeemedRef is the trace one redemption leaves behind.
type redeemedRef struct {
	// PromotionID is the promotion that was spent.
	PromotionID string `json:"promotion_id"`
	// Code is the coupon code; EMPTY for an automatic promotion.
	Code string `json:"code"`
}

// Name returns the step's name.
func (s *redeemPromotionsStep) Name() string { return StepRedeemPromotions }

// Restore rebuilds the redemptions FROM THE RECORD.
//
// A step that never ran leaves no output, and an empty output is a legitimate
// outcome too: a cart with no promotion spends nothing. The two are the same
// here — there is nothing to release either way.
func (s *redeemPromotionsStep) Restore(sc *workflow.StepContext, output json.RawMessage) error {
	if len(output) == 0 {
		return nil
	}

	var out redeemOutput
	if err := json.Unmarshal(output, &out); err != nil {
		return errors.Wrap(err, errors.KindInternal, CodeSharedStateInvalid,
			"the output of step %q could not be decoded", StepRedeemPromotions)
	}
	sc.Shared[sharedRedeemed] = out.Redeemed

	return nil
}

// Invoke spends every promotion the cart's discount rested on.
//
// # Why the CART is the reference and not the order
//
// The reference is what makes the call idempotent, and it has to be stable
// across a retry of this step. The cart is: one cart becomes one order, the
// identifier exists before the saga starts, and a saga replayed from the record
// carries the same one. The order's identifier is produced by a LATER step, so
// using it would mean this step could not run before the order exists — and then
// an exhausted coupon would be discovered only after an order had been placed.
//
// # Why the amount is not recomputed
//
// It comes from the plan, which took it from the round that produced the total
// the customer was shown. The promotion module says the same thing from its
// side: a second computation at this moment runs against whatever the cart looks
// like now and would write a figure into the campaign's budget that nobody ever
// saw.
//
// # A refusal here stops the checkout
//
// A coupon whose last use was taken while the shopper was on the payment page
// comes back as errors.Conflict, and the saga rolls back. Letting it through
// would give away a discount the campaign's budget cannot pay for, which is the
// merchant's money.
func (s *redeemPromotionsStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	if len(s.plan.Promotions) == 0 {
		return redeemOutput{}, nil
	}
	if s.w.promotions == nil {
		// The plan carries promotions and the module that owns them is not
		// bound. Going on would place an order at a discounted price with no
		// coupon ever spent, so the usage limit would mean nothing.
		return nil, errors.Internal(CodePromotionUnavailable,
			"the cart's discount rests on %d promotion(s) and the promotion module is not bound: %s",
			len(s.plan.Promotions), s.plan.CartID)
	}

	redeemed := make([]redeemedRef, 0, len(s.plan.Promotions))
	for i := range s.plan.Promotions {
		promo := s.plan.Promotions[i]

		if _, err := s.w.promotions.RedeemPromotion(ctx,
			promo.PromotionID, promo.Code, s.plan.CartID, s.plan.CurrencyCode, promo.Amount,
		); err != nil {
			return nil, s.unwind(ctx, sc, redeemed, promo, err)
		}
		redeemed = append(redeemed, redeemedRef{PromotionID: promo.PromotionID, Code: promo.Code})
	}
	sc.Shared[sharedRedeemed] = redeemed

	s.w.log.InfoContext(ctx, "promotions spent",
		"cart_id", s.plan.CartID, "promotions", len(redeemed))

	return redeemOutput{Redeemed: redeemed}, nil
}

// unwind gives back what this step spent before it failed, and returns the
// failure.
//
// The step unwinds its OWN partial work rather than leaving it to Compensate,
// for [reserveInventoryStep.unwind]'s reason: the engine compensates the steps
// that FINISHED, and a step that blew up halfway through has taken side effects
// nobody else will look for. A coupon spent for an order that was never placed
// is one the customer cannot use again.
//
// The error's CODE is preserved: the promotion module answers "no uses left" and
// "the campaign's budget is short" differently, and flattening them would leave
// the operator without the place to look.
func (s *redeemPromotionsStep) unwind(
	ctx context.Context,
	sc *workflow.StepContext,
	redeemed []redeemedRef,
	failed planPromotion,
	cause error,
) error {
	code := errors.CodeOf(cause)
	if code == "" {
		code = CodePromotionRedeemFailed
	}
	failure := errors.Wrap(cause, errors.KindOf(cause), code,
		"the coupon could not be spent: %s (cart %s)", failed.PromotionID, s.plan.CartID)

	// What was taken is written into the shared map either way: if the release
	// below fails, the compensation reads it from there and tries again.
	sc.Shared[sharedRedeemed] = redeemed
	if len(redeemed) == 0 {
		return failure
	}

	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	remaining := redeemed
	releaseErr := retryCleanup(cctx, func() error {
		var err error
		remaining, err = s.releaseAll(cctx, remaining)

		return err
	})
	sc.Shared[sharedRedeemed] = remaining

	if releaseErr == nil {
		return failure
	}

	s.w.log.ErrorContext(ctx, "the half-spent coupons could not be released; manual intervention is required",
		"cart_id", s.plan.CartID, "leaked", len(remaining), "error", releaseErr)

	return errors.Wrap(errors.Join(failure, releaseErr, workflow.ErrUncompensated),
		errors.KindInternal, CodePromotionReleaseLeaked,
		"cart %s has %d coupon use(s) left spent", s.plan.CartID, len(remaining))
}

// releaseAll gives back every use in the list and returns the ones it could NOT
// give back.
//
// Returning the remainder rather than stopping at the first failure is what
// makes a retry cheap: the next attempt tries only what is still outstanding,
// and the record names exactly which coupon is still counted against the
// customer.
func (s *redeemPromotionsStep) releaseAll(
	ctx context.Context, refs []redeemedRef,
) ([]redeemedRef, error) {
	remaining := make([]redeemedRef, 0, len(refs))
	var failures error

	for i := range refs {
		if _, err := s.w.promotions.ReleasePromotion(ctx,
			refs[i].PromotionID, refs[i].Code, s.plan.CartID,
		); err != nil {
			remaining = append(remaining, refs[i])
			failures = errors.Join(failures, err)
		}
	}

	return remaining, failures
}

// Compensate releases every use this saga took; it is IDEMPOTENT.
//
// The promotion module's release is idempotent on its own — a second call
// decrements nothing — so a compensation that runs twice is safe.
//
// Unlike the order's cancellation this one is NOT skipped after a capture. A
// capture means the customer paid, and the saga stops unwinding the things the
// customer would lose; a coupon use is not one of them. If the checkout failed
// after the money moved, the order stands and the coupon must stand with it —
// so the skip check is asked for the same reason and gives the same answer.
func (s *redeemPromotionsStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepRedeemPromotions, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	redeemed, err := sharedRedemptions(sc)
	if err != nil {
		return err
	}
	if len(redeemed) == 0 || s.w.promotions == nil {
		return nil
	}

	remaining, releaseErr := s.releaseAll(ctx, redeemed)
	// The ones that could not be released STAY in the shared map: a retried
	// compensation then tries only those, and the record names which coupon is
	// still counted against the customer.
	sc.Shared[sharedRedeemed] = remaining
	if releaseErr != nil {
		return releaseErr
	}

	s.w.log.InfoContext(ctx, "compensation: promotion uses released",
		"cart_id", s.plan.CartID, "promotions", len(redeemed))

	return nil
}
