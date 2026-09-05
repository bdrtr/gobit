//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves Phase 4's Definition of Done end to end:
//
//	A product + variant can be created from the Admin API, a price and stock
//	CAN BE BOUND to the variant, the Store API lists the products with their
//	price and stock information.
//
// The setup uses the real core: the real link service (with real link tables),
// the real Query layer and the real container. The pricing and inventory
// modules ARE NOT HERE and CANNOT BE IMPORTED (Principle 2.4); in their place
// only stub providers satisfying the query.Provider contract are put into the
// container under the names "pricing.query" and "inventory.query" — that is
// exactly how the real modules appear to the core as well.

// stubProvider imitates another module's Query provider.
type stubProvider struct {
	entity string

	mu         sync.Mutex
	records    map[string]query.Record
	fetchCalls int
	fetchedIDs []string
}

var _ query.Provider = (*stubProvider)(nil)

// newStubProvider produces a stub provider for the given entity.
func newStubProvider(entity string) *stubProvider {
	return &stubProvider{entity: entity, records: map[string]query.Record{}}
}

// put adds the record the provider will return.
func (s *stubProvider) put(id string, rec query.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec["id"] = id
	s.records[id] = rec
}

// Entity returns the provider's entity name.
func (s *stubProvider) Entity() string { return s.entity }

// List is the root listing surface; in these tests the root is always the
// variant, so it is not expected to be called.
func (s *stubProvider) List(_ context.Context, _ query.ListOptions) ([]query.Record, error) {
	return nil, nil
}

// FetchByIDs returns the records of the given IDs and counts the call.
func (s *stubProvider) FetchByIDs(_ context.Context, ids, _ []string) ([]query.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fetchCalls++
	s.fetchedIDs = append(s.fetchedIDs, ids...)

	out := make([]query.Record, 0, len(ids))
	for _, id := range ids {
		if rec, ok := s.records[id]; ok {
			out = append(out, rec)
		}
	}
	return out, nil
}

// calls returns how many times FetchByIDs was called.
func (s *stubProvider) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fetchCalls
}

// system is a system wired up end to end.
type system struct {
	router    chi.Router
	links     link.LinkService
	container *container.Container
	pricing   *stubProvider
	inventory *stubProvider
}

// The ENTITY names of the other two modules in the Query layer. These are NOT
// module names and must be byte for byte the same as the names the real
// modules register.
const (
	entityPriceSet      = "price_set"
	entityInventoryItem = "inventory_item"
)

// newSystem really brings up the core and the product module.
//
// The flow is the same as in main.go: the pool, the link service and Query are
// put into the container, then the module is Registered (it declares its
// service, its providers and its link definitions), and the routes are mounted
// last.
func newSystem(t *testing.T) system {
	t.Helper()
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))
	// The event bus is MANDATORY: if Register cannot resolve it, startup falls
	// over (see [TestRegisterFailsWithoutEventBus]). The setup follows the same
	// order as main.go's; the bus is put in place before the modules come up.
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))

	// The pricing and inventory modules are represented by this pair; the core
	// too knows them only through these names and this interface.
	//
	// The registration names are not the MODULE name but the ENTITY name: one
	// module may offer several entities (the product module registers
	// "product" and "variant"), which is why Query resolves the target through
	// link.LinkSide.Entity. The real modules use exactly these names as well
	// (pricing -> "price_set", inventory -> "inventory_item"); writing the
	// module name here would mean exercising a setup that does not exist in
	// the real system.
	pricing := newStubProvider(entityPriceSet)
	inventory := newStubProvider(entityInventoryItem)
	require.NoError(t, c.Provide(entityPriceSet+query.ProviderSuffix, pricing))
	require.NoError(t, c.Provide(entityInventoryItem+query.ProviderSuffix, inventory))

	mod := product.New(product.Options{})
	require.NoError(t, mod.Register(ctx, c))

	router := chi.NewRouter()
	mod.Routes(router)

	return system{router: router, links: links, container: c, pricing: pricing, inventory: inventory}
}

