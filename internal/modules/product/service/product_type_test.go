package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The rollback half of the delete is NOT proved here: this package's fake store
// runs InTx without rolling anything back, so a test of "both writes or
// neither" against it would pass whatever the service did. It is proved on a
// real database instead — see the product module's integration tests.

// TestDeletingATypeReleasesItsProducts is the rule that costs MONEY when it is
// missing.
//
// `type_id` carries ON DELETE SET NULL and that clause cannot fire against a
// SOFT delete, so without this the products would keep naming a deleted type —
// and a tax rate rule matches on the type, which means they would keep being
// taxed by a rule the merchant believes they removed.
func TestDeletingATypeReleasesItsProducts(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, nil, nil)

	productType, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Book"})
	require.NoError(t, err)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "book-one",
		Title:  "A Book",
		Status: models.StatusPublished,
		TypeID: &productType.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, created.TypeID, "the product was created carrying its type")

	require.NoError(t, svc.DeleteProductType(ctx, productType.ID))

	after, err := store.GetProduct(ctx, created.ID)
	require.NoError(t, err)
	assert.Nil(t, after.TypeID, "a deleted type leaves no product pointing at it")
}

// TestDeletingATypeLeavesOtherTypesAlone keeps the release from being a sweep.
func TestDeletingATypeLeavesOtherTypesAlone(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, nil, nil)

	book, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Book"})
	require.NoError(t, err)
	shirt, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Shirt"})
	require.NoError(t, err)

	kept, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "a-shirt", Title: "A Shirt", Status: models.StatusPublished, TypeID: &shirt.ID,
	})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteProductType(ctx, book.ID))

	after, err := store.GetProduct(ctx, kept.ID)
	require.NoError(t, err)
	require.NotNil(t, after.TypeID, "a product of another type is untouched")
	assert.Equal(t, shirt.ID, *after.TypeID)
}

// TestAnUnknownTypeIsNotFound keeps the delete from reporting success on an id
// that never existed.
func TestAnUnknownTypeIsNotFound(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), nil, nil)

	err := svc.DeleteProductType(ctx, "ptype_does_not_exist")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err))
}

// TestATypeGetsAHandleFromItsValue is the collection's rule, and a type follows
// it because a merchant names both.
func TestATypeGetsAHandleFromItsValue(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), nil, nil)

	productType, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{
		Value: "Printed Book",
	})
	require.NoError(t, err)

	assert.Equal(t, "printed-book", productType.Handle)
	assert.True(t, strings.HasPrefix(productType.ID, "ptype_"),
		"the prefix is what makes an id readable without looking at a table")
}

// TestATypeWithoutAValueIsRefused keeps an unnamed row out of a merchant's list.
func TestATypeWithoutAValueIsRefused(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, newMemStore(), nil, nil)

	_, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "   "})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestTheTypeIsPublishedOnTheReadLayer is the hop the tax rule depends on.
//
// The cart reads the product's type through the Query provider, so a type that
// the provider does not publish would leave the tax request empty however well
// the merchant filled the catalog.
func TestTheTypeIsPublishedOnTheReadLayer(t *testing.T) {
	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, nil, nil)

	productType, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Book"})
	require.NoError(t, err)
	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "read-layer-book", Title: "A Book",
		Status: models.StatusPublished, TypeID: &productType.ID,
	})
	require.NoError(t, err)

	records, err := service.NewProductProvider(store).FetchByIDs(ctx,
		[]string{created.ID}, []string{"id", "type_id"})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, productType.ID, records[0]["type_id"],
		"the read layer has to publish the type the cart asks for")
}
