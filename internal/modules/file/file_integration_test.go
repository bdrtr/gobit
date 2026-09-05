//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the DECISIONS of the service and of the handler with
// fakes. The tests here prove the GROUND those decisions stand on: that the
// file is REALLY written to disk, that the address REALLY works, that the
// served Content-Type comes from the row in the database, that deleting
// carries off both the row and the file, and that the uniqueness of the
// storage key sits in a real index and not in a fake map.
package file_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file"
	"github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

const postgresImage = "postgres:16-alpine"

// testMaxBytes is the size limit used in the integration tests.
const testMaxBytes int64 = 1 << 20

// pngContent is test content carrying a valid PNG signature.
//
// The signature must be REAL: the detection is done from the content and a
// made-up string would keep the upload from passing the allow list.
const pngContent = "\x89PNG\r\n\x1a\n" + "a body that is not real but is signed"

var (
	// testPool is the pool all the tests share.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs all the tests
// on it. It is a separate function because os.Exit skips defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)

		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)

		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)

		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN,
		file.New(file.Options{}).Migrations(), file.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)

		return 1
	}

	return m.Run()
}

// setUpModule sets up a module running on the REAL database and on a REAL root
// directory; it mounts the router too.
//
// The module's own Register is used: only that shows that the setup path (the
// provider registration, the creation of the root directory, the writing into
// the container) really works.
func setUpModule(t *testing.T) (*file.Module, chi.Router, string) {
	t.Helper()

	root := t.TempDir()

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := file.New(file.Options{
		Root:           root,
		MaxUploadBytes: testMaxBytes,
		AllowedTypes: []string{
			coreprovider.ContentTypeJPEG,
			coreprovider.ContentTypePNG,
			coreprovider.ContentTypeGIF,
			coreprovider.ContentTypeWebP,
		},
	})
	require.NoError(t, mod.Register(context.Background(), c))

	r := chi.NewRouter()
	mod.Routes(r)

	return mod, r, root
}

// adminPrincipal is the tests' default principal: a fully authorized admin
// user.
func adminPrincipal() corehttp.Principal {
	return corehttp.Principal{ID: "user_integration", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}
}

// upload makes a multipart upload request.
func upload(t *testing.T, r chi.Router, fileName, declaredType, content string) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	headers := make(textproto.MIMEHeader)
	headers.Set("Content-Disposition", `form-data; name="file"; filename="`+fileName+`"`)
	headers.Set("Content-Type", declaredType)

	part, err := writer.CreatePart(headers)
	require.NoError(t, err)
	_, err = part.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/uploads", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), adminPrincipal()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// uploadResponse holds the fields of a successful upload response.
type uploadResponse struct {
	Data struct {
		ID          string `json:"id"`
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
		Checksum    string `json:"checksum"`
		ProviderID  string `json:"provider_id"`
	} `json:"data"`
}

// decodeUpload decodes the response body.
func decodeUpload(t *testing.T, rec *httptest.ResponseRecorder) uploadResponse {
	t.Helper()

	var resp uploadResponse
	require.NoError(t, decodeJSON(rec.Body.Bytes(), &resp), "body: %s", rec.Body.String())

	return resp
}

// TestUploadServeDeleteEndToEnd walks a real consumer path from beginning to
// end.
//
// The chain is exactly the path the product image will follow: upload → call
// the returned address the way an <img> would → delete. Every step runs with
// REAL components; there is nothing fake.
func TestUploadServeDeleteEndToEnd(t *testing.T) {
	_, r, root := setUpModule(t)

	// 1) Upload.
	rec := upload(t, r, "product-front.png", coreprovider.ContentTypePNG, pngContent)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	resp := decodeUpload(t, rec)
	assert.Equal(t, coreprovider.ContentTypePNG, resp.Data.ContentType)
	assert.Equal(t, int64(len(pngContent)), resp.Data.Size)
	assert.Equal(t, local.ID, resp.Data.ProviderID)
	assert.NotEmpty(t, resp.Data.Checksum)

	// The file must REALLY be on the disk and INSIDE the root directory.
	key := filepath.Base(resp.Data.URL)
	diskPath := filepath.Join(root, key)
	onDisk, err := os.ReadFile(diskPath)
	require.NoError(t, err, "the uploaded file must be in the root directory")
	assert.Equal(t, pngContent, string(onDisk))

	// 2) Serving — the address must REALLY work and must NOT ASK for a
	// principal.
	served := httptest.NewRecorder()
	r.ServeHTTP(served, httptest.NewRequest(http.MethodGet, resp.Data.URL, http.NoBody))

	require.Equal(t, http.StatusOK, served.Code, "the address the upload produced must work")
	assert.Equal(t, coreprovider.ContentTypePNG, served.Header().Get("Content-Type"),
		"the Content-Type must be written from the STORED type")
	assert.Equal(t, "nosniff", served.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, pngContent, served.Body.String())

	// 3) Deleting — both the row and the file must go.
	del := deleteRequest(t, r, resp.Data.ID)
	require.Equal(t, http.StatusNoContent, del.Code)

	_, err = os.Stat(diskPath)
	assert.True(t, os.IsNotExist(err), "deleting must carry off the file too: %v", err)

	after := httptest.NewRecorder()
	r.ServeHTTP(after, httptest.NewRequest(http.MethodGet, resp.Data.URL, http.NoBody))
	assert.Equal(t, http.StatusNotFound, after.Code, "a deleted file must no longer be served")
	assert.Equal(t, "nosniff", after.Header().Get("X-Content-Type-Options"),
		"nosniff must be on EVERY response, on the error response too")
}