// request makes a request and returns the response.
//
// The request carries a FULLY PRIVILEGED principal. In production
// corehttp.RequireAdmin puts the principal into the context; because this
// setup mounts the router directly, that middleware is not in play and the
// principal is put in place by hand. The reason is that corehttp.RequireScope
// was added to the admin endpoints: a request without a principal now gets a
// 401 without ever reaching the handler, and the tests here would measure the
// authorization layer instead of the Phase 4 DoD. Authorization ITSELF is
// exercised in api/yetki_test.go; the claims of this file have not changed.
func (s system) request(t *testing.T, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// jsonBody decodes the response body into a map.
func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

// jsonField returns a field of a JSON object in the EXPECTED type.
//
// An unchecked type assertion ("object[field].(string)") would do the same job
// on one line, but it panics when something goes wrong and the test's output
// would be nothing but a stack trace. Here the expected type and the INCOMING
// value are written together; when the response schema drifts, which field is
// what shows up on a single line.
func jsonField[T any](t *testing.T, object map[string]any, field string) T {
	t.Helper()

	value, ok := object[field].(T)
	require.True(t, ok, "the %q field must be of type %T; got: %#v", field, value, object[field])
	return value
}

// jsonObject returns a JSON value as an object.
//
// [jsonField] works with a field name; this one is needed for an ITEM in an
// array, and because the item has no name the message carries the value
// itself.
func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()

	object, ok := value.(map[string]any)
	require.True(t, ok, "the value must be an object; got: %#v", value)
	return object
}

// TestModuleRegisterWiresContainer verifies that Register does all four things
// in the contract: the service registration, the interop surface, the Query
// providers and the link definitions.
func TestModuleRegisterWiresContainer(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err, "the service must be resolvable under the name %q", product.ServiceName)
	assert.NotNil(t, svc)

	// The cross-module surface is registered under a name SEPARATE from the
	// service's: plugins see the catalog only under this name and in primitive
	// types (ADR 0006).
	interop, err := container.Resolve[*service.Interop](sys.container, product.InteropName)
	require.NoError(t, err, "the surface must be resolvable under the name %q", product.InteropName)
	assert.NotNil(t, interop)

	// The category provider is in this list for the same reason the other two
	// are: it is registered under a name computed from the entity, and a name
	// that did not match would not fail at startup — Query would simply not find
	// it, and the panel's category dropdown would come back empty with an error
	// nobody sees until an operator tries to filter.
	for _, entity := range []string{service.EntityProduct, service.EntityVariant, service.EntityCategory} {
		name := entity + query.ProviderSuffix
		provider, err := container.Resolve[query.Provider](sys.container, name)
		require.NoError(t, err, "the %q provider must be registered", name)
		assert.Equal(t, entity, provider.Entity(),
			"the provider's entity name must match the registration name (Query verifies this)")
	}

	for _, want := range service.Definitions() {
		got, err := sys.links.Definition(ctx, want.Name)
		require.NoError(t, err, "the %q link definition must have been declared", want.Name)
		assert.Equal(t, want, got, "the declared definition must be byte for byte the contract")
	}
}

