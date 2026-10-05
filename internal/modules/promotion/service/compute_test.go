package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// testNow is the tests' fixed clock; the branches that depend on a campaign
// window can only be tested with a deterministic clock.
var testNow = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// newTestService builds a service with a fixed clock running on the in-memory
// repository.
func newTestService(repo *memRepo) *Service {
	return New(repo, Options{
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return testNow },
	})
}

// ptr returns a pointer to a value; for writing optional fields briefly.
func ptr[T any](v T) *T { return &v }

// percentageMethod produces a percentage-discount application method.
func percentageMethod(promotionID string, bps int64, target models.ApplicationTargetType, alloc models.Allocation) *models.ApplicationMethod {
	return &models.ApplicationMethod{
		ID:          "appm_" + promotionID,
		PromotionID: promotionID,
		Type:        models.MethodPercentage,
		TargetType:  target,
		Allocation:  alloc,
		Value:       bps,
	}
}

// fixedMethod produces a fixed-amount application method.
func fixedMethod(promotionID string, amount int64, target models.ApplicationTargetType, alloc models.Allocation) *models.ApplicationMethod {
	return &models.ApplicationMethod{
		ID:           "appm_" + promotionID,
		PromotionID:  promotionID,
		Type:         models.MethodFixed,
		TargetType:   target,
		Allocation:   alloc,
		Value:        amount,
		CurrencyCode: "TRY",
	}
}

// seedPromotion writes a promotion into the repository together with its
// method and rules.
func seedPromotion(
	repo *memRepo,
	promo models.Promotion,
	method *models.ApplicationMethod,
	rules ...models.PromotionRule,
) {
	if promo.Status == "" {
		promo.Status = models.PromotionActive
	}
	if promo.Type == "" {
		promo.Type = models.PromotionStandard
	}
	repo.promotions[promo.ID] = promo
	if method != nil {
		repo.methods[promo.ID] = *method
	}
	if len(rules) > 0 {
		repo.rules[promo.ID] = rules
	}
}

// item produces an item taking part in the computation input.
// item builds an item and derives the UNIT price from the amount.
//
// The derivation exists ONLY IN TESTS, and that is exactly why it is forbidden
// in production: the division silently rounds an amount that does not divide.
// In a test it is not silent — [normalizeComputeInput] enforces the identity
// (unit × quantity = amount), so an item built with an amount that does not
// divide is rejected at the computation's door and fails the test. The tests
// whose subject is the unit price itself write it explicitly with [unitItem].
func item(id string, amount, quantity int64, attrs map[string]string) ComputeItem {
	unit := int64(0)
	if quantity > 0 {
		unit = amount / quantity
	}
	return ComputeItem{ID: id, Amount: amount, UnitAmount: unit, Quantity: quantity, Attributes: attrs}
}

// unitItem builds an item from the UNIT price; the amount is unit × quantity.
func unitItem(id string, unitAmount, quantity int64, attrs map[string]string) ComputeItem {
	return ComputeItem{
		ID:         id,
		Amount:     unitAmount * quantity,
		UnitAmount: unitAmount,
		Quantity:   quantity,
		Attributes: attrs,
	}
}

// assertInvariants verifies the result's INVARIANTS.
//
// This helper is called in almost every test and gathers the module's most
// critical claims in one place: the line bound, the total identity and the
// Σ line = Σ promotion equality. A mistake made in any branch of the
// computation gets caught here even if it passes the branch's own assertion.
func assertInvariants(t *testing.T, in ComputeInput, res ComputeResult) {
	t.Helper()

	require.Len(t, res.Items, len(in.Items), "there has to be one result record per item")
	require.Len(t, res.ShippingMethods, len(in.ShippingMethods), "there has to be one result record per shipping method")

	var itemsTotal int64
	for i := range in.Items {
		assert.Equal(t, in.Items[i].ID, res.Items[i].ID, "the result has to be in the same order as the input")
		assert.GreaterOrEqual(t, res.Items[i].Amount, int64(0), "a discount cannot be negative")
		assert.LessOrEqual(t, res.Items[i].Amount, in.Items[i].Amount,
			"the discount of item %s cannot exceed its amount", in.Items[i].ID)
		itemsTotal += res.Items[i].Amount
	}
	var shippingTotal int64
	for i := range in.ShippingMethods {
		assert.Equal(t, in.ShippingMethods[i].ID, res.ShippingMethods[i].ID)
		assert.LessOrEqual(t, res.ShippingMethods[i].Amount, in.ShippingMethods[i].Amount,
			"the discount of shipping method %s cannot exceed its amount", in.ShippingMethods[i].ID)
		shippingTotal += res.ShippingMethods[i].Amount
	}

	assert.Equal(t, itemsTotal, res.ItemsDiscountTotal, "Σ item discount has to equal the items total exactly")
	assert.Equal(t, shippingTotal, res.ShippingDiscountTotal, "Σ shipping discount has to equal the shipping total exactly")
	assert.Equal(t, itemsTotal+shippingTotal, res.DiscountTotal, "the total discount is the sum of the two components")

	var appliedTotal int64
	for i := range res.Applied {
		assert.Positive(t, res.Applied[i].Amount, "a zero discount does not count as applied")
		appliedTotal += res.Applied[i].Amount
	}
	assert.Equal(t, res.DiscountTotal, appliedTotal,
		"the sum of the amounts applied per promotion has to equal the total discount exactly")
}

