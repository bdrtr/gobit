//go:build integration

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	promotionmodels "github.com/bdrtr/gobit/internal/modules/promotion/models"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// This file proves the chain ADR 0148 closed: "half off everything in this
// category" applies to the lines whose product is in it and to no other.
//
// # Why nothing smaller can prove it
//
// FOUR packages have to agree and no two of them may import each other. The
// product module has to publish `category_ids` on its Query record; the cart flow
// has to ask for that exact field and put it in the line's LIST map; the JSON on
// the wire has to carry a key promotion's decoder recognizes — it refuses unknown
// fields — and the engine's `any_in` has to read the list rather than the single
// value. A unit test on any one of them passes while the neighbour sends nothing:
// the cart's fake catalog answers whatever the test wrote, and the engine's fake
// input carries whatever the test typed.
//
// The failure that shape produces is the one this repository keeps closing: a
// promotion that silently discounts nothing, with no error anywhere.

// The category discount scenario's MANUALLY computed amounts.
//
// A 50% (5000 bps) percentage promotion, target "items", allocation "each"; the
// region is taxed at 20% and no shipping method is chosen.
//
//	in-category:  20_000 x 1 = 20_000 subtotal
//	              discount 20_000 x 50% = 10_000
//	              tax base 20_000 - 10_000 = 10_000, tax 2_000
//	out:          10_000 x 1 = 10_000 subtotal, no discount
//	              tax 10_000 x 20% = 2_000
//
//	subtotal = 30_000, discount = 10_000, tax = 4_000
//	total    = 30_000 - 10_000 + 4_000 = 24_000
const (
	categoryRateBps int64 = 5_000

	categoryPriceIn  int64 = 20_000
	categoryPriceOut int64 = 10_000

	categoryDiscountIn int64 = 10_000
	categoryTaxIn      int64 = 2_000
	categoryTaxOut     int64 = 2_000

	categorySubtotal int64 = 30_000
	categoryDiscount int64 = 10_000
	categoryTax      int64 = 4_000
	categoryTotal    int64 = 24_000
)

// newCategorisedVariant creates a published product filed under the given
// categories, with one priced variant, and returns the variant id.
func newCategorisedVariant(
	ctx context.Context, t *testing.T, title string, price int64, categoryIDs []string,
) string {
	t.Helper()

	seq := fixtureCounter.Add(1)
	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:      fmt.Sprintf("e2e-categorised-%d", seq),
		Title:       title,
		Status:      productmodels.StatusPublished,
		CategoryIDs: categoryIDs,
	})
	require.NoError(t, err, "the fixture product could not be created")

	variant, err := productSvc.CreateVariant(ctx, product.ID,
		productsvc.CreateVariantInput{Title: title})
	require.NoError(t, err, "the fixture variant could not be created")

	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{{
		CurrencyCode: taxedCurrency,
		Amount:       price,
		MinQuantity:  1,
	}})
	require.NoError(t, err, "the fixture price set could not be created")
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variant.ID, set.ID),
		"the price bond could not be written; without it the flow finds no price")

	return variant.ID
}

// newCategoryTargetedPromotion creates an automatic percentage promotion whose
// TARGET rule asks whether the line's product is in any of the given categories.
func newCategoryTargetedPromotion(
	ctx context.Context, t *testing.T, code string, rateBps int64, categoryIDs []string,
) string {
	t.Helper()

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        code,
		IsAutomatic: true,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err, "the fixture promotion could not be created")

	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID,
		promotionsvc.ApplicationMethodInput{
			Type:       promotionmodels.MethodPercentage,
			TargetType: promotionmodels.TargetItems,
			Allocation: promotionmodels.AllocationEach,
			Value:      rateBps,
		})
	require.NoError(t, err, "the application method could not be written")

	// The rule a merchant could not write before ADR 0148: the attribute is a
	// LIST and the operator is the one that reads one.
	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: "category_ids",
		Operator:  promotionmodels.OpAnyIn,
		Values:    categoryIDs,
	})
	require.NoError(t, err, "the category target rule could not be written")

	return promotion.ID
}

