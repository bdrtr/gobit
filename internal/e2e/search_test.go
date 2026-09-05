//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/module"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// This file proves end to end that the search PLUGIN works in the real system:
//
//	when a product is written the catalog publishes an event, the plugin picks
//	the event up and indexes it into its OWN table, and GET /store/v1/search
//	returns the product — and only to a key that carries the product's sales
//	channel.
//
// All four links of the chain are real: the real product module, the real event
// bus, the plugin's real PostgreSQL index and the real HTTP endpoint that passes
// through the production guard stack. In the tests inside the plugin's own
// package the catalog is always FAKE and has to be fake (a plugin may not import
// any module, ADR 0001); this is therefore the ONLY place where the claim "the
// plugin receives the event product really publishes and writes the record
// product really returns" can be proven.
//
// # Why the plugin is installed on the GROUND rather than inside the test
//
// In production, plugins are installed BEFORE module bootstrap and the module a
// plugin brings is added to the SAME registry as the core modules
// (see cmd/server/main.go: Install -> Bootstrap -> Start -> MountRoutes).
// Moving the installation into the test would require bringing that module up
// with a SECOND [module.Registry] — a wiring that does not exist in production.
// The test would then prove not how the plugin is installed in production, but
// only that the thing it installed itself works.
//
// The second reason is timing: the subscriptions must be in place BEFORE the
// first product. The in-memory bus keeps no history and delivers AT MOST ONCE
// (see the eventbus package documentation), so a subscriber installed in the
// middle of the run never sees the events published before it, and the claim
// "the event reached the index" would come to depend on the ORDER of the tests.
//
// # Adding to the ground does not break the existing tests
//
// For three separate reasons: (1) no test counts search results or asserts a
// fixed set of modules or routes; (2) because the guard stack is installed by
// PATH PREFIX, new endpoints are guarded automatically and the behavior of the
// existing endpoints does not change; (3) a subscriber failure does NOT REACH
// the publisher — Publish does not wait for the handlers and the in-memory
// backend runs every handler in its own goroutine, so a fault in the plugin
// cannot bring down the product write path.
//
// The same is NOT TRUE for the payment plugin, and that is why that plugin is
// not installed on the ground: the scenario in sertlestirme_test.go asserts as a
// precondition that the provider is absent BEFORE registration (see
// [TestAPluginAddsAProviderWithoutTouchingTheCore]); a stripe plugin installed on
// the ground would break that test. The decision is made per plugin.
//
// # Each scenario sets up its own products
//
// Every scenario sets up its OWN product and its own search word that appears
// nowhere else in the catalog (see [searchWord]). The ground shares a single
// index and, as the run proceeds, other scenarios' products get indexed too; a
// fixed word, or an assertion of the form "how many records are in the index",
// would tie the tests to one another's order.

// searchPollInterval is the time waited between two polls while the index is
// refreshing.
//
// The ceiling on the wait is [olayBeklemeSuresi] and that constant is NOT
// REPEATED here: what is being waited for is the same thing — the in-memory bus
// carrying an event to a subscriber.
const searchPollInterval = 20 * time.Millisecond

// searchIndexTable is the name of the plugin's index table.
//
// The name is written out by hand because the plugin does NOT export it: the
// contract it publishes is the endpoints, the module name and the schema (see
// the searchpg package documentation). A change to the table name means a schema
// migration, and in that case it is right for this test to fail — rather than
// silently reading some other table.
const searchIndexTable = "searchpg_product"

// The registry and the host of the plugins installed on the ground.
//
// Both are filled in during the TestMain flow ([setUpPlugins]) and used in two
// phases: the registrations are declared before the modules, the subscriptions
// and the routes are applied after the modules have come up.
var (
	pluginRegistry *coreplugin.Registry
	pluginHost     *coreplugin.Host
)

