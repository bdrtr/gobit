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
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// TestAnOperatorLimitsACouponToACategoryInThePanel is ADR 0315 through every
// part that has to agree: the panel writes a coupon worth half off the items,
// limits it to "Shirts" and publishes it, and a cart carrying its code takes
// half off a product filed only under "Shirts > Linen" and nothing off a shoe.
// The panel names the attribute by hand; were it not the one the cart flow
// fills, the coupon would discount nothing and no error would say so.
func TestAnOperatorLimitsACouponToACategoryInThePanel(t *testing.T) {
	ctx := t.Context()
	seq := fixtureCounter.Add(1)
	shirts, err := productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{
		Name: fmt.Sprintf("E2E Panel Shirts %d", seq), Handle: fmt.Sprintf("e2e-panel-shirts-%d", seq),
	})
	require.NoError(t, err)
	linen, err := productSvc.CreateCategory(ctx, productsvc.CreateCategoryInput{
		Name: "E2E Panel Linen", Handle: fmt.Sprintf("e2e-panel-linen-%d", seq), ParentID: &shirts.ID,
	})
	require.NoError(t, err)
	underChild := newCategorisedVariant(ctx, t, "E2E Panel Linen Shirt", categoryPriceIn, []string{linen.ID})
	elsewhere := newCategorisedVariant(ctx, t, "E2E Panel Shoe", categoryPriceOut, nil)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_marketing", Kind: "user", Scopes: []string{"promotion:read", "promotion:write", "product:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	code := fmt.Sprintf("E2ESHIRTS%d", seq)
	written := send(http.MethodPost, adminui.PromotionsPath, url.Values{
		"code": {code}, "measure": {"percentage"}, "amount": {"50"}, "target": {"items"}, "allocation": {"each"},
	})
	require.Equal(t, http.StatusSeeOther, written.Code, written.Body.String())
	pagePath := written.Header().Get("Location")
	promotionID := strings.TrimPrefix(pagePath, adminui.PromotionsPath+"/")

	page := send(http.MethodGet, pagePath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), `<option value="`+shirts.ID+`">`, "the category form offers the shop's categories")

	limited := send(http.MethodPost, pagePath+"/rules", url.Values{"category": {shirts.ID}})
	require.Equal(t, http.StatusSeeOther, limited.Code, limited.Body.String())
	page = send(http.MethodGet, pagePath, nil)
	assert.Contains(t, page.Body.String(), "<td>"+shirts.Name+"</td>", "the rule names its category")

	published := send(http.MethodPost, adminui.PromotionsPath+"/"+promotionID+"/status",
		url.Values{"from": {"draft"}, "to": {"active"}})
	require.Equal(t, http.StatusSeeOther, published.Code, published.Body.String())

	cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry})
	require.NoError(t, err)
	lineChild, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: underChild, Quantity: 1,
	})
	require.NoError(t, err)
	lineElsewhere, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID: cart.CartID, VariantID: elsewhere, Quantity: 1,
	})
	require.NoError(t, err)
	require.NoError(t, workflows.ApplyPromotionCode(ctx, cart.CartID, code))
	totals, err := workflows.CalculateTotals(ctx, cart.CartID)
	require.NoError(t, err)

	lines := lineTotalsByID(t, totals)
	assert.Equal(t, categoryDiscountIn, lines[lineChild.LineItemID].DiscountTotal,
		"half off the shirt filed under a child of the chosen category")
	assert.Zero(t, lines[lineElsewhere.LineItemID].DiscountTotal, "nothing off the shoe")
}
