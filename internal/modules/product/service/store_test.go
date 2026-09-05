package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// storeFixture is the shared setup of the storefront tests: two published
// products, each with one variant, and the fake price/stock records that
// correspond to those variants.
type storeFixture struct {
	svc      *service.Service
	graph    *fakeGraph
	products []models.Product
}

// newStoreFixture sets up two published products and prepares the records the
// Query layer will return, keyed by variant id.
func newStoreFixture(t *testing.T) storeFixture {
	t.Helper()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)

	first := seedProduct(t, svc, "shirt", "Shirt")
	second := seedProduct(t, svc, "trousers", "Trousers")

	graph.records = []query.Record{
		{
			"id": first.Variants[0].ID,
			"price_set": query.Record{
				"id": "pset_1", "currency_code": "try", "amount": int64(19900),
			},
			"inventory_item": query.Record{"id": "invitem_1", "stocked_quantity": int64(7)},
		},
		{
			"id":        second.Variants[0].ID,
			"price_set": query.Record{"id": "pset_2", "currency_code": "try", "amount": int64(49900)},
		},
	}
	return storeFixture{svc: svc, graph: graph, products: []models.Product{first, second}}
}

// TestListStoreProductsIncludesPriceAndInventory verifies the heart of the
// Phase 4 DoD: the storefront list returns the products with PRICE and STOCK
// information.
func TestListStoreProductsIncludesPriceAndInventory(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 2)

	byHandle := map[string]service.StoreProduct{}
	for i := range result.Items {
		byHandle[result.Items[i].Handle] = result.Items[i]
	}

	shirt := byHandle["shirt"]
	require.Len(t, shirt.Variants, 1)
	require.NotNil(t, shirt.Variants[0].PriceSet, "the price set should be attached to the variant")
	assert.Equal(t, int64(19900), shirt.Variants[0].PriceSet["amount"],
		"the price should be carried as an integer minor unit")
	require.NotNil(t, shirt.Variants[0].InventoryItem, "the stock item should be attached to the variant")
	assert.Equal(t, int64(7), shirt.Variants[0].InventoryItem["stocked_quantity"])

	trousers := byHandle["trousers"]
	require.Len(t, trousers.Variants, 1)
	assert.Equal(t, "pset_2", trousers.Variants[0].PriceSet["id"])
	assert.Nil(t, trousers.Variants[0].InventoryItem,
		"the field should stay empty on a variant with no stock link")
}

// TestListStoreProductsUsesSingleGraphCall verifies that the enrichment is done
// with a SINGLE Query call and that the spec follows the link contract.
//
// This test is the proof that there is no N+1: for two products and two variants
// Query is called exactly once.
func TestListStoreProductsUsesSingleGraphCall(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	_, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)

	assert.Equal(t, 1, fx.graph.callCount(), "whatever the number of variants, a single graph call should be made")

	spec := fx.graph.lastSpec(t)
	assert.Equal(t, service.EntityVariant, spec.Entity,
		"the root of the expansion is the variant; the links are made with the variant id")
	require.Len(t, spec.Expand, 2)
	assert.Equal(t, service.LinkVariantPriceSet, spec.Expand[0].Link)
	assert.Equal(t, "price_set", spec.Expand[0].As)
	assert.Equal(t, service.LinkVariantInventory, spec.Expand[1].Link)
	assert.Equal(t, "inventory_item", spec.Expand[1].As)

	ids, ok := spec.Filters["ids"].([]string)
	require.True(t, ok, "the filter should carry the variant ids as a string slice: %#v", spec.Filters)
	assert.Len(t, ids, 2, "the variants of both products should be asked for in a single call")
}

// TestListStoreProductsHidesUnpublished verifies that the storefront shows only
// the published products.
func TestListStoreProductsHidesUnpublished(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "draft-one", Title: "Draft", Status: models.StatusDraft,
	})
	require.NoError(t, err)
	_, err = svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "archived-one", Title: "Archived", Status: models.StatusArchived,
	})
	require.NoError(t, err)
	seedProduct(t, svc, "shirt", "Shirt")

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1, "only the published product should be listed")
	assert.Equal(t, "shirt", result.Items[0].Handle)
	assert.Equal(t, 1, requireCount(t, result), "the count should count only the published ones too")
}

// TestGetStoreProductByHandle verifies that the storefront endpoint works with a
// handle as well.
func TestGetStoreProductByHandle(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	product, err := fx.svc.GetStoreProduct(context.Background(), "shirt", nil)
	require.NoError(t, err)
	assert.Equal(t, "shirt", product.Handle)
	require.Len(t, product.Variants, 1)
	assert.Equal(t, "pset_1", product.Variants[0].PriceSet["id"])
}

// TestGetStoreProductByID verifies that the storefront endpoint works with an id
// as well.
func TestGetStoreProductByID(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)

	product, err := fx.svc.GetStoreProduct(context.Background(), fx.products[0].ID, nil)
	require.NoError(t, err)
	assert.Equal(t, fx.products[0].ID, product.ID)
}

// TestGetStoreProductHidesDraft verifies that a product that is not published
// returns NOT FOUND in the storefront.
func TestGetStoreProductHidesDraft(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), &fakeGraph{})
	ctx := context.Background()

	draft, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "draft-one", Title: "Draft", Status: models.StatusDraft,
	})
	require.NoError(t, err)

	_, err = svc.GetStoreProduct(ctx, draft.ID, nil)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a draft product should not be findable in the storefront: %v", err)
}