// setUpPlugins installs the plugins BEFORE THE MODULES.
//
// The module registry and the bus are handed in from outside because both are
// the REAL ground the plugin will run on: the module the plugin brings must pass
// through the same Bootstrap as the core modules, and its subscriptions must
// listen to the real catalog events. Handing it a separate registry or a
// separate bus would mean testing the plugin inside its own bubble.
//
// The settings map is nil: the search plugin does NOT WANT configuration (see
// the searchpg package documentation). That a plugin which does want a setting
// stops when the setting is missing is exercised separately (see
// [TestSetupStopsWhenAPluginSettingIsMissing]).
func setUpPlugins(ctx context.Context, modules *module.Registry, bus eventbus.EventBus) error {
	pluginRegistry = coreplugin.NewRegistry(nil)
	pluginRegistry.Add(searchpg.New())

	pluginHost = coreplugin.NewHost(ctr, modules, bus, nil, nil)

	return pluginRegistry.Install(ctx, pluginHost)
}

// startPlugins applies the subscriptions and mounts the plugin routes.
//
// It is called AFTER THE MODULES HAVE COME UP; the order is the same as in
// production. [coreplugin.Registry.MountRoutes] mounts nothing in this setup —
// the search plugin's endpoints come not from the plugin hook but from the
// Routes of the MODULE it brings. It is called anyway: skipping it would mean
// that the next plugin, one that mounts its endpoints through the plugin hook,
// silently ends up with no routes.
func startPlugins(ctx context.Context) error {
	if err := pluginRegistry.Start(ctx, pluginHost); err != nil {
		return err
	}

	return pluginRegistry.MountRoutes(testRouter, pluginHost)
}

// searchWord produces a search word that appears nowhere else in the catalog.
//
// The word is produced from the shared fixture counter: every scenario using the
// same ground gets its own word, and one test's product never falls into another
// test's query.
//
// A word ending in a digit resolves to a SINGLE lexeme in the 'simple'
// dictionary and search does not do prefix matching: the query "searchtoken1"
// does NOT FIND a product that says "searchtoken10". The counter is therefore
// enough to keep the words apart.
func searchWord() string {
	return fmt.Sprintf("searchtoken%d", fixtureCounter.Add(1))
}

// newSearchProduct creates a published product carrying the given word in its
// TITLE and returns its ID together with its handle.
//
// The product is set up from the SERVICE, not over HTTP: the first link of the
// chain is the product service's event publication, and that is exactly where
// the test starts.
//
// The status is published. A draft product is not returned by the catalog's
// storefront read anyway, so what a draft fixture measured would be the
// publication filter rather than the index.
func newSearchProduct(ctx context.Context, t *testing.T, word string) (productID, handle string) {
	t.Helper()

	seq := fixtureCounter.Add(1)
	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-search-%d", seq),
		Title:  searchTitle(word),
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err, "could not create the search fixture product")

	return product.ID, product.Handle
}

// searchTitle embeds the search word in a product title.
//
// The word sits in the TITLE because the plugin gives the title the highest
// weight; the fixed prefix lets the fixture be recognized in an error message.
func searchTitle(word string) string { return "E2E Search " + word }

// callSearch calls the search endpoint with the given publishable key.
//
// The address is built from the plugin's exported constant
// ([searchpg.SearchPath]) rather than written out by hand: the endpoint address
// is the contract the plugin PUBLISHES, and the test departing from it would
// mean the test keeps verifying the old address once the address changes.
func callSearch(t *testing.T, key string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return magazaIstegi(t, searchpg.SearchPath+"?"+query.Encode(), key)
}

// searchEnvelope decodes the search response and verifies that the endpoint
// returned 200.
//
// The response is decoded with [vitrinZarfi], that is with the SAME type as the
// store product list. This is not a convenience but a deliberate reading: the
// plugin takes the records to be shown from the catalog's storefront surface and
// does not reshape them, so the two endpoints' bodies have the same shape.
func searchEnvelope(t *testing.T, key string, query url.Values) vitrinZarfi {
	t.Helper()

	recorder := callSearch(t, key, query)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the search endpoint must return 200; body: %s", recorder.Body.String())

	var envelope vitrinZarfi
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"could not decode the search response; body: %s", recorder.Body.String())

	return envelope
}

