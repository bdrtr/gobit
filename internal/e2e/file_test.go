//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	filesvc "github.com/bdrtr/gobit/internal/modules/file/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
)

// This file proves ONE chain end to end:
//
//	upload -> URL -> product image
//
// # Why this can only be proved here
//
// The two ends of the chain live in TWO SEPARATE modules: file takes the bytes
// and produces a URL, product stores that URL on the product record. The two do
// not import each other and must not (Principle 2.1), so no unit test can ever
// see both at once. file's own integration test says "a URL was produced" and
// stops there; product's test says "the URL I gave came back" and never asks
// whether that URL actually returns anything. Exactly this failure can live in
// the gap between them: the upload works, the product is saved, and the <img>
// in the storefront renders broken.
//
// # Why the serving endpoint is called WITH NO HEADERS
//
// What calls the URL is a browser's image request, and a browser CANNOT add a
// custom header to an <img> request: neither Authorization nor a publishable
// key. The test not adding one is therefore not a convenience but the scenario
// itself ([fetchURL] sets no header at all). If the endpoint were moved under a
// protected prefix this file would go red — which is what is wanted.
//
// # What is proved
//
//  1. A PNG is uploaded through the admin endpoint; it returns 201 and the
//     response carries a URL.
//  2. That URL REALLY works: the same bytes, the STORED content type and
//     X-Content-Type-Options: nosniff.
//  3. The URL can be used as a product image, and the URL read back from the
//     product record still returns content.
//  4. An unauthenticated upload gets 401.
//  5. Text sent with a lying Content-Type is REJECTED and nothing is written to
//     disk.
//  6. A deleted upload's URL no longer returns content.

// uploadPath is the admin upload endpoint.
//
// The path is written out BY HAND rather than read from the file/api package:
// the constant there is unexported and that is exactly as it should be — what
// the client sees is the string itself. If the path changes this test gets a
// 404, and that is correct: a published endpoint cannot change its path
// silently.
const uploadPath = "/admin/v1/uploads"

// uploadFileField is the field name the file is expected under in the multipart
// body.
//
// The field name, like the path, is the contract ON THE WIRE; it is written out
// by hand for the same reason.
const uploadFileField = "file"

// nosniffHeader is the header that turns off the browser's content type
// guessing.
const nosniffHeader = "X-Content-Type-Options"

// uploadView is the test-side counterpart of the upload response.
//
// The type is not copied from file/api's DTO, it is WRITTEN OUT: that type is
// unexported and that is exactly as it should be. The field names here are the
// JSON contract the client sees; a rename must show up in the test.
type uploadView struct {
	// ID is the record's identifier; the delete endpoint takes it.
	ID string `json:"id"`
	// URL is the file's reachable address — the middle link of the chain.
	URL string `json:"url"`
	// ContentType is the STORED type, that is, the one DETECTED from the content.
	ContentType string `json:"content_type"`
	// Size is the file's size in bytes.
	Size int64 `json:"size"`
	// Checksum is the SHA-256 digest of the content.
	Checksum string `json:"checksum"`
	// ProviderID is the identifier of the provider that stores the file.
	ProviderID string `json:"provider_id"`
	// OriginalName is the name the client reported; it is display only.
	OriginalName string `json:"original_name"`
	// UploadedBy is the identity of the caller that performed the upload.
	UploadedBy string `json:"uploaded_by"`
}

// productImagesView is the part of the product response this test cares about.
//
// The whole of models.Product is not decoded: this test's claim is not about
// the shape of the catalog but ONLY about the image URL, and decoding fields it
// does not need would make an unrelated catalog change break this file.
type productImagesView struct {
	// ID is the product's identifier.
	ID string `json:"id"`
	// Images are the product's images.
	Images []struct {
		// ID is the image record's identifier.
		ID string `json:"id"`
		// URL is the image's address; the URL the upload produced goes in here.
		URL string `json:"url"`
	} `json:"images"`
}

// pngContent produces a REAL PNG.
//
// A hand-written byte sequence ("\x89PNG..." + garbage) would pass type
// detection too, but this test's claim is not "the magic bytes are recognized",
// it is "a real image makes it through end to end unharmed". An image that goes
// through the encoder also produces a body whose size and content cannot be
// known in advance, so the "the same bytes came back" claim exercises a real
// file rather than a fixed string.
//
// The image is DELIBERATELY not a single color: a single color compresses in
// PNG so well that the body would shrink to a few bytes and the "the same
// bytes" claim would fall almost empty.
func pngContent(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			img.Set(x, y, color.RGBA{
				R: uint8(x * 16),
				G: uint8(y * 16),
				B: uint8((x ^ y) * 8),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img), "could not encode the fixture PNG")
	require.NotEmpty(t, buf.Bytes(), "the fixture PNG must not be empty")

	return buf.Bytes()
}

