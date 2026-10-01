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
)

// TestAnOperatorCreatesAProductInThePanel is ADR 0307 on the production wiring:
// the panel built from this harness's container creates a draft product
// through the product module's surface, adds a variant to it, and lands on
// the variant's page; the product module holds a draft with the handle
// derived from the title and the variant with its SKU.
func TestAnOperatorCreatesAProductInThePanel(t *testing.T) {
	ctx := t.Context()
	title := fmt.Sprintf("E2E Panel Product %d", fixtureCounter.Add(1))

	panel, err := adminui.FromContainer(ctr, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	send := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_catalog", Kind: "user", Scopes: []string{
				"product:read", "product:write", "pricing:read", "pricing:write",
				"inventory:read", "inventory:write",
			},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	created := send(http.MethodPost, adminui.ProductNewPath, url.Values{"title": {title}})
	require.Equal(t, http.StatusSeeOther, created.Code, created.Body.String())
	productPath := created.Header().Get("Location")
	productID := strings.TrimPrefix(productPath, adminui.ProductsPath+"/")

	product, err := productSvc.GetProduct(ctx, productID)
	require.NoError(t, err)
	assert.Equal(t, "draft", string(product.Status))
	assert.Equal(t, strings.ToLower(strings.ReplaceAll(title, " ", "-")), product.Handle)

	sku := fmt.Sprintf("E2E-PANEL-%d", fixtureCounter.Add(1))
	added := send(http.MethodPost, productPath+"/variants", url.Values{
		"variant_title": {"Medium"}, "variant_sku": {sku},
	})
	require.Equal(t, http.StatusSeeOther, added.Code, added.Body.String())
	variantPath := added.Header().Get("Location")
	require.True(t, strings.HasPrefix(variantPath, productPath+"/variants/"), variantPath)

	variant, err := productSvc.GetVariant(ctx, strings.TrimPrefix(variantPath, productPath+"/variants/"))
	require.NoError(t, err)
	assert.Equal(t, productID, variant.ProductID)
	require.NotNil(t, variant.SKU)
	assert.Equal(t, sku, *variant.SKU)

	page := send(http.MethodGet, productPath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "Medium", "the product page lists its new variant")

	// The new variant takes its first price: its price set is created and
	// linked by the product module, as an import does (ADR 0309).
	priced := send(http.MethodPost, variantPath+"/prices", url.Values{
		"currency": {taxedCurrency}, "amount": {"250.00"},
	})
	require.Equal(t, http.StatusSeeOther, priced.Code, priced.Body.String())
	variantPage := send(http.MethodGet, variantPath, nil)
	require.Equal(t, http.StatusOK, variantPage.Code, variantPage.Body.String())
	assert.Contains(t, variantPage.Body.String(), `name="amount" value="250.00"`,
		"the variant page reads the price through the real link and price set")

	// And keeps its stock: inventory makes the item, the product module links
	// it, and the existing stock form sets a location's count (ADR 0310).
	kept := send(http.MethodPost, variantPath+"/stock-item", nil)
	require.Equal(t, http.StatusSeeOther, kept.Code, kept.Body.String())
	variantPage = send(http.MethodGet, variantPath, nil)
	require.Equal(t, http.StatusOK, variantPage.Code, variantPage.Body.String())
	item := regexp.MustCompile(`name="inventory_item_id" value="([^"]+)"`).FindStringSubmatch(variantPage.Body.String())
	require.Len(t, item, 2, "the stock form names the variant's new item")
	location := regexp.MustCompile(`name="location_id" value="([^"]+)"`).FindStringSubmatch(variantPage.Body.String())
	require.Len(t, location, 2, "an open location is offered")
	counted := send(http.MethodPost, variantPath+"/stock", url.Values{
		"inventory_item_id": {item[1]}, "location_id": {location[1]}, "read_quantity": {"0"}, "quantity": {"7"},
	})
	require.Equal(t, http.StatusSeeOther, counted.Code, counted.Body.String())
	assert.Equal(t, int64(7), sellableQuantity(ctx, t, item[1]), "the new variant holds the stock counted")
}
