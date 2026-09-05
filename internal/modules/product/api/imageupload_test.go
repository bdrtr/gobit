package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestCreateProductCarriesTheUploadIdIntoTheInput verifies that the id does not
// stop at the HTTP boundary.
//
// The DTO and the service input are separate types on purpose, and that
// separation is where a new field silently goes missing: the body parses, the
// request succeeds, and the image is written without its upload. Nothing would
// report it — the response looks exactly like a correct one.
func TestCreateProductCarriesTheUploadIdIntoTheInput(t *testing.T) {
	t.Parallel()

	var gotUploadID string
	catalog := &fakeCatalog{
		createProduct: func(_ context.Context, in service.CreateProductInput) (models.Product, error) {
			require.Len(t, in.Images, 1)
			gotUploadID = in.Images[0].UploadID
			return models.Product{ID: "prod_1"}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodPost, "/admin/v1/products",
		`{"title":"Shirt","images":[{"url":"https://cdn.example/1.png","upload_id":"upl_1"}]}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	assert.Equal(t, "upl_1", gotUploadID)
}

// TestImagesOfUploadReadsTheIdFromThePath verifies the reverse read endpoint.
//
// The upload id is the ADDRESS of what is being asked about, not a filter; if
// it were read from the query string an operator who forgot it would get the
// whole catalog's images instead of an error.
func TestImagesOfUploadReadsTheIdFromThePath(t *testing.T) {
	t.Parallel()

	var gotUploadID string
	catalog := &fakeCatalog{
		imagesOfUpload: func(_ context.Context, uploadID string) ([]models.Image, error) {
			gotUploadID = uploadID
			return []models.Image{{ID: "pimg_1", ProductID: "prod_1", URL: "https://cdn.example/1.png"}}, nil
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/product-images/by-upload/upl_1", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "upl_1", gotUploadID)

	data, ok := decodeBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "upl_1", data["upload_id"], "the body has to stand on its own, without the request URL beside it")

	images, ok := data["images"].([]any)
	require.True(t, ok)
	require.Len(t, images, 1)
	image, ok := images[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "prod_1", image["product_id"],
		"the record carries WHERE the file is used; an id list would need one request per image to find that out")
}

// TestImagesOfUploadAnswersAnUnusedUploadWithAnEmptyArray verifies the shape of
// the "nothing uses it" answer.
//
// It must be "[]" and not "null": the client treats the field as an array every
// time. It must also be a 200 and not a 404 — the catalog cannot see the file
// module's records, so "no image uses this" and "there is no such upload" are
// the same answer from here, and claiming the second would be a statement this
// module has no way to make.
func TestImagesOfUploadAnswersAnUnusedUploadWithAnEmptyArray(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		imagesOfUpload: func(context.Context, string) ([]models.Image, error) { return nil, nil },
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/product-images/by-upload/upl_UNUSED", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"images":[]`, "body: %s", rec.Body.String())
}

// TestImagesOfUploadKeepsTheErrorClass verifies that the service's typed error
// decides the status code.
func TestImagesOfUploadKeepsTheErrorClass(t *testing.T) {
	t.Parallel()

	catalog := &fakeCatalog{
		imagesOfUpload: func(context.Context, string) ([]models.Image, error) {
			return nil, coreerrors.Unavailable("product_service_not_ready", "no link service in this setup")
		},
	}

	rec := do(t, newRouter(catalog), http.MethodGet, "/admin/v1/product-images/by-upload/upl_1", "")
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "body: %s", rec.Body.String())
}
