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

// providerFixture is the shared setup of the provider tests.
type providerFixture struct {
	store    *memStore
	products query.Provider
	variants query.Provider
	seeded   models.Product
}

// newProviderFixture builds a setup that has one product and one variant.
func newProviderFixture(t *testing.T) providerFixture {
	t.Helper()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	seeded := seedProduct(t, svc, "shirt", "Shirt")

	return providerFixture{
		store:    store,
		products: service.NewProductProvider(store),
		variants: service.NewVariantProvider(store),
		seeded:   seeded,
	}
}

// TestProviderEntityNamesMatchRegistration verifies that the entity names the
// providers offer coincide with the registration names.
//
// Query verifies the Entity() value of the provider it resolved under
// "<entity>.query"; a mismatch becomes an error instead of silently pulling data
// from the wrong module.
func TestProviderEntityNamesMatchRegistration(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	assert.Equal(t, "product", fx.products.Entity())
	assert.Equal(t, "variant", fx.variants.Entity())
	assert.Equal(t, service.EntityProduct, fx.products.Entity())
	assert.Equal(t, service.EntityVariant, fx.variants.Entity())
}

// TestProductProviderListReturnsRecords verifies that the root list comes back
// as records and that the id field is present.
func TestProductProviderListReturnsRecords(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)

	records, err := fx.products.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, fx.seeded.ID, records[0][query.IDField],
		"the record should carry the %q field Query joins on", query.IDField)
	assert.Equal(t, "shirt", records[0]["handle"])
}

// TestProductProviderFilters verifies that the supported filters are applied.
func TestProductProviderFilters(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"status": "published"},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"status": "draft"},
	})
	require.NoError(t, err)
	assert.Empty(t, records, "the status filter should be applied")

	records, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"handle": "missing"},
	})
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestProviderRejectsUnknownFilter verifies that an unrecognized filter is NOT
// SILENTLY IGNORED (ADR 0004).
func TestProviderRejectsUnknownFilter(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	_, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"color": "blue"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized filter should give a validation error: %v", err)

	_, err = fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"color": "blue"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized filter should give a validation error: %v", err)
}

// TestProviderRejectsWrongFilterType shows that the type of the filter value is
// validated.
func TestProviderRejectsWrongFilterType(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)

	_, err := fx.products.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"status": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type mismatch should give a validation error: %v", err)
}

// TestProviderProjectsFields verifies that the field selection is applied and
// that an unrecognized field is rejected.
func TestProviderProjectsFields(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.variants.List(ctx, query.ListOptions{Fields: []string{"id", "title"}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2, "only the requested fields should come back: %#v", records[0])
	assert.Contains(t, records[0], "id")
	assert.Contains(t, records[0], "title")

	_, err = fx.variants.List(ctx, query.ListOptions{Fields: []string{"price"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized field should give a validation error: %v", err)
}

// TestVariantProviderFetchByIDsIsBatched verifies that the expansion is resolved
// with a SINGLE call and that an id that is not found is not an error (ADR
// 0004).
func TestVariantProviderFetchByIDsIsBatched(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	variantID := fx.seeded.Variants[0].ID

	before := fx.store.callCount("ListVariantsByIDs")
	records, err := fx.variants.FetchByIDs(context.Background(),
		[]string{variantID, "variant_missing"}, nil)
	require.NoError(t, err, "an id that is not found is not an error")
	require.Len(t, records, 1)
	assert.Equal(t, variantID, records[0][query.IDField])
	assert.Equal(t, before+1, fx.store.callCount("ListVariantsByIDs"),
		"whatever the number of ids, a single query should be made")
}

// TestVariantProviderFiltersByProduct verifies that the variants are filtered by
// product.
func TestVariantProviderFiltersByProduct(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"product_id": fx.seeded.ID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"product_ids": []string{"prod_missing"}},
	})
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestVariantProviderIDsFilterAcceptsBothShapes verifies that the id filter
// accepts both the single string and the slice shape.
func TestVariantProviderIDsFilterAcceptsBothShapes(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()
	variantID := fx.seeded.Variants[0].ID

	single, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": variantID},
	})
	require.NoError(t, err)
	require.Len(t, single, 1)

	many, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []any{variantID}},
	})
	require.NoError(t, err)
	require.Len(t, many, 1)
	assert.Equal(t, single[0][query.IDField], many[0][query.IDField])
}

// TestProviderPaging verifies that limit and offset are applied.
func TestProviderPaging(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	seedProduct(t, svc, "one", "One")
	seedProduct(t, svc, "two", "Two")
	seedProduct(t, svc, "three", "Three")
	products := service.NewProductProvider(store)
	ctx := context.Background()

	all, err := products.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 3, "when no limit is given it should count as unlimited")

	page, err := products.List(ctx, query.ListOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page, 2)

	rest, err := products.List(ctx, query.ListOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, rest, 1)
}
