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

// productWithImages creates a product carrying the given image addresses.
func productWithImages(
	t *testing.T, svc *service.Service, handle string, urls ...string,
) models.Product {
	t.Helper()

	images := make([]service.CreateImageInput, 0, len(urls))
	for _, url := range urls {
		images = append(images, service.CreateImageInput{URL: url})
	}

	product, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Handle: handle,
		Title:  "Shirt",
		Status: models.StatusPublished,
		Images: images,
	})
	require.NoError(t, err)

	return product
}

// TestAnImageCanBeAddedAfterTheProductExists is the write path the table did
// not have.
//
// Until this existed [service.Service.CreateProduct] was the only writer of
// product_image: a picture added later meant deleting the product and writing
// it again.
func TestAnImageCanBeAddedAfterTheProductExists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	product := productWithImages(t, svc, "shirt", "https://cdn.example/1.png")

	added, err := svc.AddProductImage(ctx, product.ID, service.CreateImageInput{
		URL: "https://cdn.example/2.png", AltText: "  the back  ",
	})
	require.NoError(t, err)
	assert.Equal(t, product.ID, added.ProductID)
	assert.Equal(t, "the back", added.AltText, "the text is trimmed")

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 2)
}

// TestAnAddedImageLandsLast pins where a picture with no rank goes.
//
// A zero rank means "not given", as it does in the create body, and zero itself
// would put the new picture FIRST — the one position nobody means by "add an
// image".
func TestAnAddedImageLandsLast(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	product := productWithImages(t, svc, "shirt",
		"https://cdn.example/1.png", "https://cdn.example/2.png")

	added, err := svc.AddProductImage(ctx, product.ID, service.CreateImageInput{
		URL: "https://cdn.example/3.png",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), added.Rank, "one past the highest rank the product carried")

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 3)
	assert.Equal(t, added.ID, read.Images[2].ID, "and it is last in the listing")
}

// TestAnAddedImageKeepsAChosenRank is the other half: a caller who says where
// it goes is obeyed.
func TestAnAddedImageKeepsAChosenRank(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	product := productWithImages(t, svc, "shirt",
		"https://cdn.example/1.png", "https://cdn.example/2.png")

	added, err := svc.AddProductImage(ctx, product.ID, service.CreateImageInput{
		URL: "https://cdn.example/0.png", Rank: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), added.Rank)
}

// TestAnImageCannotBeAddedToAProductThatIsNotThere keeps a picture from hanging
// off nothing.
func TestAnImageCannotBeAddedToAProductThatIsNotThere(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	_, err := svc.AddProductImage(context.Background(), "prod_NEVER_EXISTED",
		service.CreateImageInput{URL: "https://cdn.example/1.png"})

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestAWrongAltTextCanBeCorrected is what ADR 0104 left open.
//
// The text was published on the storefront and in the GraphQL type and could
// not be changed, so a mistake a screen reader reads out lasted as long as the
// product did.
func TestAWrongAltTextCanBeCorrected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	product := productWithImages(t, svc, "shirt", "https://cdn.example/1.png")

	wrong := "a red hat"
	corrected := "  a blue shirt  "

	_, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{AltText: &wrong})
	require.NoError(t, err)

	patched, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{AltText: &corrected})
	require.NoError(t, err)
	assert.Equal(t, "a blue shirt", patched.AltText, "the text is trimmed")

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	require.Len(t, read.Images, 1)
	assert.Equal(t, "a blue shirt", read.Images[0].AltText)
}

// TestAnEmptyAltTextIsAValueAndNotAnAbsence is the distinction the PATCH
// contract could have eaten.
//
// A field left out does not change; an empty STRING is HTML's own word for a
// decorative image, so it has to reach the column. A patch that treated the two
// alike would leave a decorative picture stuck with somebody's stale caption.
func TestAnEmptyAltTextIsAValueAndNotAnAbsence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	product := productWithImages(t, svc, "shirt", "https://cdn.example/1.png")

	described := "a shirt on a hanger"
	_, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{AltText: &described})
	require.NoError(t, err)

	empty := ""
	cleared, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{AltText: &empty})
	require.NoError(t, err)
	assert.Empty(t, cleared.AltText, "an empty text clears it on purpose")

	// And a patch that names another field leaves the text alone.
	rank := int32(3)
	kept, err := svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{Rank: &rank})
	require.NoError(t, err)
	assert.Empty(t, kept.AltText)
	assert.Equal(t, int32(3), kept.Rank)
}

