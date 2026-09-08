package searchpg

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// This file tests the plugin's flows WITHOUT PostgreSQL: the index store and the
// catalog surface are replaced by fakes. The tests are INSIDE the package
// because neither surface (store, and the concrete use of StoreProductReader) is
// exported; exporting them would be widening the contract for testability alone.
//
// The claims that depend on real SQL (the tsvector match, relevance ranking, the
// sweep) live in searchpg_integration_test.go.

// fakeStore is the in-memory imitation of the index table.
type fakeStore struct {
	mu sync.Mutex

	documents map[string]document
	// searchResult holds the ids Search will return; the ORDER is given by the
	// search, which is how the handler preserving it can be tested.
	searchResult []string
	lastQuery    string
	lastLimit    int
	lastOffset   int
	searchCalls  int

	upsertErr error
	searchErr error
	deleteErr error

	sweepCalls     int
	sweepThreshold time.Time
	now            time.Time
}

// newFakeStore produces an empty fake store.
func newFakeStore() *fakeStore {
	return &fakeStore{
		documents: map[string]document{},
		now:       time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
	}
}

// Upsert writes the documents into memory.
func (d *fakeStore) Upsert(_ context.Context, documents []document) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.upsertErr != nil {
		return d.upsertErr
	}
	for _, b := range documents {
		d.documents[b.productID] = b
	}

	return nil
}

// Delete removes the given ids from memory.
func (d *fakeStore) Delete(_ context.Context, productIDs ...string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.deleteErr != nil {
		return 0, d.deleteErr
	}
	var removed int64
	for _, id := range productIDs {
		if _, ok := d.documents[id]; ok {
			delete(d.documents, id)
			removed++
		}
	}

	return removed, nil
}

// Search returns the preset result and records the call's parameters.
func (d *fakeStore) Search(_ context.Context, text string, limit, offset int) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.searchCalls++
	d.lastQuery, d.lastLimit, d.lastOffset = text, limit, offset
	if d.searchErr != nil {
		return nil, d.searchErr
	}

	return slices.Clone(d.searchResult), nil
}

// Sweep records the sweep call; it deletes nothing from memory.
func (d *fakeStore) Sweep(_ context.Context, threshold time.Time) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.sweepCalls++
	d.sweepThreshold = threshold

	return 0, nil
}

// Now returns a fixed time.
func (d *fakeStore) Now(_ context.Context) (time.Time, error) {
	return d.now, nil
}

// ids returns the product ids in the index, sorted.
func (d *fakeStore) ids() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	out := make([]string, 0, len(d.documents))
	for id := range d.documents {
		out = append(out, id)
	}
	slices.Sort(out)

	return out
}

// fetchDocument returns the document of an id.
func (d *fakeStore) fetchDocument(id string) (document, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	b, ok := d.documents[id]

	return b, ok
}

// fakeCatalog is the imitation of the "product.interop" surface.
//
// It applies the channel filter with the SAME meaning as the real one (nil: no
// filtering, empty slice: products with no channel); what the tests exercise is
// whether the plugin CARRIES that distinction correctly, not the rule itself.
type fakeCatalog struct {
	mu sync.Mutex

	products map[string]json.RawMessage
	channels map[string][]string

	lastRequest catalogRequest
	calls       int
	err         error
}

// newFakeCatalog produces an empty fake catalog.
func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		products: map[string]json.RawMessage{},
		channels: map[string][]string{},
	}
}

// addProduct defines a product visible in the catalog.
func (k *fakeCatalog) addProduct(id, title, description string) {
	k.addProductRecord(id, json.RawMessage(`{
		"id": "`+id+`",
		"handle": "`+id+`-handle",
		"title": "`+title+`",
		"description": "`+description+`",
		"variants": [{"id": "variant_`+id+`", "title": "Tek", "sku": "SKU-`+id+`"}],
		"tags": [{"id": "ptag_1", "value": "new"}],
		"price_set": {"amount": 1000}
	}`))
}