// TestACategoryPromotionDiscountsOnlyTheLinesInIt is the decision, end to end.
func TestACategoryPromotionDiscountsOnlyTheLinesInIt(t *testing.T) {
	ctx := t.Context()

	seq := fixtureCounter.Add(1)
	shirts, err := productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{
		Name:   "E2E Shirts",
		Handle: fmt.Sprintf("e2e-shirts-%d", seq),
	})
	require.NoError(t, err, "the fixture category could not be created")

	// The second product is in NO category. Without it a promotion that matched
	// every line would produce the same discount on the one line that is in the
	// category, and the test would pass on an implementation that reads nothing.
	inCategory := newCategorisedVariant(ctx, t, "E2E In Category", categoryPriceIn,
		[]string{shirts.ID})
	outOfCategory := newCategorisedVariant(ctx, t, "E2E Out Of Category", categoryPriceOut, nil)

	newCategoryTargetedPromotion(ctx, t, fmt.Sprintf("E2E-CATEGORY-%d", seq),
		categoryRateBps, []string{shirts.ID})

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err, "the cart must open")

	lineIn, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: inCategory, Quantity: 1,
	})
	require.NoError(t, err, "the in-category line must be addable")

	result, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: outOfCategory, Quantity: 1,
	})
	require.NoError(t, err, "the out-of-category line must be addable")

	assertTotals(t, result.Totals, expectedTotal{
		subtotal: categorySubtotal,
		discount: categoryDiscount,
		tax:      categoryTax,
		shipping: 0,
		total:    categoryTotal,
	}, "in the cart with the category promotion")

	lines := lineTotalsByID(t, result.Totals)

	discounted, found := lines[lineIn.LineItemID]
	require.True(t, found, "the in-category line must be in the calculation")
	assert.Equal(t, categoryDiscountIn, discounted.DiscountTotal,
		"the line whose product is in the category must get half off; a zero here means "+
			"the membership never reached the engine")
	assert.Equal(t, categoryTaxIn, discounted.TaxTotal,
		"and the tax must be computed on the POST-discount base")

	untouched, found := lines[result.LineItemID]
	require.True(t, found, "the out-of-category line must be in the calculation")
	assert.Zero(t, untouched.DiscountTotal,
		"the line whose product is in NO category must be untouched; a discount here "+
			"means the rule matched everything rather than reading the list")
	assert.Equal(t, categoryTaxOut, untouched.TaxTotal)
}

// TestATagPromotionDiscountsOnlyTheLinesCarryingIt covers the second list.
//
// One mechanism carries two fields and forgetting one of them would be silent: a
// tag rule would select no line, the promotion would produce no discount, and
// nothing would report it. The fixture is separate from the category's for the
// reason ADR 0144's pair is separate — one fixture asserting both would let an
// implementation that only fed categories pass the first assertion and never
// reach the second.
func TestATagPromotionDiscountsOnlyTheLinesCarryingIt(t *testing.T) {
	ctx := t.Context()

	seq := fixtureCounter.Add(1)
	sale, err := productSvc.CreateTag(ctx, fmt.Sprintf("e2e-sale-%d", seq))
	require.NoError(t, err, "the fixture tag could not be created")

	tagged := newTaggedVariant(ctx, t, "E2E Tagged", categoryPriceIn, []string{sale.ID})
	untagged := newCategorisedVariant(ctx, t, "E2E Untagged", categoryPriceOut, nil)

	promotion, err := promotionSvc.CreatePromotion(ctx, promotionsvc.PromotionInput{
		Code:        fmt.Sprintf("E2E-TAG-%d", seq),
		IsAutomatic: true,
		Status:      promotionmodels.PromotionActive,
	})
	require.NoError(t, err)
	_, err = promotionSvc.SetApplicationMethod(ctx, promotion.ID,
		promotionsvc.ApplicationMethodInput{
			Type:       promotionmodels.MethodPercentage,
			TargetType: promotionmodels.TargetItems,
			Allocation: promotionmodels.AllocationEach,
			Value:      categoryRateBps,
		})
	require.NoError(t, err)
	_, err = promotionSvc.AddPromotionRule(ctx, promotion.ID, promotionsvc.RuleInput{
		RuleType:  promotionmodels.RuleTarget,
		Attribute: "tag_ids",
		Operator:  promotionmodels.OpAnyIn,
		Values:    []string{sale.ID},
	})
	require.NoError(t, err)

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err)

	lineTagged, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: tagged, Quantity: 1,
	})
	require.NoError(t, err)
	result, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: untagged, Quantity: 1,
	})
	require.NoError(t, err)

	lines := lineTotalsByID(t, result.Totals)
	assert.Equal(t, categoryDiscountIn, lines[lineTagged.LineItemID].DiscountTotal,
		"the tagged line must get half off")
	assert.Zero(t, lines[result.LineItemID].DiscountTotal,
		"the untagged line must be untouched")
}

// newTaggedVariant creates a published product carrying the given tags, with one
// priced variant.
func newTaggedVariant(
	ctx context.Context, t *testing.T, title string, price int64, tagIDs []string,
) string {
	t.Helper()

	seq := fixtureCounter.Add(1)
	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-tagged-%d", seq),
		Title:  title,
		Status: productmodels.StatusPublished,
		TagIDs: tagIDs,
	})
	require.NoError(t, err, "the fixture product could not be created")

	variant, err := productSvc.CreateVariant(ctx, product.ID,
		productsvc.CreateVariantInput{Title: title})
	require.NoError(t, err, "the fixture variant could not be created")

	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{{
		CurrencyCode: taxedCurrency,
		Amount:       price,
		MinQuantity:  1,
	}})
	require.NoError(t, err, "the fixture price set could not be created")
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variant.ID, set.ID),
		"the price bond could not be written")

	return variant.ID
}
