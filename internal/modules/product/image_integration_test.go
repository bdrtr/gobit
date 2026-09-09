//go:build integration

// The tests here run against a real PostgreSQL; to run them:
// make test-integration
//
// What they cover is the half a fake cannot: the COALESCE patch and the
// two-identifier WHERE clause are SQL, and the in-memory store enforces both
// from Go. A fake that imitates a rule cannot say whether the rule is in the
// query (D50).
package product_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// productWithTwoImages creates a product carrying two images.
func productWithTwoImages(ctx context.Context, t *testing.T, svc *service.Service) models.Product {
	t.Helper()

	product, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("images"),
		Title:  "Shirt",
		Status: models.StatusPublished,
		Images: []service.CreateImageInput{
			{URL: "https://cdn.example/1.png", AltText: "the front"},
			{URL: "https://cdn.example/2.png", AltText: "the back"},
		},
	})
	require.NoError(t, err)
	require.Len(t, product.Images, 2)

	return product
}

// TestTheImagePatchTouchesOnlyWhatItNames is the COALESCE, read off the real
// query.
//
// A field passed as NULL does not change. If the query wrote every column from
// its parameters, a patch naming only the rank would blank the alt text — and
// blanking it is not even an error, because the empty string is a legal value
// for that column.
func TestTheImagePatchTouchesOnlyWhatItNames(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product := productWithTwoImages(ctx, t, svc)

	rank := int32(7)
	patched, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{Rank: &rank})
	require.NoError(t, err)

	assert.Equal(t, int32(7), patched.Rank)
	assert.Equal(t, "the front", patched.AltText, "the text was not named and did not change")
	assert.Equal(t, "https://cdn.example/1.png", patched.URL, "and the address is not patchable")
	assert.True(t, patched.UpdatedAt.After(product.Images[0].UpdatedAt),
		"the stamp comes from the database")
}

// TestAnEmptyAltTextReachesTheColumn is the value the COALESCE could have
// eaten.
//
// `COALESCE(narg, alt_text)` keeps the old value when the parameter is NULL —
// and the empty string is NOT null. A patch carrying "" therefore has to land,
// because "" is HTML's own word for a decorative image and a real answer.
func TestAnEmptyAltTextReachesTheColumn(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product := productWithTwoImages(ctx, t, svc)

	empty := ""
	patched, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{AltText: &empty})
	require.NoError(t, err)
	assert.Empty(t, patched.AltText)

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 2)
	assert.Empty(t, read.Images[0].AltText, "and it is what the row holds")
}

// TestTheImageQueriesCarryBothIdentifiers is the WHERE clause.
//
// Every image write names the product AND the image. With only the image's id
// in the clause a caller could patch or delete another product's picture by
// naming a product of their own, and the answer would be a success.
func TestTheImageQueriesCarryBothIdentifiers(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	mine := productWithTwoImages(ctx, t, svc)
	theirs := productWithTwoImages(ctx, t, svc)

	text := "not mine to write"
	_, err := svc.UpdateProductImage(ctx, mine.ID, theirs.Images[0].ID,
		service.UpdateImageInput{AltText: &text})
	require.Error(t, err, "the patch")
	assert.True(t, errors.IsNotFound(err), "got %v", err)

	err = svc.RemoveProductImage(ctx, mine.ID, theirs.Images[0].ID)
	require.Error(t, err, "the removal")
	assert.True(t, errors.IsNotFound(err), "got %v", err)

	read, err := svc.GetProduct(ctx, theirs.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 2, "the other product keeps both pictures")
	assert.Equal(t, "the front", read.Images[0].AltText, "with its own text")
}

// TestARemovedImageIsStampedAndNotErased reads the row itself.
//
// The delete is SOFT, which is exactly the word that makes this worth reading
// directly: through the service a stamped row and a row that never existed look
// the same.
func TestARemovedImageIsStampedAndNotErased(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product := productWithTwoImages(ctx, t, svc)

	require.NoError(t, svc.RemoveProductImage(ctx, product.ID, product.Images[0].ID))

	var stamped, living int64
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM product_image WHERE id = $1 AND deleted_at IS NOT NULL`,
		product.Images[0].ID).Scan(&stamped))
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT count(*) FROM product_image WHERE product_id = $1 AND deleted_at IS NULL`,
		product.ID).Scan(&living))

	assert.Equal(t, int64(1), stamped, "the row is stamped, not erased")
	assert.Equal(t, int64(1), living, "and the other picture is untouched")

	// A second removal is NOT FOUND rather than a silent success.
	err := svc.RemoveProductImage(ctx, product.ID, product.Images[0].ID)
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestTheDeleteCountsTheRowsItChanged goes AROUND the service to reach the
// check that answers when two removals race.
//
// Through the service the second removal is refused by the READ that precedes
// it, so calling it twice proves the read and says nothing about the write. The
// gap between the two is real under READ COMMITTED: two operators pressing
// remove at once both pass the read, and the second UPDATE matches no row.
// Without the row count that call would report a success for a deletion it did
// not perform.
func TestTheDeleteCountsTheRowsItChanged(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product := productWithTwoImages(ctx, t, svc)

	store := repository.New(testPool.Pool())

	require.NoError(t, store.SoftDeleteImage(ctx, product.ID, product.Images[0].ID))

	err := store.SoftDeleteImage(ctx, product.ID, product.Images[0].ID)
	require.Error(t, err, "the row was already stamped; nothing changed")
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestAnImageAddedLaterLandsLastOnTheRealListing pins the rank against the
// query that orders the listing.
func TestAnImageAddedLaterLandsLastOnTheRealListing(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	product := productWithTwoImages(ctx, t, svc)

	added, err := svc.AddProductImage(ctx, product.ID, service.CreateImageInput{
		URL: "https://cdn.example/3.png", AltText: "the sleeve",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), added.Rank)

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 3)
	assert.Equal(t, added.ID, read.Images[2].ID, "ordered by rank, then id")
}
