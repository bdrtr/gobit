package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// This file is inside the package: the row -> model conversion is not exported.
// What the tests below defend is the SILENT half of a column — a value that is
// written and then dropped on the way back gives no error anywhere, it just
// makes the question unanswerable again.

// TestToImageCarriesTheUploadBack verifies that the upload id survives the row
// to model conversion.
//
// The column is the forward half of the image/upload binding: it is what
// answers "which upload is this image" in the same row read, without a join and
// without the link service. A conversion that forgot the field would leave
// every read looking exactly like the state before the column existed — the
// write would succeed, the API would return nothing, and no error would be
// raised anywhere along the way.
func TestToImageCarriesTheUploadBack(t *testing.T) {
	t.Parallel()

	uploadID := "upl_1"
	img, err := toImage(productdb.ProductImage{
		ID:        "pimg_1",
		ProductID: "prod_1",
		Url:       "https://cdn.example/1.png",
		Rank:      2,
		Metadata:  []byte(`{}`),
		UploadID:  &uploadID,
	})
	require.NoError(t, err)

	require.NotNil(t, img.UploadID)
	assert.Equal(t, "upl_1", *img.UploadID)
}

// TestToImageKeepsTheMissingUploadNil verifies that "no upload" stays nil.
//
// A NULL column means the image points at an address this installation never
// uploaded, and that is a legitimate image (an imported catalog, a hand-typed
// CDN address). Turning NULL into an empty string here would produce a third
// state that the database's own CHECK forbids, and callers would have to test
// for both.
func TestToImageKeepsTheMissingUploadNil(t *testing.T) {
	t.Parallel()

	img, err := toImage(productdb.ProductImage{
		ID:        "pimg_1",
		ProductID: "prod_1",
		Url:       "https://cdn.example/1.png",
		Metadata:  []byte(`{}`),
	})
	require.NoError(t, err)

	assert.Nil(t, img.UploadID)
}
