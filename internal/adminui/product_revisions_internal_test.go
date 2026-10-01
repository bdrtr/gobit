package adminui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// fakeHistory answers a product's revisions as scripted and records each
// listing and restore it is asked for.
type fakeHistory struct {
	fakeProductWriter
	body       string
	total      int64
	pages      [][2]int32
	restored   []string
	dropped    []string
	restoreErr error
}

func (f *fakeHistory) RevisionsJSON(_ context.Context, _ string, limit, offset int32) (json.RawMessage, int64, error) {
	f.pages = append(f.pages, [2]int32{limit, offset})
	return json.RawMessage(f.body), f.total, nil
}

func (f *fakeHistory) RestoreRevision(_ context.Context, productID string, revision, version int64) ([]string, error) {
	f.restored = append(f.restored, fmt.Sprintf("%s|%d|%d", productID, revision, version))
	return f.dropped, f.restoreErr
}

// threeRevisions is a product's history at version 3.
const threeRevisions = `[
	{"version":3,"recorded_at":"2026-09-30T12:00:00Z","changed":["title"],"request_id":"req_3"},
	{"version":2,"recorded_at":"2026-09-29T12:00:00Z","changed":["handle","title"],"request_id":null},
	{"version":1,"recorded_at":"2026-09-28T12:00:00Z","changed":[],"request_id":"req_1"}]`

// historyPanel is a panel whose catalog holds the product at version 3.
func historyPanel(t *testing.T, products ProductWriter) *UI {
	t.Helper()

	panel := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{fieldID: "prod_1", fieldTitle: "Linen shirt", fieldVersion: int64(3)}},
	}})
	panel.products = products

	return panel
}

// historyRequest sends one request through the history's routes.
func historyRequest(panel *UI, method, path string, form url.Values, scopes ...string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Get(ProductRevisionsPath, panel.showRevisions)
	r.Post(ProductRevisionRestorePath, panel.restoreRevision)

	request := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(corehttp.WithPrincipal(request.Context(),
		corehttp.Principal{ID: "user_1", Kind: "user", Scopes: scopes}))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, request)

	return rec
}

// TestAProductsHistoryListsItsRevisions is ADR 0316: the revisions newest
// first with what each changed and the request that made it, the first one
// marked, a job's said, and a restore form on every revision but the current
// one, carrying the version the page was read at, for a writer only.
func TestAProductsHistoryListsItsRevisions(t *testing.T) {
	t.Parallel()

	history := &fakeHistory{body: threeRevisions, total: 30}
	panel := historyPanel(t, history)

	rec := historyRequest(panel, http.MethodGet, ProductsPath+"/prod_1/revisions?page=2", nil,
		scopeProductRead, scopeProductWrite)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	body := rec.Body.String()
	assert.Contains(t, body, "History of Linen shirt")
	assert.Contains(t, body, "the product is at version 3")
	assert.Contains(t, body, "<td>handle, title</td>")
	assert.Contains(t, body, `<span class="muted">first</span>`)
	assert.Contains(t, body, "<code>req_3</code>")
	assert.Contains(t, body, `<span class="muted">a job</span>`)
	assert.Contains(t, body, "2026-09-29 12:00:00")
	assert.Contains(t, body, `action="`+ProductsPath+`/prod_1/revisions/2/restore"`)
	assert.Contains(t, body, `action="`+ProductsPath+`/prod_1/revisions/1/restore"`)
	assert.NotContains(t, body, "/revisions/3/restore", "the current revision is not restored")
	assert.Contains(t, body, `<input type="hidden" name="version" value="3">`)
	assert.Equal(t, [][2]int32{{revisionsPerPage, revisionsPerPage}}, history.pages, "the second page")

	rec = historyRequest(panel, http.MethodGet, ProductsPath+"/prod_1/revisions", nil, scopeProductRead)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "/restore\"", "a reader restores nothing")

	missing := newCatalogPanel(t, &fakeCatalog{byEntity: map[string][]query.Record{EntityProduct: {}}})
	missing.products = history
	rec = historyRequest(missing, http.MethodGet, ProductsPath+"/prod_9/revisions", nil, scopeProductRead)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	plain := historyPanel(t, &fakeProductWriter{})
	rec = historyRequest(plain, http.MethodGet, ProductsPath+"/prod_1/revisions", nil, scopeProductRead)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code, "a surface that reads no history says so")
}

// TestARevisionIsRestoredAtTheVersionRead: the restore names the product,
// the revision and the version the page was read at, and the history then
// says what was left out; a product written since is refused on the history,
// and a failure is not a refusal.
func TestARevisionIsRestoredAtTheVersionRead(t *testing.T) {
	t.Parallel()

	history := &fakeHistory{body: threeRevisions, total: 3, dropped: []string{"tag:ptag_gone", "category:pcat_gone"}}
	panel := historyPanel(t, history)

	rec := historyRequest(panel, http.MethodPost, ProductsPath+"/prod_1/revisions/2/restore",
		url.Values{fieldVersion: {"3"}}, scopeProductRead, scopeProductWrite)
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, []string{"prod_1|2|3"}, history.restored)
	location := rec.Header().Get("Location")
	require.True(t, strings.HasPrefix(location, ProductsPath+"/prod_1/revisions?"), location)

	after := historyRequest(panel, http.MethodGet, location, nil, scopeProductRead)
	require.Equal(t, http.StatusOK, after.Code)
	assert.Contains(t, after.Body.String(), "Revision 2 was restored as a new revision.")
	assert.Contains(t, after.Body.String(), "Left out, since removed: tag:ptag_gone, category:pcat_gone.")

	history.restoreErr = errors.PreconditionFailed("product_version_mismatch", "the product was changed since version 3")
	rec = historyRequest(panel, http.MethodPost, ProductsPath+"/prod_1/revisions/2/restore",
		url.Values{fieldVersion: {"3"}}, scopeProductRead, scopeProductWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "the product was changed since version 3")
	assert.Contains(t, rec.Body.String(), "History of Linen shirt", "the refusal is drawn on the history")

	rec = historyRequest(panel, http.MethodPost, ProductsPath+"/prod_1/revisions/2/restore",
		url.Values{fieldVersion: {"3"}}, scopeProductWrite)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotContains(t, rec.Body.String(), "History of", "a writer who cannot read is told the reason alone")

	history.restoreErr = errors.Unavailable("db_down", "no answer")
	rec = historyRequest(panel, http.MethodPost, ProductsPath+"/prod_1/revisions/2/restore",
		url.Values{fieldVersion: {"3"}}, scopeProductRead, scopeProductWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)

	rec = historyRequest(panel, http.MethodPost, ProductsPath+"/prod_1/revisions/2/restore", url.Values{},
		scopeProductRead, scopeProductWrite)
	assert.Equal(t, http.StatusBadRequest, rec.Code, "a form without the version read restores nothing")
	rec = historyRequest(panel, http.MethodPost, ProductsPath+"/prod_1/revisions/latest/restore",
		url.Values{fieldVersion: {"3"}}, scopeProductRead, scopeProductWrite)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Len(t, history.restored, 4, "neither reached the surface")

	plain := historyPanel(t, &fakeProductWriter{})
	rec = historyRequest(plain, http.MethodPost, ProductsPath+"/prod_1/revisions/2/restore",
		url.Values{fieldVersion: {"3"}}, scopeProductWrite)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
