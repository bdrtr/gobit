package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// pngContent is the test content carrying a valid PNG signature.
//
// The signature being real is A MUST: the detection is done from the content
// and a made-up string would make the detection claim untestable.
const pngContent = "\x89PNG\r\n\x1a\n" + "body bytes"

// decodeJSON decodes the body into the target.
func decodeJSON(raw []byte, target any) error { return json.Unmarshal(raw, target) }

// TestUploadDoesNotUseTheClientFileNameASAPATH is the task's first security
// claim.
//
// The client sends the name "../../etc/passwd". The name enters the ledger as
// DISPLAY data but the storage key and the address take no interest in it at
// all: the key is produced by the provider. That is, path traversal is
// prevented not by "sanitizing" but — STRUCTURALLY — by the name never entering
// any path expression.
//
// # Why the recorded name is "passwd"
//
// [mime/multipart.Part.FileName] runs the name through filepath.Base as RFC
// 7578 §4.2 requires; the directory components fall away before they even reach
// us. The test records that AS IT IS but DOES NOT REST its claim on it: the
// real claims are that the key and the address do not derive from the client's
// name. A design that leans on this behavior of the stdlib would collapse
// silently on the first change that takes the name from another channel (e.g.
// from a JSON field).
func TestUploadDoesNotUseTheClientFileNameASAPATH(t *testing.T) {
	t.Parallel()

	const badName = "../../etc/passwd"

	svc := &fakeUploads{}
	body, contentType := multipartBody(t, "file", badName, coreprovider.ContentTypePNG, pngContent)

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	var response struct {
		Data struct {
			URL          string `json:"url"`
			OriginalName string `json:"original_name"`
			ContentType  string `json:"content_type"`
			Size         int64  `json:"size"`
			ID           string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, decodeJSON(rec.Body.Bytes(), &response))

	assert.Equal(t, "passwd", response.Data.OriginalName,
		"the name is stored for DISPLAY; what is avoided is not storing the name but USING IT AS A PATH")
	assert.NotContains(t, response.Data.OriginalName, "..",
		"the multipart layer already drops the directory components (RFC 7578)")
	assert.NotContains(t, response.Data.URL, "..", "the address does not derive from the client's name")
	assert.NotContains(t, response.Data.URL, "passwd")
	assert.Equal(t, "/files/GENERATEDKEY0123456789.png", response.Data.URL)

	// The response fields the task asks for have to be complete.
	assert.NotEmpty(t, response.Data.ID)
	assert.Equal(t, coreprovider.ContentTypePNG, response.Data.ContentType)
	assert.Equal(t, int64(len(pngContent)), response.Data.Size)

	assert.Equal(t, []string{"passwd"}, svc.names(),
		"the name passes to the service as DATA; the provider's contract has no name field anyway")
	assert.Contains(t, badName, "..", "the test really has to send a path traversal attempt")
}

// TestTheContentTypeIsNotASKEDOFTheClient is the second security claim.
//
// The client sends a text file that LIES by saying "image/png". Because the
// detection is done from the content, the type that reaches the service is
// "text/plain" and the allow list rejects it. A list that trusted the client's
// header would filter out nothing: with the same trick an HTML file would enter
// the storage and would run in the browser when it was served.
func TestTheContentTypeIsNotASKEDOFTheClient(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{
		uploadErr: coreerrors.Invalid(service.CodeTypeNotAllowed,
			"the %q content type is not accepted", "text/plain"),
	}
	body, contentType := multipartBody(t, "file", "fake.png", coreprovider.ContentTypePNG,
		"<html><script>alert(1)</script></html>")

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, service.CodeTypeNotAllowed, errorCode(t, rec))

	require.Len(t, svc.types(), 1)
	assert.NotEqual(t, coreprovider.ContentTypePNG, svc.types()[0],
		"the client's header is a CLAIM; the type passed to the service has to come from the content")
	assert.True(t, strings.HasPrefix(svc.types()[0], "text/"),
		"detected type: %s", svc.types()[0])
}

