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

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file exercises the SCOPE layer of product's admin endpoints.
//
// The identity layer (corehttp.RequireAdmin) is imitated here: what the test
// wants to prove is not "is the identity resolved correctly" but "is the SCOPE
// of a resolved identity enforced endpoint by endpoint". When the two are
// exercised separately, the case where authentication works flawlessly while
// authorization was never wired up — that is, the fault that was fixed — stays
// visible.

// scopeCatalog is an [api.Catalog] that counts every call and returns zero
// values.
//
// The fakeCatalog in api_test.go is NOT USED here: thanks to its embedded nil
// interface that fake panics on every method that is not overridden, and it
// would make the test fail with a panic instead of a 403 the day a new endpoint
// is added to the table. The scope test's table deliberately walks ALL the
// endpoints, which is why a quiet fake covering the whole surface is needed
// here.
type scopeCatalog struct {
	callCount int
}

var _ api.Catalog = (*scopeCatalog)(nil)

// count records a service call.
func (f *scopeCatalog) count() { f.callCount++ }

// CreateProduct counts the call.
func (f *scopeCatalog) CreateProduct(context.Context, service.CreateProductInput) (models.Product, error) {
	f.count()
	return models.Product{}, nil
}

// GetProduct counts the call.
func (f *scopeCatalog) GetProduct(context.Context, string) (models.Product, error) {
	f.count()
	return models.Product{}, nil
}

// ListProducts counts the call.
func (f *scopeCatalog) ListProducts(
	context.Context, service.ListProductsOptions,
) (service.ListResult[models.Product], error) {
	f.count()
	return service.ListResult[models.Product]{}, nil
}

// UpdateProduct counts the call.
func (f *scopeCatalog) UpdateProduct(
	context.Context, string, service.UpdateProductInput,
) (models.Product, error) {
	f.count()
	return models.Product{}, nil
}

// DeleteProduct counts the call.
func (f *scopeCatalog) DeleteProduct(context.Context, string) error {
	f.count()
	return nil
}

// CreateVariant counts the call.
func (f *scopeCatalog) CreateVariant(
	context.Context, string, service.CreateVariantInput,
) (models.Variant, error) {
	f.count()
	return models.Variant{}, nil
}

// GetVariant counts the call.
func (f *scopeCatalog) GetVariant(context.Context, string) (models.Variant, error) {
	f.count()
	return models.Variant{}, nil
}

// ListVariants counts the call.
func (f *scopeCatalog) ListVariants(
	context.Context, service.ListVariantsOptions,
) (service.ListResult[models.Variant], error) {
	f.count()
	return service.ListResult[models.Variant]{}, nil
}

// UpdateVariant counts the call.
func (f *scopeCatalog) UpdateVariant(
	context.Context, string, service.UpdateVariantInput,
) (models.Variant, error) {
	f.count()
	return models.Variant{}, nil
}

// DeleteVariant counts the call.
func (f *scopeCatalog) DeleteVariant(context.Context, string) error {
	f.count()
	return nil
}

// CreateOption counts the call.
func (f *scopeCatalog) CreateOption(
	context.Context, string, service.CreateOptionInput,
) (models.Option, error) {
	f.count()
	return models.Option{}, nil
}

// ListOptions counts the call.
func (f *scopeCatalog) ListOptions(context.Context, string) ([]models.Option, error) {
	f.count()
	return nil, nil
}

// AddOptionValue counts the call.
func (f *scopeCatalog) AddOptionValue(context.Context, string, string) (models.OptionValue, error) {
	f.count()
	return models.OptionValue{}, nil
}

// DeleteOption counts the call.
func (f *scopeCatalog) DeleteOption(context.Context, string) error {
	f.count()
	return nil
}

// SetVariantPriceSet counts the call.
func (f *scopeCatalog) SetVariantPriceSet(context.Context, string, string) error {
	f.count()
	return nil
}

// ClearVariantPriceSet counts the call.
func (f *scopeCatalog) ClearVariantPriceSet(context.Context, string) error {
	f.count()
	return nil
}

// SetVariantInventoryItem counts the call.
func (f *scopeCatalog) SetVariantInventoryItem(context.Context, string, string) error {
	f.count()
	return nil
}