func TestComputeDiscountsPercentageEachRoundsDown(t *testing.T) {
	repo := newMemRepo()
	// 20% → 999 * 2000 / 10000 = 199.8 → 199 (DOWN).
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE20", IsAutomatic: true},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 999, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(199), res.Items[0].Amount,
		"a percentage discount has to round down (199.8 → 199); rounding up would exceed the promised rate")
	assert.Equal(t, int64(199), res.DiscountTotal)
}

func TestComputeDiscountsFixedAmountEachAppliesToTheQuantity(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "FIXED10", IsAutomatic: true},
		fixedMethod("promo_1", 1000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			unitItem("li_1", 1666, 3, nil), // 3 units × 1000 = 3000
			unitItem("li_2", 2000, 1, nil), // 1 unit × 1000 = 1000
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(3000), res.Items[0].Amount, "a fixed amount is applied to EACH UNIT")
	assert.Equal(t, int64(1000), res.Items[1].Amount)
	assert.Equal(t, int64(4000), res.DiscountTotal)
}

func TestComputeDiscountsFixedAmountEachIsBoundedByMaxQuantity(t *testing.T) {
	repo := newMemRepo()
	method := fixedMethod("promo_1", 1000, models.TargetItems, models.AllocationEach)
	method.MaxQuantity = ptr(int64(2))
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "FIXED10", IsAutomatic: true}, method)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 50000, 5, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.Items[0].Amount,
		"with a maximum quantity of 2 the discount applies to only two units")
}

func TestComputeDiscountsPercentageEachIgnoresMaxQuantity(t *testing.T) {
	repo := newMemRepo()
	method := percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach)
	method.MaxQuantity = ptr(int64(1))
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YARIM", IsAutomatic: true}, method)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 5, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.Items[0].Amount,
		"a percentage discount ignores the maximum quantity; the base is the line's AMOUNT")
}

func TestComputeDiscountsAcrossDistributesTheLeftoverCentAndTheTotalAddsUpExactly(t *testing.T) {
	repo := newMemRepo()
	// A fixed discount of 100 units is distributed across three equal lines:
	// 33 + 33 + 33 = 99, and the leftover 1 cent goes, among those with an
	// equal fractional remainder, to the one with the SMALLEST IDENTITY.
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "FIXED100", IsAutomatic: true},
		fixedMethod("promo_1", 100, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 1000, 1, nil),
			item("li_b", 1000, 1, nil),
			item("li_c", 1000, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(100), res.DiscountTotal, "Σ line discount has to equal the distributed total EXACTLY")
	assert.Equal(t, int64(34), res.Items[0].Amount, "the leftover cent goes to the line with the smallest identity")
	assert.Equal(t, int64(33), res.Items[1].Amount)
	assert.Equal(t, int64(33), res.Items[2].Amount)
}

func TestComputeDiscountsAcrossAllocationIsIndependentOfInputOrder(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "FIXED100", IsAutomatic: true},
		fixedMethod("promo_1", 100, models.TargetItems, models.AllocationAcross))
	svc := newTestService(repo)

	inOrder := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 1000, 1, nil),
			item("li_b", 1000, 1, nil),
			item("li_c", 1000, 1, nil),
		},
	}
	reversed := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_c", 1000, 1, nil),
			item("li_b", 1000, 1, nil),
			item("li_a", 1000, 1, nil),
		},
	}

	forward, err := svc.ComputeDiscounts(context.Background(), inOrder)
	require.NoError(t, err)
	backward, err := svc.ComputeDiscounts(context.Background(), reversed)
	require.NoError(t, err)

	byID := map[string]int64{}
	for _, line := range backward.Items {
		byID[line.ID] = line.Amount
	}
	for _, line := range forward.Items {
		assert.Equal(t, line.Amount, byID[line.ID],
			"the cent of line %s has to be independent of the lines' ARRIVAL ORDER", line.ID)
	}
}

func TestComputeDiscountsAcrossPercentageRoundsOnce(t *testing.T) {
	repo := newMemRepo()
	// The base is 3 × 333 = 999; 20% → 199 (in one go). Computed per line, each
	// line would get 66 and the total would be 198 — one cent lost.
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE20", IsAutomatic: true},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 333, 1, nil),
			item("li_b", 333, 1, nil),
			item("li_c", 333, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(199), res.DiscountTotal,
		"an across percentage is rounded once over the TOTAL (199), not per line (198)")
}

func TestComputeDiscountsOrderTargetIsDistributedToAllItemsAndIgnoresTheTargetRule(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "ORDER10", IsAutomatic: true},
		percentageMethod("promo_1", 1000, models.TargetOrder, models.AllocationAcross),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"elektronik"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 6000, 1, map[string]string{"kategori": "elektronik"}),
			item("li_b", 4000, 1, map[string]string{"kategori": "giyim"}),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.DiscountTotal, "10% × 10000 = 1000")
	assert.Equal(t, int64(600), res.Items[0].Amount)
	assert.Equal(t, int64(400), res.Items[1].Amount,
		"an order target ignores the target rule; the discount is distributed to ALL items")
}

