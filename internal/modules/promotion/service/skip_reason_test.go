package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// usableCandidate is a candidate that passes every gate.
//
// Each case below breaks exactly ONE thing about it, so a case that fails proves
// which gate answered rather than which combination of them did.
func usableCandidate() models.PromotionCandidate {
	promo := models.Promotion{
		ID:          "promo_ok",
		Code:        "USABLE",
		IsAutomatic: true,
		Type:        models.PromotionStandard,
		Status:      models.PromotionActive,
	}

	return models.PromotionCandidate{
		Promotion: promo,
		Method:    percentageMethod(promo.ID, 1000, models.TargetItems, models.AllocationEach),
	}
}

// usableInput is a computation the candidate above is eligible for.
func usableInput() ComputeInput {
	return ComputeInput{CurrencyCode: "TRY", At: testNow}
}

// TestEveryReasonIsReachable is the CLOSED SET claim, tested from both ends.
//
// One direction: every case produces the reason it names, so the words are not
// guesses. The other: the reasons the cases produce COVER every declared
// constant, so a constant nobody can produce — a word for a gate that was
// removed, or one that was never wired — cannot sit in the vocabulary looking
// like an answer the endpoint might give.
func TestEveryReasonIsReachable(t *testing.T) {
	cases := map[SkipReason]func(*models.PromotionCandidate, *ComputeInput){
		SkipNotActive: func(c *models.PromotionCandidate, _ *ComputeInput) {
			c.Promotion.Status = models.PromotionDraft
		},
		SkipNotStandard: func(c *models.PromotionCandidate, _ *ComputeInput) {
			c.Promotion.Type = models.PromotionBuyGet
		},
		SkipNoApplicationMethod: func(c *models.PromotionCandidate, _ *ComputeInput) {
			c.Method = nil
		},
		SkipUsageExhausted: func(c *models.PromotionCandidate, _ *ComputeInput) {
			c.Promotion.UsageLimit = ptr(int64(1))
			c.Promotion.UsageCount = 1
		},
		SkipCodeNotGiven: func(c *models.PromotionCandidate, in *ComputeInput) {
			c.Promotion.IsAutomatic = false
			in.Codes = []string{"SOMETHING_ELSE"}
		},
		SkipCampaignClosed: func(c *models.PromotionCandidate, _ *ComputeInput) {
			// The campaign is NAMED and its record is absent, which is what a
			// deleted campaign looks like from here.
			c.Promotion.CampaignID = ptr("camp_gone")
			c.Campaign = nil
		},
		SkipCurrencyMismatch: func(c *models.PromotionCandidate, in *ComputeInput) {
			c.Method = fixedMethod(c.Promotion.ID, 500, models.TargetItems, models.AllocationEach)
			in.CurrencyCode = "EUR"
		},
		SkipRulesNotMatched: func(c *models.PromotionCandidate, _ *ComputeInput) {
			c.Rules = []models.PromotionRule{{
				RuleType:  models.RuleContext,
				Attribute: "customer_group",
				Operator:  models.OpIn,
				Values:    []string{"wholesale"},
			}}
		},
	}

	for reason, breakIt := range cases {
		t.Run(string(reason), func(t *testing.T) {
			candidate, in := usableCandidate(), usableInput()
			breakIt(&candidate, &in)

			assert.Equal(t, reason, skipReasonOf(candidate, in))
			assert.False(t, eligible(candidate, in),
				"a candidate with a reason is not eligible; the two answers come from one place")
		})
	}

	assert.ElementsMatch(t, declaredSkipReasons(), reasonKeys(cases),
		"every declared reason has to be produced by a case, and every case has to "+
			"name a declared reason; a word nobody can produce is an answer the "+
			"endpoint promises and never gives")
}

// TestAUsableCandidateHasNoReason is the other end of the same function: the
// empty string means eligible, and it has to be produced by something.
func TestAUsableCandidateHasNoReason(t *testing.T) {
	candidate, in := usableCandidate(), usableInput()

	assert.Empty(t, skipReasonOf(candidate, in))
	assert.True(t, eligible(candidate, in))
}

// TestACouponWhoseCodeWasGivenIsNotSkipped keeps the code gate from refusing the
// case it exists to allow.
func TestACouponWhoseCodeWasGivenIsNotSkipped(t *testing.T) {
	candidate, in := usableCandidate(), usableInput()
	candidate.Promotion.IsAutomatic = false
	in.Codes = []string{candidate.Promotion.Code}

	assert.Empty(t, skipReasonOf(candidate, in))
}

// TestTheComputationReportsWhatItLeftOut is the field the result carries.
func TestTheComputationReportsWhatItLeftOut(t *testing.T) {
	paused := usableCandidate()
	paused.Promotion.ID = "promo_paused"
	paused.Promotion.Code = "PAUSED"
	paused.Promotion.Status = models.PromotionInactive
	paused.Method = percentageMethod(paused.Promotion.ID, 1000,
		models.TargetItems, models.AllocationEach)

	in := usableInput()
	in.Items = []ComputeItem{item("li_1", 1000, 1, nil)}

	result := computeDiscounts([]models.PromotionCandidate{usableCandidate(), paused}, in)

	require.Len(t, result.Skipped, 1, "the paused one was considered and left out")
	assert.Equal(t, "promo_paused", result.Skipped[0].PromotionID)
	assert.Equal(t, "PAUSED", result.Skipped[0].Code)
	assert.Equal(t, SkipNotActive, result.Skipped[0].Reason)

	require.Len(t, result.Applied, 1, "and the usable one still applied")
	assert.Equal(t, "promo_ok", result.Applied[0].PromotionID)
}