// ClearVariantInventoryItem counts the call.
func (f *scopeCatalog) ClearVariantInventoryItem(context.Context, string) error {
	f.count()
	return nil
}

// VariantLinkIDs counts the call.
func (f *scopeCatalog) VariantLinkIDs(context.Context, string) (service.VariantLinks, error) {
	f.count()
	return service.VariantLinks{}, nil
}

// AddProductSalesChannel counts the call.
func (f *scopeCatalog) AddProductSalesChannel(context.Context, string, string) error {
	f.count()
	return nil
}

// RemoveProductSalesChannel counts the call.
func (f *scopeCatalog) RemoveProductSalesChannel(context.Context, string, string) error {
	f.count()
	return nil
}

// ProductSalesChannelIDs counts the call.
func (f *scopeCatalog) ProductSalesChannelIDs(context.Context, string) ([]string, error) {
	f.count()
	return nil, nil
}

// CreateCollection counts the call.
func (f *scopeCatalog) CreateCollection(
	context.Context, service.CreateCollectionInput,
) (models.Collection, error) {
	f.count()
	return models.Collection{}, nil
}

// GetCollection counts the call.
func (f *scopeCatalog) GetCollection(context.Context, string) (models.Collection, error) {
	f.count()
	return models.Collection{}, nil
}

// ListCollections counts the call.
func (f *scopeCatalog) ListCollections(
	context.Context, int, int,
) (service.ListResult[models.Collection], error) {
	f.count()
	return service.ListResult[models.Collection]{}, nil
}

// CreateCategory counts the call.
func (f *scopeCatalog) CreateCategory(
	context.Context, service.CreateCategoryInput,
) (models.Category, error) {
	f.count()
	return models.Category{}, nil
}

// GetCategory counts the call.
func (f *scopeCatalog) GetCategory(context.Context, string) (models.Category, error) {
	f.count()
	return models.Category{}, nil
}

// ListCategories counts the call.
func (f *scopeCatalog) ListCategories(
	context.Context, service.ListCategoriesOptions,
) (service.ListResult[models.Category], error) {
	f.count()
	return service.ListResult[models.Category]{}, nil
}

// CreateTag counts the call.
func (f *scopeCatalog) CreateTag(context.Context, string) (models.Tag, error) {
	f.count()
	return models.Tag{}, nil
}

// ListTags counts the call.
func (f *scopeCatalog) ListTags(context.Context, int, int) (service.ListResult[models.Tag], error) {
	f.count()
	return service.ListResult[models.Tag]{}, nil
}

// ListStoreProducts counts the call.
func (f *scopeCatalog) ListStoreProducts(
	context.Context, service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	f.count()
	return service.ListResult[service.StoreProduct]{}, nil
}

// GetStoreProduct counts the call.
func (f *scopeCatalog) GetStoreProduct(context.Context, string, []string) (service.StoreProduct, error) {
	f.count()
	return service.StoreProduct{}, nil
}

// scopedRouter builds a router with an AUTHENTICATED identity carrying the
// given scopes.
//
// Giving no scope at all is a valid case and produces a "has an identity but no
// scope" caller — this user was the fault itself.
func scopedRouter(t *testing.T, scopes ...string) (chi.Router, *scopeCatalog) {
	t.Helper()

	svc := &scopeCatalog{}
	r := chi.NewRouter()
	r.Use(principalMiddleware(scopes...))
	api.New(svc, graph.Options{}).Routes(r)

	return r, svc
}

// anonymousRouter builds a router where NO identity is put into the context.
func anonymousRouter(t *testing.T) (chi.Router, *scopeCatalog) {
	t.Helper()

	svc := &scopeCatalog{}
	r := chi.NewRouter()
	api.New(svc, graph.Options{}).Routes(r)

	return r, svc
}

// principalMiddleware returns a middleware that puts an authenticated identity
// into the context.
//
// In production corehttp.RequireAdmin does this; setting the identity by hand in
// the test makes it possible to exercise the scope layer without token issuing
// and without a database.
func principalMiddleware(scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := corehttp.Principal{ID: "usr_test", Kind: "user", Scopes: scopes}
			next.ServeHTTP(w, r.WithContext(corehttp.WithPrincipal(r.Context(), principal)))
		})
	}
}