// searchResults searches for a single word.
func searchResults(t *testing.T, key, word string) vitrinZarfi {
	t.Helper()

	return searchEnvelope(t, key, url.Values{"q": {word}})
}

// pollUntil polls SYNCHRONOUSLY until the condition holds.
//
// [require.Eventually] was deliberately not used: it runs the condition in a
// separate goroutine, and when the deadline expires that goroutine may still be
// running. A variable kept outside the condition in order to carry the last
// observation into the failure message would in that case be a real data race,
// and the tests run with -race. A synchronous loop has no such race; the caller
// can safely assert on the last value it saw.
func pollUntil(condition func() bool) bool {
	deadline := time.Now().Add(olayBeklemeSuresi)

	for {
		if condition() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}

		time.Sleep(searchPollInterval)
	}
}

// waitForSearch waits for the query to return the expected set of IDs.
//
// The wait is MANDATORY: [eventbus.EventBus].Publish does not wait for the
// handlers, so by the time the product service returns, the index write may not
// even have started.
//
// The expected set may be given empty; in that case the call returns
// immediately and the "nothing was found" observation is not the result of a
// wait. This distinction matters: we cannot prove by waiting that something is
// NOT there, so every scenario that asserts an empty set must first have waited
// for a non-empty one.
func waitForSearch(t *testing.T, key, word string, expected ...string) {
	t.Helper()

	var last []string
	// slices.Equal is ORDER sensitive; since the scenarios assert sets with a
	// single ID this is enough, and it preserves the relevance order too.
	pollUntil(func() bool {
		last = searchResults(t, key, word).kimlikler()

		return slices.Equal(last, expected)
	})

	require.ElementsMatch(t, expected, last,
		"the %q query must return the expected IDs; if it does not, a link of the "+
			"event -> index -> search chain is broken", word)
}

// indexRowExists says whether the product has a row in the plugin's OWN table.
//
// # Why raw SQL
//
// The plugin's only exported read path is the search endpoint, and that endpoint
// returns a result that HAS PASSED the channel filter. The observation "search
// came back empty" is therefore consistent with two different worlds: the row
// was deleted, or the row is still standing but the catalog is hiding the
// record. That is exactly what separates the two claims — on a delete the row
// MUST BE GONE, on channel filtering the row MUST REMAIN — and there is no way
// to see that distinction over HTTP.
//
// The table being read belongs to the plugin itself; no other module's schema is
// touched.
func indexRowExists(ctx context.Context, t *testing.T, productID string) bool {
	t.Helper()

	var exists bool
	err := testPool.Pool().QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+searchIndexTable+" WHERE product_id = $1)",
		productID).Scan(&exists)
	require.NoError(t, err, "could not read the search index")

	return exists
}

// tableExists says whether a table with the given name exists in the database.
func tableExists(ctx context.Context, t *testing.T, name string) bool {
	t.Helper()

	var exists bool
	require.NoError(t,
		testPool.Pool().QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists),
		"could not query the %q table", name)

	return exists
}

// TestTheSearchPluginBringsItsOwnModule verifies the plugin's FIRST extension
// point on the real ground.
//
// A plugin does not merely register a provider, it brings its OWN module and
// that module passes through the same lifecycle as the core modules. The
// existence of the two tables is the proof of this: the data table shows that
// the migration was applied, while the SEPARATE version ledger shows that the
// migration ran under the plugin's own ownership — that is, that its schema is
// not mixed into any module's ledger.
//
// A shared "schema_migrations" table must NEVER come into existence; if it did,
// the plugin's schema would be writing into the same ledger as the modules', and
// it would therefore be impossible to know what the plugin leaves behind when it
// is removed.
func TestTheSearchPluginBringsItsOwnModule(t *testing.T) {
	ctx := t.Context()

	assert.True(t, tableExists(ctx, t, searchIndexTable),
		"the %q table must come into existence; if it did not, the plugin's module never passed through the migration step",
		searchIndexTable)

	ledger := searchpg.ModuleName + "_schema_migrations"
	assert.True(t, tableExists(ctx, t, ledger),
		"the %q version ledger must come into existence; the plugin's schema must write into its own ledger", ledger)

	assert.False(t, tableExists(ctx, t, "schema_migrations"),
		"a shared schema_migrations table must NEVER come into existence")
}

