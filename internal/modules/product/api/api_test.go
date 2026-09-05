package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// fakeCatalog is the fake of the surface the api layer expects from the service.
//
// The embedded interface is deliberate: if a method that is NOT USED in the test
// gets called, the nil interface panics and it becomes visible the moment the
// handler makes an unexpected call. Only the methods under test are overridden.
type fakeCatalog struct {
	api.Catalog

	createProduct     func(ctx context.Context, in service.CreateProductInput) (models.Product, error)
	getProduct        func(ctx context.Context, id string) (models.Product, error)
	listProducts      func(ctx context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error)
	deleteProduct     func(ctx context.Context, id string) error
	createVariant     func(ctx context.Context, productID string, in service.CreateVariantInput) (models.Variant, error)
	setPriceSet       func(ctx context.Context, variantID, priceSetID string) error
	variantLinks      func(ctx context.Context, variantID string) (service.VariantLinks, error)
	listStoreProducts func(ctx context.Context, opts service.StoreListOptions) (service.ListResult[service.StoreProduct], error)
	getStoreProduct   func(ctx context.Context, idOrHandle string, salesChannelIDs []string) (service.StoreProduct, error)

	addSalesChannel    func(ctx context.Context, productID, salesChannelID string) error
	removeSalesChannel func(ctx context.Context, productID, salesChannelID string) error
	salesChannelIDs    func(ctx context.Context, productID string) ([]string, error)
}

func (f *fakeCatalog) CreateProduct(ctx context.Context, in service.CreateProductInput) (models.Product, error) {
	return f.createProduct(ctx, in)
}

func (f *fakeCatalog) GetProduct(ctx context.Context, id string) (models.Product, error) {
	return f.getProduct(ctx, id)
}

func (f *fakeCatalog) ListProducts(
	ctx context.Context,
	opts service.ListProductsOptions,
) (service.ListResult[models.Product], error) {
	return f.listProducts(ctx, opts)
}

func (f *fakeCatalog) DeleteProduct(ctx context.Context, id string) error {
	return f.deleteProduct(ctx, id)
}

func (f *fakeCatalog) CreateVariant(
	ctx context.Context,
	productID string,
	in service.CreateVariantInput,
) (models.Variant, error) {
	return f.createVariant(ctx, productID, in)
}

func (f *fakeCatalog) SetVariantPriceSet(ctx context.Context, variantID, priceSetID string) error {
	return f.setPriceSet(ctx, variantID, priceSetID)
}

func (f *fakeCatalog) VariantLinkIDs(ctx context.Context, variantID string) (service.VariantLinks, error) {
	return f.variantLinks(ctx, variantID)
}

func (f *fakeCatalog) ListStoreProducts(
	ctx context.Context,
	opts service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	return f.listStoreProducts(ctx, opts)
}

func (f *fakeCatalog) GetStoreProduct(
	ctx context.Context,
	idOrHandle string,
	salesChannelIDs []string,
) (service.StoreProduct, error) {
	return f.getStoreProduct(ctx, idOrHandle, salesChannelIDs)
}

func (f *fakeCatalog) AddProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	return f.addSalesChannel(ctx, productID, salesChannelID)
}

func (f *fakeCatalog) RemoveProductSalesChannel(ctx context.Context, productID, salesChannelID string) error {
	return f.removeSalesChannel(ctx, productID, salesChannelID)
}

func (f *fakeCatalog) ProductSalesChannelIDs(ctx context.Context, productID string) ([]string, error) {
	return f.salesChannelIDs(ctx, productID)
}

// newRouter builds a router wired to the fake service.
func newRouter(catalog api.Catalog) chi.Router {
	r := chi.NewRouter()
	api.New(catalog, graph.Options{}).Routes(r)
	return r
}