// scopeRequest runs a request and returns the response recorder.
//
// It stands apart from the do in api_test.go because that helper adds a FULLY
// PRIVILEGED identity to the request; here the test itself decides the identity.
func scopeRequest(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// scopeErrorCode returns the code in the error envelope.
func scopeErrorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "body: %s", rec.Body.String())
	return envelope.Error.Code
}

// writeEndpoints are all the admin endpoints that ask for [api.ScopeWrite].
//
// The list has to grow along with [api.Handler.Routes]: a write endpoint that is
// added but not written here is the only place that could silently end up
// unguarded.
var writeEndpoints = map[string]struct {
	method string
	path   string
	body   string
}{
	"create product":   {http.MethodPost, "/admin/v1/products", `{}`},
	"update product":   {http.MethodPatch, "/admin/v1/products/prod_1", `{}`},
	"delete product":   {http.MethodDelete, "/admin/v1/products/prod_1", ""},
	"create variant":   {http.MethodPost, "/admin/v1/products/prod_1/variants", `{}`},
	"update variant":   {http.MethodPatch, "/admin/v1/variants/var_1", `{}`},
	"delete variant":   {http.MethodDelete, "/admin/v1/variants/var_1", ""},
	"create option":    {http.MethodPost, "/admin/v1/products/prod_1/options", `{}`},
	"add option value": {http.MethodPost, "/admin/v1/product-options/opt_1/values", `{}`},
	"delete option":    {http.MethodDelete, "/admin/v1/product-options/opt_1", ""},
	"link price set":   {http.MethodPut, "/admin/v1/variants/var_1/price-set", `{}`},
	"unlink price set": {
		http.MethodDelete, "/admin/v1/variants/var_1/price-set", "",
	},
	"link inventory item": {http.MethodPut, "/admin/v1/variants/var_1/inventory-item", `{}`},
	"unlink inventory item": {
		http.MethodDelete, "/admin/v1/variants/var_1/inventory-item", "",
	},
	"link sales channel": {
		http.MethodPost, "/admin/v1/products/prod_1/sales-channels", `{}`,
	},
	"unlink sales channel": {
		http.MethodDelete, "/admin/v1/products/prod_1/sales-channels/sc_1", "",
	},
	"create collection": {http.MethodPost, "/admin/v1/product-collections", `{}`},
	"create category":   {http.MethodPost, "/admin/v1/product-categories", `{}`},
	"create tag":        {http.MethodPost, "/admin/v1/product-tags", `{}`},
}

// readEndpoints are all the admin endpoints that ask for [api.ScopeRead].
var readEndpoints = map[string]string{
	"product list":    "/admin/v1/products",
	"single product":  "/admin/v1/products/prod_1",
	"variant list":    "/admin/v1/products/prod_1/variants",
	"single variant":  "/admin/v1/variants/var_1",
	"option list":     "/admin/v1/products/prod_1/options",
	"variant links":   "/admin/v1/variants/var_1/links",
	"sales channels":  "/admin/v1/products/prod_1/sales-channels",
	"collection list": "/admin/v1/product-collections",
	"category list":   "/admin/v1/product-categories",
	"tag list":        "/admin/v1/product-tags",
}

// TestWriteEndpointRejectsReadOnlyCaller proves that the write endpoints ask for
// [api.ScopeWrite].
//
// The caller is a REAL identity and it has the read scope; the only thing
// missing is the write scope. This was exactly the fault: every caller whose
// identity was authenticated could delete the catalog regardless of its scope.
func TestWriteEndpointRejectsReadOnlyCaller(t *testing.T) {
	for name, tt := range writeEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t, api.ScopeRead)

			rec := scopeRequest(t, r, tt.method, tt.path, tt.body)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a caller with the read scope has to get a 403 on a write endpoint; body: %s", rec.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, scopeErrorCode(t, rec))
			assert.Zero(t, svc.callCount,
				"a rejected request must NEVER reach the service; the write would have happened before the rejection")
		})
	}
}