// TestListStoreProductsDegradesWhenProviderMissing verifies that when
// pricing/inventory is not registered the listing DOES NOT FAIL and only the
// price/stock fields stay empty.
func TestListStoreProductsDegradesWhenProviderMissing(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{err: errors.NotFound("query_provider_not_found",
		"no query provider was found for the \"pricing\" entity")}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()
	seedProduct(t, svc, "shirt", "Shirt")

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err, "a missing module should not fail the catalog")
	require.Len(t, result.Items, 1)
	require.Len(t, result.Items[0].Variants, 1)
	assert.Nil(t, result.Items[0].Variants[0].PriceSet, "the price field should stay empty")
	assert.Nil(t, result.Items[0].Variants[0].InventoryItem, "the stock field should stay empty")
}

// TestListStoreProductsPropagatesProviderNotFound verifies that a NotFound
// produced by a REGISTERED provider is not swallowed.
//
// "The provider is not registered" and "the provider said not found" are errors
// of the same KIND (NotFound) but they are different events: the first is a
// setup fact, the second is a fault. A fallback that looks at the kind swallows
// the second one too and the storefront returns 200 without prices, leaving no
// trace beyond a single log line.
func TestListStoreProductsPropagatesProviderNotFound(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{err: errors.NotFound("query_provider_failed",
		"the FetchByIDs call of the \"price_set\" provider failed")}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()
	seedProduct(t, svc, "shirt", "Shirt")

	_, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.Error(t, err, "the error of a registered provider should not be swallowed silently")
	assert.Equal(t, "product_query_failed", errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err), "the error kind should be preserved: %v", err)
}

// TestListStoreProductsPropagatesQueryFailure verifies that a transient Query
// failure is NOT SWALLOWED SILENTLY.
//
// The distinction is critical: "the provider is not registered" is a setup fact
// and the catalog is meaningful without it; "the database is unreachable" is a
// transient fault and a storefront page without prices should not enter the
// cache as a correct result.
func TestListStoreProductsPropagatesQueryFailure(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{err: errors.Unavailable("query_link_failed", "the link table could not be read")}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()
	seedProduct(t, svc, "shirt", "Shirt")

	_, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable), "the error kind should be preserved: %v", err)
}

// TestListStoreProductsWithoutQueryLayer verifies that the listing works while
// no Query layer was given at all (the module is deployable on its own).
func TestListStoreProductsWithoutQueryLayer(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()
	seedProduct(t, svc, "shirt", "Shirt")

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Nil(t, result.Items[0].Variants[0].PriceSet)
}

// TestListStoreProductsSkipsGraphWhenNoVariants verifies that on a catalog with
// no variants the Query layer is not visited at all.
func TestListStoreProductsSkipsGraphWhenNoVariants(t *testing.T) {
	t.Parallel()

	graph := &fakeGraph{}
	svc := newService(t, newMemStore(), newFakeLinker(), graph)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "shirt", Title: "Shirt", Status: models.StatusPublished,
	})
	require.NoError(t, err)

	result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Empty(t, result.Items[0].Variants)
	assert.Zero(t, graph.callCount(), "if there is no variant to expand, Query should not be visited")
}

// TestListStoreProductsSkipCountRunsNoCountQuery verifies that SkipCount is not
// a FILTER but the CANCELLATION OF THE QUERY.
//
// The assertion deliberately looks not at the field of the result but at THE
// REPOSITORY. "Count came back nil" would not be enough: an implementation that
// computes the count and throws the result away passes that assertion too and
// would silently lose the only gain there is — not asking the 64.07 ms query at
// all on a catalog of 52,004 products. The measured gain comes from the absence
// of the query, not from the emptiness of the field.
func TestListStoreProductsSkipCountRunsNoCountQuery(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()

	seedProduct(t, svc, "shirt", "Shirt")

	counted, err := svc.ListStoreProducts(ctx, service.StoreListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, requireCount(t, counted), "the default should still count")

	countingCalls := store.callCount("CountProducts")
	require.Positive(t, countingCalls, "the default should run the count query")

	uncounted, err := svc.ListStoreProducts(ctx, service.StoreListOptions{SkipCount: true})
	require.NoError(t, err)

	assert.Nil(t, uncounted.Count,
		"if it was not counted the field should be nil; 0 would be confused with \"no match\"")
	assert.Len(t, uncounted.Items, 1,
		"turning the count off should not affect THE LIST")
	assert.Equal(t, countingCalls, store.callCount("CountProducts"),
		"with SkipCount the count query should NEVER be run")
}

// TestListStoreProductsSkipCountLeavesThePageUnchanged verifies that turning the
// count off leaves ALL OF THE RECORDS on the page exactly as they were.
//
// The paging, the relations and the count come out of the same call; one of them
// touching the others would be silent. The comparison is deliberately not the
// handle list but THE RECORDS THEMSELVES: an assertion that only looks at the
// handles would let through an "improvement" that drops the variants along with
// the count (it was tried — the handle comparison survives it, the record
// comparison fails it). The real content of a storefront product is its variants
// carrying price and stock; if they empty out the page looks the same but is
// useless.
func TestListStoreProductsSkipCountLeavesThePageUnchanged(t *testing.T) {
	t.Parallel()

	fx := newStoreFixture(t)
	ctx := context.Background()

	counted, err := fx.svc.ListStoreProducts(ctx, service.StoreListOptions{Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, counted.Items, 1)
	require.NotEmpty(t, counted.Items[0].Variants,
		"the fixture should return an enriched variant; otherwise the assertion below is empty")

	uncounted, err := fx.svc.ListStoreProducts(ctx,
		service.StoreListOptions{Limit: 1, Offset: 1, SkipCount: true})
	require.NoError(t, err)

	assert.Equal(t, counted.Items, uncounted.Items,
		"turning the count off should not change the records of the page (variants, price and stock included)")
	assert.Equal(t, counted.Limit, uncounted.Limit)
	assert.Equal(t, counted.Offset, uncounted.Offset)
}