func TestComputeDiscountsTargetRuleFiltersItems(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "ELEKTRONIK", IsAutomatic: true},
		percentageMethod("promo_1", 1000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"elektronik"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 6000, 1, map[string]string{"kategori": "elektronik"}),
			item("li_b", 4000, 1, map[string]string{"kategori": "giyim"}),
			item("li_c", 3000, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(600), res.Items[0].Amount)
	assert.Zero(t, res.Items[1].Amount, "an item that does not satisfy the rule gets no discount")
	assert.Zero(t, res.Items[2].Amount, "an item without the attribute DOES NOT SATISFY the rule")
}

func TestComputeDiscountsShippingTarget(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "FREESHIPPING", IsAutomatic: true},
		percentageMethod("promo_1", 10000, models.TargetShippingMethods, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode:    "TRY",
		Items:           []ComputeItem{item("li_a", 10000, 1, nil)},
		ShippingMethods: []ComputeShippingMethod{{ID: "sm_1", Amount: 4990}},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.Items[0].Amount, "a shipping discount does not touch the items")
	assert.Equal(t, int64(4990), res.ShippingMethods[0].Amount)
	assert.Equal(t, int64(4990), res.ShippingDiscountTotal)
}

func TestComputeDiscountsPercentagesDoNotStack(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "ON", IsAutomatic: true},
		percentageMethod("promo_1", 1000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "YIRMI", IsAutomatic: true},
		percentageMethod("promo_2", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(3000), res.DiscountTotal,
		"10% + 20% over the ORIGINAL amount comes to 3000; a compound computation would give 2800")
	require.Len(t, res.Applied, 2)
	assert.Equal(t, int64(1000), res.Applied[0].Amount)
	assert.Equal(t, int64(2000), res.Applied[1].Amount)
}

func TestComputeDiscountsCouponsApplyBEFOREAutomatics(t *testing.T) {
	repo := newMemRepo()
	// Together the two exceed the line's amount; the one clipped has to be the
	// LAST, and the last is the automatic promotion.
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "OTOMATIK", IsAutomatic: true},
		percentageMethod("promo_1", 8000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "KUPON", IsAutomatic: false},
		percentageMethod("promo_2", 8000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"KUPON"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(10000), res.DiscountTotal, "the total discount cannot exceed the line amount")
	require.Len(t, res.Applied, 2)
	assert.Equal(t, "KUPON", res.Applied[0].Code, "the coupon is applied first")
	assert.Equal(t, int64(8000), res.Applied[0].Amount, "the coupon the customer typed is applied IN FULL")
	assert.Equal(t, "OTOMATIK", res.Applied[1].Code)
	assert.Equal(t, int64(2000), res.Applied[1].Amount, "the one clipped is the automatic promotion that comes after")
}

func TestComputeDiscountsWithinAGroupAppliesInIDOrder(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_a", Code: "ILK", IsAutomatic: true},
		percentageMethod("promo_a", 8000, models.TargetItems, models.AllocationEach))
	seedPromotion(repo, models.Promotion{ID: "promo_b", Code: "IKINCI", IsAutomatic: true},
		percentageMethod("promo_b", 8000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	require.Len(t, res.Applied, 2)
	assert.Equal(t, "promo_a", res.Applied[0].PromotionID, "within the same group the one with the smaller identity is applied first")
	assert.Equal(t, int64(8000), res.Applied[0].Amount)
	assert.Equal(t, int64(2000), res.Applied[1].Amount)
}

func TestComputeDiscountsDiscountCannotExceedTheLineAmount(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "COKBUYUK", IsAutomatic: true},
		fixedMethod("promo_1", 999_999, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 500, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(500), res.Items[0].Amount, "the discount stops at the line's amount")
	assert.Equal(t, int64(500), res.Applied[0].Amount,
		"the amount written to the promotion is the real amount AFTER CLIPPING")
}

func TestComputeDiscountsZeroAmountItemGetsNoDiscount(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "FIXED100", IsAutomatic: true},
		fixedMethod("promo_1", 100, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 0, 1, nil),
			item("li_b", 1000, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.Items[0].Amount, "no cent is written onto a zero-amount item")
	assert.Equal(t, int64(100), res.Items[1].Amount)
}

func TestComputeDiscountsNoDiscountWhenEveryItemIsZero(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE50", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_a", 0, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
	assert.Empty(t, res.Applied, "a promotion that produces no discount at all does not count as applied")
}

func TestComputeDiscountsCartWithoutItemsIsNotAnError(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE50", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{CurrencyCode: "TRY"}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
	assert.Empty(t, res.Items)
}

func TestComputeDiscountsLargeAmountsDoNotOverflow(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YUZDE100", IsAutomatic: true},
		percentageMethod("promo_1", 10000, models.TargetItems, models.AllocationAcross))

	// Both lines are half the maximum amount; the base comes to exactly
	// [models.MaxAmount] and a 100% discount needs the intermediate product
	// 10^12 × 10^4 / 10^4.
	half := models.MaxAmount / 2
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", half, 1, nil),
			item("li_b", half, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, models.MaxAmount, res.DiscountTotal, "even at the maximum amount the computation completes without overflowing")
	assert.Equal(t, half, res.Items[0].Amount)
	assert.Equal(t, half, res.Items[1].Amount)
}