// TestAProductEventReachesTheIndexAndSearchFindsIt proves the whole chain:
// product.created is published, the plugin indexes it and the search endpoint
// finds the product.
//
// The product is written only from the SERVICE; the test never touches the
// index. Every step in between (publishing the event, invoking the subscriber,
// reading the catalog, writing the document) happens in real components, so the
// result only arrives if the whole chain works.
func TestAProductEventReachesTheIndexAndSearchFindsIt(t *testing.T) {
	ctx := t.Context()
	word := searchWord()

	productID, handle := newSearchProduct(ctx, t, word)

	waitForSearch(t, publishableKey, word, productID)

	envelope := searchResults(t, publishableKey, word)
	require.Len(t, envelope.Data, 1, "search must return a single record; body: %+v", envelope)
	assert.Equal(t, handle, envelope.Data[0].Handle,
		"the record must come FROM THE CATALOG: nothing but the ID is kept in the index, "+
			"so a filled-in handle can only come from the storefront read")
	assert.Equal(t, 1, envelope.Count,
		"the counter must report the number of records in this response")
}

// TestUpdatingAProductRefreshesTheIndex verifies that the index does not fall
// behind the record.
//
// The two assertions are meaningful together: the product must be found by its
// NEW title and must NOT be found by its OLD title. Had only the first been
// exercised, an implementation that ADDS ON TOP of the document instead of
// refreshing it would pass as well; under that implementation a product would go
// on showing up in search under an old title it never carried.
//
// The old word coming back empty needs no wait here: the two words live in the
// SAME row, so the moment the new word is found the old word has long been
// deleted.
func TestUpdatingAProductRefreshesTheIndex(t *testing.T) {
	ctx := t.Context()
	oldWord, newWord := searchWord(), searchWord()

	productID, _ := newSearchProduct(ctx, t, oldWord)
	waitForSearch(t, publishableKey, oldWord, productID)

	newTitle := searchTitle(newWord)
	_, err := productSvc.UpdateProduct(ctx, productID, productsvc.UpdateProductInput{Title: &newTitle})
	require.NoError(t, err, "could not update the product title")

	waitForSearch(t, publishableKey, newWord, productID)
	waitForSearch(t, publishableKey, oldWord)
}

// TestDeletingAProductDropsItFromTheIndex verifies that the delete event really
// drops the index row.
//
// Search going empty proves nothing ON ITS OWN: a deleted product is not
// returned by the catalog's storefront read either, so search would have come
// back empty even if the plugin had never processed the delete event — and the
// stale row would have stayed in the index forever. The real claim is therefore
// about the row itself.
func TestDeletingAProductDropsItFromTheIndex(t *testing.T) {
	ctx := t.Context()
	word := searchWord()

	productID, _ := newSearchProduct(ctx, t, word)
	waitForSearch(t, publishableKey, word, productID)
	require.True(t, indexRowExists(ctx, t, productID),
		"precondition: since the product is found in search it must have a row in the index")

	require.NoError(t, productSvc.DeleteProduct(ctx, productID), "could not delete the product")

	assert.True(t, pollUntil(func() bool { return !indexRowExists(ctx, t, productID) }),
		"the deleted product's index row MUST BE DROPPED; if it stays, the index diverges from "+
			"the catalog and the divergence is only noticed once the row becomes visible again some day")
	waitForSearch(t, publishableKey, word)
}