// addProductRecord defines a raw catalog record.
func (k *fakeCatalog) addProductRecord(id string, record json.RawMessage) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.products[id] = record
}

// assignChannel binds the product to the given sales channels.
func (k *fakeCatalog) assignChannel(id string, channels ...string) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.channels[id] = channels
}

// StoreProductsByIDsJSON returns the records of the asked ids, in order.
func (k *fakeCatalog) StoreProductsByIDsJSON(
	_ context.Context, request json.RawMessage,
) (json.RawMessage, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.calls++
	if err := json.Unmarshal(request, &k.lastRequest); err != nil {
		return nil, err
	}
	if k.err != nil {
		return nil, k.err
	}

	out := make([]json.RawMessage, 0, len(k.lastRequest.IDs))
	for _, id := range k.lastRequest.IDs {
		record, ok := k.products[id]
		if !ok || !k.visible(id, k.lastRequest.SalesChannelIDs) {
			continue
		}
		out = append(out, record)
	}

	return json.Marshal(catalogResponse{Products: out})
}

// visible reports whether the product appears in the requested channels.
func (k *fakeCatalog) visible(id string, istenen []string) bool {
	if istenen == nil {
		return true
	}
	atanan, ok := k.channels[id]
	if !ok || len(atanan) == 0 {
		return true
	}
	for _, channel := range atanan {
		if slices.Contains(istenen, channel) {
			return true
		}
	}

	return false
}

// fakeGraph is the imitation of the core's Query layer.
type fakeGraph struct {
	mu sync.Mutex

	ids       []string
	offsetler []int
	lastSpec  query.GraphSpec
	err       error
	errOffset int
}

// Graph returns the given page as id records.
func (g *fakeGraph) Graph(_ context.Context, spec query.GraphSpec) ([]query.Record, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.lastSpec = spec
	g.offsetler = append(g.offsetler, spec.Offset)
	if g.err != nil && spec.Offset == g.errOffset {
		return nil, g.err
	}

	if spec.Offset >= len(g.ids) {
		return []query.Record{}, nil
	}
	son := min(spec.Offset+spec.Limit, len(g.ids))

	out := make([]query.Record, 0, son-spec.Offset)
	for _, id := range g.ids[spec.Offset:son] {
		out = append(out, query.Record{query.IDField: id})
	}

	return out, nil
}

// testModule produces a module built with fake dependencies.
func testModule(d store, k StoreProductReader) *searchModule {
	m := newSearchModule(nil, slog.New(slog.DiscardHandler))
	m.index = d
	m.catalog = &catalog{reader: k}

	return m
}

// testRouter returns a router with the module's endpoints bound.
func testRouter(m *searchModule) chi.Router {
	r := chi.NewRouter()
	m.Routes(r)

	return r
}

// request verilen hedefe request atar; kimlik verilirse context'e konur.
func request(m *searchModule, method, target string, principal *corehttp.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, http.NoBody)
	if principal != nil {
		req = req.WithContext(corehttp.WithPrincipal(req.Context(), *principal))
	}
	rec := httptest.NewRecorder()
	testRouter(m).ServeHTTP(rec, req)

	return rec
}

// storePrincipal produces a store identity bound to the given channels.
func storePrincipal(channels ...string) *corehttp.Principal {
	if channels == nil {
		channels = []string{}
	}

	return &corehttp.Principal{ID: "pk_test", Kind: "api_key", SalesChannelIDs: channels}
}

// testChannel is the sales channel the search tests address.
const testChannel = "sc_web"

// searchURL is the search endpoint's address for one channel.
//
// [SearchPath] is a chi PATTERN and not a URL: since ADR 0044 the channel is a
// path SEGMENT, so an address is built by substituting one in. The substitution
// is spelled out here rather than derived from the constant, which is what keeps
// the constant honest about what it is serving — a test that built its URL out
// of the same constant the router was registered with would pass through any
// typo in it.
func searchURL(channel, params string) string {
	return "/store/v1/sales-channels/" + channel + "/search" + params
}

