//go:build integration

package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	b2bmodels "github.com/bdrtr/gobit/internal/modules/b2b/models"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// TestAContractPriceNamesItsBuyer is ADR 0185 on the production wiring: the
// cart asks the b2b module which company a customer buys for, puts the customer
// and the company into the rule context, and pricing's ladder ranks a price
// that names the buyer above one that does not.
//
// Three customers see one variant. The customer with a contract of their own
// pays it, although their company's contract is on the same override rung; a
// colleague pays the company's; a customer of no company pays the base price.
// Every contract is the CHEAPER price here, so this proves the attributes
// arrived; that the ladder picks a contract over a cheaper segment price is
// pricing's own test. The order line then names the contract list it charged
// (ADR 0168).
func TestAContractPriceNamesItsBuyer(t *testing.T) {
	ctx := t.Context()

	contracted, contractedEmail := newCustomer(ctx, t)
	colleague, _ := newCustomer(ctx, t)
	stranger, _ := newCustomer(ctx, t)
	companyID := b2bCalisan(ctx, t, contracted, nil, b2bmodels.ResetMonthly)
	_, err := b2bSvc.CreateEmployee(ctx, b2bsvc.EmployeeInput{CompanyID: companyID, CustomerID: colleague})
	require.NoError(t, err)

	variantID, _ := newStockedVariant(ctx, t, "E2E Contract Price Product", nil, 5)
	set, err := pricingSvc.CreatePriceSet(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))

	companyList := activeOverrideList(ctx, t, "E2E contract of "+companyID)
	customerList := activeOverrideList(ctx, t, "E2E contract of "+contracted)
	_, err = pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: 20_000, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: 18_000, MinQuantity: 1, PriceListID: &companyList,
			Rules: []pricingsvc.RuleInput{{Attribute: pricingmodels.AttrCompanyID,
				Operator: pricingmodels.OpEq, Values: []string{companyID}}}},
		{CurrencyCode: taxedCurrency, Amount: 17_500, MinQuantity: 1, PriceListID: &customerList,
			Rules: []pricingsvc.RuleInput{{Attribute: pricingmodels.AttrCustomerID,
				Operator: pricingmodels.OpEq, Values: []string{contracted}}}},
	})
	require.NoError(t, err)

	for _, buyer := range []struct {
		name       string
		customerID string
		unitPrice  int64
	}{
		{"their own contract", contracted, 17_500},
		{"their company's contract", colleague, 18_000},
		{"the base price", stranger, 20_000},
	} {
		_, totals := prepareCart(ctx, t, buyer.customerID, variantID, 1)
		require.Len(t, totals.Lines, 1)
		assert.Equal(t, buyer.unitPrice, totals.Lines[0].UnitPrice, buyer.name)
	}

	cartID, totals := prepareCart(ctx, t, contracted, variantID, 1)
	placed, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             contractedEmail,
		ExpectedTotal:     totals.Total,
	})
	require.NoError(t, err)

	detail, err := orderSvc.GetOrder(ctx, placed.OrderID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)
	require.NotNil(t, detail.Items[0].PriceOrigin)
	assert.Equal(t, customerList, detail.Items[0].PriceOrigin.PriceListID,
		"the order line names the contract it was charged under")
	assert.Equal(t, string(pricingmodels.PriceListOverride), detail.Items[0].PriceOrigin.PriceListType)
	assert.Equal(t, int64(17_500), detail.Items[0].UnitPrice)
}

// activeOverrideList creates a published override list and returns its id.
func activeOverrideList(ctx context.Context, t *testing.T, title string) string {
	t.Helper()

	list, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
		Title: title, Type: pricingmodels.PriceListOverride, Status: pricingmodels.PriceListActive,
	})
	require.NoError(t, err)
	return list.ID
}
