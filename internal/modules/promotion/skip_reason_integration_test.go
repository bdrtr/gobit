//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: the status filter is in the SQL. The
// in-memory repository imitates the difference between the two candidate reads,
// and a fake that imitates a rule cannot say whether the rule is in the query
// (D50).
package promotion_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// TestTheWiderReadSeesAPromotionTheNarrowOneCannot is the query difference.
//
// `ListApplicablePromotions` carries `status = 'active'` and
// `ListCandidatesForDiagnosis` does not. That one line is what makes "you never
// activated it" an answer the merchant can be given, and it is the commonest
// answer to "I typed the code and nothing happened".
func TestTheWiderReadSeesAPromotionTheNarrowOneCannot(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	code := uniqueCode()
	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: code, Status: models.PromotionDraft, IsAutomatic: false,
	})
	require.NoError(t, err)
	_, err = svc.SetApplicationMethod(ctx, promo.ID, service.ApplicationMethodInput{
		Type:       models.MethodPercentage,
		TargetType: models.TargetItems,
		Allocation: models.AllocationEach,
		Value:      2000,
	})
	require.NoError(t, err)

	in := service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, UnitAmount: 10000, Quantity: 1}},
		Codes:        []string{code},
	}

	narrow, err := svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)
	assert.NotContains(t, skippedIDs(narrow), promo.ID,
		"the narrow query never returns a draft promotion, so it can report no reason for it")

	wide, err := svc.ExplainDiscounts(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, service.SkipNotActive, reasonFor(t, wide, promo.ID))
	assert.NotContains(t, appliedIDs(wide), promo.ID,
		"seeing it does not apply it")
	assert.Equal(t, narrow.DiscountTotal, wide.DiscountTotal,
		"the amounts of the two paths are identical")
}

// TestBothQueriesGiveTheSameDiscountOnTheRealSchema is the claim the split rests
// on, measured against the database.
//
// The merchant is shown the numbers from one query and the customer is charged
// the numbers from the other. They are safe to differ only because the wider
// read's extra members cannot pass the elimination.
func TestBothQueriesGiveTheSameDiscountOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	live := activePromotion(ctx, t, svc, service.PromotionInput{IsAutomatic: true})
	draftCode := uniqueCode()
	draft, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: draftCode, Status: models.PromotionDraft, IsAutomatic: true,
	})
	require.NoError(t, err)
	_, err = svc.SetApplicationMethod(ctx, draft.ID, service.ApplicationMethodInput{
		Type:       models.MethodPercentage,
		TargetType: models.TargetItems,
		Allocation: models.AllocationEach,
		Value:      5000,
	})
	require.NoError(t, err)

	in := service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, UnitAmount: 10000, Quantity: 1}},
	}

	narrow, err := svc.ComputeDiscounts(ctx, in)
	require.NoError(t, err)
	wide, err := svc.ExplainDiscounts(ctx, in)
	require.NoError(t, err)

	assert.Equal(t, narrow.DiscountTotal, wide.DiscountTotal)
	assert.Equal(t, narrow.Items, wide.Items)
	assert.Equal(t, narrow.Applied, wide.Applied)
	assert.Equal(t, service.SkipNotActive, reasonFor(t, wide, draft.ID))

	// The live one applied on BOTH paths, which is what makes the equality above
	// mean something: an empty computation would satisfy it too.
	//
	// It is looked up by id rather than by position. The tests share one database
	// and other fixtures' automatic promotions land on this cart as well — the
	// hazard the e2e helper's godoc names — so an assertion on the FIRST applied
	// promotion, or on an absolute total, would fail for somebody else's reason.
	assert.Contains(t, appliedIDs(narrow), live.ID)
	assert.Contains(t, appliedIDs(wide), live.ID)
	assert.NotContains(t, appliedIDs(wide), draft.ID)
}