func TestComputeDiscountsSubtotalAboveTheBoundIsRejected(t *testing.T) {
	repo := newMemRepo()
	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", models.MaxAmount, 1, nil),
			item("li_b", 1, 1, nil),
		},
	}
	_, err := newTestService(repo).ComputeDiscounts(context.Background(), in)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"a subtotal exceeding the maximum amount is the limit of the overflow protection and is rejected")
}

func TestComputeDiscountsEliminationBranches(t *testing.T) {
	pastWindow := models.Campaign{
		ID: "camp_gecmis", Name: "Past", CampaignIdentifier: "GECMIS",
		BudgetType: models.BudgetNone,
		EndsAt:     ptr(testNow.Add(-time.Hour)),
	}
	exhaustedBudget := models.Campaign{
		ID: "camp_tukenmis", Name: "Exhausted", CampaignIdentifier: "TUKENMIS",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(1000)),
		BudgetUsed: 1000, BudgetCurrencyCode: "TRY",
	}

	tests := []struct {
		name   string
		setup  func(repo *memRepo)
		reason string
	}{
		{
			name: "draft promotion",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TASLAK", IsAutomatic: true, Status: models.PromotionDraft,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "a draft promotion produces no discount",
		},
		{
			name: "inactive promotion",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "PASIF", IsAutomatic: true, Status: models.PromotionInactive,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "an inactive promotion produces no discount",
		},
		{
			name: "no application method",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "YONTEMSIZ", IsAutomatic: true}, nil)
			},
			reason: "a promotion without a method does not say HOW the discount is to be applied",
		},
		{
			name: "buyget type",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "BUYGET", IsAutomatic: true, Type: models.PromotionBuyGet,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "a buyget promotion whose method carries no buy and reward counts is " +
				"skipped as a reward mismatch (buyget_test.go asserts the reason)",
		},
		{
			name: "usage allowance used up",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "BITMIS", IsAutomatic: true,
					UsageLimit: ptr(int64(2)), UsageCount: 2,
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "a coupon whose usage allowance has run out is not applied",
		},
		{
			name: "campaign window closed",
			setup: func(repo *memRepo) {
				repo.campaigns[pastWindow.ID] = pastWindow
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "GECMIS", IsAutomatic: true, CampaignID: ptr(pastWindow.ID),
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "the promotion of a campaign whose window has closed is not applied",
		},
		{
			name: "campaign budget exhausted",
			setup: func(repo *memRepo) {
				repo.campaigns[exhaustedBudget.ID] = exhaustedBudget
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "TUKENMIS", IsAutomatic: true, CampaignID: ptr(exhaustedBudget.ID),
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "a campaign whose budget is exhausted is not applied PARTIALLY either",
		},
		{
			name: "campaign deleted",
			setup: func(repo *memRepo) {
				seedPromotion(repo, models.Promotion{
					ID: "promo_1", Code: "SAHIPSIZ", IsAutomatic: true, CampaignID: ptr("camp_missing"),
				}, percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))
			},
			reason: "a promotion whose campaign was deleted does NOT BECOME unlimited; it is eliminated",
		},
		{
			name: "currency mismatch on a fixed discount",
			setup: func(repo *memRepo) {
				method := fixedMethod("promo_1", 1000, models.TargetItems, models.AllocationEach)
				method.CurrencyCode = "USD"
				seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "DOLAR", IsAutomatic: true}, method)
			},
			reason: "no currency conversion is made; a fixed discount in a different currency is eliminated",
		},
		{
			name: "context rule not satisfied",
			setup: func(repo *memRepo) {
				seedPromotion(repo,
					models.Promotion{ID: "promo_1", Code: "VIPONLY", IsAutomatic: true},
					percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
					models.PromotionRule{
						ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
						Attribute: "customer_group_id", Operator: models.OpEq, Values: []string{"vip"},
					},
				)
			},
			reason: "if the field is not in the context the rule DOES NOT MATCH and the promotion is eliminated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			tt.setup(repo)

			in := ComputeInput{
				CurrencyCode: "TRY",
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
			require.NoError(t, err)

			assertInvariants(t, in, res)
			assert.Zero(t, res.DiscountTotal, tt.reason)
			assert.Empty(t, res.Applied, tt.reason)
		})
	}
}

func TestComputeDiscountsWhenTheContextRuleIsSatisfied(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "VIPONLY", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
			Attribute: "customer_group_id", Operator: models.OpIn, Values: []string{"vip", "b2b"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"customer_group_id": "vip"},
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.DiscountTotal, "when the context rule is satisfied the promotion is applied")
}

