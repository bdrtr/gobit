package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestAdminSurfaceRejectsAnUnknownStatus proves the status is parsed before the
// service is touched.
//
// The consumer sends a string because it cannot import this package's Status
// type, so this is the only place that knows the valid values. An unknown value
// reaching UpdateProduct would be caught there too, but with a message about a
// type rather than about the value the operator chose.
func TestAdminSurfaceRejectsAnUnknownStatus(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	surface := service.NewAdminSurface(svc)

	created, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title: "Coffee", Handle: "coffee",
	})
	require.NoError(t, err)

	err = surface.UpdateProductBasics(context.Background(), created.ID, "Coffee", "coffee", "on-sale", created.Version)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unknown status must be Invalid, got %v", errors.KindOf(err))
	assert.Equal(t, service.CodeAdminInputInvalid, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "on-sale", "the rejected value must be named")
}

// TestAdminSurfaceGoesThroughTheService proves the write takes the service's
// path and not the repository's.
//
// The distinction is the whole reason this surface exists rather than a thinner
// one, and it has two halves that fail differently:
//
//   - The handle uniqueness check belongs to the service. Reached through the
//     repository, one product could take another's handle and the storefront
//     would resolve that handle to whichever row came back first.
//   - "product.updated" is published by the service. A surface that skipped it
//     would write a product no subscriber ever hears about — a search index
//     would keep serving the old title and nothing in the response would say
//     so.
//
// The second half is the silent one, which is why it is asserted here rather
// than left to the handle check to imply.
func TestAdminSurfaceGoesThroughTheService(t *testing.T) {
	t.Parallel()

	bus := newFakeBus()
	svc := newServiceWithBus(t, newMemStore(), newFakeLinker(), nil, bus)
	surface := service.NewAdminSurface(svc)

	first, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title: "Coffee", Handle: "coffee",
	})
	require.NoError(t, err)
	second, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title: "Tea", Handle: "tea",
	})
	require.NoError(t, err)

	require.NoError(t, surface.UpdateProductBasics(
		context.Background(), first.ID, "Filter Coffee", "filter-coffee", "published", first.Version))

	assert.NotEmpty(t, bus.byName(service.EventProductUpdated),
		"a write through the admin surface must publish the module's own event; "+
			"otherwise a subscriber keeps serving the old record")

	// The handle check belongs to the service too: reaching the repository
	// would let the second product take the first one's handle.
	err = surface.UpdateProductBasics(context.Background(), second.ID, "Tea", "filter-coffee", "draft", second.Version)

	require.Error(t, err)
	assert.True(t, errors.IsConflict(err),
		"a handle already taken must be a Conflict, got %v", errors.KindOf(err))
}

// TestAdminSurfaceUpdatesTheBasics proves the happy path writes all three
// fields.
func TestAdminSurfaceUpdatesTheBasics(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	surface := service.NewAdminSurface(svc)

	created, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title: "Coffee", Handle: "coffee",
	})
	require.NoError(t, err)

	require.NoError(t, surface.UpdateProductBasics(
		context.Background(), created.ID, "  Filter Coffee  ", " filter-coffee ", "published", created.Version))

	updated, err := svc.GetProduct(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Filter Coffee", updated.Title, "surrounding space must be trimmed")
	assert.Equal(t, "filter-coffee", updated.Handle)
	assert.Equal(t, "published", updated.Status.String())
}

// TestAdminSurfaceIsNilSafe proves a surface built without a service answers
// instead of panicking.
func TestAdminSurfaceIsNilSafe(t *testing.T) {
	t.Parallel()

	var surface *service.AdminSurface

	err := surface.UpdateProductBasics(context.Background(), "prod_1", "Coffee", "coffee", "draft", 1)

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}

// TestTheAdminSurfaceCreatesADraftAndAddsItsVariants is ADR 0307: the panel
// creates a draft whose handle is the title's slug when none is given, with a
// handle of its own when one is, and adds a variant with and without a SKU;
// a taken handle is refused as the service refuses it.
func TestTheAdminSurfaceCreatesADraftAndAddsItsVariants(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	surface := service.NewAdminSurface(svc)

	id, err := surface.CreateProduct(ctx, " Linen Shirt ", "")
	require.NoError(t, err)
	product, err := svc.GetProduct(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Linen Shirt", product.Title)
	assert.Equal(t, "linen-shirt", product.Handle, "the handle is the title's slug")
	assert.Equal(t, "draft", string(product.Status), "a new product is a draft")

	named, err := surface.CreateProduct(ctx, "Coffee", " house-blend ")
	require.NoError(t, err)
	product, err = svc.GetProduct(ctx, named)
	require.NoError(t, err)
	assert.Equal(t, "house-blend", product.Handle)

	_, err = surface.CreateProduct(ctx, "Another", "house-blend")
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err) || errors.IsInvalid(err), "a taken handle is refused: %v", err)

	medium, err := surface.AddVariant(ctx, id, " M ", " SH-M ")
	require.NoError(t, err)
	plain, err := surface.AddVariant(ctx, id, "L", "")
	require.NoError(t, err)
	variants, err := svc.ListVariants(ctx, service.ListVariantsOptions{ProductID: &id, Limit: 10})
	require.NoError(t, err)
	byID := map[string]string{}
	for i := range variants.Items {
		sku := ""
		if variants.Items[i].SKU != nil {
			sku = *variants.Items[i].SKU
		}
		byID[variants.Items[i].ID] = variants.Items[i].Title + "|" + sku
	}
	assert.Equal(t, map[string]string{medium: "M|SH-M", plain: "L|"}, byID)
	for i := range variants.Items {
		if variants.Items[i].ID == plain {
			assert.Nil(t, variants.Items[i].SKU, "an empty SKU is no SKU, not an empty one")
		}
	}
}