// TestSVGIsRejected shows end to end that SVG cannot pass the allow list.
//
// Two layers are exercised at once: the detection never returns
// "image/svg+xml" for an SVG (DetectContentType sees it as XML or as plain
// text) and the allow list does not know those types either. An SVG looks like
// an image but it is a DOCUMENT: it can carry <script> and, when it is served
// from the same origin, it becomes stored XSS.
func TestSVGIsRejected(t *testing.T) {
	t.Parallel()

	const svg = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert(document.cookie)</script></svg>`

	svc := &fakeUploads{
		uploadErr: coreerrors.Invalid(service.CodeTypeNotAllowed,
			"the content type is not accepted"),
	}
	body, contentType := multipartBody(t, "file", "logo.svg", "image/svg+xml", svg)

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, service.CodeTypeNotAllowed, errorCode(t, rec))

	require.Len(t, svc.types(), 1)
	assert.NotEqual(t, "image/svg+xml", svc.types()[0],
		"DetectContentType DOES NOT RETURN image/svg+xml for an SVG; the client's name must not be carried over")
}

// TestABodyExceedingTheSizeLimitIsRejected verifies that the limit is enforced
// in the HTTP layer as well.
//
// The MaxBytesReader wrapping the body cuts the read chain in the middle and
// the error comes back wrapped from inside the multipart parser or from inside
// the service; the handler recognizes it BY ITS TYPE.
//
// The response is 422, not 413: the handler does not choose the status code
// (plan Section 2.7), it derives from the class of the error and the core's set
// of classes has no counterpart for 413. What the client is going to branch on
// is the machine code anyway.
func TestABodyExceedingTheSizeLimitIsRejected(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{}
	// The body HAS TO EXCEED the MaxBytesReader limit (the file limit plus the
	// envelope allowance); had it not exceeded it, what was being exercised
	// would be the fake service's behavior and not the handler.
	large := pngContent + strings.Repeat("A", 16<<10)
	body, contentType := multipartBody(t, "file", "large.png", coreprovider.ContentTypePNG, large)

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, service.CodeTooLarge, errorCode(t, rec))
}

// TestTheFirst512BytesArePUTBACKIntoTheStream verifies that the bytes read for
// the detection are not lost.
//
// The handler reads the head of the body to find the content type. Had they not
// been put back with io.MultiReader, every file larger than 512 bytes would be
// recorded WITH ITS HEAD MISSING — and that would only be noticed once the
// image failed to open, after the file had been declared "uploaded
// successfully".
func TestTheFirst512BytesArePUTBACKIntoTheStream(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{}
	// A body LONGER than 512 is a must: on a file that stays below the sniff
	// limit it would not even be noticed that the "put back" step never ran.
	content := pngContent + strings.Repeat("B", 600)
	body, contentType := multipartBody(t, "file", "long.png", coreprovider.ContentTypePNG, content)

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.Len(t, svc.bodies(), 1)
	assert.Equal(t, content, svc.bodies()[0],
		"the bytes flowing to the service have to be EXACTLY the ones that were sent")
	assert.Equal(t, coreprovider.ContentTypePNG, svc.types()[0],
		"the detection has to be made from the signature in front of the bytes that were put back")
}

// TestAnUnexpectedFieldIsRejected verifies that the single-file contract is
// enforced.
func TestAnUnexpectedFieldIsRejected(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{}
	body, contentType := multipartBody(t, "document", "a.png", coreprovider.ContentTypePNG, pngContent)

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.types(), "the service must not be reached at all")
}

// TestAJSONBodyIsRejected verifies that the endpoint expects multipart.
//
// "application/json" IS NOT WRITTEN in the schema either (see describe.go): had
// it been, the generated client would try to send the file in a JSON body and
// every request would fall into the error here.
func TestAJSONBodyIsRejected(t *testing.T) {
	t.Parallel()

	rec := upload(t, newRouter(&fakeUploads{}),
		strings.NewReader(`{"url":"https://example/a.png"}`), "application/json")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestAnEmptyFileIsRejected verifies that a zero-byte upload is not accepted.
func TestAnEmptyFileIsRejected(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{}
	body, contentType := multipartBody(t, "file", "empty.png", coreprovider.ContentTypePNG, "")

	rec := upload(t, newRouter(svc), body, contentType)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.types(), "the service must not be reached at all")
}

// TestServingWritesNOSNIFFAndTheSTOREDType is the task's serving claim.
//
// The two headers are exercised together because without one the other is not
// enough: the Content-Type is written from the stored type, but without nosniff
// the browser looks at the content and makes its own guess, and a file stored
// as "image/png" that looks like HTML could be executed as HTML. That is why
// the content is deliberately chosen to be HTML.
func TestServingWritesNOSNIFFAndTheSTOREDType(t *testing.T) {
	t.Parallel()

	const htmlLike = "<html><body><script>alert(1)</script></body></html>"

	svc := &fakeUploads{opened: openedFile(coreprovider.ContentTypePNG, htmlLike)}

	rec := request(t, newRouter(svc), http.MethodGet, "/files/GENERATEDKEY0123456789.png")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, coreprovider.ContentTypePNG, rec.Header().Get("Content-Type"),
		"the Content-Type has to be written from the STORED type")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, htmlLike, rec.Body.String())
	assert.Empty(t, rec.Header().Get("Content-Disposition"),
		"the client's file name must not be written into ANY HEADER")
}

// TestServingCarriesNOSNIFFOnErrorResponsesToo verifies that the header is on
// EVERY response.
//
// Had the "on every response" rule been applied only to the successful
// response, the 404 body (the JSON error envelope) would stay open to the
// browser's guessing. A rule that is cheap and absolute must have no exception.
func TestServingCarriesNOSNIFFOnErrorResponsesToo(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{openErr: notFound()}

	rec := request(t, newRouter(svc), http.MethodGet, "/files/MISSINGMISSINGMISSING00000.png")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestTheServingEndpointASKSFORNoScope pins that the unprotected prefix is
// deliberate.
//
// The <img> tag on the storefront can send neither an Authorization header nor
// a publishable key; had the endpoint been put under a protected prefix, every
// uploaded image would return 401 on the storefront. The test verifies that an
// identity-less request REALLY passes — this blows up if the decision is ever
// changed silently.
func TestTheServingEndpointASKSFORNoScope(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{opened: openedFile(coreprovider.ContentTypePNG, "bytes")}
	r := newRouter(svc)

	// The identity IS NOT PUT into the context: that is exactly what the
	// browser does on an <img> request.
	req := httptest.NewRequest(http.MethodGet, "/files/GENERATEDKEY0123456789.png", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"the serving endpoint asks for no identity; if it did, the storefront images would return 401")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestTheAdminEndpointsREQUIREAScope verifies that the protection is really
// plugged in.
//
// The authentication (corehttp.RequireAdmin) is plugged in on the side that
// builds the router; the claim here is the SCOPE layer: an admin user whose
// scopes have been emptied is a valid identity too, and without this layer it
// could upload and delete files.
func TestTheAdminEndpointsREQUIREAScope(t *testing.T) {
	t.Parallel()

	r := newRouter(&fakeUploads{})
	unscoped := corehttp.Principal{ID: "user_x", Kind: "user", Scopes: []string{}}

	endpoints := map[string]struct{ method, path string }{
		"listing":  {http.MethodGet, "/admin/v1/uploads"},
		"deleting": {http.MethodDelete, "/admin/v1/uploads/upl_1"},
	}

	for name, endpoint := range endpoints {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(endpoint.method, endpoint.path, http.NoBody)
			req = req.WithContext(corehttp.WithPrincipal(req.Context(), unscoped))

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}

// TestDeleteISIDEMPOTENT verifies that a second delete also returns 204.
//
// The service is idempotent and the handler reflects that as it is: a delete is
// a claim about an END STATE and a retried cleanup flow must not get an error on
// its second round.
func TestDeleteISIDEMPOTENT(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{}
	r := newRouter(svc)

	first := request(t, r, http.MethodDelete, "/admin/v1/uploads/upl_TEST")
	second := request(t, r, http.MethodDelete, "/admin/v1/uploads/upl_TEST")

	assert.Equal(t, http.StatusNoContent, first.Code)
	assert.Equal(t, http.StatusNoContent, second.Code, "the SECOND delete has to return 204 as well")
	assert.Empty(t, first.Body.String(), "a 204 has no body")
	assert.Equal(t, []string{"upl_TEST", "upl_TEST"}, svc.deleted)
}

// TestTheListComesBackInAnEnvelope pins the shape of the list response.
func TestTheListComesBackInAnEnvelope(t *testing.T) {
	t.Parallel()

	svc := &fakeUploads{}
	uploaded, err := svc.Upload(t.Context(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(pngContent),
	})
	require.NoError(t, err)
	svc.records = append(svc.records, uploaded)

	rec := request(t, newRouter(svc), http.MethodGet, "/admin/v1/uploads")

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
		Limit int64            `json:"limit"`
	}
	require.NoError(t, decodeJSON(rec.Body.Bytes(), &body))

	require.Len(t, body.Data, 1)
	assert.Equal(t, int64(1), body.Count)
	assert.Equal(t, service.DefaultLimit, body.Limit,
		"the envelope has to report the limit the service APPLIED")
	assert.NotContains(t, body.Data[0], "storage_key",
		"the storage key is not published; the only thing the client needs is the address")
	assert.Contains(t, body.Data[0], "url")
}