// TestSearchDoesNotBypassChannelFiltering verifies that search is not a BYPASS
// of the catalog filtering.
//
// The setup is two storefronts and the product is assigned ONLY to the second
// channel: the same word must give two different results with two different
// publishable keys. A "not found" observation with a single key would prove
// nothing — the product might simply not have been indexed; the second key
// FINDING the SAME product is the only observation that says the reason for the
// hiding is exactly the channel.
//
// That the index row is STILL STANDING is exercised as well, and that is the
// core of the claim: the plugin does not repeat the channel rule in its own
// index, it produces nothing but IDs from the index and the catalog applies the
// filter. The product not showing up in the first storefront while the row is
// still standing shows that the filter really runs at read time.
func TestSearchDoesNotBypassChannelFiltering(t *testing.T) {
	ground := channelCatalogFixture(t)
	ctx := t.Context()

	word := searchWord()
	productID, _ := newSearchProduct(ctx, t, word)
	require.NoError(t, bindChannel(productID, ground.secondChannelID),
		"the product must be bound to the second sales channel")

	// The product is first expected to be found in its OWN storefront; that is
	// the proof that the index has been filled. The observation "it is not in
	// the first storefront", made without waiting for this, could just as well
	// be explained by the index not having been written yet.
	waitForSearch(t, ground.ikinciAnahtar, word, productID)

	assert.True(t, indexRowExists(ctx, t, productID),
		"the product MUST BE in the index; that the filter is applied in the catalog can only be seen while the row is standing")

	first := searchResults(t, publishableKey, word)
	assert.Empty(t, first.kimlikler(),
		"a product assigned to another channel must not show up in this storefront's SEARCH; if it does, "+
			"search has become a bypass of the channel filtering")
	assert.Zero(t, first.Count, "the counter must reflect the filtered set too")

	// The channel cannot be chosen FROM THE QUERY STRING: if it could, a client
	// arriving with any publishable key it happens to hold would be searching in
	// another storefront's catalog. The same guard is exercised on the storefront
	// list too (see [TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString]);
	// it cannot be assumed that search inherits it, because the code that reads
	// the channels from the request's identity is SEPARATE.
	bypass := searchEnvelope(t, publishableKey, url.Values{
		"q":                {word},
		"sales_channel_id": {ground.secondChannelID},
	})
	assert.Empty(t, bypass.kimlikler(),
		"the channel ID in the query string MUST BE IGNORED")
}

// TestTheSearchEndpointIsRejectedWithoutAPublishableKey verifies that the new
// endpoint enters the guard stack automatically.
//
// The guard stack is attached to the PATH PREFIX rather than to individual
// endpoints (see corehttp.APIGuards), so a /store/v1 endpoint opened by a plugin
// is guarded without doing anything at all. That is exactly what is exercised:
// the possibility of a plugin author forgetting the guard must be
// architecturally gone.
//
// BOTH of the two requests are necessary. A 401 on its own does NOT SAY that the
// endpoint exists: because the guard runs before routing, a NON-EXISTENT path
// under /store/v1 returns 401 too. The same address returning 200 with a valid
// key is the second observation that nails down that what was rejected really is
// the search endpoint.
func TestTheSearchEndpointIsRejectedWithoutAPublishableKey(t *testing.T) {
	query := url.Values{"q": {searchWord()}}

	withoutKey := callSearch(t, "", query)
	require.Equal(t, http.StatusUnauthorized, withoutKey.Code,
		"search without a publishable key must be rejected; body: %s", withoutKey.Body.String())

	valid := callSearch(t, publishableKey, query)
	assert.Equal(t, http.StatusOK, valid.Code,
		"the same address must work with a valid key; if it does not, the 401 comes not from the "+
			"endpoint's PRESENCE but from its ABSENCE; body: %s", valid.Body.String())
}
