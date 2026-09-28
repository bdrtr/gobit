package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestAWriteAskedOnAnOlderVersionIsRefused is ADR 0222: a write asked on a
// version the product has moved past is refused before it writes anything, one
// asked on the current version goes through, and the version after it is
// reported — for a write whose answer is not the product too.
func TestAWriteAskedOnAnOlderVersionIsRefused(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	product := seedProduct(t, svc, "kettle", "Kettle")
	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Kettle, steel")})
	require.NoError(t, err)

	_, err = svc.UpdateProduct(service.ExpectVersion(ctx, 1), product.ID, service.UpdateProductInput{Title: ptr("Stale")})
	require.Error(t, err)
	assert.True(t, errors.IsPreconditionFailed(err))
	assert.Equal(t, service.CodeVersionMismatch, errors.CodeOf(err))
	stored, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	assert.Equal(t, "Kettle, steel", stored.Title, "nothing was written")
	assert.Equal(t, int64(2), stored.Version)

	asked, read := service.WithVersionSink(service.ExpectVersion(ctx, 2))
	_, err = svc.CreateVariant(asked, product.ID, service.CreateVariantInput{Title: "Large"})
	require.NoError(t, err)
	version, ok := read()
	require.True(t, ok)
	assert.Equal(t, int64(3), version, "the version after the write")

	unchanged, read := service.WithVersionSink(ctx)
	_, err = svc.UpdateProduct(unchanged, product.ID, service.UpdateProductInput{Title: ptr("Kettle, steel")})
	require.NoError(t, err)
	version, ok = read()
	require.True(t, ok)
	assert.Equal(t, int64(3), version, "a write that changed nothing leaves the version where it was")
}

// TestAProductWrittenBeforeRevisionsIsAtVersionZero: a product the migration
// found is asked on version 0.
func TestAProductWrittenBeforeRevisionsIsAtVersionZero(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	product := seedProduct(t, svc, "teapot", "Teapot")
	store.revisions = nil
	stored := store.products[product.ID]
	stored.Version = 0
	store.products[product.ID] = stored

	ctx, read := service.WithVersionSink(service.ExpectVersion(context.Background(), 0))
	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Teapot, glass")})

	require.NoError(t, err)
	version, _ := read()
	assert.Equal(t, int64(2), version, "what it was is revision 1, the write revision 2")
}

// TestThePanelsSaveIsAskedOnTheVersionItWasReadAt: the admin surface forwards
// the form's version.
func TestThePanelsSaveIsAskedOnTheVersionItWasReadAt(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	surface := service.NewAdminSurface(svc)
	product := seedProduct(t, svc, "cup", "Cup")

	err := surface.UpdateProductBasics(context.Background(), product.ID, "Cup", "cup", "draft", product.Version+1)

	assert.True(t, errors.IsPreconditionFailed(err))
}

// TestTheReadLayerCarriesTheVersion: the panel reads the product through the
// read layer, and its edit form sends the version back (ADR 0222).
func TestTheReadLayerCarriesTheVersion(t *testing.T) {
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), &fakeGraph{})
	ctx := context.Background()
	product := seedProduct(t, svc, "saucer", "Saucer")
	_, err := svc.UpdateProduct(ctx, product.ID, service.UpdateProductInput{Title: ptr("Saucer, white")})
	require.NoError(t, err)

	records, err := service.NewProductProvider(store).List(ctx, query.ListOptions{Fields: []string{"id", "version"}})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, int64(2), records[0]["version"])
}