// TestAComputationThatLeftNothingOutReportsAnEmptyList keeps the wire from
// having two spellings for "none".
func TestAComputationThatLeftNothingOutReportsAnEmptyList(t *testing.T) {
	in := usableInput()
	in.Items = []ComputeItem{item("li_1", 1000, 1, nil)}

	result := computeDiscounts([]models.PromotionCandidate{usableCandidate()}, in)

	assert.NotNil(t, result.Skipped)
	assert.Empty(t, result.Skipped)
}

// reasonKeys returns the reasons the case table names.
func reasonKeys(cases map[SkipReason]func(*models.PromotionCandidate, *ComputeInput)) []SkipReason {
	out := make([]SkipReason, 0, len(cases))
	for reason := range cases {
		out = append(out, reason)
	}

	return out
}

// declaredSkipReasons is the vocabulary, written out.
//
// It is a SECOND list rather than a reflection over the constants, because Go
// gives no way to enumerate the members of a named string type: the compiler
// forgets them. The list is what makes the cover assertion above two-sided, and
// keeping it honest is the same discipline as any other hand-written population
// — a constant added without a line here leaves the assertion passing on a
// smaller set, so the assertion is written to fail in BOTH directions.
func declaredSkipReasons() []SkipReason {
	return []SkipReason{
		SkipNotActive,
		SkipNotStandard,
		SkipNoApplicationMethod,
		SkipUsageExhausted,
		SkipCodeNotGiven,
		SkipCampaignClosed,
		SkipCurrencyMismatch,
		SkipRulesNotMatched,
	}
}

// TestBothPathsGiveTheSameDiscount is the claim [Service.ExplainDiscounts] rests
// on, and it is the one that would hurt if it were false.
//
// The two entry points read DIFFERENT candidate sets, and the merchant is told
// the numbers from one while the customer is charged the numbers from the other.
// The sets are safe to differ only because the extra members cannot pass the
// elimination — so the applied set, and every amount, has to be identical.
func TestBothPathsGiveTheSameDiscount(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()

	// One promotion that applies, and three the wider read can see and the
	// narrow one cannot.
	seedPromotion(repo, models.Promotion{ID: "promo_live", Code: "LIVE", IsAutomatic: true},
		percentageMethod("promo_live", 1000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{
		ID: "promo_draft", Code: "DRAFT", IsAutomatic: true, Status: models.PromotionDraft,
	}, percentageMethod("promo_draft", 5000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{
		ID: "promo_paused", Code: "PAUSED", IsAutomatic: true, Status: models.PromotionInactive,
	}, percentageMethod("promo_paused", 5000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{
		ID: "promo_coupon_draft", Code: "COUPON_DRAFT", Status: models.PromotionDraft,
	}, percentageMethod("promo_coupon_draft", 5000, models.TargetItems, models.AllocationEach))

	svc := newTestService(repo)
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"COUPON_DRAFT"},
	}

	narrow, err := svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)
	wide, err := svc.ExplainDiscounts(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, narrow.DiscountTotal, wide.DiscountTotal,
		"the merchant is shown the numbers from one path and the customer is charged "+
			"the numbers from the other; they cannot differ")
	assert.Equal(t, narrow.Items, wide.Items)
	assert.Equal(t, narrow.Applied, wide.Applied)
	assert.Equal(t, int64(1000), wide.DiscountTotal, "10% of 10_000, from the live one alone")

	// And the reasons are the whole point of the wider read.
	assert.Empty(t, narrow.Skipped,
		"the narrow read cannot see a promotion that is not active, so it reports none")

	reasons := map[string]SkipReason{}
	for i := range wide.Skipped {
		reasons[wide.Skipped[i].PromotionID] = wide.Skipped[i].Reason
	}
	assert.Equal(t, map[string]SkipReason{
		"promo_draft":        SkipNotActive,
		"promo_paused":       SkipNotActive,
		"promo_coupon_draft": SkipNotActive,
	}, reasons,
		"'you never activated it' is the commonest answer to \"I typed the code and "+
			"nothing happened\", and it is the one the narrow read cannot give")
}

// TestTheWiderReadStillDoesNotNameEveryPromotion pins the population.
//
// Dropping the status filter is not the same as dropping the rest. A coupon whose
// code was NOT typed stays out of the answer: an answer that grew with the
// catalog would say nothing about the cart that was asked about.
func TestTheWiderReadStillDoesNotNameEveryPromotion(t *testing.T) {
	ctx := context.Background()
	repo := newMemRepo()

	seedPromotion(repo, models.Promotion{
		ID: "promo_other", Code: "NOT_TYPED", Status: models.PromotionDraft,
	}, percentageMethod("promo_other", 5000, models.TargetItems, models.AllocationEach))

	result, err := newTestService(repo).ExplainDiscounts(ctx, ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	})
	require.NoError(t, err)

	assert.Empty(t, result.Skipped,
		"a coupon nobody typed is not an answer to a question about this cart")
}
