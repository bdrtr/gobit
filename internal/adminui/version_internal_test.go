package adminui

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// versionedCatalog is prod_1 at version 4.
func versionedCatalog() *fakeCatalog {
	return &fakeCatalog{byEntity: map[string][]query.Record{
		EntityProduct: {{"id": "prod_1", "title": "Coffee", "handle": "coffee", "status": "draft", fieldVersion: int64(4)}},
	}}
}

// TestTheEditFormIsSavedAtTheVersionItWasReadAt is ADR 0222: the form carries
// the version it was read at, and the save sends it back.
func TestTheEditFormIsSavedAtTheVersionItWasReadAt(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, versionedCatalog(), writer)

	rec := httptest.NewRecorder()
	editRouter(panel).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ProductsPath+"/prod_1/edit", http.NoBody))
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `<input type="hidden" name="version" value="4">`)

	rec = postEdit(panel, "prod_1", url.Values{
		"title": {"Coffee"}, "handle": {"coffee"}, "status": {"draft"}, "version": {"4"},
	})
	require.Equal(t, http.StatusSeeOther, rec.Code, rec.Body.String())
	assert.Equal(t, int64(4), writer.version)
}

// TestAStaleEditComesBackWithWhatWasTyped: a save refused because somebody
// saved first shows the form again, with the typed values, the message, and
// the version now stored.
func TestAStaleEditComesBackWithWhatWasTyped(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{err: errors.PreconditionFailed("product_version_mismatch", "stale")}
	panel := newEditPanel(t, versionedCatalog(), writer)

	rec := postEdit(panel, "prod_1", url.Values{
		"title": {"Filter Coffee"}, "handle": {"coffee"}, "status": {"draft"}, "version": {"3"},
	})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	body := rec.Body.String()
	assert.Contains(t, body, "Somebody saved this product after you opened it")
	assert.Contains(t, body, `value="Filter Coffee"`, "what was typed is kept")
	assert.Contains(t, body, `name="version" value="4"`, "saving again is at the version now stored")
	assert.Empty(t, writer.scheduled, "nothing after the refused save runs")
}

// TestAnEditThatNamesNoVersionIsRefused: a form without a readable version is
// not saved at all.
func TestAnEditThatNamesNoVersionIsRefused(t *testing.T) {
	t.Parallel()

	writer := &fakeProductWriter{}
	panel := newEditPanel(t, versionedCatalog(), writer)

	for _, version := range []string{"", "four"} {
		rec := postEdit(panel, "prod_1", url.Values{
			"title": {"Coffee"}, "handle": {"coffee"}, "status": {"draft"}, "version": {version},
		})
		assert.Equal(t, http.StatusBadRequest, rec.Code, version)
	}
	assert.Zero(t, writer.calls)
}