// do applies the request to the router and returns the response.
//
// The request carries a FULLY PRIVILEGED identity. In production the identity is
// put into the context by corehttp.RequireAdmin; these tests build the router
// directly, so that middleware is not in play and the identity is set by hand.
// The reason is that corehttp.RequireScope was added to the admin endpoints: a
// request with no identity now gets a 401 without ever reaching the handler and
// the tests here would end up exercising the scope layer instead of the
// envelope/error mapping. The scope ITSELF is exercised in a separate file
// (yetki_test.go); the assertions of this file did not change.
func do(t *testing.T, r chi.Router, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeBody decodes the response body into a map.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

// TestListEnvelopeShape verifies that the list envelope carries the shape from
// plan Section 8.
func TestListEnvelopeShape(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(_ context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error) {
			return service.ListResult[models.Product]{
				Items:  []models.Product{{ID: "prod_1", Handle: "tisort", Title: "T-shirt"}},
				Count:  ptr(42),
				Offset: opts.Offset,
				Limit:  opts.Limit,
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products?limit=5&offset=10", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	body := decodeBody(t, rec)
	assert.Equal(t, float64(42), body["count"], "count is the total number of records")
	assert.Equal(t, float64(10), body["offset"])
	assert.Equal(t, float64(5), body["limit"])

	data, ok := body["data"].([]any)
	require.True(t, ok, "data has to be an array: %#v", body["data"])
	require.Len(t, data, 1)
	item, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", item["id"])
	assert.Equal(t, "tisort", item["handle"])
}

// TestEmptyListReturnsArrayNotNull verifies that an empty list comes back in
// JSON as an empty array, not as null.
func TestEmptyListReturnsArrayNotNull(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(context.Context, service.ListProductsOptions) (service.ListResult[models.Product], error) {
			return service.ListResult[models.Product]{}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`,
		"an empty list has to be an empty array, not null: %s", rec.Body.String())
}

// TestCreateProductReturns201AndItemEnvelope verifies the envelope and the
// status code of the creation response.
func TestCreateProductReturns201AndItemEnvelope(t *testing.T) {
	t.Parallel()

	var got service.CreateProductInput
	catalog := &fakeCatalog{
		createProduct: func(_ context.Context, in service.CreateProductInput) (models.Product, error) {
			got = in
			return models.Product{ID: "prod_1", Handle: "tisort", Title: in.Title}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products",
		`{"title":"T-shirt","status":"published","options":[{"title":"Size","values":["S"]}],
		  "variants":[{"title":"S","options":{"Size":"S"},"manage_inventory":false}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	body := decodeBody(t, rec)
	data, ok := body["data"].(map[string]any)
	require.True(t, ok, "a single response has to carry a data object: %#v", body)
	assert.Equal(t, "prod_1", data["id"])
	assert.NotContains(t, body, "count", "a single response has no pagination fields")

	assert.Equal(t, "T-shirt", got.Title, "the body has to be converted into the service input")
	assert.Equal(t, models.StatusPublished, got.Status)
	require.Len(t, got.Options, 1)
	assert.Equal(t, []string{"S"}, got.Options[0].Values)
	require.Len(t, got.Variants, 1)
	require.NotNil(t, got.Variants[0].ManageInventory)
	assert.False(t, *got.Variants[0].ManageInventory,
		"thanks to the pointer, a false value has to be told apart from 'not given'")
}

// TestErrorKindsMapToStatus verifies that service errors are mapped to the
// right HTTP status code.
//
// The handler DOES NOT PICK the status code; the code comes from the class of
// the error (plan Section 8).
func TestErrorKindsMapToStatus(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"not found":   {coreerrors.NotFound("product_not_found", "no such product"), http.StatusNotFound, "product_not_found"},
		"conflict":    {coreerrors.Conflict("product_handle_taken", "taken"), http.StatusConflict, "product_handle_taken"},
		"validation":  {coreerrors.Invalid("product_invalid_input", "title is required"), http.StatusUnprocessableEntity, "product_invalid_input"},
		"unavailable": {coreerrors.Unavailable("product_link_failed", "no link"), http.StatusServiceUnavailable, "product_link_failed"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			catalog := &fakeCatalog{
				getProduct: func(context.Context, string) (models.Product, error) {
					return models.Product{}, tc.err
				},
			}

			rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products/prod_1", "")
			require.Equal(t, tc.status, rec.Code)

			body := decodeBody(t, rec)
			errBody, ok := body["error"].(map[string]any)
			require.True(t, ok, "an error envelope was expected: %#v", body)
			assert.Equal(t, tc.code, errBody["code"])
		})
	}
}

// TestInternalErrorIsMasked verifies that the detail of a server error does not
// leak to the client.
func TestInternalErrorIsMasked(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		getProduct: func(context.Context, string) (models.Product, error) {
			return models.Product{}, coreerrors.Internal("db_failed", "dsn=postgres://secret@host/db")
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products/prod_1", "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "secret", "the internal error text must not leak: %s", rec.Body.String())
}

// TestRejectsUnknownJSONField verifies that an unknown field in the body is not
// silently ignored.
func TestRejectsUnknownJSONField(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		createProduct: func(context.Context, service.CreateProductInput) (models.Product, error) {
			t.Fatal("a malformed body should never have reached the service")
			return models.Product{}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products", `{"titel":"T-shirt"}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	errBody, ok := decodeBody(t, rec)["error"].(map[string]any)
	require.True(t, ok, "an error envelope was expected")
	assert.Equal(t, "product_bad_json", errBody["code"])
}

// TestRejectsEmptyAndDoubleBody verifies that an empty body and more than one
// JSON object are rejected.
func TestRejectsEmptyAndDoubleBody(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		createProduct: func(context.Context, service.CreateProductInput) (models.Product, error) {
			t.Fatal("a malformed body should never have reached the service")
			return models.Product{}, nil
		},
	}
	r := newRouter(catalog)

	rec := do(t, r, http.MethodPost, "/admin/v1/products", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "an empty body has to be rejected")

	rec = do(t, r, http.MethodPost, "/admin/v1/products", `{"title":"One"}{"title":"Two"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "the second body must not be silently swallowed")
}

// TestRejectsBadQueryParams shows that the query parameters are validated.
func TestRejectsBadQueryParams(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(context.Context, service.ListProductsOptions) (service.ListResult[models.Product], error) {
			t.Fatal("a malformed parameter should never have reached the service")
			return service.ListResult[models.Product]{}, nil
		},
	}
	r := newRouter(catalog)

	rec := do(t, r, http.MethodGet, "/admin/v1/products?limit=many", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/products?expand=maybe", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestListProductsParsesFilters verifies that the query parameters are
// converted into the service options.
func TestListProductsParsesFilters(t *testing.T) {
	t.Parallel()

	var got service.ListProductsOptions
	catalog := &fakeCatalog{
		listProducts: func(_ context.Context, opts service.ListProductsOptions) (service.ListResult[models.Product], error) {
			got = opts
			return service.ListResult[models.Product]{Items: []models.Product{}}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet,
		"/admin/v1/products?status=published&collection_id=pcol_1&q=tis&expand=true&limit=7&offset=3", "")
	require.Equal(t, http.StatusOK, rec.Code)

	require.NotNil(t, got.Status)
	assert.Equal(t, models.StatusPublished, *got.Status)
	require.NotNil(t, got.CollectionID)
	assert.Equal(t, "pcol_1", *got.CollectionID)
	require.NotNil(t, got.Search)
	assert.Equal(t, "tis", *got.Search)
	assert.True(t, got.WithRelations)
	assert.Equal(t, 7, got.Limit)
	assert.Equal(t, 3, got.Offset)
	// A filter that was not given has to stay nil; an empty-string filter would
	// match nothing at all.
	assert.Nil(t, got.Handle)
}

// TestCreateVariantRouteCarriesProductID verifies that the path parameter is
// passed through to the service.
func TestCreateVariantRouteCarriesProductID(t *testing.T) {
	t.Parallel()

	var gotProductID string
	catalog := &fakeCatalog{
		createVariant: func(_ context.Context, productID string, in service.CreateVariantInput) (models.Variant, error) {
			gotProductID = productID
			return models.Variant{ID: "variant_1", ProductID: productID, Title: in.Title}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products/prod_9/variants",
		`{"title":"size S","option_value_ids":["poptval_1"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "prod_9", gotProductID)
}

// TestSetPriceSetRouteLinksVariant verifies that the price set linking endpoint
// passes the id from the body to the service and returns the current links.
//
// This endpoint is the counterpart of Phase 4's "the link is established in the
// admin flow" requirement.
func TestSetPriceSetRouteLinksVariant(t *testing.T) {
	t.Parallel()

	var gotVariantID, gotPriceSetID string
	catalog := &fakeCatalog{
		setPriceSet: func(_ context.Context, variantID, priceSetID string) error {
			gotVariantID, gotPriceSetID = variantID, priceSetID
			return nil
		},
		variantLinks: func(context.Context, string) (service.VariantLinks, error) {
			id := "pset_1"
			return service.VariantLinks{PriceSetID: &id}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPut, "/admin/v1/variants/variant_1/price-set",
		`{"price_set_id":"pset_1"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "variant_1", gotVariantID)
	assert.Equal(t, "pset_1", gotPriceSetID)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "pset_1", data["price_set_id"])
}

// TestDeleteProductReturnsDeletionEnvelope verifies that the deletion response
// reports the deleted record.
func TestDeleteProductReturnsDeletionEnvelope(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{deleteProduct: func(context.Context, string) error { return nil }}

	rec := do(t, newRouter(catalog), http.MethodDelete, "/admin/v1/products/prod_1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", data["id"])
	assert.Equal(t, "product", data["object"])
	assert.Equal(t, true, data["deleted"])
}

// TestStoreListIncludesPriceAndInventory verifies that the storefront response
// carries the price and stock fields.
func TestStoreListIncludesPriceAndInventory(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listStoreProducts: func(_ context.Context, opts service.StoreListOptions) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{
				Items: []service.StoreProduct{{
					Product: models.Product{ID: "prod_1", Handle: "tisort", Title: "T-shirt"},
					Variants: []service.StoreVariant{{
						Variant:       models.Variant{ID: "variant_1", ProductID: "prod_1", Title: "S"},
						PriceSet:      query.Record{"id": "pset_1", "amount": 19900, "currency_code": "try"},
						InventoryItem: query.Record{"id": "invitem_1", "stocked_quantity": 3},
					}},
				}},
				Count:  ptr(1),
				Limit:  opts.Limit,
				Offset: opts.Offset,
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	data, ok := body["data"].([]any)
	require.True(t, ok, "data has to be an array: %#v", body["data"])
	require.Len(t, data, 1)
	product, ok := data[0].(map[string]any)
	require.True(t, ok)
	variants, ok := product["variants"].([]any)
	require.True(t, ok, "a storefront product has to carry its variants: %#v", product)
	require.Len(t, variants, 1)

	variant, ok := variants[0].(map[string]any)
	require.True(t, ok)
	priceSet, ok := variant["price_set"].(map[string]any)
	require.True(t, ok, "a variant has to carry its price set: %#v", variant)
	assert.Equal(t, float64(19900), priceSet["amount"])
	inventory, ok := variant["inventory_item"].(map[string]any)
	require.True(t, ok, "a variant has to carry its inventory item: %#v", variant)
	assert.Equal(t, float64(3), inventory["stocked_quantity"])
}

// TestStoreGetProductAcceptsHandle verifies that the single storefront endpoint
// can be called with a handle.
func TestStoreGetProductAcceptsHandle(t *testing.T) {
	t.Parallel()

	var got string
	catalog := &fakeCatalog{
		getStoreProduct: func(_ context.Context, idOrHandle string, _ []string) (service.StoreProduct, error) {
			got = idOrHandle
			return service.StoreProduct{
				Product:  models.Product{ID: "prod_1", Handle: idOrHandle},
				Variants: []service.StoreVariant{},
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products/tisort", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "tisort", got)
}

// TestStoreProductHidesEmbeddedVariants verifies that the variants field of the
// embedded product DOES NOT SHADOW the storefront response.
//
// StoreProduct embeds models.Product and shadows it with its own Variants field;
// there has to be a single "variants" key in the JSON, otherwise the client
// cannot tell which list to look at.
func TestStoreProductHidesEmbeddedVariants(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		getStoreProduct: func(context.Context, string, []string) (service.StoreProduct, error) {
			return service.StoreProduct{
				Product: models.Product{
					ID: "prod_1",
					// The embedded field is filled in deliberately: it MUST NOT
					// APPEAR in the response.
					Variants: []models.Variant{{ID: "variant_hidden"}},
				},
				Variants: []service.StoreVariant{{Variant: models.Variant{ID: "variant_1"}}},
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products/prod_1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "variant_hidden",
		"the embedded variant list must not leak into the response: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "variant_1")
}

// TestRoutesDoNotMountSharedPrefixes verifies that the routes DO NOT MOUNT the
// "/admin/v1" or the "/store/v1" prefix.
//
// The registry calls the Routes of every module on the same router; the second
// module to mount the shared prefix would go down with a panic in chi. This test
// locks that contract: it has to be possible to add another module's endpoint
// under the same prefix to the same router.
func TestRoutesDoNotMountSharedPrefixes(t *testing.T) {
	t.Parallel()

	r := chi.NewRouter()
	api.New(&fakeCatalog{}, graph.Options{}).Routes(r)

	assert.NotPanics(t, func() {
		// Another module's (say pricing's) endpoint under the same version
		// prefix.
		r.Get("/admin/v1/price-sets", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		r.Get("/store/v1/prices", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	}, "the shared version prefix must not be mounted; other modules have to be able to add endpoints under the same prefix")

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestGraphQLEndpointReachesStorefrontService verifies that the GraphQL
// endpoint is WIRED into the router and that it reaches the storefront service.
//
// The behavior of the surface itself is exercised in the graph package; the
// claim here is only about the wiring: is the endpoint registered, and is the
// handler that takes the request really the GraphQL server. Had the wiring
// broken, all of the graph package's tests would stay green while the endpoint
// returned a 404.
func TestGraphQLEndpointReachesStorefrontService(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listStoreProducts: func(
			context.Context, service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{
				Items: []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
				Count: ptr(1),
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/store/v1/graphql",
		`{"query":"{ products { count items { id } } }"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body := decodeBody(t, rec)
	assert.NotContains(t, body, "errors", "body: %s", rec.Body.String())

	data, ok := body["data"].(map[string]any)
	require.True(t, ok)

	list, ok := data["products"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, float64(1), list["count"], 0)
}

// TestGraphQLEndpointRejectsGET verifies that the endpoint is registered with
// POST only.
//
// For why GET is not opened see graph.NewHandler. chi returns a 405 for a method
// that is not registered; that is more honest than gqlgen's "transport not
// supported" 400 because the problem is not in the transport but IN THE METHOD.
func TestGraphQLEndpointRejectsGET(t *testing.T) {
	t.Parallel()

	rec := do(t, newRouter(&fakeCatalog{}), http.MethodGet,
		"/store/v1/graphql?query={products{count}}", "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// ptr returns the address of the given value.
//
// The counter in the envelope is a pointer (nil means "not counted", see
// service.ListResult) and the constants the fake services return therefore have
// to be addressable.
func ptr[T any](v T) *T { return &v }

// countAwareCatalog returns a storefront catalog that RECORDS the "with_count"
// decision from the query string, along with the pointer that reads the record.
//
// The fake imitates the service's contract: if SkipCount was asked for the
// counter comes back nil, otherwise filled. Had it returned a fixed result, the
// test would only measure the handler's envelope writing and would never measure
// that the decision REACHED THE SERVICE.
func countAwareCatalog() (catalog *fakeCatalog, skipped *bool) {
	skipped = new(bool)
	catalog = &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			*skipped = opts.SkipCount

			res := service.ListResult[service.StoreProduct]{
				Items:  []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
				Limit:  opts.Limit,
				Offset: opts.Offset,
			}
			if !opts.SkipCount {
				res.Count = ptr(7)
			}

			return res, nil
		},
	}

	return catalog, skipped
}

// TestStoreListCountsByDefault verifies that a request with no parameter gives
// today's response.
//
// This test's job is backward compatibility: the quietest failure of adding a
// new key is the default drifting. If it drifts no client sees an error — only
// the number in its envelope disappears.
func TestStoreListCountsByDefault(t *testing.T) {
	t.Parallel()

	catalog, skipped := countAwareCatalog()

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products", "")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.False(t, *skipped, "a request that gives no parameter HAS TO COUNT")

	body := decodeBody(t, rec)
	assert.Equal(t, float64(7), body["count"], "the default response has to carry the counter")
	assert.Contains(t, body, "offset")
	assert.Contains(t, body, "limit")
}

// TestStoreListWithCountFalseDropsTheField verifies that the counter drops out
// of the envelope ENTIRELY when it is turned off.
//
// Both assertions are needed and the second is the real one: it IS NOT ENOUGH
// that the field does not return 0, the field has to be ABSENT. An
// implementation that returns 0 says "no matching product" and the client
// computes zero pages; an absent field, on the other hand, makes the
// computation impossible — that is, it gives the wrong answer LOUDLY.
func TestStoreListWithCountFalseDropsTheField(t *testing.T) {
	t.Parallel()

	catalog, skipped := countAwareCatalog()

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products?with_count=false", "")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.True(t, *skipped, "the decision HAS TO REACH the service; it must not be swallowed in the handler")

	body := decodeBody(t, rec)
	assert.NotContains(t, body, "count",
		"if it was not counted the field must be ENTIRELY absent from the body: %s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `"count"`,
		"it must not be present in the raw body either (a null may have been written)")

	// The rest of the envelope DOES NOT CHANGE: the only thing that drops is
	// the counter.
	assert.Contains(t, body, "data")
	assert.Contains(t, body, "offset")
	assert.Contains(t, body, "limit")
}

// TestStoreListWritesAZeroCount verifies that the "counted and it came out
// zero" case DOES NOT DROP the field.
//
// The claim is the LIMIT of the mechanism that drops the field. The envelope
// uses `*int` + omitempty: on pointers omitempty only looks at nil, so a pointer
// showing zero is written. Had the field been a plain `int` + omitempty — which
// is the most likely change someone would make in the name of simplification —
// the "no product matched" response would silently look COUNTERLESS and the
// client would think the number had not been computed. The two cases have to
// stay distinguishable in the response.
func TestStoreListWritesAZeroCount(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listStoreProducts: func(
			_ context.Context, opts service.StoreListOptions,
		) (service.ListResult[service.StoreProduct], error) {
			return service.ListResult[service.StoreProduct]{
				Count: ptr(0), Limit: opts.Limit, Offset: opts.Offset,
			}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products", "")
	require.Equal(t, http.StatusOK, rec.Code)

	body := decodeBody(t, rec)
	require.Contains(t, body, "count",
		"zero is a COUNTED result; the field has to be in the body: %s", rec.Body.String())
	assert.InDelta(t, float64(0), body["count"], 0)
}

// TestStoreListWithCountTrueCountsExplicitly verifies that the explicit form of
// the key works too.
//
// It is expected to give the same result as the default; the value of the test
// is showing that a "true" value is not mistakenly counted as "given but
// unreadable".
func TestStoreListWithCountTrueCountsExplicitly(t *testing.T) {
	t.Parallel()

	catalog, skipped := countAwareCatalog()

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products?with_count=true", "")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.False(t, *skipped)
	assert.Equal(t, float64(7), decodeBody(t, rec)["count"])
}

// TestStoreListRejectsMalformedWithCount verifies that an unreadable value does
// not silently fall back to the default.
//
// A silent fallback meant a client thinking it had turned the counter off while
// it kept paying the cost — and without being able to see it anywhere.
func TestStoreListRejectsMalformedWithCount(t *testing.T) {
	t.Parallel()

	catalog, _ := countAwareCatalog()

	rec := do(t, newRouter(catalog), http.MethodGet, "/store/v1/products?with_count=maybe", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
}

// TestAdminListNeverDropsTheCount verifies that the relaxation belongs ONLY to
// the storefront listing.
//
// The envelope type is shared by every endpoint; turning "count" into a pointer
// would mean every list could silently drop the field. The admin listing always
// counts and has to always write the field.
func TestAdminListNeverDropsTheCount(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		listProducts: func(
			_ context.Context, opts service.ListProductsOptions,
		) (service.ListResult[models.Product], error) {
			assert.False(t, opts.SkipCount, "the admin listing does not turn the counter off")

			return service.ListResult[models.Product]{
				Items:  []models.Product{{ID: "prod_1"}},
				Count:  ptr(1),
				Offset: opts.Offset,
				Limit:  opts.Limit,
			}, nil
		},
	}

	// The storefront's key IS NOT READ on the admin endpoint: even if it is
	// sent, the counter stays.
	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/products?with_count=false", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, float64(1), decodeBody(t, rec)["count"])
}

// TestAdminListExpandDefaultsToOff verifies that the "expand" key is OFF when it
// is not given.
//
// The test closes the cost of [boolParam] carrying the default to the call site:
// the default now sits on the call line rather than in the function body, and a
// single word there ("false" → "true") compiles, breaks nothing and would
// silently make the admin listing MORE EXPENSIVE — every request would start
// pulling the variants, the options, the images and the taxonomy too. The drift
// was measured: no test was failing.
func TestAdminListExpandDefaultsToOff(t *testing.T) {
	t.Parallel()

	var requested []bool

	catalog := &fakeCatalog{
		listProducts: func(
			_ context.Context, opts service.ListProductsOptions,
		) (service.ListResult[models.Product], error) {
			requested = append(requested, opts.WithRelations)

			return service.ListResult[models.Product]{Count: ptr(0)}, nil
		},
	}

	router := newRouter(catalog)

	require.Equal(t, http.StatusOK, do(t, router, http.MethodGet, "/admin/v1/products", "").Code)
	require.Equal(t, http.StatusOK,
		do(t, router, http.MethodGet, "/admin/v1/products?expand=true", "").Code)

	require.Len(t, requested, 2)
	assert.False(t, requested[0], "if expand is not given the relations MUST NOT BE PULLED")
	assert.True(t, requested[1], "expand=true has to pull the relations")
}
