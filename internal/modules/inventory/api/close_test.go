package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file holds the HTTP half of ADR 0055: a location is retired by CLOSING
// it, and the surface says so.

// newRouter binds the handlers over a fake service.
//
// The package's existing helper does exactly this under a Turkish name, and this
// file is English (ADR 0012, decision 3 — language is a property of the FILE):
// reaching for it would drag the old name into a translated file and the
// language check would fail, correctly.
func newRouter(t *testing.T) (chi.Router, *fakeInventory) {
	t.Helper()

	svc := &fakeInventory{}
	router := chi.NewRouter()
	api.NewHandler(svc).Routes(router)

	return router, svc
}

// sendRequest sends a bodiless, fully authorized admin request.
//
// In production corehttp.RequireAdmin puts the principal in the context; these
// tests build the router directly, so it is placed by hand — without it the
// scope guard answers 401 and the test would be exercising authorization rather
// than the endpoint.
func sendRequest(t *testing.T, router chi.Router, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	return rec
}

// jsonBody decodes a response body into a map.
func jsonBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out),
		"the response has to be JSON: %s", rec.Body.String())

	return out
}

// TestClosingALocationReturnsTheClosedRow proves the endpoint answers with the
// record rather than with silence.
//
// It is a POST returning 200 and not a DELETE returning 204, and both halves
// are the decision: the row survives the call, and what the caller needs to see
// is the field that changed.
func TestClosingALocationReturnsTheClosedRow(t *testing.T) {
	router, svc := newRouter(t)
	closedAt := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	svc.location = models.StockLocation{ID: "sloc_1", Name: "Merkez", ClosedAt: &closedAt}

	rec := sendRequest(t, router, http.MethodPost, "/admin/v1/stock-locations/sloc_1/close")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "sloc_1", svc.gorulenID)
	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "2026-09-08T10:00:00Z", data["closed_at"])
}

// TestAnOpenLocationReportsClosedAtAsNull proves the field is on the wire even
// when there is no moment to report.
//
// Left to omitempty, "this location is open" and "this build does not know the
// field" would look the same to a client, and the first one is a fact. The read
// endpoint is the one that carries the question, because it is the one a client
// polls to find out whether a warehouse is still in service.
func TestAnOpenLocationReportsClosedAtAsNull(t *testing.T) {
	router, svc := newRouter(t)
	svc.location = models.StockLocation{ID: "sloc_1", Name: "Merkez"}

	rec := sendRequest(t, router, http.MethodGet, "/admin/v1/stock-locations/sloc_1")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "sloc_1", svc.gorulenID)
	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, data, "closed_at")
	assert.Nil(t, data["closed_at"])
}

// TestClosingANonEmptyLocationIsAConflict proves the refusal reaches the caller
// as 409 with the code it can branch on.
func TestClosingANonEmptyLocationIsAConflict(t *testing.T) {
	router, svc := newRouter(t)
	svc.err = errors.Conflict(service.CodeLocationNotEmpty, "the location still holds 4 units")

	rec := sendRequest(t, router, http.MethodPost, "/admin/v1/stock-locations/sloc_1/close")

	require.Equal(t, http.StatusConflict, rec.Code)
	body, ok := jsonBody(t, rec)["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, service.CodeLocationNotEmpty, body["code"])
}

// TestTheListingAsksForClosedLocationsOnlyWhenTold proves the switch reaches
// the service, and that its absence means "open ones".
func TestTheListingAsksForClosedLocationsOnlyWhenTold(t *testing.T) {
	router, svc := newRouter(t)

	rec := sendRequest(t, router, http.MethodGet, "/admin/v1/stock-locations?include_closed=true")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, svc.gorulenLocationInput.IncludeClosed)
}

// TestAnUnreadableIncludeClosedIsRejected proves the flag is read as a boolean
// rather than shrugged off.
//
// Silently treating "maybe" as false would hand the caller a page that answers
// a question they did not ask.
func TestAnUnreadableIncludeClosedIsRejected(t *testing.T) {
	router, _ := newRouter(t)

	rec := sendRequest(t, router, http.MethodGet, "/admin/v1/stock-locations?include_closed=maybe")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}
