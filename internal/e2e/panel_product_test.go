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
			ID: "usr_catalog", Kind: "user", Scopes: []string{"product:read", "product:write"},
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
}