// TestANegativeContextRuleDoesNotMatchAnAbsentField proves that a context rule
// under ne or nin does not hold when the context does not carry its field.
//
// Otherwise a cart with an empty context would satisfy every negative rule and
// a discount meant for everybody but the blocked group would open to carts
// nobody has placed in any group. Each case first applies with the field
// present, so the absence is what closes it (ADR 0396).
func TestANegativeContextRuleDoesNotMatchAnAbsentField(t *testing.T) {
	for _, op := range []models.RuleOperator{models.OpNe, models.OpNin} {
		t.Run(string(op), func(t *testing.T) {
			repo := newMemRepo()
			seedPromotion(repo,
				models.Promotion{ID: "promo_1", Code: "NOTBLOCKED", IsAutomatic: true},
				percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
				models.PromotionRule{
					ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
					Attribute: "customer_group_id", Operator: op, Values: []string{"blocked"},
				},
			)
			svc := newTestService(repo)

			present := ComputeInput{
				CurrencyCode: "TRY",
				Context:      map[string]string{"customer_group_id": "vip"},
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := svc.ComputeDiscounts(context.Background(), present)
			require.NoError(t, err)
			assertInvariants(t, present, res)
			require.Equal(t, int64(5000), res.DiscountTotal, "with the field present the rule holds")

			absent := ComputeInput{
				CurrencyCode: "TRY",
				Context:      map[string]string{"region_id": "reg_1"},
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err = svc.ComputeDiscounts(context.Background(), absent)
			require.NoError(t, err)
			assertInvariants(t, absent, res)
			assert.Zero(t, res.DiscountTotal, "a context without the field MUST NOT satisfy the negative rule")
			assert.Empty(t, res.Applied)
		})
	}
}

func TestComputeDiscountsNonAutomaticIsNotAppliedWithoutItsCode(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SECRETCOUPON", IsAutomatic: false},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal, "a coupon whose code was not given is not applied on its own")
}

func TestComputeDiscountsCouponCodeIsCaseInsensitive(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SUMMER20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"  summer20 "},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.DiscountTotal, "a coupon code is insensitive to case and whitespace")
	assert.Empty(t, res.UnmatchedCodes)
}

func TestComputeDiscountsSameCodeGivenTwiceAppliesOnce(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SUMMER20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		// The same coupon twice, and an unmatchable code twice too: the first
		// tests that the discount is not doubled, the second that the
		// deduplication holds in the RESPONSE as well.
		Codes: []string{"SUMMER20", "summer20", "NOSUCHCODE", "nosuchcode"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(2000), res.DiscountTotal, "a repeated code must not double the discount")
	require.Len(t, res.Applied, 1)
	assert.Equal(t, []string{"NOSUCHCODE"}, res.UnmatchedCodes,
		"an unmatchable code is reported once, however many times it is given")
}

func TestComputeDiscountsUnmatchedCodesAreReported(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "TASLAK", IsAutomatic: false, Status: models.PromotionDraft,
	}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		Codes:        []string{"TASLAK", "NOSUCHCODE"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, []string{"TASLAK", "NOSUCHCODE"}, res.UnmatchedCodes,
		"a draft promotion and a nonexistent code are reported the SAME way; a distinction would be a leak")
}

func TestComputeDiscountsValidCodeThatProducesNoDiscountCountsAsMatched(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "ELEKTRONIK", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"elektronik"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, map[string]string{"kategori": "giyim"})},
		Codes:        []string{"ELEKTRONIK"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal)
	assert.Empty(t, res.UnmatchedCodes,
		"a VALID coupon with no item matching its target is not an invalid code")
}

func TestComputeDiscountsInputValidation(t *testing.T) {
	validItem := item("li_1", 1000, 1, nil)

	tests := []struct {
		name   string
		in     ComputeInput
		reason string
	}{
		{
			name:   "no currency",
			in:     ComputeInput{Items: []ComputeItem{validItem}},
			reason: "the currency is mandatory",
		},
		{
			name: "item id is repeated",
			in: ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{
				item("li_1", 1000, 1, nil), item("li_1", 2000, 1, nil),
			}},
			reason: "if the same identity appears twice, which line received which discount cannot be told apart",
		},
		{
			name:   "negative amount",
			in:     ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{item("li_1", -1, 1, nil)}},
			reason: "a negative amount is not a discount",
		},
		{
			name:   "zero quantity",
			in:     ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{item("li_1", 1000, 0, nil)}},
			reason: "the quantity has to be at least one",
		},
		{
			name:   "invalid coupon code",
			in:     ComputeInput{CurrencyCode: "TRY", Codes: []string{"a b"}},
			reason: "a coupon code cannot contain whitespace",
		},
		{
			name: "unit price not sent",
			in: ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{
				{ID: "li_1", Amount: 1000, Quantity: 1},
			}},
			reason: "the unit price is MANDATORY; a field left empty would make the reward mechanic " +
				"silently not work for some callers",
		},
		{
			name: "unit price × quantity does not give the amount",
			in: ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{
				{ID: "li_1", Amount: 1000, UnitAmount: 400, Quantity: 2},
			}},
			reason: "the reward is read from the unit, the line bound from the amount; when the two diverge " +
				"the promotion promises more than the line can carry",
		},
		{
			name: "negative unit price",
			in: ComputeInput{CurrencyCode: "TRY", Items: []ComputeItem{
				{ID: "li_1", Amount: -2, UnitAmount: -1, Quantity: 2},
			}},
			reason: "a negative unit price is not a price",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestService(newMemRepo()).ComputeDiscounts(context.Background(), tt.in)
			require.Error(t, err, tt.reason)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), tt.reason)
		})
	}
}

func TestComputeDiscountsDoesNotModifyTheInput(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "SUMMER20", IsAutomatic: false},
		percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	codes := []string{"summer20"}
	attrs := map[string]string{"kategori": "giyim"}
	in := ComputeInput{
		CurrencyCode: "try",
		Items:        []ComputeItem{item("li_1", 10000, 1, attrs)},
		Codes:        codes,
	}
	_, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assert.Equal(t, []string{"summer20"}, codes, "the caller's code slice must not be modified")
	assert.Equal(t, "try", in.CurrencyCode, "the caller's input must not change because of normalization")
	assert.Equal(t, map[string]string{"kategori": "giyim"}, attrs)
}