// event produces an event with the given name and product id.
func event(ad, productID string) eventbus.Event {
	return eventbus.Event{Name: ad, Data: map[string]any{eventFieldProductID: productID}}
}

// TestProductWrittenReadsTheCatalogAndIndexes verifies that on receiving the
// event the subscriber reads the record and writes the weighted document.
func TestProductWrittenReadsTheCatalogAndIndexes(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_1", "Blue Shirt", "A cotton summer shirt")
	m := testModule(d, k)

	require.NoError(t, m.productWritten(t.Context(), event(eventProductCreated, "prod_1")))

	b, ok := d.fetchDocument("prod_1")
	require.True(t, ok, "the product has to have been indexed")
	assert.Equal(t, "Blue Shirt", b.title, "the title goes to weight A")
	assert.Equal(t, "A cotton summer shirt", b.body, "the description goes to weight C")
	assert.Contains(t, b.keywords, "SKU-prod_1", "the SKU has to be searchable")
	assert.Contains(t, b.keywords, "prod_1-handle", "the handle has to be searchable")
	assert.Contains(t, b.keywords, "new", "the tag value has to be searchable")
}

// TestTheIndexIsIndependentOfTheChannel verifies that the event handler reads
// from the catalog WITHOUT a channel filter.
//
// Were the index kept per channel, the same product would be written once per
// channel and the index would have to be rebuilt whenever a channel assignment
// changed; the filtering happens at read time.
func TestTheIndexIsIndependentOfTheChannel(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_1", "Blue Shirt", "")
	k.assignChannel("prod_1", "sc_web")
	m := testModule(d, k)

	require.NoError(t, m.productWritten(t.Context(), event(eventProductUpdated, "prod_1")))

	assert.Equal(t, []string{"prod_1"}, d.ids())
	assert.Nil(t, k.lastRequest.SalesChannelIDs,
		"the indexing read must carry no channel id (nil = no filter)")
}

// TestTheCatalogIsReadInsteadOfTheEventsStatus verifies that a stale status field
// does not push the index in the WRONG direction.
//
// The event says "draft" but the catalog still shows the product in the
// storefront: the right behavior is to index it. Had the shortcut been taken,
// the product would fall out of search silently because two events were
// delivered out of order.
func TestTheCatalogIsReadInsteadOfTheEventsStatus(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_1", "Blue Shirt", "")
	m := testModule(d, k)

	e := event(eventProductUpdated, "prod_1")
	e.Data["status"] = "draft"
	require.NoError(t, m.productWritten(t.Context(), e))

	assert.Equal(t, []string{"prod_1"}, d.ids(),
		"the decision has to rest on the catalog's CURRENT state, not on what the event says")
}

// TestAProductNotVisibleInTheStorefrontLeavesTheIndex verifies that an
// unpublished product is deleted from the index.
func TestAProductNotVisibleInTheStorefrontLeavesTheIndex(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	require.NoError(t, d.Upsert(t.Context(), []document{{productID: "prod_1", title: "Eski"}}))
	m := testModule(d, k)

	// The catalog returns this id NOT AT ALL: it was unpublished, archived or
	// deleted.
	require.NoError(t, m.productWritten(t.Context(), event(eventProductUpdated, "prod_1")))

	assert.Empty(t, d.ids(), "a product not visible in the storefront must not stay in the index")
}

// TestProductDeletedDoesNotReadTheCatalog verifies that the deletion event never
// speaks to the catalog.
//
// A soft-deleted record comes back from no read anyway; the read round would be a
// wasted round trip.
func TestProductDeletedDoesNotReadTheCatalog(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	require.NoError(t, d.Upsert(t.Context(), []document{{productID: "prod_1", title: "Eski"}}))
	m := testModule(d, k)

	require.NoError(t, m.productDeleted(t.Context(), event(eventProductDeleted, "prod_1")))

	assert.Empty(t, d.ids())
	assert.Zero(t, k.calls, "the deletion event must not read the catalog")
}

