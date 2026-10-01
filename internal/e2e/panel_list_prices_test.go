//go:build integration

package e2e

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
)

// TestAnOperatorPricesAVariantForAGroupInThePanel is ADR 0327 on the
// production wiring: on a variant's page the operator puts a price on an
// override list for one customer group, through the pricing module's
// registered surface; the page lists it with the group's name; a cart of a
// customer in that group is priced at it and another customer's at the base
// price; and removing it from the page brings the group back to the base.
func TestAnOperatorPricesAVariantForAGroupInThePanel(t *testing.T) {
	ctx := t.Context()
	seq := fixtureCounter.Add(1)
	group, err := customerSvc.CreateGroup(ctx, customersvc.GroupInput{Name: fmt.Sprintf("E2E Panel Trade %d", seq)})
	require.NoError(t, err)
	member, _ := newCustomer(ctx, t)
	stranger, _ := newCustomer(ctx, t)
	require.NoError(t, customerSvc.AddToGroup(ctx, member, group.ID))
	variantID := newCategorisedVariant(ctx, t, "E2E Panel Trade Item", categoryPriceOut, nil)
	variant, err := productSvc.GetVariant(ctx, variantID)
	require.NoError(t, err)
	list, err := pricingSvc.CreatePriceList(ctx, pricingsvc.PriceListInput{
		Title: fmt.Sprintf("E2E Trade %d", seq), Type: "override", Status: "active",
	})
	require.NoError(t, err)

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_pricing", Kind: "user",
			Scopes: []string{"product:read", "pricing:read", "pricing:write", "customer:read"},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	pagePath := adminui.ProductsPath + "/" + variant.ProductID + "/variants/" + variantID
	page := send(http.MethodGet, pagePath, nil).Body.String()
	setID := regexp.MustCompile(`name="price_set_id" value="(pset_[0-9A-Z]+)"`).FindStringSubmatch(page)
	require.Len(t, setID, 2, "the page names the variant's price set")
	assert.Contains(t, page, `<option value="`+list.ID+`">`, "the list is offered")
	assert.Contains(t, page, `<option value="`+group.ID+`">`+group.Name+`</option>`, "the group is offered")

	half := categoryPriceOut / 2
	added := send(http.MethodPost, pagePath+"/list-prices", url.Values{
		"price_set_id": {setID[1]}, "price_list_id": {list.ID}, "currency": {taxedCurrency},
		"amount": {fmt.Sprintf("%d.%02d", half/100, half%100)}, "customer_group": {group.ID},
	})
	require.Equal(t, http.StatusSeeOther, added.Code, added.Body.String())
	page = send(http.MethodGet, pagePath, nil).Body.String()
	assert.Contains(t, page, "1 or more, for "+group.Name, "the page lists the price with the group's name")

	unitPrice := func(customerID string) int64 {
		t.Helper()
		cart, err := workflows.CreateCart(ctx, cartwf.CreateCartInput{CountryCode: taxedCountry, CustomerID: customerID})
		require.NoError(t, err)
		line, err := workflows.AddLineItem(ctx, cartwf.AddLineItemInput{CartID: cart.CartID, VariantID: variantID, Quantity: 1})
		require.NoError(t, err)
		totals, err := workflows.CalculateTotals(ctx, cart.CartID)
		require.NoError(t, err)
		return lineTotalsByID(t, totals)[line.LineItemID].UnitPrice
	}
	assert.Equal(t, half, unitPrice(member), "the group's customer is priced on the list")
	assert.Equal(t, categoryPriceOut, unitPrice(stranger), "another customer pays the base price")

	remove := regexp.MustCompile(`action="(` + regexp.QuoteMeta(pagePath) + `/list-prices/price_[0-9A-Z]+/remove)"`).FindStringSubmatch(page)
	require.Len(t, remove, 2)
	removed := send(http.MethodPost, remove[1], url.Values{"price_set_id": {setID[1]}})
	require.Equal(t, http.StatusSeeOther, removed.Code, removed.Body.String())
	assert.Equal(t, categoryPriceOut, unitPrice(member), "without the list price the group pays the base price")
}
