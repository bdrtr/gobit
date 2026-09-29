//go:build integration

package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
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
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestThePanelEditsARealVariantsBundle is ADR 0236 on a real installation: the
// form reads the product's version and the parts the provider publishes, a
// save sends a SKU and an id with their units through the product module's
// real admin surface, the variant page lists what was stored, and a save on a
// version somebody else has moved comes back unsaved.
func TestThePanelEditsARealVariantsBundle(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-bundle-test-secret-32-bytes-long!")
	t.Setenv("LOG_LEVEL", "warn")
	cfg, err := config.Load()
	require.NoError(t, err)

	app, closeApp, err := openApplication(ctx, cfg, slog.New(slog.DiscardHandler), errorreport.NewSink(),
		Options{}, publishesOnly)
	require.NoError(t, err)
	defer closeApp()

	svc, err := container.Resolve[*productsvc.Service](app.container, product.ServiceName)
	require.NoError(t, err)
	suffix := time.Now().UnixNano()
	create := func(handle, sku string) models.Product {
		t.Helper()

		variant := productsvc.CreateVariantInput{Title: handle}
		if sku != "" {
			variant.SKU = &sku
		}
		created, err := svc.CreateProduct(ctx, productsvc.CreateProductInput{
			Handle: fmt.Sprintf("%s-%d", handle, suffix), Title: handle, Status: models.StatusPublished,
			Variants: []productsvc.CreateVariantInput{variant},
		})
		require.NoError(t, err)
		return created
	}
	box := create("box", "")
	towelSKU := fmt.Sprintf("TOWEL-%d", suffix)
	towel := create("towel", towelSKU)
	soap := create("soap", "")

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
	variantPath := adminui.ProductsPath + "/" + box.ID + "/variants/" + box.Variants[0].ID
	current := func() string {
		t.Helper()

		read, err := svc.GetProduct(ctx, box.ID)
		require.NoError(t, err)
		return strconv.FormatInt(read.Version, 10)
	}

	form := request(http.MethodGet, variantPath+"/bundle", nil)
	require.Equal(t, http.StatusOK, form.Code, form.Body.String())
	assert.Contains(t, form.Body.String(), `name="version" value="`+current()+`"`)

	saved := request(http.MethodPost, variantPath+"/bundle", url.Values{
		"parts":   {towelSKU + "\n" + soap.Variants[0].ID + " 2"},
		"version": {current()},
	})
	require.Equal(t, http.StatusSeeOther, saved.Code, saved.Body.String())
	stored, err := svc.VariantBundle(ctx, box.Variants[0].ID)
	require.NoError(t, err)
	assert.Equal(t, []models.BundleComponent{
		{VariantID: towel.Variants[0].ID, Quantity: 1}, {VariantID: soap.Variants[0].ID, Quantity: 2},
	}, stored, "the SKU and the id the form sent are the parts the module stored, with their units")

	page := request(http.MethodGet, variantPath, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	assert.Contains(t, page.Body.String(), "2 × <a href=\""+adminui.ProductsPath+"/"+soap.ID+"/variants/"+soap.Variants[0].ID+"\">",
		"the page read the parts the provider published")
	again := request(http.MethodGet, variantPath+"/bundle", nil)
	assert.Contains(t, again.Body.String(), towelSKU+" 1\n"+soap.Variants[0].ID+" 2</textarea>")

	stale := request(http.MethodPost, variantPath+"/bundle", url.Values{
		"parts": {towelSKU + " 3"}, "version": {"1"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, stale.Code, stale.Body.String())
	assert.Contains(t, stale.Body.String(), "Somebody saved this product after you opened it")
	after, err := svc.VariantBundle(ctx, box.Variants[0].ID)
	require.NoError(t, err)
	assert.Equal(t, stored, after, "a save on a stale version writes nothing")
}