// TestDeletionIsIdempotent verifies that a second delivery of the same deletion
// event produces no error.
//
// The Redis backend delivers AT LEAST ONCE; a handler that returned an error on
// the second delivery would produce noise on every restart.
func TestDeletionIsIdempotent(t *testing.T) {
	t.Parallel()

	m := testModule(newFakeStore(), newFakeCatalog())

	require.NoError(t, m.productDeleted(t.Context(), event(eventProductDeleted, "prod_absent")))
	require.NoError(t, m.productDeleted(t.Context(), event(eventProductDeleted, "prod_absent")))
}

// TestABrokenEventPayloadIsRefused verifies that a payload not matching the
// contract returns an error.
func TestABrokenEventPayloadIsRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]map[string]any{
		"no field":       {},
		"not a string":   {eventFieldProductID: 42},
		"empty":          {eventFieldProductID: "   "},
		"the wrong type": {eventFieldProductID: []string{"prod_1"}},
	}

	for ad, yuk := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			d := newFakeStore()
			m := testModule(d, newFakeCatalog())

			err := m.productWritten(t.Context(), eventbus.Event{Name: eventProductCreated, Data: yuk})

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "a payload error has to be KindInvalid: %v", err)
			assert.Empty(t, d.ids(), "no row may be written to the index from a broken payload")
		})
	}
}

// TestACatalogErrorIsNotSwallowed verifies that an indexing error does not
// consume the event silently.
//
// Returning an error does NOT cause a retry on the bus (by contract the event is
// ACKed either way); its only effect is that the error is logged together with
// the event's name and id. Returning nil would make the index falling behind
// invisible everywhere.
func TestACatalogErrorIsNotSwallowed(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.err = coreerrors.Unavailable("test_catalog_down", "the catalog is unreachable")
	m := testModule(d, k)

	err := m.productWritten(t.Context(), event(eventProductCreated, "prod_1"))

	require.Error(t, err)
	assert.Equal(t, coreerrors.KindUnavailable, coreerrors.KindOf(err),
		"the catalog's error class has to be preserved; unavailability must not be reported as a server error")
}

// TestAnUnregisteredModuleReturnsAnError verifies that an event arriving without
// Register having run produces a typed error rather than a panic.
func TestAnUnregisteredModuleReturnsAnError(t *testing.T) {
	t.Parallel()

	m := newSearchModule(nil, nil)
	m.catalog = &catalog{reader: newFakeCatalog()}

	err := m.productWritten(t.Context(), event(eventProductCreated, "prod_1"))

	require.Error(t, err)
	assert.Equal(t, codeNotRegistered, coreerrors.CodeOf(err))
	assert.Empty(t, chiPatterns(testRouter(m)), "no endpoint may be bound while there is no index")
}

// chiPatterns returns the route patterns in the router tree.
func chiPatterns(r chi.Router) []string {
	var out []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out = append(out, method+" "+route)

		return nil
	})

	return out
}

