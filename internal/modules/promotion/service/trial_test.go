package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// trialPurchase is one past purchase of a single line in TRY, in a region.
func trialPurchase(reference, regionID string, amount int64) TrialEntry {
	return TrialEntry{
		Reference: reference,
		Input: ComputeInput{
			CurrencyCode: "TRY",
			Context:      map[string]string{"region_id": regionID},
			Items:        []ComputeItem{{ID: "li_" + reference, Amount: amount, UnitAmount: amount, Quantity: 1}},
		},
	}
}

// TestATrialPricesThePromotionAsIfPublished verifies what a trial sets aside
// and what it does not (ADR 0176).
//
// A draft coupon whose campaign is over and whose usage limit is spent is
// everything the live computation refuses; the trial prices it anyway, at the
// rate its method says. Its CONTEXT rule still decides: the trial asks what the
// promotion would do to a purchase, not whether it may run today.
func TestATrialPricesThePromotionAsIfPublished(t *testing.T) {
	repo := newMemRepo()
	ended := testNow.Add(-48 * time.Hour)
	repo.campaigns["camp_over"] = models.Campaign{ID: "camp_over", EndsAt: &ended}
	limit := int64(1)
	campaign := "camp_over"
	seedPromotion(repo, models.Promotion{
		ID: "promo_trial", Code: "LATER10", IsAutomatic: false, Status: models.PromotionDraft,
		CampaignID: &campaign, UsageLimit: &limit, UsageCount: 1,
	}, percentageMethod("promo_trial", 1000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{ID: "rule_1", PromotionID: "promo_trial", RuleType: models.RuleContext,
			Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_tr"}})
	svc := newTestService(repo)

	outcomes, err := svc.TrialDiscounts(context.Background(), "promo_trial", []TrialEntry{
		trialPurchase("cart_tr", "reg_tr", 20_000),
		trialPurchase("cart_de", "reg_de", 20_000),
	})
	require.NoError(t, err)
	require.Len(t, outcomes, 2)

	assert.Equal(t, int64(2_000), outcomes[0].Result.ItemsDiscountTotal,
		"draft, coupon, closed campaign and spent limit are all set aside; 10 percent of 20000 applies")
	assert.Empty(t, outcomes[0].Skipped)
	assert.Equal(t, SkipRulesNotMatched, outcomes[1].Skipped,
		"the region rule still decides: a trial is not a way around what the promotion says")
	assert.Zero(t, outcomes[1].Result.ItemsDiscountTotal)
}

// TestATrialDoesNotPriceAPurchaseThePromotionAlreadyDiscounted verifies that a
// live redemption marks the purchase and a released one does not.
func TestATrialDoesNotPriceAPurchaseThePromotionAlreadyDiscounted(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_ran", Code: "RAN10", IsAutomatic: true},
		percentageMethod("promo_ran", 1000, models.TargetItems, models.AllocationEach))
	released := testNow
	repo.redemptions = []models.Redemption{
		{ID: "red_1", PromotionID: "promo_ran", Reference: "cart_live"},
		{ID: "red_2", PromotionID: "promo_ran", Reference: "cart_released", ReleasedAt: &released},
	}
	svc := newTestService(repo)

	outcomes, err := svc.TrialDiscounts(context.Background(), "promo_ran", []TrialEntry{
		trialPurchase("cart_live", "reg_tr", 10_000),
		trialPurchase("cart_released", "reg_tr", 10_000),
	})
	require.NoError(t, err)

	assert.True(t, outcomes[0].AlreadyApplied,
		"its discount is already in the purchase; pricing it again would count it twice")
	assert.Zero(t, outcomes[0].Result.ItemsDiscountTotal)
	assert.False(t, outcomes[1].AlreadyApplied, "a released use gave its discount back")
	assert.Equal(t, int64(1_000), outcomes[1].Result.ItemsDiscountTotal)
	assert.Equal(t, 1, repo.calls["ListRedemptions"], "the ledger is read once, not once per purchase")
}

// TestATrialRefusesAPromotionThatDiscountsNothing verifies that a reason no
// purchase can change refuses the trial as a whole, while one a purchase can
// change does not.
func TestATrialRefusesAPromotionThatDiscountsNothing(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_bare", Code: "BARE10", IsAutomatic: true}, nil)
	svc := newTestService(repo)

	_, err := svc.TrialDiscounts(context.Background(), "promo_bare", []TrialEntry{trialPurchase("cart_1", "reg_tr", 100)})

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "error: %v", err)
	assert.Equal(t, CodeTrialCannotApply, errors.CodeOf(err))
	assert.Contains(t, err.Error(), string(SkipNoApplicationMethod))
}

// TestATrialRefusesMorePurchasesThanItPrices verifies the bound.
func TestATrialRefusesMorePurchasesThanItPrices(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_wide", Code: "WIDE10", IsAutomatic: true},
		percentageMethod("promo_wide", 1000, models.TargetItems, models.AllocationEach))
	svc := newTestService(repo)

	_, err := svc.TrialDiscounts(context.Background(), "promo_wide", make([]TrialEntry, MaxTrialEntries+1))

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "error: %v", err)
}
