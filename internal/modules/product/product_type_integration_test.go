//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: the release of a type's products
// happening in the SAME transaction as the delete, and the constraints the
// schema holds on its own. This package's unit fake runs InTx without rolling
// anything back, so "both writes or neither" can only be asked here.
package product_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// typedProduct writes a type and a product wearing it.
func typedProduct(
	ctx context.Context, t *testing.T, svc *service.Service, handle string,
) (models.ProductType, models.Product) {
	t.Helper()

	productType, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{
		Value: "Book " + handle,
	})
	require.NoError(t, err)

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: handle,
		Title:  "A Book",
		Status: models.StatusPublished,
		TypeID: &productType.ID,
	})
	require.NoError(t, err)

	return productType, product
}

// TestATypeSurvivesTheRoundTripOnTheRealSchema proves the column is written and
// read back, and that a product may carry none.
func TestATypeSurvivesTheRoundTripOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	productType, product := typedProduct(ctx, t, svc, uniqueHandle("typed"))

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.NotNil(t, read.TypeID)
	assert.Equal(t, productType.ID, *read.TypeID)

	untyped, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("untyped"), Title: "No type", Status: models.StatusPublished,
	})
	require.NoError(t, err)
	assert.Nil(t, untyped.TypeID, "a product with no type carries NULL, not an empty string")
}

// TestDeletingATypeReleasesItsProductsOnTheRealSchema is the statement
// ON DELETE SET NULL cannot make.
//
// The clause is on the column and it never fires here: the delete is a soft one,
// so the type's row stays physically in place and the database never runs the
// action the schema promises. Only SQL of our own can keep it.
func TestDeletingATypeReleasesItsProductsOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	productType, product := typedProduct(ctx, t, svc, uniqueHandle("released"))

	require.NoError(t, svc.DeleteProductType(ctx, productType.ID))

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err, "the product is untouched apart from its type")
	assert.Nil(t, read.TypeID,
		"a soft-deleted type must leave no product pointing at it, or a tax rule "+
			"written for that type keeps charging")
}

// TestADeletedTypeLeavesBothTheListingAndTheCount is the collection's claim for
// the type, and it is one claim rather than two.
//
// A listing that hides the row while the counter still counts it tells a client
// there are three types, hands it two, and sends it paging after a third that no
// read will ever return.
func TestADeletedTypeLeavesBothTheListingAndTheCount(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	first, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Book"})
	require.NoError(t, err)
	_, err = svc.CreateProductType(ctx, service.CreateProductTypeInput{Value: "Shirt"})
	require.NoError(t, err)

	before, err := svc.ListProductTypes(ctx, 50, 0)
	require.NoError(t, err)
	require.Len(t, before.Items, 2)
	require.NotNil(t, before.Count)
	require.Equal(t, 2, *before.Count)

	require.NoError(t, svc.DeleteProductType(ctx, first.ID))

	after, err := svc.ListProductTypes(ctx, 50, 0)
	require.NoError(t, err)
	assert.Len(t, after.Items, 1, "the deleted type leaves the listing")
	require.NotNil(t, after.Count)
	assert.Equal(t, 1, *after.Count, "and it leaves the count with it")
}

// TestADeletedTypeFreesItsHandle proves the partial unique index is the shape
// every other taxonomy row takes.
func TestADeletedTypeFreesItsHandle(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	handle := uniqueHandle("freed")
	first, err := svc.CreateProductType(ctx, service.CreateProductTypeInput{
		Value: "Book", Handle: handle,
	})
	require.NoError(t, err)

	_, err = svc.CreateProductType(ctx, service.CreateProductTypeInput{
		Value: "Other", Handle: handle,
	})
	require.Error(t, err, "a live handle is taken")

	require.NoError(t, svc.DeleteProductType(ctx, first.ID))

	_, err = svc.CreateProductType(ctx, service.CreateProductTypeInput{
		Value: "Other", Handle: handle,
	})
	require.NoError(t, err, "a deleted type's handle is free again")
}

// TestAnImageKeepsItsAltTextOnTheRealSchema proves the column round-trips and
// that an image written without one carries the empty string rather than NULL.
//
// The default is the half a fake cannot show: every image written before the
// column existed gets ”, which is exactly what is true of them — nobody said
// what they show.
func TestAnImageKeepsItsAltTextOnTheRealSchema(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("alt-text"),
		Title:  "A Product",
		Status: models.StatusPublished,
		Images: []service.CreateImageInput{
			{URL: "https://cdn.example/described.jpg", AltText: "A red mug", Rank: 0},
			{URL: "https://cdn.example/decorative.jpg", Rank: 1},
		},
	})
	require.NoError(t, err)

	read, err := svc.GetProduct(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 2)

	assert.Equal(t, "A red mug", read.Images[0].AltText)
	assert.Equal(t, "", read.Images[1].AltText,
		"the column is NOT NULL, so an undescribed image reads back as empty, "+
			"never as a null the caller has to handle")
}