// TestSearchTakesIDsFromTheIndexAndRecordsFromTheCatalog verifies the whole
// search flow: the index gives the ids and the ORDER, the catalog gives the
// records.
func TestSearchTakesIDsFromTheIndexAndRecordsFromTheCatalog(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_2", "Shirt", "")
	k.addProduct("prod_1", "Shirt White", "")
	// The relevance order comes from the search; the handler MUST preserve it.
	d.searchResult = []string{"prod_2", "prod_1"}
	m := testModule(d, k)

	rec := request(m, http.MethodGet, searchURL(testChannel, "?q=shirt&limit=5&offset=10"),
		storePrincipal(testChannel))

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, "shirt", d.lastQuery)
	assert.Equal(t, 5, d.lastLimit)
	assert.Equal(t, 10, d.lastOffset)

	var response struct {
		Data   []map[string]any `json:"data"`
		Count  int              `json:"count"`
		Offset int              `json:"offset"`
		Limit  int              `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Data, 2)
	assert.Equal(t, "prod_2", response.Data[0]["id"], "the relevance order the index gave has to be preserved")
	assert.Equal(t, "prod_1", response.Data[1]["id"])
	assert.Equal(t, 2, response.Count)
	assert.Equal(t, 10, response.Offset)
	assert.Equal(t, 5, response.Limit)
	assert.Contains(t, response.Data[0], "price_set",
		"the records have to be EXACTLY the storefront representation; the plugin does not reshape them")
}

// TestSearchReadsTheChannelsFromTheIdentity verifies that the channel filter
// comes from the request's IDENTITY and that the query string is never read.
//
// Had the query string been accepted, a client arriving with any publishable key
// could search another channel's catalog.
func TestSearchReadsTheChannelsFromTheIdentity(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_web", "Shirt", "")
	k.assignChannel("prod_web", "sc_web")
	k.addProduct("prod_pos", "Shirt", "")
	k.assignChannel("prod_pos", "sc_pos")
	d.searchResult = []string{"prod_web", "prod_pos"}
	m := testModule(d, k)

	rec := request(m, http.MethodGet,
		searchURL("sc_web", "?q=shirt&sales_channel_ids=sc_pos"), storePrincipal("sc_web", "sc_pos"))

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_web"}, k.lastRequest.SalesChannelIDs,
		"the scope is the PATH's single channel, and never the key's whole set")

	var response struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Data, 1, "another channel's product must not appear in the result")
	assert.Equal(t, "prod_web", response.Data[0]["id"])
}

// TestARequestWithNoIdentityGetsNoChannelFilter verifies that search keeps
// working on an installation where store identity was never wired.
func TestARequestWithNoIdentityGetsNoChannelFilter(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_1", "Shirt", "")
	k.assignChannel("prod_1", "sc_web")
	d.searchResult = []string{"prod_1"}
	m := testModule(d, k)

	rec := request(m, http.MethodGet, searchURL("sc_web", "?q=shirt"), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, []string{"sc_web"}, k.lastRequest.SalesChannelIDs,
		"with no identity the path stands alone, which NARROWS to the channel the URL names")
}

// TestEveryChannelIsRefusedToAChannellessIdentity verifies that an identity
// holding no channel is refused whatever channel it names.
//
// This is where the channel moving into the path CHANGED the answer, and the
// change is the point rather than a side effect. The route used to hand such a
// key the empty set and a 200 with no results; now there is a channel in the
// URL and the key holds nothing to narrow to, so the request is refused
// outright — the same answer the catalog's own listing gives (see product's
// TestStoreListRefusesEveryChannelForAChannellessIdentity).
//
// The distinction being defended is unchanged and is the whole of the rule: an
// identity with no channel is an EMPTY SET and not "no filtering". Had the two
// been treated as one, a key with no channel would be handed every channel's
// catalog.
func TestEveryChannelIsRefusedToAChannellessIdentity(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_1", "Shirt", "")
	d.searchResult = []string{"prod_1"}
	m := testModule(d, k)

	rec := request(m, http.MethodGet, searchURL(testChannel, "?q=shirt"), storePrincipal())

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	assert.Zero(t, d.searchCalls, "a refused request must never reach the index")
	assert.Zero(t, k.calls, "a refused request must never reach the catalog")
}

// TestAChannelTheKeyDoesNotHoldIsRefused verifies that the path can only NARROW.
//
// The segment is a value the client types. Honored on its own it would let any
// holder of a publishable key read any channel's catalog by editing a URL,
// which is the query-string mistake with a different spelling; intersected with
// the key's set it can only pick among channels the caller already had.
//
// The refusal does not depend on the named channel EXISTING: this handler never
// consults the channel table, so a channel that belongs to another merchant and
// one that was never created are answered identically. That is what keeps a 403
// from being an existence oracle.
func TestAChannelTheKeyDoesNotHoldIsRefused(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProduct("prod_pos", "Shirt", "")
	d.searchResult = []string{"prod_pos"}
	m := testModule(d, k)

	rec := request(m, http.MethodGet, searchURL("sc_pos", "?q=shirt"), storePrincipal("sc_web"))

	require.Equal(t, http.StatusForbidden, rec.Code, "body: %s", rec.Body.String())
	assert.Zero(t, d.searchCalls, "a refused request must never reach the index")
}

// TestAnEmptyResultNeverCallsTheCatalog verifies that no needless round is taken
// when there is no match.
func TestAnEmptyResultNeverCallsTheCatalog(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	m := testModule(d, k)

	rec := request(m, http.MethodGet, searchURL(testChannel, "?q=nothingatall"),
		storePrincipal(testChannel))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Zero(t, k.calls, "the catalog must not be called for an empty id list")
	assert.JSONEq(t, `{"data":[],"count":0,"offset":0,"limit":20}`, rec.Body.String(),
		"an empty result has to be an EMPTY array, not null")
}

// TestInvalidSearchParametersAreRefused verifies that bound and format errors
// return 422.
func TestInvalidSearchParametersAreRefused(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"no query":                searchURL(testChannel, ""),
		"an empty query":          searchURL(testChannel, "?q="),
		"a whitespace query":      searchURL(testChannel, "?q=%20%20"),
		"a zero limit":            searchURL(testChannel, "?q=a&limit=0"),
		"a limit above the bound": searchURL(testChannel, "?q=a&limit="+strconv.Itoa(maxLimit+1)),
		"a non-numeric limit":     searchURL(testChannel, "?q=a&limit=abc"),
		"a negative offset":       searchURL(testChannel, "?q=a&offset=-1"),
	}

	for name, target := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			d := newFakeStore()
			rec := request(testModule(d, newFakeCatalog()), http.MethodGet, target,
				storePrincipal(testChannel))

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
			assert.Zero(t, d.searchCalls, "an invalid request must never reach the index")
		})
	}
}

// TestAnOverlongQueryIsRefused verifies that an unbounded text is never handed
// to the query parser.
func TestAnOverlongQueryIsRefused(t *testing.T) {
	t.Parallel()

	overlong := make([]byte, maxQueryBytes+1)
	for i := range overlong {
		overlong[i] = 'a'
	}

	d := newFakeStore()
	rec := request(testModule(d, newFakeCatalog()), http.MethodGet,
		searchURL(testChannel, "?q="+string(overlong)), storePrincipal(testChannel))

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Zero(t, d.searchCalls)
}

// TestReindexing verifies that the catalog is read page by page and swept at the
// end of the round.
func TestReindexing(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	graph := &fakeGraph{}
	// Two full pages plus a short one: the last page being short has to end the
	// loop, with no extra round taken.
	total := reindexPageSize*2 + 7
	for i := range total {
		id := "prod_" + strconv.Itoa(i)
		graph.ids = append(graph.ids, id)
		k.addProduct(id, "Product "+strconv.Itoa(i), "")
	}

	m := testModule(d, k)
	m.graph = graph

	sonuc, err := m.reindex(t.Context())

	require.NoError(t, err)
	assert.Equal(t, total, sonuc.Indexed)
	assert.Equal(t, 3, sonuc.Pages)
	assert.Equal(t, []int{0, reindexPageSize, reindexPageSize * 2}, graph.offsetler,
		"paging offset'i sayfa boyu kadar ilerlemeli")
	assert.Len(t, d.ids(), total)

	assert.Equal(t, catalogEntity, graph.lastSpec.Entity)
	assert.Equal(t, []string{query.IDField}, graph.lastSpec.Fields,
		"no field other than the id may be asked for")
	assert.Equal(t, map[string]any{catalogStatusFilter: catalogStatusPublished},
		graph.lastSpec.Filters, "only published products may be indexed")

	assert.Equal(t, 1, d.sweepCalls, "one full sweep has to happen when the round ends")
	assert.Equal(t, d.now, d.sweepThreshold, "the threshold has to come from the DATABASE clock")
}

// TestAHalfFinishedRoundDoesNotSweep verifies that a round which failed does not
// delete the valid rows.
//
// Had the sweep run after a round that stopped halfway, every product on the
// pages that were never read would fall out of the index — that is, the repair
// tool would turn into a tool that deletes the catalog from search.
func TestAHalfFinishedRoundDoesNotSweep(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	graph := &fakeGraph{err: coreerrors.Unavailable("test_query_down", "the query layer went down"), errOffset: reindexPageSize}
	for i := range reindexPageSize * 2 {
		id := "prod_" + strconv.Itoa(i)
		graph.ids = append(graph.ids, id)
		k.addProduct(id, "Product", "")
	}

	m := testModule(d, k)
	m.graph = graph

	_, err := m.reindex(t.Context())

	require.Error(t, err)
	assert.Zero(t, d.sweepCalls, "no sweep may happen after a round that stopped halfway")
	assert.Len(t, d.ids(), reindexPageSize, "the first page still has to have been written")
}

// TestTheReindexEndpointRequiresTheScope verifies that the admin endpoint is not
// unprotected.
func TestTheReindexEndpointRequiresTheScope(t *testing.T) {
	t.Parallel()

	m := testModule(newFakeStore(), newFakeCatalog())
	m.graph = &fakeGraph{}

	t.Run("kimliksiz", func(t *testing.T) {
		t.Parallel()

		rec := request(m, http.MethodPost, ReindexPath, nil)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("yetkisiz", func(t *testing.T) {
		t.Parallel()

		rec := request(m, http.MethodPost, ReindexPath,
			&corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{"product:read"}})
		assert.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("the module scope", func(t *testing.T) {
		t.Parallel()

		rec := request(m, http.MethodPost, ReindexPath,
			&corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{ScopeWrite}})
		assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("the parent scope", func(t *testing.T) {
		t.Parallel()

		rec := request(m, http.MethodPost, ReindexPath,
			&corehttp.Principal{ID: "usr_1", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}})
		assert.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	})
}

// TestTheCatalogIsResolvedLazily verifies that the surface is resolved ON FIRST
// USE rather than in Setup, and that a failed resolution is not permanent.
//
// Had sync.Once been used, a single resolution that failed while product was not
// yet registered would leave search dead for the life of the process.
func TestTheCatalogIsResolvedLazily(t *testing.T) {
	t.Parallel()

	c := container.New(slog.New(slog.DiscardHandler))
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	k := newCatalog(c)

	_, err := k.products(t.Context(), []string{"prod_1"}, nil)
	require.Error(t, err, "a read with no registration has to return an error")
	assert.Equal(t, codeCatalogMissing, coreerrors.CodeOf(err))

	// The registration happens AFTERWARDS; this imitates the modules coming up
	// after the plugin's Setup.
	fake := newFakeCatalog()
	fake.addProduct("prod_1", "Shirt", "")
	require.NoError(t, c.Provide(catalogInteropName, fake))

	records, err := k.products(t.Context(), []string{"prod_1"}, nil)
	require.NoError(t, err, "the resolution has to succeed once the registration is made")
	require.Len(t, records, 1)

	// The second call uses the cached surface.
	_, err = k.products(t.Context(), []string{"prod_1"}, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, fake.calls)
}

// TestTheCatalogIsNotCalledForAnEmptyIDSet verifies that with an empty list not
// even the container is consulted.
func TestTheCatalogIsNotCalledForAnEmptyIDSet(t *testing.T) {
	t.Parallel()

	k := newCatalog(nil)

	records, err := k.products(t.Context(), nil, nil)

	require.NoError(t, err, "an empty id list must not even require a resolution")
	assert.Empty(t, records)
}

// TestABrokenCatalogRecordIsRefused verifies that a record with no id is not
// written to the index.
func TestABrokenCatalogRecordIsRefused(t *testing.T) {
	t.Parallel()

	d, k := newFakeStore(), newFakeCatalog()
	k.addProductRecord("prod_1", json.RawMessage(`{"title": "Kimliksiz"}`))
	m := testModule(d, k)

	err := m.productWritten(t.Context(), event(eventProductCreated, "prod_1"))

	require.Error(t, err)
	assert.Equal(t, codeCatalogResponse, coreerrors.CodeOf(err))
	assert.Empty(t, d.ids(), "a row whose primary key is empty must not be written")
}