// TestDeleteIsIdempotent verifies that a second delete returns 204 on the real
// database as well.
func TestDeleteIsIdempotent(t *testing.T) {
	_, r, _ := setUpModule(t)

	rec := upload(t, r, "a.png", coreprovider.ContentTypePNG, pngContent)
	require.Equal(t, http.StatusCreated, rec.Code)
	id := decodeUpload(t, rec).Data.ID

	assert.Equal(t, http.StatusNoContent, deleteRequest(t, r, id).Code, "the first delete")
	assert.Equal(t, http.StatusNoContent, deleteRequest(t, r, id).Code, "the SECOND delete")
	assert.Equal(t, http.StatusNoContent, deleteRequest(t, r, "upl_NEVEREXISTED").Code,
		"an id that never existed satisfies the end state too")
}

// TestLyingContentTypeIsRejectedAndNotWrittenToDisk shows what an installation
// that does not validate the client's claim would look like.
//
// What is sent is an HTML file with an "image/png" header. The rejection is
// exercised on a REAL disk: the root directory must stay EMPTY — that is, the
// validation must happen before the write. Had it happened afterwards, every
// rejected file would need a delete call, and when that delete failed the file
// would stay in the store.
func TestLyingContentTypeIsRejectedAndNotWrittenToDisk(t *testing.T) {
	_, r, root := setUpModule(t)

	rec := upload(t, r, "fake.png", coreprovider.ContentTypePNG,
		"<html><body><script>alert(document.cookie)</script></body></html>")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rootContents(t, root), "a rejected file must NOT be written to disk at all")
}