// multipartUploadBody builds a single-file multipart body; it returns the body
// and the request's Content-Type header.
//
// The part's Content-Type is a PARAMETER and that is the crux of this test: the
// type the client reports is a CLAIM and it may be a lie. That is the only
// reason this helper does not use [multipart.Writer.CreateFormFile] — that
// method always writes the type as "application/octet-stream" and cannot lie,
// so it cannot set up the situation we want to exercise.
func multipartUploadBody(t *testing.T, fileName, claimedType string, content []byte) (body []byte, contentType string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name=%q; filename=%q`, uploadFileField, fileName))
	header.Set("Content-Type", claimedType)

	part, err := writer.CreatePart(header)
	require.NoError(t, err, "could not open the multipart part")

	_, err = part.Write(content)
	require.NoError(t, err, "could not write the multipart part")
	require.NoError(t, writer.Close(), "could not close the multipart body")

	return buf.Bytes(), writer.FormDataContentType()
}

// postUpload makes a multipart request to the upload endpoint.
//
// If auth is given EMPTY the Authorization header is NOT added at all; "no
// header" and "empty header" are different situations and the 401 claim targets
// the first (see [adminRequest], the same distinction).
func postUpload(t *testing.T, auth, fileName, claimedType string, content []byte) *httptest.ResponseRecorder {
	t.Helper()

	body, contentType := multipartUploadBody(t, fileName, claimedType, content)

	req := httptest.NewRequest(http.MethodPost, uploadPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// uploadFile uploads a file with the secret key and decodes the record that
// comes back.
func uploadFile(t *testing.T, fileName, claimedType string, content []byte) uploadView {
	t.Helper()

	rec := postUpload(t, "Bearer "+secretKey, fileName, claimedType, content)
	require.Equal(t, http.StatusCreated, rec.Code,
		"the upload must return 201; body: %s", rec.Body.String())

	var envelope struct {
		Data uploadView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"could not decode the upload response; body: %s", rec.Body.String())

	return envelope.Data
}

// fetchURL calls an upload URL SENDING NO HEADERS AT ALL.
//
// The absence of headers is deliberate and is the scenario itself: what calls
// this URL is an <img> tag in the storefront and a browser cannot attach
// credentials to it (package doc).
func fetchURL(t *testing.T, address string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, address, http.NoBody)
	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// errorCode returns the MACHINE code in an error response.
//
// The claim binds to this code rather than to the status code: the status is a
// coarse signal derived from the error CLASS (422 can mean both "type not
// allowed" and "malformed body"), whereas what the client actually branches on
// is the code.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"could not decode the error response; body: %s", rec.Body.String())

	return envelope.Error.Code
}

// filesInStore returns the file names under the upload root.
//
// The reason for looking at the disk level is concrete: "the request was
// rejected" and "nothing was written to disk" are NOT THE SAME THING. A
// rejected upload can leave a half-written file (or a temporary file that was
// never cleaned up) behind, and seeing that from the HTTP response is
// impossible — a file with no record has no URL either, so no endpoint can be
// asked about it.
func filesInStore(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(fileRoot)
	require.NoError(t, err, "could not read the upload root: %s", fileRoot)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// createProductWithImage creates a product with the given image URL and returns
// the record.
//
// The product is set up through the ADMIN ENDPOINT, not through the SERVICE;
// the last link of the chain lives precisely in HTTP: the URL the upload
// returned is put into a product creation body by a client. Calling the service
// directly would be the test skipping that link with its own hand.
//
// The image is supplied WHILE the product is being created because the product
// module has no separate image endpoint: the way in is the "images" field (see
// product/api createProductRequest).
func createProductWithImage(t *testing.T, imageURL string) productImagesView {
	t.Helper()

	seq := fixtureCounter.Add(1)
	rec, err := adminRequestWithBody(http.MethodPost, "/admin/v1/products", map[string]any{
		"handle": fmt.Sprintf("e2e-product-with-image-%d", seq),
		"title":  "Product With Image",
		"status": string(productmodels.StatusPublished),
		"images": []map[string]any{{"url": imageURL, "rank": 0}},
	})
	require.NoError(t, err, "could not build the product request")
	require.Equal(t, http.StatusCreated, rec.Code,
		"the product must return 201; body: %s", rec.Body.String())

	return decodeProductView(t, rec)
}

// decodeProductView decodes the envelope of a product response.
func decodeProductView(t *testing.T, rec *httptest.ResponseRecorder) productImagesView {
	t.Helper()

	var envelope struct {
		Data productImagesView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"could not decode the product response; body: %s", rec.Body.String())

	return envelope.Data
}

// TestUploadedImageIsUsableAsProductImage proves the WHOLE chain in a single
// flow.
//
// The steps are deliberately in ONE test: what binds them is the data itself
// (the URL the upload produced), and splitting them into separate tests would
// require a cross-test global to share that URL — that is, taking the very
// thing that proves the chain out of the chain.
func TestUploadedImageIsUsableAsProductImage(t *testing.T) {
	content := pngContent(t)

	var upload uploadView

	t.Run("upload returns 201 and carries a URL", func(t *testing.T) {
		upload = uploadFile(t, "landscape.png", "image/png", content)

		require.NotEmpty(t, upload.URL,
			"the response must carry a URL; an upload record without one is unusable anywhere")
		assert.NotEmpty(t, upload.ID, "the identifier the delete endpoint takes must be in the response")
		assert.Equal(t, "image/png", upload.ContentType,
			"the stored type must have been detected FROM THE CONTENT")
		assert.Equal(t, int64(len(content)), upload.Size,
			"the recorded size must match the body that was sent; if it differs the stream was cut off somewhere")
		assert.NotEmpty(t, upload.Checksum, "the content digest must be recorded")
		assert.Equal(t, "landscape.png", upload.OriginalName,
			"the client's name must be stored for DISPLAY")
		assert.NotEmpty(t, upload.UploadedBy,
			"an upload coming through the protected endpoint must carry the caller's identity")
		assert.NotEmpty(t, upload.ProviderID, "the provider that stores the file must be known")
	})

	require.NotEmpty(t, upload.URL, "precondition: the later steps cannot run without a URL")

	t.Run("the URL really works", func(t *testing.T) {
		rec := fetchURL(t, upload.URL)

		require.Equal(t, http.StatusOK, rec.Code,
			"the upload's URL must return content; body: %s", rec.Body.String())
		assert.Equal(t, content, rec.Body.Bytes(),
			"the returned bytes must be EXACTLY the ones that were uploaded")
		assert.Equal(t, upload.ContentType, rec.Header().Get("Content-Type"),
			"Content-Type must be written from the STORED type, not from the client's claim")
		assert.Equal(t, "nosniff", rec.Header().Get(nosniffHeader),
			"without nosniff the browser looks at the content and guesses for itself despite the type we sent")
	})

	var product productImagesView

	t.Run("the URL can be used as a product image", func(t *testing.T) {
		product = createProductWithImage(t, upload.URL)

		require.Len(t, product.Images, 1, "the product must have been created with a single image")
		assert.Equal(t, upload.URL, product.Images[0].URL,
			"the URL on the product record must be THE VERY SAME URL the upload produced")
	})

	require.NotEmpty(t, product.ID, "precondition: the last step cannot run without a product")

	// The last link: the URL is READ BACK from the product record and that
	// read-back URL is the one called. Calling the URL from the upload response
	// again would not be enough — it never exercises the part of the chain that
	// goes through product, and the test would stay green even if the product
	// record stored the URL truncated (or with an escape character added).
	t.Run("the URL on the product record still returns content", func(t *testing.T) {
		fetched := adminRequest(t, http.MethodGet, "/admin/v1/products/"+product.ID,
			"Bearer "+secretKey)
		require.Equal(t, http.StatusOK, fetched.Code,
			"the product must be readable; body: %s", fetched.Body.String())

		persisted := decodeProductView(t, fetched)
		require.Len(t, persisted.Images, 1, "the image must be persisted")
		require.Equal(t, upload.URL, persisted.Images[0].URL,
			"the URL on the persisted record must match the upload's URL")

		rec := fetchURL(t, persisted.Images[0].URL)
		require.Equal(t, http.StatusOK, rec.Code,
			"the URL the product carries must return content; body: %s", rec.Body.String())
		assert.Equal(t, content, rec.Body.Bytes(),
			"the bytes the storefront will see must be the ones that were uploaded")
	})
}

// TestUnauthenticatedUploadIsRejected verifies that the upload endpoint is
// PROTECTED.
//
// The serving endpoint being unprotected is a deliberate trade-off (file/api
// package doc), and exactly for that reason it must be proved separately that
// the WRITE path stays protected: together the two make the sentence "whoever
// knows the URL reads, but only whoever has an identity writes to the store".
// If the upload endpoint were left unprotected, the unprotected serving
// endpoint would turn into a file sharing service.
func TestUnauthenticatedUploadIsRejected(t *testing.T) {
	before := filesInStore(t)

	rec := postUpload(t, "", "sneaky.png", "image/png", pngContent(t))

	assert.Equal(t, http.StatusUnauthorized, rec.Code,
		"an unauthenticated upload must get 401; body: %s", rec.Body.String())
	assert.Equal(t, before, filesInStore(t),
		"a rejected request must write NOTHING to the store")
}

// TestTextSentWithLyingContentTypeIsRejected proves that the allow list looks
// at the file's CONTENT and not at the client's CLAIM.
//
// The scenario is the unprotected serving endpoint at its most dangerous: if an
// HTML file sent as "image/png" were accepted, serving it from the same origin
// would be stored XSS. An allow list that trusts the client's Content-Type
// filters nothing — because the attacker writes that header too.
//
// Both bodies are exercised and the distinction is meaningful: plain text
// answers the question "does the allow list reject a type it does not know",
// while HTML answers the question "does the genuinely dangerous type get
// through".
func TestTextSentWithLyingContentTypeIsRejected(t *testing.T) {
	cases := []struct {
		name         string
		file         string
		content      []byte
		expectedType string
	}{
		{
			name:         "plain text",
			file:         "image.png",
			content:      []byte("this is a text file, not an image.\n"),
			expectedType: "text/plain",
		},
		{
			name:    "HTML",
			file:    "image.png",
			content: []byte("<html><body><script>alert(1)</script></body></html>"),
			// The detected type DOES NOT HAVE TO appear in the message; here it
			// only makes what the scenario is readable.
			expectedType: "text/html",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := filesInStore(t)

			// The client says "png" both in the header and in the file name;
			// both are CLAIMS and both are lies.
			rec := postUpload(t, "Bearer "+secretKey, tc.file, "image/png", tc.content)

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
				"%s content must be rejected; body: %s", tc.expectedType, rec.Body.String())
			assert.Equal(t, filesvc.CodeTypeNotAllowed, errorCode(t, rec),
				"the rejection reason must be the allow list; another code means the file fell "+
					"over for some other reason and that the type check may never have run")
			assert.Equal(t, before, filesInStore(t),
				"rejected content must not be written to the store; not even a half-written or temporary file may be left")
		})
	}
}

// TestDeletedUploadURLServesNothing verifies that a delete REALLY takes the
// file away.
//
// An implementation that deletes only the record would also return 204 and the
// record would disappear from the admin listing; the file would stay on disk
// and everyone who knows its URL would go on reading it. That is the worst lie
// a system that says "deleted" can tell, because the person who thinks they
// deleted it never looks again.
func TestDeletedUploadURLServesNothing(t *testing.T) {
	content := pngContent(t)
	upload := uploadFile(t, "to-delete.png", "image/png", content)

	require.Equal(t, http.StatusOK, fetchURL(t, upload.URL).Code,
		"precondition: the URL must work BEFORE the delete; if it does not, the test would "+
			"exercise a file that never existed rather than the delete")

	// That the file is on disk BEFORE the delete is asserted separately. Saying
	// only "it is gone afterwards" would prove nothing: a lookup whose name
	// never matched says "gone" too, and the claim would quietly fall empty.
	key := storeKeyFromURL(upload.URL)
	require.Contains(t, filesInStore(t), key,
		"precondition: the uploaded file must be in the store before the delete")

	deleted := adminRequest(t, http.MethodDelete, uploadPath+"/"+upload.ID,
		"Bearer "+secretKey)
	require.Equal(t, http.StatusNoContent, deleted.Code,
		"the delete must return 204; body: %s", deleted.Body.String())

	rec := fetchURL(t, upload.URL)

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"the deleted upload's URL must return 404; body: %s", rec.Body.String())
	assert.NotEqual(t, content, rec.Body.Bytes(),
		"the deleted file's bytes must not come back under any circumstances")
	assert.Equal(t, "nosniff", rec.Header().Get(nosniffHeader),
		"nosniff must be present on ERROR responses too; since the header is written on the "+
			"first line, its absence means the whole serving endpoint is left unprotected")
	assert.NotContains(t, filesInStore(t), key,
		"the delete must take the file off DISK too; deleting only the record would mean "+
			"someone who knows the URL goes on reading it")
}

// storeKeyFromURL returns the last segment of an upload URL, that is, the store
// key.
//
// The key is not published in the response (file/api dto doc) and that is
// right; deriving it from the URL so the disk can be inspected is the one place
// this test leans on the LOCAL provider. In a setup that moves to an object
// store the URL is signed and this line loses its meaning — on that day the
// right answer is not to fix the line but to drop the disk claim altogether.
func storeKeyFromURL(address string) string {
	for i := len(address) - 1; i >= 0; i-- {
		if address[i] == '/' {
			return address[i+1:]
		}
	}

	return address
}
