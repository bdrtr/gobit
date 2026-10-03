package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
)

// repoWithCoupon produces a repository prepared with a promotion (and an
// optional campaign) for the counter tests.
func repoWithCoupon(campaign *models.Campaign, usageLimit *int64) *memRepo {
	repo := newMemRepo()
	promo := models.Promotion{
		ID: "promo_1", Code: "YAZ20", Status: models.PromotionActive,
		Type: models.PromotionStandard, UsageLimit: usageLimit,
	}
	if campaign != nil {
		repo.campaigns[campaign.ID] = *campaign
		promo.CampaignID = ptr(campaign.ID)
	}
	seedPromotion(repo, promo, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))
	return repo
}

func TestRedeemPromotionIncrementsTheCounter(t *testing.T) {
	repo := repoWithCoupon(nil, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "try",
	})
	require.NoError(t, err)

	assert.Equal(t, "order_1", redemption.Reference)
	assert.Equal(t, int64(2500), redemption.Amount)
	assert.Equal(t, "TRY", redemption.CurrencyCode, "the currency is normalized to upper case")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)
}

func TestRedeemPromotionIsIdempotent(t *testing.T) {
	repo := repoWithCoupon(nil, nil)
	svc := newTestService(repo)
	in := RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY"}

	first, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err)

	second, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err, "a second call with the same reference returns NO error")

	assert.Equal(t, first.ID, second.ID, "the second call returns the existing record")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount,
		"the saga may rerun a step; the counter must not increase a second time")
}

func TestRedeemPromotionStopsAtTheUsageLimit(t *testing.T) {
	repo := repoWithCoupon(nil, ptr(int64(1)))
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	_, err = svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_2", Amount: 100, CurrencyCode: "TRY",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeUsageLimitReached, errors.CodeOf(err))
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount, "a refused redemption does not increment the counter")
}

func TestRedeemPromotionConsumesAMoneyMeasuredBudget(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := repoWithCoupon(&campaign, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2500), redemption.BudgetDelta, "a money-measured budget is consumed by the AMOUNT")
	assert.Equal(t, int64(2500), repo.campaigns["camp_1"].BudgetUsed)
}

func TestRedeemPromotionConsumesOneOfACountMeasuredBudget(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(3)),
	}
	repo := repoWithCoupon(&campaign, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), redemption.BudgetDelta, "a count-measured budget is consumed by ONE per redemption")
	assert.Equal(t, int64(1), repo.campaigns["camp_1"].BudgetUsed)
}

func TestRedeemPromotionRefusesWhenTheBudgetIsExceeded(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(1000)),
		BudgetUsed: 900, BudgetCurrencyCode: "TRY",
	}
	repo := repoWithCoupon(&campaign, nil)

	_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 500, CurrencyCode: "TRY",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, repository.CodeBudgetExceeded, errors.CodeOf(err))
	assert.Equal(t, int64(900), repo.campaigns["camp_1"].BudgetUsed, "a refused redemption does not change the budget")
	assert.Zero(t, repo.promotions["promo_1"].UsageCount, "no counter is left half-written")
}

func TestRedeemPromotionBudgetCurrencyMustMatch(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := repoWithCoupon(&campaign, nil)

	_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "USD",
	})

	require.Error(t, err)
	assert.Equal(t, repository.CodeBudgetCurrencyMismatch, errors.CodeOf(err),
		"two currencies cannot be summed in the same counter")
}

func TestReleasePromotionReversesTheCounters(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := repoWithCoupon(&campaign, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	released, err := svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	assert.True(t, released, "the compensation must really do work")
	assert.Zero(t, repo.promotions["promo_1"].UsageCount)
	assert.Zero(t, repo.campaigns["camp_1"].BudgetUsed, "the budget is not GUESSED; the share recorded in the ledger is subtracted")
}

func TestReleasePromotionIsIdempotent(t *testing.T) {
	repo := repoWithCoupon(nil, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	in := ReleaseInput{PromotionID: "promo_1", Reference: "order_1"}
	first, err := svc.ReleasePromotion(context.Background(), in)
	require.NoError(t, err)
	require.True(t, first)

	second, err := svc.ReleasePromotion(context.Background(), in)
	require.NoError(t, err, "the compensation must be rerunnable (plan Section 5.5)")

	assert.False(t, second, "the second call reverses nothing")
	assert.Zero(t, repo.promotions["promo_1"].UsageCount, "the counter must not drop a SECOND time")
}

func TestReleasePromotionDoesNotFailWithoutAnyRedemption(t *testing.T) {
	repo := repoWithCoupon(nil, nil)

	released, err := newTestService(repo).ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_missing",
	})

	require.NoError(t, err, "the compensation of a step that blew up before writing must work too")
	assert.False(t, released)
}

func TestReleasePromotionMissingPromotionNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_missing", Reference: "order_1",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err),
		"a compensation called with the wrong id must not be swallowed silently")
}

func TestAReleasedReferenceCanBeRedeemedAgain(t *testing.T) {
	repo := repoWithCoupon(nil, nil)
	svc := newTestService(repo)
	redeem := RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY"}

	first, err := svc.RedeemPromotion(context.Background(), redeem)
	require.NoError(t, err)

	_, err = svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	second, err := svc.RedeemPromotion(context.Background(), redeem)
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID, "a released reference can be redeemed again")
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)
}

func TestRedeemPromotionResolvesByCode(t *testing.T) {
	repo := repoWithCoupon(nil, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		Code: "yaz20", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, "promo_1", redemption.PromotionID)
}

func TestRedeemPromotionRefusesConflictingIDAndCode(t *testing.T) {
	repo := repoWithCoupon(nil, nil)

	_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Code: "BASKAKOD", Reference: "order_1",
		Amount: 100, CurrencyCode: "TRY",
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"with a request naming two different promotions the counter would be written to the wrong place")
}

func TestRedeemPromotionInputValidation(t *testing.T) {
	tests := []struct {
		name   string
		in     RedeemInput
		reason string
	}{
		{
			name:   "promotion not named",
			in:     RedeemInput{Reference: "order_1", Amount: 100, CurrencyCode: "TRY"},
			reason: "an id or a code must be given",
		},
		{
			name:   "empty reference",
			in:     RedeemInput{PromotionID: "promo_1", Amount: 100, CurrencyCode: "TRY"},
			reason: "the idempotency key is required",
		},
		{
			name:   "negative amount",
			in:     RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: -1, CurrencyCode: "TRY"},
			reason: "a negative discount is meaningless",
		},
		{
			name:   "no currency",
			in:     RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 100},
			reason: "the currency is required",
		},
		{
			name: "amount exceeds the maximum",
			in: RedeemInput{
				PromotionID: "promo_1", Reference: "order_1",
				Amount: models.MaxAmount + 1, CurrencyCode: "TRY",
			},
			reason: "overflow protection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := repoWithCoupon(nil, nil)

			_, err := newTestService(repo).RedeemPromotion(context.Background(), tt.in)
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
		})
	}
}

func TestListRedemptionsMissingPromotionNotFound(t *testing.T) {
	_, err := newTestService(newMemRepo()).ListRedemptions(context.Background(), "promo_missing", 10, 0)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

func TestListRedemptionsReturnsReleasedOnesToo(t *testing.T) {
	repo := repoWithCoupon(nil, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 100, CurrencyCode: "TRY",
	})
	require.NoError(t, err)
	_, err = svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	page, err := svc.ListRedemptions(context.Background(), "promo_1", 10, 0)
	require.NoError(t, err)

	require.Len(t, page.Items, 1)
	assert.True(t, page.Items[0].Released(), "the ledger is a history; the trace of a reversed redemption is not erased")
}

// TestRedeemPromotionRefusesAPromotionThatIsNotLive pins that draft and
// inactive promotions cannot be redeemed.
//
// Without the check, the counter of a promotion that was NEVER published would
// increase and its campaign's budget would be eaten silently; on the admin
// surface the error would be noticed only by reading the ledger. The surface
// is open to callers both through /admin/v1/promotions/{id}/redeem and through
// "promotion.interop".
func TestRedeemPromotionRefusesAPromotionThatIsNotLive(t *testing.T) {
	for _, status := range []models.PromotionStatus{models.PromotionDraft, models.PromotionInactive} {
		t.Run(string(status), func(t *testing.T) {
			campaign := models.Campaign{
				ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
				BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
			}
			repo := repoWithCoupon(&campaign, nil)
			promo := repo.promotions["promo_1"]
			promo.Status = status
			repo.promotions["promo_1"] = promo

			_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
				PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
			})

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, repository.CodePromotionNotActive, errors.CodeOf(err))
			assert.Zero(t, repo.promotions["promo_1"].UsageCount, "a refused redemption does not increment the counter")
			assert.Zero(t, repo.campaigns["camp_1"].BudgetUsed,
				"a promotion that was not published does NOT eat the campaign budget")
		})
	}
}