// TestSVGIsRejectedAndNotWrittenToDisk verifies that SVG does not get through
// in the real flow either.
//
// An SVG looks like an image but it is a DOCUMENT: it can carry <script> and,
// when served from the same origin, it becomes stored XSS — the uploading user
// runs code in the session of everyone who opens the image.
func TestSVGIsRejectedAndNotWrittenToDisk(t *testing.T) {
	_, r, root := setUpModule(t)

	const svg = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert(document.cookie)</script></svg>`

	rec := upload(t, r, "logo.svg", "image/svg+xml", svg)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, rootContents(t, root))
}

// TestPathTraversalAttemptDoesNotWriteOutsideTheRoot verifies on a REAL file
// system that the client's file name is a path at no stage.
//
// The claim has two directions: nothing comes into being outside the root
// directory AND the only thing that comes into being inside the root directory
// is a file with a generated key.
func TestPathTraversalAttemptDoesNotWriteOutsideTheRoot(t *testing.T) {
	_, r, root := setUpModule(t)

	parent := filepath.Dir(root)
	parentBefore := dirContents(t, parent)

	rec := upload(t, r, "../../etc/passwd", coreprovider.ContentTypePNG, pngContent)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	contents := rootContents(t, root)
	require.Len(t, contents, 1, "there must be exactly one file in the root directory")
	assert.NotContains(t, contents[0], "passwd", "the key does not derive from the client's name")
	assert.Equal(t, filepath.Base(contents[0]), contents[0], "the key is not a PATH")

	assert.ElementsMatch(t, parentBefore, dirContents(t, parent),
		"nothing must come into being OUTSIDE the root directory")
}

// TestBodyExceedingTheSizeLimitIsRejected verifies that the limit is enforced
// in the real flow and that it leaves no trace on the disk.
//
// Had a half object been left behind, requests that exceed the limit could
// fill the disk EVEN THOUGH they are REJECTED: no record points at that file
// and no deletion path knows its key.
func TestBodyExceedingTheSizeLimitIsRejected(t *testing.T) {
	root := t.TempDir()

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	// The limit is kept small on purpose; producing a 1 MiB body would slow
	// the test down.
	mod := file.New(file.Options{
		Root:           root,
		MaxUploadBytes: 64,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, mod.Register(context.Background(), c))

	r := chi.NewRouter()
	mod.Routes(r)

	oversized := pngContent + string(bytes.Repeat([]byte("A"), 256))
	rec := upload(t, r, "large.png", coreprovider.ContentTypePNG, oversized)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, service.CodeTooLarge, errorCode(t, rec))
	assert.Empty(t, rootContents(t, root),
		"a body exceeding the limit must leave neither a half file nor a temporary file")
}

// TestStorageKeyIsUnique verifies that the constraint sits in a REAL index and
// not in a fake map.
//
// The uniqueness protects two things at once: deleting cannot carry off
// another record's file, and the serving path reaches the record from the key
// with a SINGLE row — since the served Content-Type is written from that row,
// the question "which row" must have exactly one answer.
func TestStorageKeyIsUnique(t *testing.T) {
	_, r, _ := setUpModule(t)

	rec := upload(t, r, "a.png", coreprovider.ContentTypePNG, pngContent)
	require.Equal(t, http.StatusCreated, rec.Code)
	key := filepath.Base(decodeUpload(t, rec).Data.URL)

	// Opening a second record with the same key requires bypassing the service
	// and writing directly to the store: because the key is produced by the
	// provider, no collision can arise in the normal flow. And what is being
	// exercised here is the LAST LINE OF DEFENCE anyway.
	_, err := testPool.Pool().Exec(context.Background(),
		`INSERT INTO file_uploads (id, storage_key, provider_id, content_type, size, checksum, url)
		 VALUES ($1, $2, 'local', 'image/png', 1, 'x', '/files/x')`,
		models.NewUploadID(time.Now()), key)

	require.Error(t, err, "a second record with the same storage key must not be possible")
}

// TestModuleRegistersWithTheDefaultProvider verifies that the out-of-the-box
// setup is complete: there is a provider even when there is no plugin at all,
// and the selected id finds it.
func TestModuleRegistersWithTheDefaultProvider(t *testing.T) {
	mod, _, root := setUpModule(t)

	assert.Equal(t, []string{local.ID}, mod.Providers().IDs())
	assert.Equal(t, file.DefaultProviderID, mod.Service().ProviderID())

	info, err := os.Stat(root)
	require.NoError(t, err, "the root directory must be prepared during Register")
	assert.True(t, info.IsDir())
}

// TestLocalProviderIsNotRegisteredWhenNoRootIsGiven verifies that a setup
// without a root directory does not FALL BACK to a TEMPORARY DIRECTORY.
//
// Had it fallen back, the installation would look like it "works", on the
// first restart every image would be lost and the addresses in the product
// records would stay where they are. Instead the provider is not registered at
// all; if it is the selected provider the startup stops at the composition
// root (see cmd/server verifyFileProvider).
func TestLocalProviderIsNotRegisteredWhenNoRootIsGiven(t *testing.T) {
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := file.New(file.Options{
		MaxUploadBytes: testMaxBytes,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, mod.Register(context.Background(), c),
		"not giving a root is not a setup ERROR; an installation that uploads no files is legitimate")

	assert.Empty(t, mod.Providers().IDs(), "a provider must NOT BE INVENTED with a temporary directory")

	_, err := mod.Providers().Get(local.ID)
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
}

// TestListReturnsTheUploads verifies that the ledger shows up in the admin
// list.
func TestListReturnsTheUploads(t *testing.T) {
	_, r, _ := setUpModule(t)

	require.Equal(t, http.StatusCreated,
		upload(t, r, "a.png", coreprovider.ContentTypePNG, pngContent).Code)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/uploads", http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), adminPrincipal()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	require.NoError(t, decodeJSON(rec.Body.Bytes(), &body))

	assert.GreaterOrEqual(t, body.Count, int64(1))
	require.NotEmpty(t, body.Data)
	assert.Equal(t, "user_integration", body.Data[0]["uploaded_by"],
		"the uploader's id must be written into the record")
}

// deleteRequest makes a delete request with a principal.
func deleteRequest(t *testing.T, r chi.Router, id string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodDelete, "/admin/v1/uploads/"+id, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), adminPrincipal()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// rootContents returns the file names in the root directory.
func rootContents(t *testing.T, root string) []string {
	t.Helper()

	return dirContents(t, root)
}

// dirContents returns the entry names in the given directory.
func dirContents(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// decodeJSON decodes the body into the target.
func decodeJSON(raw []byte, target any) error { return json.Unmarshal(raw, target) }

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