func TestComputeDiscountsPropagatesTheRepositoryError(t *testing.T) {
	repo := newMemRepo()
	repo.errOn["ListCandidates"] = errors.Unavailable("test_db", "no database")

	_, err := newTestService(repo).ComputeDiscounts(context.Background(), ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 1000, 1, nil)},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindUnavailable, errors.KindOf(err),
		"a repository error must not silently fall to 'no discount'")
}

func TestComputeDiscountsNeverWrites(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "SUMMER20", IsAutomatic: true, UsageLimit: ptr(int64(5)),
	}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

	svc := newTestService(repo)
	for range 3 {
		_, err := svc.ComputeDiscounts(context.Background(), ComputeInput{
			CurrencyCode: "TRY",
			Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
		})
		require.NoError(t, err)
	}

	assert.Zero(t, repo.promotions["promo_1"].UsageCount,
		"the computation has no side effects; looking at the cart DOES NOT SPEND the coupon")
	assert.Zero(t, repo.calls["Redeem"])
}

// TestComputeDiscountsAcrossFullLineDoesNotHalveTheSecondPromotion pins that in
// an "across" allocation the total to distribute is computed from the targets'
// ORIGINAL amounts (see [acrossTotal]).
//
// The scenario is two overlapping promotions: the coupon discounts li_1
// entirely, and the automatic order promotion applies 100% to the WHOLE order.
// Had the total been clipped to the targets' REMAINDER, the pool would drop to
// 1000, but since the shares are still distributed by the original amounts, the
// full li_1 would eat half the share and get it clipped, and li_2 would be
// given half of what was promised (500) — that is, the promotion would be
// penalized twice.
func TestComputeDiscountsAcrossFullLineDoesNotHalveTheSecondPromotion(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "KUPON", IsAutomatic: false},
		percentageMethod("promo_1", 10000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "kategori", Operator: models.OpEq, Values: []string{"a"},
		},
	)
	seedPromotion(repo, models.Promotion{ID: "promo_2", Code: "ORDER", IsAutomatic: true},
		percentageMethod("promo_2", 10000, models.TargetOrder, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_1", 1000, 1, map[string]string{"kategori": "a"}),
			item("li_2", 1000, 1, map[string]string{"kategori": "b"}),
		},
		Codes: []string{"KUPON"},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.Items[0].Amount, "the coupon discounts li_1 entirely")
	assert.Equal(t, int64(1000), res.Items[1].Amount,
		"the full line's share is clipped and lost; the empty line still receives its FULL share")
	assert.Equal(t, int64(2000), res.DiscountTotal,
		"had the 100% order discount been clipped to the remainder the total would be 1500; percentages are not compound")
	require.Len(t, res.Applied, 2)
	assert.Equal(t, int64(1000), res.Applied[1].Amount,
		"the amount written to the order promotion is what remains after the part caught on the line bound is subtracted")
}

// TestComputeDiscountsAcrossFixedAmountCannotExceedTheBase pins that the
// clipping still exists: a change removing the clipping entirely would try to
// distribute more than the targets' amount.
func TestComputeDiscountsAcrossFixedAmountCannotExceedTheBase(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{ID: "promo_1", Code: "COKBUYUK", IsAutomatic: true},
		fixedMethod("promo_1", 999_999, models.TargetItems, models.AllocationAcross))

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			item("li_a", 300, 1, nil),
			item("li_b", 700, 1, nil),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(1000), res.DiscountTotal, "an allocation cannot distribute more than the base it distributes over")
	assert.Equal(t, int64(300), res.Items[0].Amount)
	assert.Equal(t, int64(700), res.Items[1].Amount)
}

// TestComputeDiscountsEliminatesOnCampaignBudgetCurrency pins that when a
// MONEY-measured campaign budget is in a currency different from the cart's,
// the promotion does NOT enter the computation AT ALL (see
// [campaignBudgetCurrencyMatches]).
//
// Without the elimination the discount would show in the cart, while
// [Service.RedeemPromotion] would refuse the same amount with
// campaign_budget_currency_mismatch.
func TestComputeDiscountsEliminatesOnCampaignBudgetCurrency(t *testing.T) {
	tryBudget := models.Campaign{
		ID: "camp_try", Name: "Summer", CampaignIdentifier: "SUMMER",
		BudgetType: models.BudgetSpend, BudgetLimit: ptr(int64(100_000)), BudgetCurrencyCode: "TRY",
	}
	usageBudget := models.Campaign{
		ID: "camp_quantity", Name: "Quantity", CampaignIdentifier: "QUANTITY",
		BudgetType: models.BudgetUsage, BudgetLimit: ptr(int64(10)),
	}

	tests := []struct {
		name         string
		campaign     models.Campaign
		cartCurrency string
		want         int64
		reason       string
	}{
		{
			name: "currencies do not match", campaign: tryBudget, cartCurrency: "USD", want: 0,
			reason: "the promotion of a campaign with a TRY budget cannot be applied to a USD cart",
		},
		{
			name: "currencies match", campaign: tryBudget, cartCurrency: "TRY", want: 2000,
			reason: "in the same currency NO elimination is made",
		},
		{
			name: "a usage-measured budget does not look at the currency", campaign: usageBudget, cartCurrency: "USD", want: 2000,
			reason: "a budget that counts usage has no currency; it cannot be compared with the cart's",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			repo.campaigns[tt.campaign.ID] = tt.campaign
			seedPromotion(repo, models.Promotion{
				ID: "promo_1", Code: "YUZDE20", IsAutomatic: true, CampaignID: ptr(tt.campaign.ID),
			}, percentageMethod("promo_1", 2000, models.TargetItems, models.AllocationEach))

			in := ComputeInput{
				CurrencyCode: tt.cartCurrency,
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
			require.NoError(t, err)

			assertInvariants(t, in, res)
			assert.Equal(t, tt.want, res.DiscountTotal, tt.reason)
		})
	}
}

