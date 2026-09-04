package api_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/file/api"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// testMaxBytes is the size limit used in the handler tests.
const testMaxBytes int64 = 64

// fakeUploads is the scriptable counterpart of api.Uploads.
//
// A REAL service is not used: what is being exercised is the handler's
// decisions — multipart parsing, content type detection, serving headers,
// status mapping. The service's own decisions (the allow list, the size, the
// order of the delete) are exercised in its own package and repeating them here
// would be holding the same claim in two places.
type fakeUploads struct {
	mu sync.Mutex

	// seenTypes are the content types that arrived at Upload; the correctness
	// of the detection is proven with these.
	seenTypes []string
	// seenNames are the client file names that arrived at Upload.
	seenNames []string
	// seenBodies are the bytes that FLOWED into Upload.
	seenBodies []string
	// deleted are the identifiers of the DeleteUpload calls.
	deleted []string

	// uploadErr, when given, is the error Upload returns.
	uploadErr error
	// deleteErr, when given, is the error DeleteUpload returns.
	deleteErr error
	// opened is the file OpenByKey will return.
	opened service.OpenedFile
	// openErr, when given, is the error OpenByKey returns.
	openErr error
	// records are the records ListUploads will return.
	records []models.Upload
}

// That the fake satisfies the surface the handler expects is pinned at compile
// time.
var _ api.Uploads = (*fakeUploads)(nil)

// Upload reads the body and returns a fake record.
//
// The body is REALLY read: the handler reading the first 512 bytes and putting
// them back in front of the stream can only be verified if the bytes flow all
// the way to the end.
func (f *fakeUploads) Upload(_ context.Context, in service.UploadInput) (models.Upload, error) {
	raw, err := io.ReadAll(in.Body)
	if err != nil {
		return models.Upload{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	f.seenTypes = append(f.seenTypes, in.ContentType)
	f.seenNames = append(f.seenNames, in.OriginalName)
	f.seenBodies = append(f.seenBodies, string(raw))

	if f.uploadErr != nil {
		return models.Upload{}, f.uploadErr
	}

	return models.Upload{
		ID:           "upl_TEST",
		StorageKey:   "GENERATEDKEY0123456789.png",
		ProviderID:   "local",
		ContentType:  in.ContentType,
		Size:         int64(len(raw)),
		Checksum:     "digest",
		OriginalName: in.OriginalName,
		URL:          "/files/GENERATEDKEY0123456789.png",
		UploadedBy:   in.UploadedBy,
		CreatedAt:    time.Unix(0, 0).UTC(),
	}, nil
}

// ListUploads returns the fake records.
func (f *fakeUploads) ListUploads(
	_ context.Context, _ service.Page,
) ([]models.Upload, int64, error) {
	return f.records, int64(len(f.records)), nil
}

// DeleteUpload records the delete call.
func (f *fakeUploads) DeleteUpload(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.deleted = append(f.deleted, id)

	return f.deleteErr
}

// OpenByKey returns the fake file.
func (f *fakeUploads) OpenByKey(_ context.Context, _ string) (service.OpenedFile, error) {
	if f.openErr != nil {
		return service.OpenedFile{}, f.openErr
	}

	return f.opened, nil
}

// MaxUploadBytes returns the size limit.
func (f *fakeUploads) MaxUploadBytes() int64 { return testMaxBytes }

// types returns the content types that arrived at Upload.
func (f *fakeUploads) types() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.seenTypes...)
}

// names returns the client file names that arrived at Upload.
func (f *fakeUploads) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.seenNames...)
}

// bodies returns the bytes that flowed into Upload.
func (f *fakeUploads) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.seenBodies...)
}

// nopCloser adds an empty Close to a reader.
type nopCloser struct {
	*strings.Reader
}

// Close satisfies io.Closer and does nothing.
func (nopCloser) Close() error { return nil }

// openedFile produces a file ready to be served with the given type and
// content.
func openedFile(contentType, content string) service.OpenedFile {
	return service.OpenedFile{
		Upload:  models.Upload{ContentType: contentType, StorageKey: "K.png"},
		Content: nopCloser{strings.NewReader(content)},
		ModTime: time.Unix(1_700_000_000, 0).UTC(),
	}
}

// newRouter builds a router working on the fake service.
func newRouter(svc *fakeUploads) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r
}

// admin is the default identity of the tests: a fully scoped admin user.
//
// The router is built DIRECTLY here, so corehttp.RequireAdmin is not in the
// chain and nobody puts an identity into the context; since the admin endpoints
// are protected with corehttp.RequireScope, an identity-less request would
// return 401 and the behavior the test really verifies would never be reached.
func admin() corehttp.Principal {
	return corehttp.Principal{ID: "user_test", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}
}

// multipartBody builds a multipart body carrying a single file.
//
// The Content-Type of the part comes FROM THE CALLER and may lie on purpose:
// what most of the tests exercise is exactly that claim being ignored.
func multipartBody(
	t *testing.T, field, fileName, declaredType, content string,
) (body io.Reader, contentType string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Disposition",
		`form-data; name="`+field+`"; filename="`+fileName+`"`)
	if declaredType != "" {
		headers.Set("Content-Type", declaredType)
	}

	part, err := writer.CreatePart(headers)
	require.NoError(t, err)

	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return &buf, writer.FormDataContentType()
}

// upload makes an upload request with the given body.
func upload(t *testing.T, r chi.Router, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/uploads", body)
	req.Header.Set("Content-Type", contentType)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), admin()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// request makes a request carrying an identity.
func request(t *testing.T, r chi.Router, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), admin()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// errorCode returns the machine code in the response body.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, decodeJSON(rec.Body.Bytes(), &body), "body: %s", rec.Body.String())

	return body.Error.Code
}

// notFound produces a typed NotFound error.
func notFound() error {
	return coreerrors.NotFound("file_upload_not_found", "the upload was not found")
}
