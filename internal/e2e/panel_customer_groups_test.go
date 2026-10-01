//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// TestAnOperatorLimitsACouponToACustomerGroupInThePanel is ADR 0321 on the
// production wiring: an operator writes a coupon, finds the shop's customer
// groups offered on its page through the customer module's group entity,
// limits the coupon to one and publishes it; the coupon then takes a tenth
// off the cart of a customer in that group and nothing off another's. The
// panel names the attribute by hand; were it not the one the cart flow fills,
// the coupon would apply to nobody and no error would say so.
func TestAnOperatorLimitsACouponToACustomerGroupInThePanel(t *testing.T) {
	ctx := t.Context()
	seq := fixtureCounter.Add(1)
	group, err := customerSvc.CreateGroup(ctx, customersvc.GroupInput{Name: fmt.Sprintf("E2E Panel VIP %d", seq)})
	require.NoError(t, err)
	member, _ := newCustomer(ctx, t)
	stranger, _ := newCustomer(ctx, t)
	require.NoError(t, customerSvc.AddToGroup(ctx, member, group.ID))
	variant := newCategorisedVariant(ctx, t, "E2E Panel Group Item", categoryPriceOut, nil)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_marketing", Kind: "user", Scopes: []string{"promotion:read", "promotion:write", "customer:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	code := fmt.Sprintf("E2EVIP%d", seq)
	written := send(http.MethodPost, adminui.PromotionsPath, url.Values{
		"code": {code}, "measure": {"percentage"}, "amount": {"10"}, "target": {"order"}, "allocation": {"across"},
	})
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	pagePath := written.Header().Get("Location")
	promotionID := strings.TrimPrefix(pagePath, adminui.PromotionsPath+"/")

	page := send(http.MethodGet, pagePath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `<option value="`+group.ID+`">`+group.Name+`</option>`,
		"the group form offers the shop's groups")

	limited := send(http.MethodPost, pagePath+"/rules/customer-groups", url.Values{"customer_group": {group.ID}})
	require.Equal(t, http.StatusSeeOther, limited.Code, limited.Body.String())
	page = send(http.MethodGet, pagePath, nil)
	assert.Contains(t, page.Body.String(), "<td>"+group.Name+"</td>", "the rule names its group")

	published := send(http.MethodPost, adminui.PromotionsPath+"/"+promotionID+"/status",
		url.Values{"from": {"draft"}, "to": {"active"}})
	require.Equal(t, http.StatusSeeOther, published.Code, published.Body.String())

	discountFor := func(customerID string) int64 {
		t.Helper()
		cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry, CustomerID: customerID})
		require.NoError(t, err)
		_, err = workflows.AddLineItem(ctx, cartwf.AddLineItemInput{CartID: cart.CartID, VariantID: variant, Quantity: 1})
		require.NoError(t, err)
		require.NoError(t, workflows.ApplyPromotionCode(ctx, cart.CartID, code))
		totals, err := workflows.CalculateTotals(ctx, cart.CartID)
		require.NoError(t, err)
		return totals.DiscountTotal
	}
	assert.Equal(t, categoryPriceOut/10, discountFor(member), "a tenth off the cart of a customer in the group")
	assert.Zero(t, discountFor(stranger), "nothing off the cart of a customer in no group")
}
