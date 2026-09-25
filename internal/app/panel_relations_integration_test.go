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
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestThePanelEditsARealProductsNeighbors is the gate ADR 0181 rests on: the
// panel built from a real installation's container, reading through the real
// read layer and writing through the product module's real admin surface.
//
// The panel's own tests fake both sides, and the module's tests never see the
// panel; what only an assembly can show is that the two meet — that the fields
// the page reads are the ones the provider fills, and that the handles the form
// sends are the ones the surface resolves. The identity ring is not installed:
// the principal is put in place by hand, as the ring would after a sign-in.
func TestThePanelEditsARealProductsNeighbors(t *testing.T) {
	ctx := context.Background()

	dsn := migrateDSN(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("JWT_SECRET", "panel-relations-test-secret-32-bytes-long")
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
	create := func(handle string, status models.Status) models.Product {
		t.Helper()

		created, err := svc.CreateProduct(ctx, productsvc.CreateProductInput{
			Handle: fmt.Sprintf("%s-%d", handle, suffix), Title: handle, Status: status,
		})
		require.NoError(t, err)
		return created
	}
	shirt := create("shirt", models.StatusPublished)
	belt := create("belt", models.StatusPublished)
	socks := create("socks", models.StatusDraft)

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
	relationsPath := adminui.ProductsPath + "/" + shirt.ID + "/relations"

	saved := request(http.MethodPost, relationsPath, url.Values{
		"cross_sell": {socks.Handle + "\n" + belt.Handle},
		"up_sell":    {belt.ID},
		"substitute": {""},
	})
	require.Equal(t, http.StatusSeeOther, saved.Code, saved.Body.String())

	stored, err := svc.ProductRelations(ctx, shirt.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{socks.ID, belt.ID}, stored[models.RelationCrossSell],
		"the handles the form sent are the products the module stored, in order")
	assert.Equal(t, []string{belt.ID}, stored[models.RelationUpSell], "an id is taken as it is")

	page := request(http.MethodGet, adminui.ProductsPath+"/"+shirt.ID, nil)
	require.Equal(t, http.StatusOK, page.Code, page.Body.String())
	body := page.Body.String()
	socksAt, beltAt := strings.Index(body, ">socks<"), strings.Index(body, ">belt<")
	require.Positive(t, socksAt, "the page read the lists the provider filled")
	require.Positive(t, beltAt)
	assert.Less(t, socksAt, beltAt)
	assert.Contains(t, body, "("+socks.Handle+") — draft; the storefront leaves it out")

	form := request(http.MethodGet, relationsPath, nil)
	require.Equal(t, http.StatusOK, form.Code, form.Body.String())
	assert.Contains(t, form.Body.String(), socks.Handle+"\n"+belt.Handle+"</textarea>")

	refused := request(http.MethodPost, relationsPath, url.Values{
		"cross_sell": {belt.Handle},
		"up_sell":    {"no-such-handle"},
	})
	require.Equal(t, http.StatusUnprocessableEntity, refused.Code, refused.Body.String())
	assert.Contains(t, refused.Body.String(), "no such product: no-such-handle")
	after, err := svc.ProductRelations(ctx, shirt.ID)
	require.NoError(t, err)
	assert.Equal(t, stored, after, "a refused save leaves every list as it was")
}
