//go:build integration

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAnOrderLineRemembersThePriceItWasCharged walks the origin across every
// boundary it crosses (ADR 0168): pricing's ladder picks a sale price, the
// cart's totals round carries which one, the checkout's plan copies it, and the
// order line keeps it — and after the set's prices are replaced, which deletes
// the row, pricing's history still names it (ADR 0167).
//
// Two of those boundaries drop a field they do not know, so no hop can prove
// the chain on its own; ADR 0096 records the same shape for the tax breakdown.
func TestAnOrderLineRemembersThePriceItWasCharged(t *testing.T) {
	ctx := t.Context()

	customerID, email := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, "E2E Price Origin Product", nil, 5)

	set, err := pricingSvc.CreatePriceSet(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))

	list, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
		Title: "E2E Price Origin Sale", Type: pricingmodels.PriceListSale,
		Status: pricingmodels.PriceListActive,
	})
	require.NoError(t, err)

	written, err := pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: 20_000, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: 15_000, MinQuantity: 1, PriceListID: &list.ID},
	})
	require.NoError(t, err)
	var sale pricingmodels.Price
	for i := range written {
		if written[i].PriceListID != nil {
			sale = written[i]
		}
	}
	require.NotEmpty(t, sale.ID)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, 1)
	require.Len(t, totals.Lines, 1)
	assert.Equal(t, sale.ID, totals.Lines[0].PriceID, "the totals round names the price it used")

	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     totals.Total,
	})
	require.NoError(t, err)

	detail, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	require.NotNil(t, detail.Items[0].PriceOrigin,
		"the origin crossed pricing, the cart, the plan and the order; a nil here is a "+
			"boundary that dropped it in silence")
	assert.Equal(t, ordermodels.LinePriceOrigin{
		PriceID: sale.ID, PriceListID: list.ID, PriceListType: string(pricingmodels.PriceListSale),
	}, *detail.Items[0].PriceOrigin)
	assert.Equal(t, int64(15_000), detail.Items[0].UnitPrice)

	// The set is repriced: the row the line names is deleted from pricing's
	// table, and the line's origin has to stay readable anyway.
	_, err = pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: 21_000, MinQuantity: 1},
	})
	require.NoError(t, err)

	timeline, err := pricingSvc.PriceTimeline(ctx, set.ID,
		pricingsvc.TimelineQuery{CurrencyCode: taxedCurrency})
	require.NoError(t, err)
	named := false
	for _, stretch := range timeline.Stretches {
		if stretch.PriceID == sale.ID {
			named = true
			assert.Equal(t, int64(15_000), stretch.Amount)
			assert.Equal(t, pricingmodels.PriceListSale, stretch.PriceListType)
		}
	}
	assert.True(t, named, "the price history still says what the line's price row was")
}