// TestAdminAPICreatesProductAndVariant verifies that a product and a variant
// can be created from the admin API (Phase 4 DoD).
func TestAdminAPICreatesProductAndVariant(t *testing.T) {
	sys := newSystem(t)
	handle := uniqueHandle("admin-product")

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Product From Admin",
		"status": "published",
		"options": [{"title": "Size", "values": ["S", "M"]}],
		"variants": [{"title": "S size", "options": {"Size": "S"}}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := itemData(t, rec)
	productID := jsonField[string](t, created, "id")
	assert.True(t, strings.HasPrefix(productID, "prod_"))
	require.Len(t, jsonField[[]any](t, created, "variants"), 1)

	rec = sys.request(t, http.MethodPost, "/admin/v1/products/"+productID+"/variants", `{
		"title": "M size",
		"options": {"Size": "M"}
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	variant := itemData(t, rec)
	assert.True(t, strings.HasPrefix(jsonField[string](t, variant, "id"), "variant_"))
	optionValues := jsonField[[]any](t, variant, "option_values")
	require.Len(t, optionValues, 1, "the variant must be bound to an option value")
	firstValue, ok := optionValues[0].(map[string]any)
	require.True(t, ok, "an option value must be an object; got: %#v", optionValues[0])
	assert.Equal(t, "M", firstValue["value"])

	rec = sys.request(t, http.MethodGet, "/admin/v1/products/"+productID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, jsonField[[]any](t, itemData(t, rec), "variants"), 2,
		"the second variant must be bound to the product as well")

	// The same handle cannot be used a second time; the error must be 409.
	rec = sys.request(t, http.MethodPost, "/admin/v1/products",
		`{"handle": "`+handle+`", "title": "Copy"}`)
	assert.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())
}

// TestStoreListingReturnsPriceAndStock IS THE HEART OF PHASE 4: the storefront
// list returns the products with their price and stock information.
//
// The price is the pricing module's and the stock the inventory module's data;
// the product module imports neither. The data is gathered over the REAL link
// rows established in the admin flow, with the REAL Query layer.
func TestStoreListingReturnsPriceAndStock(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	// The storefront lists all published products; this test separates its own
	// set with a collection so that the records left behind by other tests do
	// not muddy the result.
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Storefront " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	handle := uniqueHandle("storefront-product")
	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Storefront Product",
		"status": "published",
		"collection_id": "`+collection.ID+`",
		"variants": [{"title": "Single size"}, {"title": "Double size"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := itemData(t, rec)
	productID := jsonField[string](t, created, "id")
	variants := jsonField[[]any](t, created, "variants")
	require.Len(t, variants, 2)
	firstVariantID := jsonField[string](t, jsonObject(t, variants[0]), "id")
	secondVariantID := jsonField[string](t, jsonObject(t, variants[1]), "id")

	// The records the pricing and inventory modules produce.
	sys.pricing.put("pset_"+firstVariantID, query.Record{"currency_code": "try", "amount": int64(19900)})
	sys.pricing.put("pset_"+secondVariantID, query.Record{"currency_code": "try", "amount": int64(24900)})
	sys.inventory.put("invitem_"+firstVariantID, query.Record{"stocked_quantity": int64(12)})

	// The bindings are established in the admin flow.
	for _, variantID := range []string{firstVariantID, secondVariantID} {
		rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set",
			`{"price_set_id": "pset_`+variantID+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+firstVariantID+"/inventory-item",
		`{"inventory_item_id": "invitem_`+firstVariantID+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	links := itemData(t, rec)
	assert.Equal(t, "invitem_"+firstVariantID, links["inventory_item_id"])
	assert.Equal(t, "pset_"+firstVariantID, links["price_set_id"],
		"the two bindings must not overwrite each other")

	pricingCallsBefore := sys.pricing.calls()
	inventoryCallsBefore := sys.inventory.calls()

	// --- Storefront list ---
	rec = sys.request(t, http.MethodGet, "/store/v1/products?collection_id="+collection.ID, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body := jsonBody(t, rec)
	assert.Equal(t, float64(1), body["count"])
	data := jsonField[[]any](t, body, "data")
	require.Len(t, data, 1)

	storeProduct := jsonObject(t, data[0])
	assert.Equal(t, handle, storeProduct["handle"])
	storeVariants := jsonField[[]any](t, storeProduct, "variants")
	require.Len(t, storeVariants, 2)

	byID := map[string]map[string]any{}
	for _, raw := range storeVariants {
		variant := jsonObject(t, raw)
		byID[jsonField[string](t, variant, "id")] = variant
	}

	first := byID[firstVariantID]
	require.NotNil(t, first)
	priceSet, ok := first["price_set"].(map[string]any)
	require.True(t, ok, "the variant must carry the price set: %#v", first)
	assert.Equal(t, "pset_"+firstVariantID, priceSet["id"])
	assert.Equal(t, float64(19900), priceSet["amount"], "the price is an integer in minor units")
	inventoryItem, ok := first["inventory_item"].(map[string]any)
	require.True(t, ok, "the variant must carry the inventory item: %#v", first)
	assert.Equal(t, float64(12), inventoryItem["stocked_quantity"])

	second := byID[secondVariantID]
	require.NotNil(t, second)
	assert.Equal(t, "pset_"+secondVariantID, jsonField[map[string]any](t, second, "price_set")["id"])
	assert.NotContains(t, second, "inventory_item",
		"on a variant with no stock binding the field must not be written at all")

	// No N+1: for two variants a SINGLE call is made to each of the target
	// modules.
	assert.Equal(t, pricingCallsBefore+1, sys.pricing.calls(),
		"the price provider must be called once per expansion, not once per variant")
	assert.Equal(t, inventoryCallsBefore+1, sys.inventory.calls(),
		"the stock provider must be called once per expansion, not once per variant")

	// --- Storefront single endpoint ---
	rec = sys.request(t, http.MethodGet, "/store/v1/products/"+handle, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	single := itemData(t, rec)
	assert.Equal(t, productID, single["id"])
	require.Len(t, jsonField[[]any](t, single, "variants"), 2)
}

// TestStoreListingHidesDraftProducts verifies against the real database that
// the storefront does not show a draft product.
func TestStoreListingHidesDraftProducts(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Draft " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	handle := uniqueHandle("draft-product")
	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Draft Product",
		"collection_id": "`+collection.ID+`",
		"variants": [{"title": "Single"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	rec = sys.request(t, http.MethodGet, "/store/v1/products?collection_id="+collection.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := jsonBody(t, rec)
	assert.Zero(t, body["count"], "a draft product must not be counted in the storefront")
	assert.Empty(t, jsonField[[]any](t, body, "data"), "a draft product must not be listed in the storefront")

	rec = sys.request(t, http.MethodGet, "/store/v1/products/"+handle, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a draft product must not be found in the storefront")
}

// TestStoreListingWithCountFalseKeepsPageDropsCount verifies against the REAL
// database that turning the count off does not change the page and that it
// drops the field from the body.
//
// The unit tests prove that the decision REACHES the service and that the
// count query does not run at all; this test answers the one remaining
// question: does the list query, which shares the same filter, return THE SAME
// rows once the count is gone. Because the two go through a single SQL
// template (see repository/saleschannel.go), that has to be exercised against
// the real plan.
//
// Measurement context: on the gobit_load setup with 52,004 products the count
// is 99% of the request's SQL (67.00 ms -> 0.65 ms). The catalog here is
// small, so the test pins the BEHAVIOR and not the DURATION — making a test
// assert a duration would tie the test to the machine's load.
func TestStoreListingWithCountFalseKeepsPageDropsCount(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title: "Count " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	for i := range 3 {
		rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
			"handle": "`+uniqueHandle(fmt.Sprintf("count-%d", i))+`",
			"title": "Count Product",
			"status": "published",
			"collection_id": "`+collection.ID+`",
			"variants": [{"title": "Single"}]
		}`)
		require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	}

	path := "/store/v1/products?limit=2&collection_id=" + collection.ID

	rec := sys.request(t, http.MethodGet, path, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	counted := jsonBody(t, rec)
	assert.InDelta(t, float64(3), counted["count"], 0,
		"by default the count must count the WHOLE filtered set (the page holds 2 records)")

	rec = sys.request(t, http.MethodGet, path+"&with_count=false", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.NotContains(t, rec.Body.String(), `"count"`,
		"while the count is off the field must NOT be in the body at all: %s", rec.Body.String())

	uncounted := jsonBody(t, rec)
	assert.Equal(t, jsonField[[]any](t, counted, "data"), jsonField[[]any](t, uncounted, "data"),
		"turning the count off must not change the CONTENT of the page")
	assert.InDelta(t, counted["limit"], uncounted["limit"], 0)
	assert.InDelta(t, counted["offset"], uncounted["offset"], 0)
}

// TestVariantDeletionRemovesLinks verifies that a deleted variant's bindings
// in the REAL link table are cleaned up.
//
// Had they not been cleaned up, a query made in the reverse direction by
// pricing would land on a variant that does not exist.
func TestVariantDeletionRemovesLinks(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("delete-link")+`",
		"title": "Linked Product",
		"status": "published",
		"variants": [{"title": "Single"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := itemData(t, rec)
	variantID := jsonField[string](t, jsonObject(t, jsonField[[]any](t, created, "variants")[0]), "id")

	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set",
		`{"price_set_id": "pset_to_delete"}`)
	require.Equal(t, http.StatusOK, rec.Code)

	linked, err := sys.links.List(ctx, service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	require.Equal(t, []string{"pset_to_delete"}, linked, "the binding must really be established")

	rec = sys.request(t, http.MethodDelete, "/admin/v1/variants/"+variantID, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	linked, err = sys.links.List(ctx, service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	assert.Empty(t, linked, "no binding may remain for a deleted variant")
}

// TestPriceSetLinkIsReplacedNotDuplicated verifies in the real link table that
// the cardinality (OneToOne) is preserved when the price set is changed.
func TestPriceSetLinkIsReplacedNotDuplicated(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("link-replace")+`",
		"title": "Price Changes",
		"status": "published",
		"variants": [{"title": "Single"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := itemData(t, rec)
	variantID := jsonField[string](t, jsonObject(t, jsonField[[]any](t, created, "variants")[0]), "id")

	for _, priceSetID := range []string{"pset_old", "pset_new"} {
		rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+variantID+"/price-set",
			`{"price_set_id": "`+priceSetID+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	}

	linked, err := sys.links.List(ctx, service.LinkVariantPriceSet, variantID)
	require.NoError(t, err)
	assert.Equal(t, []string{"pset_new"}, linked,
		"a OneToOne binding must be replaced, a second row must not be added")
}

// TestPriceSetLinkKeepsExistingWhenTargetIsTaken verifies with the REAL link
// service that a rebinding which fails with a conflict does not break the
// variant's EXISTING binding.
//
// A fake linker cannot prove this: what enforces the constraint is the unique
// index built on both ends of the OneToOne. The test locks two scenarios
// together:
//
//   - asking for a target bound to another variant returns 409 and NOTHING
//     changes (otherwise the variant would be left without a price and the
//     storefront would publish it that way);
//   - moving the same variant to a FREE new target keeps working (because the
//     FROM end is unique too, removing the old binding is mandatory).
func TestPriceSetLinkKeepsExistingWhenTargetIsTaken(t *testing.T) {
	sys := newSystem(t)
	ctx := context.Background()

	rec := sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+uniqueHandle("link-conflict")+`",
		"title": "Conflicting Binding",
		"status": "published",
		"variants": [{"title": "First"}, {"title": "Second"}]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	created := itemData(t, rec)
	variants := jsonField[[]any](t, created, "variants")
	require.Len(t, variants, 2)
	firstVariantID := jsonField[string](t, jsonObject(t, variants[0]), "id")
	secondVariantID := jsonField[string](t, jsonObject(t, variants[1]), "id")

	// The IDs must be unique per test: the TO end of a OneToOne is unique
	// ACROSS THE WHOLE link table, so a fixed ID would collide with another
	// test's binding.
	owned := "pset_" + uniqueHandle("owned")
	held := "pset_" + uniqueHandle("held")
	free := "pset_" + uniqueHandle("free")

	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+firstVariantID+"/price-set",
		`{"price_set_id": "`+owned+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+secondVariantID+"/price-set",
		`{"price_set_id": "`+held+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	// owned is already bound to the first variant; the second variant cannot
	// ask for it.
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+secondVariantID+"/price-set",
		`{"price_set_id": "`+owned+`"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "body: %s", rec.Body.String())

	linked, err := sys.links.List(ctx, service.LinkVariantPriceSet, secondVariantID)
	require.NoError(t, err)
	assert.Equal(t, []string{held}, linked,
		"a request that returns 409 must not break the variant's existing price binding")

	linked, err = sys.links.List(ctx, service.LinkVariantPriceSet, firstVariantID)
	require.NoError(t, err)
	assert.Equal(t, []string{owned}, linked, "the target's real owner must not be affected either")

	// Moving to a free target must still work.
	rec = sys.request(t, http.MethodPut, "/admin/v1/variants/"+secondVariantID+"/price-set",
		`{"price_set_id": "`+free+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	linked, err = sys.links.List(ctx, service.LinkVariantPriceSet, secondVariantID)
	require.NoError(t, err)
	assert.Equal(t, []string{free}, linked, "moving to a free target must replace the old binding")
}

// TestPriceSetLinkRejectsUnknownVariant verifies that binding a nonexistent
// variant returns 404.
func TestPriceSetLinkRejectsUnknownVariant(t *testing.T) {
	sys := newSystem(t)

	rec := sys.request(t, http.MethodPut, "/admin/v1/variants/variant_missing/price-set",
		`{"price_set_id": "pset_1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// TestStoreListingDegradesWithoutOtherModules proves that the storefront keeps
// working when product is deployed ON ITS OWN.
//
// That is Phase 4's modularity guarantee: if pricing and inventory are not
// registered, the storefront returns NOT a 500 but a 200 without price and
// stock. This behavior rests on Query's telling the "no provider" error apart,
// and product recognizes that error through a string constant COPIED FROM THE
// CORE (service.codeProviderNotFound).
//
// This test BINDS the two strings to each other: if the core renames the
// constant, this fails here. Otherwise a rename in the core would silently
// break this guarantee without bringing down a single gate.
func TestStoreListingDegradesWithoutOtherModules(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))
	require.NoError(t, c.Provide("core.eventbus", eventbus.NewInMemory(nil)))
	// The pricing and inventory providers are DELIBERATELY not registered.

	mod := product.New(product.Options{})
	require.NoError(t, mod.Register(ctx, c))

	router := chi.NewRouter()
	mod.Routes(router)

	svc, err := container.Resolve[*service.Service](c, product.ServiceName)
	require.NoError(t, err)

	prod, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Standalone module product",
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err)
	_, err = svc.CreateVariant(ctx, prod.ID, service.CreateVariantInput{Title: "One size"})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"while pricing/inventory are not registered the storefront must return NOT 500 but 200: %s", rec.Body.String())

	var body struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))

	// Because the pool is shared between the tests, our own product is looked
	// up; relying on the length of the list would make the test depend on the
	// neighboring tests.
	var mine map[string]any
	for _, rec := range body.Data {
		if rec["id"] == prod.ID {
			mine = rec
			break
		}
	}
	require.NotNil(t, mine, "the created product must be returned in the storefront")

	variants, ok := mine["variants"].([]any)
	require.True(t, ok, "the variants must be returned: %v", mine)
	require.Len(t, variants, 1)

	variant, ok := variants[0].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, variant, "price_set", "with no provider the price field must not exist at all")
	assert.NotContains(t, variant, "inventory_item", "with no provider the stock field must not exist at all")
}