// TestComputeDiscountsNumericRuleDoesNotMatchAnUnparsableValue pins the
// decision in the [github.com/bdrtr/gobit/internal/core/condition.Match] godoc
// that a value that does not parse as an integer makes the condition not match.
//
// The decision is load-bearing for security: otherwise a single broken or
// malicious context field ("total": "abc") would open the threshold rules to
// everyone.
func TestComputeDiscountsNumericRuleDoesNotMatchAnUnparsableValue(t *testing.T) {
	tests := []struct {
		name         string
		ruleValue    string
		contextValue string
		want         int64
		reason       string
	}{
		{
			name: "the context value is not a number", ruleValue: "5000", contextValue: "abc", want: 0,
			reason: "a context value that cannot be converted DOES NOT MATCH the rule; it does not open the threshold to everyone",
		},
		{
			name: "the rule's value is not a number", ruleValue: "besbin", contextValue: "10000", want: 0,
			reason: "a rule value that cannot be converted does not match either; an unreadable condition must not open the discount",
		},
		{
			name: "both sides are numbers", ruleValue: "5000", contextValue: "10000", want: 5000,
			reason: "if both sides can be converted the rule is evaluated normally",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMemRepo()
			seedPromotion(repo,
				models.Promotion{ID: "promo_1", Code: "THRESHOLD", IsAutomatic: true},
				percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
				models.PromotionRule{
					ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
					Attribute: "cart_total", Operator: models.OpGte, Values: []string{tt.ruleValue},
				},
			)

			in := ComputeInput{
				CurrencyCode: "TRY",
				Context:      map[string]string{"cart_total": tt.contextValue},
				Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
			}
			res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
			require.NoError(t, err)

			assertInvariants(t, in, res)
			assert.Equal(t, tt.want, res.DiscountTotal, tt.reason)
		})
	}
}

// anyInPromotion builds the promotion carrying the "in ANY ONE of these
// groups" rule.
func anyInPromotion(repo *memRepo, operator models.RuleOperator) {
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "SEGMENT", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleContext,
			Attribute: "customer_group_id", Operator: operator, Values: []string{"vip"},
		},
	)
}

// TestAnyInAppliesTheSegmentDiscountEvenWhenItIsNotTheLEADINGGroup is the
// defect itself.
//
// The customer is in the groups {retail, vip} and the LEADING one, as the
// merchant ordered them, is "retail". The only value the cart could send was
// that leading one, so a rule written for vip was silently not applied to a
// customer INSIDE the segment — ADR 0103's opening defect (ADR 0144).
func TestAnyInAppliesTheSegmentDiscountEvenWhenItIsNotTheLEADINGGroup(t *testing.T) {
	repo := newMemRepo()
	anyInPromotion(repo, models.OpAnyIn)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"customer_group_id": "retail"},
		ContextLists: map[string][]string{"customer_group_id": {"retail", "vip"}},
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.DiscountTotal,
		"the customer is in the vip group; the leading group being retail must not shut the discount off")
}

// TestTheAnswerOfSHIPPEDRulesDoesNotCHANGEWithTheList nails down the second
// risk separately.
//
// The same customer, the same list, but the rule is written with `in`. `in`
// looks at a single value and, since the leading one is "retail", does not
// match — exactly today's answer. Had the operators been mixed up, a live
// discount would widen without announcing anything.
//
// It has to be a separate case: had both been tested in a single fixture, a
// thoroughly wrong implementation would get caught on the first assertion and
// this one would never fire.
func TestTheAnswerOfSHIPPEDRulesDoesNotCHANGEWithTheList(t *testing.T) {
	repo := newMemRepo()
	anyInPromotion(repo, models.OpIn)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"customer_group_id": "retail"},
		ContextLists: map[string][]string{"customer_group_id": {"retail", "vip"}},
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal,
		"`in` DOES NOT LOOK AT THE LIST: the answer of a shipped rule has to stay the same")
}

// TestAnyInDoesNotMatchWhenTheListIsNOTSENT does not count the unknown as
// matched.
//
// Without a list, which groups the customer is in is NOT KNOWN, and counting
// an unknown segment as matched would open the segment discount to everyone —
// the same answer the matcher gives for a field missing from the context.
func TestAnyInDoesNotMatchWhenTheListIsNOTSENT(t *testing.T) {
	repo := newMemRepo()
	anyInPromotion(repo, models.OpAnyIn)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Context:      map[string]string{"customer_group_id": "vip"},
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assert.Zero(t, res.DiscountTotal,
		"even if the single value is vip: any_in reads the LIST side, and there is no list")
}

