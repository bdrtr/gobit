package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// preconditionCatalog records the version each revising write was asked on.
type preconditionCatalog struct {
	api.Catalog

	asked map[string]int64
	calls int
}

func (c *preconditionCatalog) saw(ctx context.Context, name string) {
	c.calls++
	if version, ok := service.ExpectedVersion(ctx); ok {
		c.asked[name] = version
	}
}

func (c *preconditionCatalog) UpdateProduct(ctx context.Context, id string, _ service.UpdateProductInput) (models.Product, error) {
	c.saw(ctx, "UpdateProduct")
	return models.Product{ID: id}, nil
}

func (c *preconditionCatalog) RestoreRevision(ctx context.Context, id string, _ int64) (service.RestoreResult, error) {
	c.saw(ctx, "RestoreRevision")
	return service.RestoreResult{Product: models.Product{ID: id}}, nil
}

func (c *preconditionCatalog) CreateVariant(ctx context.Context, _ string, _ service.CreateVariantInput) (models.Variant, error) {
	c.saw(ctx, "CreateVariant")
	return models.Variant{}, nil
}

func (c *preconditionCatalog) UpdateVariant(ctx context.Context, _ string, _ service.UpdateVariantInput) (models.Variant, error) {
	c.saw(ctx, "UpdateVariant")
	return models.Variant{}, nil
}

func (c *preconditionCatalog) DeleteVariant(ctx context.Context, _ string) error {
	c.saw(ctx, "DeleteVariant")
	return nil
}

func (c *preconditionCatalog) CreateOption(ctx context.Context, _ string, _ service.CreateOptionInput) (models.Option, error) {
	c.saw(ctx, "CreateOption")
	return models.Option{}, nil
}

func (c *preconditionCatalog) AddOptionValue(ctx context.Context, _, _ string) (models.OptionValue, error) {
	c.saw(ctx, "AddOptionValue")
	return models.OptionValue{}, nil
}

func (c *preconditionCatalog) DeleteOption(ctx context.Context, _ string) error {
	c.saw(ctx, "DeleteOption")
	return nil
}

func (c *preconditionCatalog) DeleteOptionValue(ctx context.Context, _ string) error {
	c.saw(ctx, "DeleteOptionValue")
	return nil
}

func (c *preconditionCatalog) AddProductImage(ctx context.Context, _ string, _ service.CreateImageInput) (models.Image, error) {
	c.saw(ctx, "AddProductImage")
	return models.Image{}, nil
}

func (c *preconditionCatalog) UpdateProductImage(
	ctx context.Context, _, _ string, _ service.UpdateImageInput,
) (models.Image, error) {
	c.saw(ctx, "UpdateProductImage")
	return models.Image{}, nil
}

func (c *preconditionCatalog) RemoveProductImage(ctx context.Context, _, _ string) error {
	c.saw(ctx, "RemoveProductImage")
	return nil
}

func (c *preconditionCatalog) SetProductAttributes(
	ctx context.Context, _ string, _ []service.ProductAttributeInput,
) ([]models.ProductAttributeValue, error) {
	c.saw(ctx, "SetProductAttributes")
	return nil, nil
}

func (c *preconditionCatalog) SetSchedule(ctx context.Context, id string, _ service.Schedule) (models.Product, error) {
	c.saw(ctx, "SetSchedule")
	return models.Product{ID: id}, nil
}

// revisingRoutes are the writes that revise a product, each with the method
// it reaches and a body it takes.
var revisingRoutes = []struct {
	method, path, body, reaches string
}{
	{http.MethodPatch, "/admin/v1/products/prod_1", `{"title":"x"}`, "UpdateProduct"},
	{http.MethodPost, "/admin/v1/products/prod_1/revisions/2/restore", "", "RestoreRevision"},
	{http.MethodPost, "/admin/v1/products/prod_1/variants", `{"title":"x"}`, "CreateVariant"},
	{http.MethodPatch, "/admin/v1/variants/variant_1", `{"title":"x"}`, "UpdateVariant"},
	{http.MethodDelete, "/admin/v1/variants/variant_1", "", "DeleteVariant"},
	{http.MethodPost, "/admin/v1/products/prod_1/options", `{"title":"Size","values":["S"]}`, "CreateOption"},
	{http.MethodPost, "/admin/v1/product-options/popt_1/values", `{"value":"M"}`, "AddOptionValue"},
	{http.MethodDelete, "/admin/v1/product-options/popt_1", "", "DeleteOption"},
	{http.MethodDelete, "/admin/v1/product-option-values/poptval_1", "", "DeleteOptionValue"},
	{http.MethodPost, "/admin/v1/products/prod_1/images", `{"url":"https://cdn.example.com/a.jpg"}`, "AddProductImage"},
	{http.MethodPatch, "/admin/v1/products/prod_1/images/pimg_1", `{"rank":1}`, "UpdateProductImage"},
	{http.MethodDelete, "/admin/v1/products/prod_1/images/pimg_1", "", "RemoveProductImage"},
	{http.MethodPut, "/admin/v1/products/prod_1/attributes", `{"values":[]}`, "SetProductAttributes"},
}

// TestEveryRevisingWriteIsAskedOnTheVersionItNames is ADR 0222: If-Match
// reaches the service on each write that revises a product, and `*` or no
// header asks nothing.
func TestEveryRevisingWriteIsAskedOnTheVersionItNames(t *testing.T) {
	for _, route := range revisingRoutes {
		t.Run(route.reaches, func(t *testing.T) {
			catalog := &preconditionCatalog{asked: map[string]int64{}}
			r := newRouter(catalog)

			rec := doWithHeader(t, r, route.method, route.path, route.body, map[string]string{"If-Match": `"7"`})
			require.Less(t, rec.Code, 300, rec.Body.String())
			assert.Equal(t, int64(7), catalog.asked[route.reaches])

			delete(catalog.asked, route.reaches)
			doWithHeader(t, r, route.method, route.path, route.body, map[string]string{"If-Match": "*"})
			doWithHeader(t, r, route.method, route.path, route.body, nil)
			assert.NotContains(t, catalog.asked, route.reaches, "* and no header ask nothing")
		})
	}
}

// TestAnIfMatchOutOfShapeIsRefused: a weak tag, a bare number, a list and a
// word are refused before the service, and a write that does not revise a
// product carries no precondition.
func TestAnIfMatchOutOfShapeIsRefused(t *testing.T) {
	catalog := &preconditionCatalog{asked: map[string]int64{}}
	r := newRouter(catalog)

	for _, header := range []string{`W/"7"`, `7`, `"6", "7"`, `"seven"`, `"-1"`} {
		rec := doWithHeader(t, r, http.MethodPatch, "/admin/v1/products/prod_1", `{"title":"x"}`,
			map[string]string{"If-Match": header})
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, header)
		assert.Equal(t, "product_if_match_invalid", errorCode(t, rec), header)
	}
	assert.Zero(t, catalog.calls, "a refused header reaches no write")

	rec := doWithHeader(t, r, http.MethodPut, "/admin/v1/products/prod_1/schedule", `{"publish_at":"2099-01-01T00:00:00Z"}`,
		map[string]string{"If-Match": `"7"`})
	require.Less(t, rec.Code, 300, rec.Body.String())
	assert.NotContains(t, catalog.asked, "SetSchedule", "a schedule is not a revision (ADR 0221)")
}

// doWithHeader is [do] with request headers.
func doWithHeader(
	t *testing.T, r chi.Router, method, target, body string, headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID: "usr_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin},
	}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}
