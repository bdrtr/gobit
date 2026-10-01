//go:build integration

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	"github.com/bdrtr/gobit/internal/modules/product"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestThePanelEditsARealVariantsPriceAtOneUnit is ADR 0206 on a real
// installation: a price set with a quantity tier, read through the real read
// layer and written through pricing's real admin surface.
//
// The panel's own tests build the price sub-records by hand, with the types
// they assume pricing's provider writes. Only an assembly shows that the
// provider writes them so, and that the tier the page leaves out of its form is
// the tier the write leaves alone.
func TestThePanelEditsARealVariantsPriceAtOneUnit(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-prices-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	products, err := container.Resolve[*productsvc.Service](app.container, product.ServiceName)
	require.NoError(t, err)
	prices, err := container.Resolve[*pricingsvc.Service](app.container, pricing.ServiceName)
	require.NoError(t, err)

	created, err := products.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("tiered-%d", time.Now().UnixNano()), Title: "Tiered",
		Variants: []productsvc.CreateVariantInput{{Title: "One"}},
	})
	require.NoError(t, err)
	variantID := created.Variants[0].ID
	nine := int32(9)
	set, err := prices.CreatePriceSet(ctx, []pricingsvc.PriceInput{
		{CurrencyCode: "XTS", Amount: 19990, MinQuantity: 1, MaxQuantity: &nine},
		{CurrencyCode: "XTS", Amount: 17990, MinQuantity: 10},
	})
	require.NoError(t, err)
	require.NoError(t, products.SetVariantPriceSet(ctx, variantID, set.ID))

	panel, err := adminui.FromContainer(app.container, false, nil)
	require.NoError(t, err)
	router := chi.NewRouter()
	panel.Routes(router)
	request := func(method, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()

		req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
			ID: "usr_panel", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
		}))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	variantPath := adminui.ProductsPath + "/" + created.ID + "/variants/" + variantID

	page := request(http.MethodGet, variantPath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	assert.Equal(t, 1, strings.Count(body, `name="currency" value="XTS"`),
		"one form, for the price at one unit; the provider's types were read")
	assert.Contains(t, body, `value="19990"`)
	assert.NotContains(t, body, `value="17990"`, "the tier gets no box")
	assert.Contains(t, body, "10 or more")
	// The form carries the amount it was drawn with, and the save sends it back
	// (ADR 0280).
	assert.Contains(t, body, `name="read_amount" value="19990"`)

	saved := request(http.MethodPost, variantPath+"/price", url.Values{
		"price_set_id": {set.ID}, "currency": {"XTS"}, "minor": {"1"},
		"read_amount": {"19990"}, "amount": {"25000"},
	})
	require.Equal(t, http.StatusSeeOther, saved.Code, saved.Body.String())

	stored, err := prices.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	amounts := map[int32]int64{}
	for _, price := range stored {
		amounts[price.MinQuantity] = price.Amount
	}
	assert.Equal(t, map[int32]int64{1: 25000, 10: 17990}, amounts, "the tier keeps its amount")
}
