// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English.
package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// fakeBindings is an in-memory stand-in for the core's link service.
//
// It keeps the pairs rather than counting calls, because the endpoints answer
// with the REMAINING list and a fake that only recorded the write could not
// tell a binding that landed from one that was reported.
type fakeBindings struct {
	pairs map[string][]string
	err   error
	calls []string
}

// newFakeBindings produces an empty binding store.
func newFakeBindings() *fakeBindings {
	return &fakeBindings{pairs: map[string][]string{}}
}

// Create binds the pair; a repeated pair is a no-op, as in the real service.
func (f *fakeBindings) Create(_ context.Context, name, fromID, toID string) error {
	f.calls = append(f.calls, "Create")
	if f.err != nil {
		return f.err
	}
	key := name + "|" + fromID
	for _, bound := range f.pairs[key] {
		if bound == toID {
			return nil
		}
	}
	f.pairs[key] = append(f.pairs[key], toID)

	return nil
}

// Delete removes the pair; an absent pair is a no-op.
func (f *fakeBindings) Delete(_ context.Context, name, fromID, toID string) error {
	f.calls = append(f.calls, "Delete")
	if f.err != nil {
		return f.err
	}
	key := name + "|" + fromID
	kept := make([]string, 0, len(f.pairs[key]))
	for _, bound := range f.pairs[key] {
		if bound != toID {
			kept = append(kept, bound)
		}
	}
	f.pairs[key] = kept

	return nil
}

// List returns what is bound to fromID.
func (f *fakeBindings) List(_ context.Context, name, fromID string) ([]string, error) {
	f.calls = append(f.calls, "List")
	if f.err != nil {
		return nil, f.err
	}

	return f.pairs[name+"|"+fromID], nil
}

// boundRouter wires the handler with a working location and a binding store.
func boundRouter(t *testing.T) (chi.Router, *fakeInventory, *fakeBindings) {
	t.Helper()

	svc := &fakeInventory{location: models.StockLocation{ID: "sloc_1", Name: "Main"}}
	links := newFakeBindings()
	router := chi.NewRouter()
	api.NewHandler(svc, links).Routes(router)

	return router, svc, links
}

// channelsOf decodes the channel list out of a response body.
func channelsOf(t *testing.T, body []byte) []string {
	t.Helper()

	var decoded struct {
		Data struct {
			SalesChannelIDs []string `json:"sales_channel_ids"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &decoded), "body: %s", body)

	return decoded.Data.SalesChannelIDs
}

// sendRequestWithBody sends a fully authorized admin request carrying a body.
//
// [sendRequest] beside it sends a bodiless one; the two differ in the reader
// and in nothing else.
func sendRequestWithBody(
	t *testing.T, router chi.Router, method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
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

// TestAWarehouseCanSayWhichChannelsItShipsFor is the write the mapping was
// missing.
//
// Until this endpoint existed nothing in the tree could say that a channel
// ships from one warehouse and not another, so the checkout reserved from
// whichever warehouse held stock.
func TestAWarehouseCanSayWhichChannelsItShipsFor(t *testing.T) {
	router, _, links := boundRouter(t)

	bound := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/stock-locations/sloc_1/sales-channels",
		`{"sales_channel_id":"sc_web"}`)

	require.Equal(t, http.StatusOK, bound.Code, "body: %s", bound.Body.String())
	assert.Equal(t, []string{"sc_web"}, channelsOf(t, bound.Body.Bytes()),
		"the answer is the remaining list, which is what a client refreshing needs")
	assert.Equal(t, []string{"sc_web"},
		links.pairs[service.LinkStockLocationSalesChannel+"|sloc_1"],
		"the pair has to be written under the link this module declares")

	again := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/stock-locations/sloc_1/sales-channels",
		`{"sales_channel_id":"sc_web"}`)
	require.Equal(t, http.StatusOK, again.Code)
	assert.Len(t, channelsOf(t, again.Body.Bytes()), 1,
		"binding the same pair twice is one binding")

	listed := sendRequest(t, router, http.MethodGet,
		"/admin/v1/stock-locations/sloc_1/sales-channels")
	require.Equal(t, http.StatusOK, listed.Code)
	assert.Equal(t, []string{"sc_web"}, channelsOf(t, listed.Body.Bytes()))

	removed := sendRequest(t, router, http.MethodDelete,
		"/admin/v1/stock-locations/sloc_1/sales-channels/sc_web")
	require.Equal(t, http.StatusOK, removed.Code)
	assert.Empty(t, channelsOf(t, removed.Body.Bytes()),
		"a warehouse bound to nothing narrows no channel")
}

// TestBindingAnUnknownWarehouseIsRefused keeps the binding on a record this
// module really has.
//
// The CHANNEL is not checked and cannot be: it is the auth module's record and
// this module never validates a foreign reference (Principle 2.2).
func TestBindingAnUnknownWarehouseIsRefused(t *testing.T) {
	svc := &fakeInventory{err: errors.New("not found")}
	links := newFakeBindings()
	router := chi.NewRouter()
	api.NewHandler(svc, links).Routes(router)

	response := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/stock-locations/sloc_MISSING/sales-channels",
		`{"sales_channel_id":"sc_web"}`)

	assert.NotEqual(t, http.StatusOK, response.Code)
	assert.Empty(t, links.calls, "no binding may be written for a warehouse that is not there")
}

// TestBindingWithoutAChannelIsRefused stops a call that would bind nothing.
func TestBindingWithoutAChannelIsRefused(t *testing.T) {
	router, _, links := boundRouter(t)

	response := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/stock-locations/sloc_1/sales-channels", `{}`)

	assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
	assert.Empty(t, links.calls)
}

// TestTheBindingEndpointsFailClosedWithoutTheLinkService keeps a merchant from
// believing a channel was narrowed when nothing was written.
func TestTheBindingEndpointsFailClosedWithoutTheLinkService(t *testing.T) {
	svc := &fakeInventory{location: models.StockLocation{ID: "sloc_1"}}
	router := chi.NewRouter()
	api.NewHandler(svc, nil).Routes(router)

	response := sendRequestWithBody(t, router, http.MethodPost,
		"/admin/v1/stock-locations/sloc_1/sales-channels",
		`{"sales_channel_id":"sc_web"}`)

	assert.Equal(t, http.StatusInternalServerError, response.Code)
}