// TestADeletedPromotionIsGoneRatherThanSkipped pins what the wider read did NOT
// drop.
//
// `deleted_at IS NULL` stays in both queries: a deleted promotion is not a
// refused candidate, it is not a candidate.
func TestADeletedPromotionIsGoneRatherThanSkipped(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	code := uniqueCode()
	promo, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: code, Status: models.PromotionDraft,
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeletePromotion(ctx, promo.ID))

	wide, err := svc.ExplainDiscounts(ctx, service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, UnitAmount: 10000, Quantity: 1}},
		Codes:        []string{code},
	})
	require.NoError(t, err)

	for i := range wide.Skipped {
		assert.NotEqual(t, promo.ID, wide.Skipped[i].PromotionID,
			"a deleted promotion must not be named at all")
	}
	assert.Equal(t, []string{code}, wide.UnmatchedCodes,
		"what the caller is told is that the code matched nothing, which is true")
}

// TestTheWiderReadStillAsksAboutOneCart pins the population against the real
// query.
//
// Dropping the status filter is not the same as dropping the rest. A coupon whose
// code was NOT typed stays out: an answer that grew with the promotion table
// would say nothing about the cart that was asked about, and it would grow on
// every cart the operator diagnosed.
//
// It is an INTEGRATION test because the population is a WHERE clause. The unit
// test beside it reads the in-memory repository, which imitates the same rule and
// therefore cannot say whether the query carries it.
func TestTheWiderReadStillAsksAboutOneCart(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	// A coupon nobody is going to type, in the one state the wider read exists
	// to reveal.
	untypedCode := uniqueCode()
	untyped, err := svc.CreatePromotion(ctx, service.PromotionInput{
		Code: untypedCode, Status: models.PromotionDraft, IsAutomatic: false,
	})
	require.NoError(t, err)
	_, err = svc.SetApplicationMethod(ctx, untyped.ID, service.ApplicationMethodInput{
		Type:       models.MethodPercentage,
		TargetType: models.TargetItems,
		Allocation: models.AllocationEach,
		Value:      2000,
	})
	require.NoError(t, err)

	wide, err := svc.ExplainDiscounts(ctx, service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, UnitAmount: 10000, Quantity: 1}},
		// No codes at all: the question is about a cart with no coupon typed.
	})
	require.NoError(t, err)

	for i := range wide.Skipped {
		assert.NotEqual(t, untyped.ID, wide.Skipped[i].PromotionID,
			"a coupon nobody typed is not an answer to a question about this cart")
	}

	// And the same promotion IS named once its code is sent, so the assertion
	// above is about the code rather than about the promotion being invisible.
	asked, err := svc.ExplainDiscounts(ctx, service.ComputeInput{
		CurrencyCode: "TRY",
		Items:        []service.ComputeItem{{ID: "li_1", Amount: 10000, UnitAmount: 10000, Quantity: 1}},
		Codes:        []string{untypedCode},
	})
	require.NoError(t, err)
	assert.Equal(t, service.SkipNotActive, reasonFor(t, asked, untyped.ID))
}

// appliedIDs returns the promotions that produced a discount.
func appliedIDs(result service.ComputeResult) []string {
	out := make([]string, 0, len(result.Applied))
	for i := range result.Applied {
		out = append(out, result.Applied[i].PromotionID)
	}

	return out
}

// skippedIDs returns the promotions that were considered and refused.
func skippedIDs(result service.ComputeResult) []string {
	out := make([]string, 0, len(result.Skipped))
	for i := range result.Skipped {
		out = append(out, result.Skipped[i].PromotionID)
	}

	return out
}

// reasonFor returns the reason reported for a promotion, failing if there is
// none.
func reasonFor(t *testing.T, result service.ComputeResult, promotionID string) service.SkipReason {
	t.Helper()

	for i := range result.Skipped {
		if result.Skipped[i].PromotionID == promotionID {
			return result.Skipped[i].Reason
		}
	}
	t.Fatalf("no reason was reported for %s; the result named %d skipped promotion(s)",
		promotionID, len(result.Skipped))

	return ""
}