// TestAPatchWithNoFieldIsRefused keeps a call that changes nothing from
// reporting a change.
func TestAPatchWithNoFieldIsRefused(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	product := productWithImages(t, svc, "shirt", "https://cdn.example/1.png")

	_, err := svc.UpdateProductImage(context.Background(), product.ID,
		product.Images[0].ID, service.UpdateImageInput{})

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestAnAltTextCannotBeADescription bounds the column, on BOTH write paths.
//
// A limit on one of two ways into a column is not a limit, so the create body
// passes through the same check.
func TestAnAltTextCannotBeADescription(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	tooLong := strings.Repeat("x", 1025)

	_, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: "shirt", Title: "Shirt", Status: models.StatusPublished,
		Images: []service.CreateImageInput{{URL: "https://cdn.example/1.png", AltText: tooLong}},
	})
	require.Error(t, err, "the create path")
	assert.True(t, errors.IsInvalid(err), "got %v", err)

	product := productWithImages(t, svc, "hat", "https://cdn.example/1.png")
	_, err = svc.UpdateProductImage(ctx, product.ID, product.Images[0].ID,
		service.UpdateImageInput{AltText: &tooLong})
	require.Error(t, err, "the patch path")
	assert.True(t, errors.IsInvalid(err), "got %v", err)
}

// TestOneProductCannotReachAnothersImage is the identifier pair.
//
// Every image query carries the product's id AND the image's. Addressing an
// image by its own id alone would let a caller name a product of their own and
// edit somebody else's picture, and the answer would look like a success.
func TestOneProductCannotReachAnothersImage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	mine := productWithImages(t, svc, "mine", "https://cdn.example/1.png")
	theirs := productWithImages(t, svc, "theirs", "https://cdn.example/2.png")

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
	require.Len(t, read.Images, 1, "and the other product still has its picture")
	assert.Empty(t, read.Images[0].AltText)
}

// TestRemovingAnImageReleasesItsUploadBinding is the cleanup an operator
// depends on.
//
// The reverse read answers "which images use this file", and an image no
// storefront shows must stop saying yes — otherwise the file can never be
// removed. The FILE itself is untouched: it belongs to the file module and may
// back another product's image.
func TestRemovingAnImageReleasesItsUploadBinding(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))
	product := productWithUpload(t, svc, "shirt", "upl_1")
	require.Len(t, product.Images, 1)

	assert.Equal(t, []string{product.Images[0].ID},
		links.linked(service.LinkUploadProductImage, "upl_1"),
		"the binding is there before the removal")

	require.NoError(t, svc.RemoveProductImage(ctx, product.ID, product.Images[0].ID))

	assert.Empty(t, links.linked(service.LinkUploadProductImage, "upl_1"),
		"and the file stops answering that it is in use")

	read, err := svc.GetProduct(ctx, product.ID)
	require.NoError(t, err)
	assert.Empty(t, read.Images)
}

// TestAddingAnImageVerifiesItsUpload keeps an id that names no file out, by the
// create path's rule.
func TestAddingAnImageVerifiesItsUpload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	links := newFakeLinker()
	store := newMemStore()
	svc := newServiceWithUploads(t, store, links, newFakeUploads("upl_1"))
	product := productWithImages(t, svc, "shirt", "https://cdn.example/1.png")

	_, err := svc.AddProductImage(ctx, product.ID, service.CreateImageInput{
		URL: "https://cdn.example/2.png", UploadID: "upl_NOT_THERE",
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "got %v", err)

	added, err := svc.AddProductImage(ctx, product.ID, service.CreateImageInput{
		URL: "https://cdn.example/3.png", UploadID: "upl_1",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{added.ID},
		links.linked(service.LinkUploadProductImage, "upl_1"),
		"and the one that exists gets its binding")
}