// TestRedeemPromotionRefusesWhenTheCampaignWindowIsClosed pins that the MOMENT
// of redemption has to be inside the campaign's date window.
//
// The window is already eliminated in the computation
// ([Service.ComputeDiscounts]); but the computation has no side effects, and
// the window may close between the cart and order completion. This is the
// arbiter of the moment the counter is written.
func TestRedeemPromotionRefusesWhenTheCampaignWindowIsClosed(t *testing.T) {
	tests := []struct {
		name     string
		campaign models.Campaign
	}{
		{
			name: "window has closed",
			campaign: models.Campaign{
				ID: "camp_1", Name: "Ended", CampaignIdentifier: "BITMIS",
				BudgetType: models.BudgetNone, EndsAt: ptr(testNow.Add(-time.Hour)),
			},
		},
		{
			name: "window has not opened yet",
			campaign: models.Campaign{
				ID: "camp_1", Name: "Gelecek", CampaignIdentifier: "GELECEK",
				BudgetType: models.BudgetNone, StartsAt: ptr(testNow.Add(time.Hour)),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := repoWithCoupon(&tt.campaign, nil)

			_, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
				PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
			})

			require.Error(t, err)
			assert.Equal(t, errors.KindConflict, errors.KindOf(err))
			assert.Equal(t, repository.CodeCampaignWindowClosed, errors.CodeOf(err))
			assert.Zero(t, repo.promotions["promo_1"].UsageCount, "a refused redemption does not increment the counter")
		})
	}
}

// TestRedeemPromotionWorksInsideAnOpenCampaignWindow pins the POSITIVE side of
// the check rather than its REFUSAL side: the promotion of a campaign whose
// window is open must be redeemable normally.
func TestRedeemPromotionWorksInsideAnOpenCampaignWindow(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		StartsAt:   ptr(testNow.Add(-time.Hour)),
		EndsAt:     ptr(testNow.Add(time.Hour)),
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := repoWithCoupon(&campaign, nil)

	redemption, err := newTestService(repo).RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	assert.Equal(t, int64(2500), redemption.BudgetDelta)
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount)
}

// TestRedeemPromotionIdempotencyComesBeforeTheStatusCheck pins that the order
// is deliberate: the saga step of a promotion that was stopped AFTER its
// redemption was written must be rerunnable.
//
// Had the order been reversed, an order would be left that cannot be
// compensated: the step returns an error, but the redemption stays in the
// ledger.
func TestRedeemPromotionIdempotencyComesBeforeTheStatusCheck(t *testing.T) {
	repo := repoWithCoupon(nil, nil)
	svc := newTestService(repo)
	in := RedeemInput{PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY"}

	first, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err)

	promo := repo.promotions["promo_1"]
	promo.Status = models.PromotionInactive
	repo.promotions["promo_1"] = promo

	second, err := svc.RedeemPromotion(context.Background(), in)
	require.NoError(t, err, "a retry of a written redemption must be readable even if the promotion was stopped")

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, int64(1), repo.promotions["promo_1"].UsageCount, "the counter does not increase a second time")
}

// TestReleasePromotionReleasesAStoppedPromotionToo pins that the compensation
// performs no eligibility check: the redemption of a stopped promotion must be
// reversible too, otherwise the saga compensation would get stuck.
func TestReleasePromotionReleasesAStoppedPromotionToo(t *testing.T) {
	campaign := models.Campaign{
		ID: "camp_1", Name: "Yaz", CampaignIdentifier: "YAZ",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(10_000)), BudgetCurrencyCode: "TRY",
	}
	repo := repoWithCoupon(&campaign, nil)
	svc := newTestService(repo)

	_, err := svc.RedeemPromotion(context.Background(), RedeemInput{
		PromotionID: "promo_1", Reference: "order_1", Amount: 2500, CurrencyCode: "TRY",
	})
	require.NoError(t, err)

	promo := repo.promotions["promo_1"]
	promo.Status = models.PromotionInactive
	repo.promotions["promo_1"] = promo

	released, err := svc.ReleasePromotion(context.Background(), ReleaseInput{
		PromotionID: "promo_1", Reference: "order_1",
	})
	require.NoError(t, err)

	assert.True(t, released, "the redemption of a stopped promotion must be compensable too")
	assert.Zero(t, repo.campaigns["camp_1"].BudgetUsed)
}