// TestReadEndpointWorksWithReadScope proves that the read endpoints LET THROUGH
// the same narrow identity.
//
// Being a separate test is deliberate: a middleware that rejects every request
// would pass the table above flawlessly while locking the admin surface
// entirely. [api.ScopeRead] exists only to keep writing closed; binding reading
// to admin as well would force a narrowly scoped integration that reports the
// catalog to ask for full privileges.
func TestReadEndpointWorksWithReadScope(t *testing.T) {
	for name, path := range readEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t, api.ScopeRead)

			rec := scopeRequest(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusOK, rec.Code,
				"the read scope has to be enough for a read endpoint; body: %s", rec.Body.String())
			assert.Positive(t, svc.callCount, "the request has to reach the service")
		})
	}
}

// TestWriteEndpointAcceptsAdminCaller proves that corehttp.ScopeAdmin is the
// SUPER SCOPE, that is, that it is enough for writing without "product:write"
// being granted separately.
func TestWriteEndpointAcceptsAdminCaller(t *testing.T) {
	for name, tt := range writeEndpoints {
		t.Run(name, func(t *testing.T) {
			r, svc := scopedRouter(t, corehttp.ScopeAdmin)

			rec := scopeRequest(t, r, tt.method, tt.path, tt.body)

			assert.NotEqual(t, http.StatusForbidden, rec.Code,
				"admin MUST NOT get a 403 on a write endpoint; body: %s", rec.Body.String())
			assert.Positive(t, svc.callCount, "the request has to reach the service")
		})
	}
}

// TestScopelessUserCannotReachCatalog proves that an admin user with no scope at
// all cannot call any catalog endpoint.
//
// The godoc of auth service.CreateUserInput.Scopes says an empty scope list
// produces a user that "can log in but can reach no admin endpoint"; this test
// is the counterpart of that sentence on the product side.
func TestScopelessUserCannotReachCatalog(t *testing.T) {
	for name, path := range readEndpoints {
		t.Run("read/"+name, func(t *testing.T) {
			r, svc := scopedRouter(t)

			rec := scopeRequest(t, r, http.MethodGet, path, "")

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a user with no scope has to get a 403 on a read endpoint; body: %s", rec.Body.String())
			assert.Zero(t, svc.callCount)
		})
	}

	for name, tt := range writeEndpoints {
		t.Run("write/"+name, func(t *testing.T) {
			r, svc := scopedRouter(t)

			rec := scopeRequest(t, r, tt.method, tt.path, tt.body)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"a user with no scope has to get a 403 on a write endpoint; body: %s", rec.Body.String())
			assert.Zero(t, svc.callCount)
		})
	}
}

// TestStoreEndpointsRequireNoScope proves that the /store/v1 endpoints DO NOT
// ASK for a scope.
//
// The identity of the store surface is the publishable key and that key by
// definition CARRIES NO scope. Had a scope been added to the store endpoints, no
// store client could list products — that is, the storefront would close.
func TestStoreEndpointsRequireNoScope(t *testing.T) {
	r, svc := anonymousRouter(t)

	list := scopeRequest(t, r, http.MethodGet, "/store/v1/products", "")
	assert.Equal(t, http.StatusOK, list.Code,
		"the storefront listing must not ask for a scope; body: %s", list.Body.String())

	single := scopeRequest(t, r, http.MethodGet, "/store/v1/products/prod_1", "")
	assert.Equal(t, http.StatusOK, single.Code,
		"the single storefront endpoint must not ask for a scope; body: %s", single.Body.String())

	assert.Equal(t, 2, svc.callCount)
}

// TestAdminRequestWithoutPrincipalReturns401 proves that when there is no
// identity at all the scope layer returns a 401, NOT a 403.
//
// The distinction matters to the client: 401 means "tell me who you are", 403
// means "I know who you are but you have no scope". Had it returned a 403, a
// client that forgot the identity header would go asking for a scope instead of
// refreshing its token.
func TestAdminRequestWithoutPrincipalReturns401(t *testing.T) {
	r, svc := anonymousRouter(t)

	rec := scopeRequest(t, r, http.MethodGet, "/admin/v1/products", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "Bearer", rec.Header().Get("WWW-Authenticate"),
		"RFC 9110: a 401 has to report which scheme is expected")
	assert.Zero(t, svc.callCount)
}
