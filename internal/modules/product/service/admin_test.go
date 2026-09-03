package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
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

	err = surface.UpdateProductBasics(context.Background(), created.ID, "Coffee", "coffee", "on-sale")

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
		context.Background(), first.ID, "Filter Coffee", "filter-coffee", "published"))

	assert.NotEmpty(t, bus.byName(service.EventProductUpdated),
		"a write through the admin surface must publish the module's own event; "+
			"otherwise a subscriber keeps serving the old record")

	// The handle check belongs to the service too: reaching the repository
	// would let the second product take the first one's handle.
	err = surface.UpdateProductBasics(context.Background(), second.ID, "Tea", "filter-coffee", "draft")

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
		context.Background(), created.ID, "  Filter Coffee  ", " filter-coffee ", "published"))

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

	err := surface.UpdateProductBasics(context.Background(), "prod_1", "Coffee", "coffee", "draft")

	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}
