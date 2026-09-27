//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	customermodels "github.com/bdrtr/gobit/internal/modules/customer/models"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	pricingmodels "github.com/bdrtr/gobit/internal/modules/pricing/models"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// segmentCountry is a country no other scenario gives an address in, so the
// rule below names this scenario's customers alone.
const segmentCountry = "IS"

// TestACustomerSegmentFollowsItsRule is ADR 0217 on the production wiring: the
// operator previews a rule over the admin surface and sets it on a group priced
// by a price list, a hand edit of the group is refused, and a pass of the flow
// the customer-segments job runs puts in the customer the rule names — who is
// then charged the group's price — and leaves out the one it does not.
func TestACustomerSegmentFollowsItsRule(t *testing.T) {
	ctx := t.Context()
	buyer, buyerEmail := newCustomer(ctx, t)
	browser, _ := newCustomer(ctx, t)
	for _, who := range []string{buyer, browser} {
		address, err := customerSvc.CreateAddress(ctx, who, customersvc.AddressInput{
			FirstName: "E2E", LastName: "Segment", Address1: "Laugavegur 1", City: "Reykjavik",
			CountryCode: segmentCountry, PostalCode: "101",
		})
		require.NoError(t, err)
		_, err = customerSvc.SetDefaultShippingAddress(ctx, who, address.ID)
		require.NoError(t, err)
	}

	variantID, _ := newStockedVariant(ctx, t, "E2E Segment Product", nil, 5)
	set, err := pricingSvc.CreatePriceSet(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, productSvc.SetVariantPriceSet(ctx, variantID, set.ID))
	group, err := customerSvc.CreateGroup(ctx, customersvc.GroupInput{Name: "E2E segment of " + buyer})
	require.NoError(t, err)
	list := activeOverrideList(ctx, t, "E2E segment price of "+group.ID)
	_, err = pricingSvc.SetPrices(ctx, set.ID, []pricingsvc.PriceInput{
		{CurrencyCode: taxedCurrency, Amount: 20_000, MinQuantity: 1},
		{CurrencyCode: taxedCurrency, Amount: 15_000, MinQuantity: 1, PriceListID: &list,
			Rules: []pricingsvc.RuleInput{{Attribute: "customer_group_id",
				Operator: pricingmodels.OpEq, Values: []string{group.ID}}}},
	})
	require.NoError(t, err)

	cartID, totals := prepareCart(ctx, t, buyer, variantID, 1)
	require.Len(t, totals.Lines, 1)
	require.Equal(t, int64(20_000), totals.Lines[0].UnitPrice, "no segment yet")
	_, err = orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: cartID, LocationID: stockLocationID, PaymentProviderID: manual.ID,
		PaymentData: paymentBehavior(t, manual.OutcomeAuthorize), Email: buyerEmail,
		ExpectedTotal: totals.Total,
	})
	require.NoError(t, err)

	rule := map[string]any{"conditions": []map[string]any{
		{"attribute": "country_code", "operator": "eq", "value": segmentCountry},
		{"attribute": "order_count", "operator": "gte", "value": 1},
	}}
	preview, err := adminRequestWithBody(http.MethodPost, "/admin/v1/customer-segments/preview", rule)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())
	assert.Equal(t, float64(1), storefrontData(t, preview)["members"], "the buyer alone, before anything is written")

	setRule, err := adminRequestWithBody(http.MethodPut, "/admin/v1/customer-groups/"+group.ID+"/segment", rule)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, setRule.Code, setRule.Body.String())
	assert.NotNil(t, storefrontData(t, setRule)["segment"])

	byHand, err := adminRequestWithBody(http.MethodPost, "/admin/v1/customer-groups/"+group.ID+"/customers",
		map[string]string{"customer_id": browser})
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, byHand.Code, byHand.Body.String())
	assert.Contains(t, byHand.Body.String(), customermodels.CodeSegmentManaged)

	report, err := segments.Pass(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, report.Segments, 1)

	inGroup := func(customerID string) bool {
		groups, err := customerSvc.ListGroupsOf(ctx, customerID)
		require.NoError(t, err)
		for i := range groups {
			if groups[i].ID == group.ID {
				return true
			}
		}
		return false
	}
	assert.True(t, inGroup(buyer), "the buyer ordered and ships to the rule's country")
	assert.False(t, inGroup(browser), "the browser never ordered")

	_, totals = prepareCart(ctx, t, buyer, variantID, 1)
	require.Len(t, totals.Lines, 1)
	assert.Equal(t, int64(15_000), totals.Lines[0].UnitPrice, "the segment's price list prices the buyer")
	_, totals = prepareCart(ctx, t, browser, variantID, 1)
	require.Len(t, totals.Lines, 1)
	assert.Equal(t, int64(20_000), totals.Lines[0].UnitPrice)

	read, err := adminRequestWithBody(http.MethodGet, "/admin/v1/customer-groups/"+group.ID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	assert.NotEmpty(t, storefrontData(t, read)["segment_evaluated_at"], "the pass finished the segment")
}

// TestASegmentRuleOutsideTheVocabularyIsRefused: the admin surface answers 422
// and stores nothing.
func TestASegmentRuleOutsideTheVocabularyIsRefused(t *testing.T) {
	ctx := t.Context()
	group, err := customerSvc.CreateGroup(ctx, customersvc.GroupInput{Name: "E2E refused segment"})
	require.NoError(t, err)

	refused, err := adminRequestWithBody(http.MethodPut, "/admin/v1/customer-groups/"+group.ID+"/segment",
		json.RawMessage(`{"conditions":[{"attribute":"lifetime_value","operator":"gt","value":1}]}`))
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	read, err := customerSvc.GetGroup(ctx, group.ID)
	require.NoError(t, err)
	assert.Nil(t, read.Segment)
}