// listItem builds an item with LIST attributes (ADR 0148).
func listItem(id string, amount, quantity int64, lists map[string][]string) ComputeItem {
	out := item(id, amount, quantity, nil)
	out.Lists = lists

	return out
}

// categoryPromotion builds a promotion whose target rule is "in any one of
// these categories".
func categoryPromotion(repo *memRepo, operator models.RuleOperator, attribute string) {
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "KATEGORI", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: attribute, Operator: operator, Values: []string{"cat_shirts"},
		},
	)
}

// TestATargetRuleCanReadTheLinesCATEGORY is the half ADR 0144 left behind.
//
// The operator existed, the set the line would offer did not: the product
// module did not publish membership, so "50% off lines in any one of these
// categories" could not be written (ADR 0148).
//
// There are two items and ONLY one is in that category: an implementation that
// selects all of them fails this test, and so does one that selects none.
func TestATargetRuleCanReadTheLinesCATEGORY(t *testing.T) {
	repo := newMemRepo()
	categoryPromotion(repo, models.OpAnyIn, "category_ids")

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			listItem("li_1", 10000, 1, map[string][]string{
				"category_ids": {"cat_hats", "cat_shirts"},
			}),
			listItem("li_2", 10000, 1, map[string][]string{
				"category_ids": {"cat_hats"},
			}),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.DiscountTotal,
		"only the line in the category should get a discount: 50% of 10000")
	assert.Equal(t, int64(5000), res.Items[0].Amount)
	assert.Zero(t, res.Items[1].Amount,
		"no discount should fall to the line in the other category; if it does, the rule is NOT BEING READ")
}

// TestATargetRuleCanReadTheTAGToo nails down the second list separately.
//
// The same mechanism carries two fields, and forgetting one while the other
// works would be silent: the tag rule selects no line, produces no discount,
// and there is no error either.
func TestATargetRuleCanReadTheTAGToo(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "ETIKET", IsAutomatic: true},
		percentageMethod("promo_1", 5000, models.TargetItems, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "tag_ids", Operator: models.OpAnyIn, Values: []string{"tag_sale"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			listItem("li_1", 10000, 1, map[string][]string{"tag_ids": {"tag_sale"}}),
			listItem("li_2", 10000, 1, map[string][]string{"tag_ids": {"tag_new"}}),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Equal(t, int64(5000), res.Items[0].Amount)
	assert.Zero(t, res.Items[1].Amount)
}

// TestASingleValuedOperatorInALineRuleDoesNotREADTheLIST repeats ADR 0144's
// rule on the LINE side.
//
// The same list, the rule written with `in`: it must not match. Had the
// operators been mixed up, a shipped `category_ids in [cat_shirts]` rule —
// which never matched a single value — would one day start matching and a live
// discount would widen without announcing anything.
//
// It has to be a separate case: had both been tested in a single fixture, a
// thoroughly wrong implementation would get caught on the first assertion.
func TestASingleValuedOperatorInALineRuleDoesNotREADTheLIST(t *testing.T) {
	repo := newMemRepo()
	categoryPromotion(repo, models.OpIn, "category_ids")

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			listItem("li_1", 10000, 1, map[string][]string{
				"category_ids": {"cat_shirts"},
			}),
		},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.DiscountTotal,
		"`in` DOES NOT LOOK AT THE LIST; on the line side as well")
}

// TestALineWithoutListsDoesNotMatchACATEGORYRule does not count the unknown as
// matched.
//
// If a line HAS NO list, which categories the product is in is not known —
// that is exactly how a product whose catalog cannot be read looks (see the
// cart flow's `lineLists`). Counting the unknown as matched would open the
// discount to every unreadable product.
func TestALineWithoutListsDoesNotMatchACATEGORYRule(t *testing.T) {
	repo := newMemRepo()
	categoryPromotion(repo, models.OpAnyIn, "category_ids")

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items:        []ComputeItem{item("li_1", 10000, 1, nil)},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assert.Zero(t, res.DiscountTotal)
}

// TestAShippingTargetDoesNotMatchACATEGORYRule states the shipping side
// separately.
//
// A shipping method is in no category and carries no list. The rule does not
// match, which is the right answer; had it matched, a "discount on products in
// this category" rule would turn shipping free.
func TestAShippingTargetDoesNotMatchACATEGORYRule(t *testing.T) {
	repo := newMemRepo()
	seedPromotion(repo,
		models.Promotion{ID: "promo_1", Code: "SHIPPING", IsAutomatic: true},
		percentageMethod("promo_1", 10000, models.TargetShippingMethods, models.AllocationEach),
		models.PromotionRule{
			ID: "prule_1", PromotionID: "promo_1", RuleType: models.RuleTarget,
			Attribute: "category_ids", Operator: models.OpAnyIn, Values: []string{"cat_shirts"},
		},
	)

	in := ComputeInput{
		CurrencyCode: "TRY",
		Items: []ComputeItem{
			listItem("li_1", 10000, 1, map[string][]string{"category_ids": {"cat_shirts"}}),
		},
		ShippingMethods: []ComputeShippingMethod{{ID: "sm_1", Amount: 4990}},
	}
	res, err := newTestService(repo).ComputeDiscounts(context.Background(), in)
	require.NoError(t, err)

	assertInvariants(t, in, res)
	assert.Zero(t, res.ShippingDiscountTotal,
		"a shipping method has no category; the rule must not select it")
}
