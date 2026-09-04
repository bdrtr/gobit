//go:build integration

package product_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves the storefront's SALES CHANNEL filter against a real
// database.
//
// None of the claims here can be proven with a fake repository: the filter is
// an EXISTS/NOT EXISTS condition added to the product's own query, the link
// table the condition queries is created by core/link at run time, and that
// the total count runs over the filtered set together with LIMIT/OFFSET is
// visible only in real SQL.
//
// The auth module IS NOT HERE and CANNOT BE IMPORTED (Principle 2.4): the
// sales channel IDs are plain strings, exactly as they will arrive from the
// publishable key in production. The only things product knows about auth are
// the link name and the entity name.

// storeChannelRequest makes a store request bound to the given sales
// channels.
//
// In production corehttp.RequireStore puts the principal in place (with the
// publishable key's channels); because this setup mounts the router directly,
// the principal is put in place by hand. If channels is nil the principal is
// still PUT IN PLACE but has no channels — there is a separate helper for
// telling a nil slice apart from "no principal at all"
// (see [system.storeRequestWithoutPrincipal]).
func (s system) storeChannelRequest(t *testing.T, target string, channels []string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:              "apk_test",
		Kind:            "api_key",
		SalesChannelIDs: channels,
	}))
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// storeRequestWithoutPrincipal makes a store request without a principal.
func (s system) storeRequestWithoutPrincipal(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec
}

// storeListing gives the handles returned by the storefront list and the total
// count.
func storeListing(t *testing.T, rec *httptest.ResponseRecorder) (handles []string, count int) {
	t.Helper()

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := jsonBody(t, rec)

	countValue, ok := body["count"].(float64)
	require.True(t, ok, "count must be a number: %#v", body["count"])

	data, ok := body["data"].([]any)
	require.True(t, ok, "data must be an array: %#v", body["data"])
	for _, raw := range data {
		item, ok := raw.(map[string]any)
		require.True(t, ok, "a list item must be an object: %#v", raw)
		handle, ok := item["handle"].(string)
		require.True(t, ok, "a product must carry a handle: %#v", item)
		handles = append(handles, handle)
	}
	return handles, int(countValue)
}

// itemData gives the "data" object inside a single-item response envelope.
func itemData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	data, ok := jsonBody(t, rec)["data"].(map[string]any)
	require.True(t, ok, "data must be an object: %s", rec.Body.String())
	return data
}

// channelFixture is the shared setup of the channel tests.
//
// Because the tests share a single database, every test separates its own
// products WITH A COLLECTION; otherwise the published products left behind by
// neighboring tests get mixed into the list and the count assertions become
// meaningless. The channel IDs are unique per test as well.
type channelFixture struct {
	sys          system
	collectionID string
	channelA     string
	channelB     string
}

// newChannelFixture produces an isolated collection and two channel IDs.
func newChannelFixture(t *testing.T) channelFixture {
	t.Helper()

	sys := newSystem(t)
	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)

	collection, err := svc.CreateCollection(context.Background(), service.CreateCollectionInput{
		Title: "Channel " + uniqueHandle("collection"),
	})
	require.NoError(t, err)

	return channelFixture{
		sys:          sys,
		collectionID: collection.ID,
		channelA:     "sc_" + uniqueHandle("a"),
		channelB:     "sc_" + uniqueHandle("b"),
	}
}

// seedPublished adds a published product to the collection and returns its ID.
func (f channelFixture) seedPublished(t *testing.T, handle string) string {
	t.Helper()

	rec := f.sys.request(t, http.MethodPost, "/admin/v1/products", `{
		"handle": "`+handle+`",
		"title": "Channel Product",
		"status": "published",
		"collection_id": "`+f.collectionID+`"
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	id, ok := itemData(t, rec)["id"].(string)
	require.True(t, ok, "the created product must carry an ID: %s", rec.Body.String())
	return id
}

// assign binds the product to a sales channel (through the admin endpoint).
func (f channelFixture) assign(t *testing.T, productID, channelID string) {
	t.Helper()

	rec := f.sys.request(t, http.MethodPost, "/admin/v1/products/"+productID+"/sales-channels",
		`{"sales_channel_id": "`+channelID+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
}

// list reads the collection's storefront list with the given channels.
func (f channelFixture) list(t *testing.T, channels []string) (handles []string, count int) {
	t.Helper()
	return storeListing(t, f.sys.storeChannelRequest(t,
		"/store/v1/products?collection_id="+f.collectionID, channels))
}

// TestStoreListingShowsUnassignedProductInEveryChannel verifies the
// backward compatible half of the rule against a real database: a product
// with no assignment shows up in every channel.
func TestStoreListingShowsUnassignedProductInEveryChannel(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("unassigned")
	fx.seedPublished(t, handle)

	for _, channels := range [][]string{{fx.channelA}, {fx.channelB}, {fx.channelA, fx.channelB}} {
		handles, count := fx.list(t, channels)
		assert.Equal(t, []string{handle}, handles, "an unassigned product must show up in the %v channels", channels)
		assert.Equal(t, 1, count, "the count must count the unassigned product too")
	}
}

// TestStoreListingHidesProductFromForeignChannel verifies the filter's REAL
// job in real SQL: a product assigned to channel A shows up in A and does not
// show up in B.
//
// The fault was exactly this: the publishable key's channels were resolved but
// no module read them, so every key got the same catalog.
func TestStoreListingHidesProductFromForeignChannel(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("channel-a")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	handles, count := fx.list(t, []string{fx.channelA})
	assert.Equal(t, []string{handle}, handles, "the product must show up in the channel it is assigned to")
	assert.Equal(t, 1, count)

	handles, count = fx.list(t, []string{fx.channelB})
	assert.Empty(t, handles, "the product MUST NOT SHOW UP in a channel it is not assigned to")
	assert.Zero(t, count, "the count must not count the hidden product either")
}

// TestStoreListingShowsProductInAllAssignedChannels verifies in the REAL link
// table that the ManyToMany binding really is multiple.
//
// Had the cardinality been declared wrongly, the second assignment would hit
// the uniqueness index and return 409; a fake link service cannot prove that.
func TestStoreListingShowsProductInAllAssignedChannels(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("two-channels")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)
	fx.assign(t, productID, fx.channelB)

	for _, channel := range []string{fx.channelA, fx.channelB} {
		handles, count := fx.list(t, []string{channel})
		assert.Equal(t, []string{handle}, handles, "it must show up in the %q channel", channel)
		assert.Equal(t, 1, count)
	}

	// The bindings really are in the link table; the admin endpoint reads them
	// back.
	rec := fx.sys.request(t, http.MethodGet, "/admin/v1/products/"+productID+"/sales-channels", "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.ElementsMatch(t, []any{fx.channelA, fx.channelB},
		itemData(t, rec)["sales_channel_ids"])

	linked, err := fx.sys.links.List(context.Background(), service.LinkProductSalesChannel, productID)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.channelA, fx.channelB}, linked,
		"the bindings must be readable from the core's link service as well")
}

// TestStoreListingFilterKeepsPagingConsistent verifies with real LIMIT/OFFSET
// that the filter does not break PAGING.
//
// Had the filtering been done on the Go side, LIMIT would be applied over the
// unfiltered set, the pages would come back short and the count would promise
// pages the client can never reach. The test locks two claims together: the
// count reflects the filtered set, and the pages carry exactly that many
// records.
func TestStoreListingFilterKeepsPagingConsistent(t *testing.T) {
	fx := newChannelFixture(t)

	var hidden, expected []string
	for i := range 6 {
		handle := uniqueHandle(fmt.Sprintf("page-%d", i))
		productID := fx.seedPublished(t, handle)
		if i%3 == 0 {
			fx.assign(t, productID, fx.channelB)
			hidden = append(hidden, handle)
			continue
		}
		expected = append(expected, handle)
	}
	require.Len(t, hidden, 2)
	require.Len(t, expected, 4)

	const pageSize = 3
	var collected []string
	for offset := 0; offset < 6; offset += pageSize {
		rec := fx.sys.storeChannelRequest(t, fmt.Sprintf(
			"/store/v1/products?collection_id=%s&limit=%d&offset=%d", fx.collectionID, pageSize, offset),
			[]string{fx.channelA})
		handles, count := storeListing(t, rec)

		assert.Equal(t, 4, count, "the total count must reflect the FILTERED set (offset=%d)", offset)
		for _, handle := range handles {
			assert.NotContains(t, hidden, handle, "a foreign channel's product must not show up on any page")
		}
		collected = append(collected, handles...)
	}

	assert.ElementsMatch(t, expected, collected,
		"the pages must carry all of the records the count promised, and only those")
	// The first page must be FULL: had the elimination not been done in the
	// database, the page would come back short by the number of filtered rows.
	rec := fx.sys.storeChannelRequest(t, fmt.Sprintf(
		"/store/v1/products?collection_id=%s&limit=%d&offset=0", fx.collectionID, pageSize),
		[]string{fx.channelA})
	firstPage, _ := storeListing(t, rec)
	assert.Len(t, firstPage, pageSize, "the first page must carry as many records as the requested page size")
}

// TestStoreSingleProductIsFilteredToo verifies that the SINGLE endpoint is
// filtered as well.
//
// Hiding it in the list and showing it on the single endpoint would make the
// hiding meaningless: storefront addresses carry the handle, so this is
// precisely the endpoint that is guessable. In a foreign channel a product
// returns the SAME error (404) as a product that is not published.
func TestStoreSingleProductIsFilteredToo(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("single-channel")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	rec := fx.sys.storeChannelRequest(t, "/store/v1/products/"+handle, []string{fx.channelA})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	assert.Equal(t, productID, itemData(t, rec)["id"])

	rec = fx.sys.storeChannelRequest(t, "/store/v1/products/"+handle, []string{fx.channelB})
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"the product must not be found in a foreign channel; body: %s", rec.Body.String())

	rec = fx.sys.storeChannelRequest(t, "/store/v1/products/"+productID, []string{fx.channelB})
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"a call by ID must be filtered too; body: %s", rec.Body.String())
}

// TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned pins the defensive
// behavior of a principal without channels in real SQL: an empty set is not
// "no filtering".
func TestStoreListingWithEmptyChannelSetShowsOnlyUnassigned(t *testing.T) {
	fx := newChannelFixture(t)
	assignedHandle := uniqueHandle("empty-assigned")
	freeHandle := uniqueHandle("empty-unassigned")
	assignedID := fx.seedPublished(t, assignedHandle)
	fx.seedPublished(t, freeHandle)
	fx.assign(t, assignedID, fx.channelA)

	handles, count := fx.list(t, []string{})
	assert.Equal(t, []string{freeHandle}, handles,
		"a principal without channels must see only the unassigned products")
	assert.Equal(t, 1, count)
}

// TestStoreListingWithoutPrincipalIsNotFiltered verifies that a request
// without a principal is not filtered.
//
// This is the setup in which store authentication is not wired at all
// (product can be deployed on its own). It is at the same time the proof that
// the "parameter is NULL" branch in the SQL really works: had the nil slice
// not gone to the database as NULL, this request would have missed the
// assigned product.
func TestStoreListingWithoutPrincipalIsNotFiltered(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("no-principal")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	handles, count := storeListing(t, fx.sys.storeRequestWithoutPrincipal(t,
		"/store/v1/products?collection_id="+fx.collectionID))
	assert.Equal(t, []string{handle}, handles, "with no principal the filter must not be applied")
	assert.Equal(t, 1, count)
}

// TestAdminListingIsNotFilteredBySalesChannel verifies that the admin listing
// is not filtered.
//
// Were it filtered, assigning a product to a channel would drop it out of the
// admin list too and the operator would never find the product again.
func TestAdminListingIsNotFilteredBySalesChannel(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("admin-channel")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	rec := fx.sys.request(t, http.MethodGet,
		"/admin/v1/products?collection_id="+fx.collectionID, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body := jsonBody(t, rec)
	assert.Equal(t, float64(1), body["count"])
	data, ok := body["data"].([]any)
	require.True(t, ok, "data must be an array: %#v", body["data"])
	require.Len(t, data, 1)

	item, ok := data[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, handle, item["handle"])
}

// TestRemoveSalesChannelMakesProductGloballyVisible verifies that removing the
// last binding does not hide the product but, on the contrary, makes it
// visible in every channel.
func TestRemoveSalesChannelMakesProductGloballyVisible(t *testing.T) {
	fx := newChannelFixture(t)
	handle := uniqueHandle("unbind")
	productID := fx.seedPublished(t, handle)
	fx.assign(t, productID, fx.channelA)

	handles, _ := fx.list(t, []string{fx.channelB})
	require.Empty(t, handles, "the product must first be hidden in the foreign channel")

	rec := fx.sys.request(t, http.MethodDelete,
		"/admin/v1/products/"+productID+"/sales-channels/"+fx.channelA, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	handles, count := fx.list(t, []string{fx.channelB})
	assert.Equal(t, []string{handle}, handles, "a product with no assignment left is again visible in all channels")
	assert.Equal(t, 1, count)

	linked, err := fx.sys.links.List(context.Background(), service.LinkProductSalesChannel, productID)
	require.NoError(t, err)
	assert.Empty(t, linked, "the binding must really be deleted from the link table")
}

// TestProductDeletionRemovesSalesChannelLinks verifies that a deleted
// product's channel bindings are cleaned out of the REAL link table.
func TestProductDeletionRemovesSalesChannelLinks(t *testing.T) {
	fx := newChannelFixture(t)
	productID := fx.seedPublished(t, uniqueHandle("deleted-channel"))
	fx.assign(t, productID, fx.channelA)

	rec := fx.sys.request(t, http.MethodDelete, "/admin/v1/products/"+productID, "")
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	linked, err := fx.sys.links.List(context.Background(), service.LinkProductSalesChannel, productID)
	require.NoError(t, err)
	assert.Empty(t, linked, "no channel binding may remain for a deleted product")
}

// TestSalesChannelLinkRejectsUnknownProduct verifies that binding a
// nonexistent product returns 404.
func TestSalesChannelLinkRejectsUnknownProduct(t *testing.T) {
	sys := newSystem(t)

	rec := sys.request(t, http.MethodPost, "/admin/v1/products/prod_missing/sales-channels",
		`{"sales_channel_id": "sc_1"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
}

// --- the scope question on the WRITE path ----------------------------------

// seedPublishedWithVariant adds a published product and a variant bound to it
// to the collection; it returns the IDs of the product and of the variant.
//
// A variant is needed because what enters the cart is not the PRODUCT but the
// VARIANT, and on the write path the scope question is asked with the variant
// ID. The channel assignment, on the other hand, is made on the product; what
// these tests exercise is exactly that this inheritance works in real SQL.
func (f channelFixture) seedPublishedWithVariant(t *testing.T, handle string) (productID, variantID string) {
	t.Helper()

	productID = f.seedPublished(t, handle)

	rec := f.sys.request(t, http.MethodPost, "/admin/v1/products/"+productID+"/variants",
		`{"title": "One size"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

	variantID, ok := itemData(t, rec)["id"].(string)
	require.True(t, ok, "the created variant must carry an ID: %s", rec.Body.String())
	return productID, variantID
}

// variantIDsInChannels resolves the variant provider under the NAME Query
// registered it with and returns the IDs visible in the given channels.
//
// The provider is resolved from the container by name so that the test follows
// the path the cart flow really follows: the flow knows not the concrete type
// but the name "variant.query" (ADR 0004/0006). A test that called the
// constructor directly would stay green even if the registration's name were
// wrong.
func (f channelFixture) variantIDsInChannels(t *testing.T, ids, channels []string) []string {
	t.Helper()

	provider, err := container.Resolve[query.Provider](f.sys.container,
		service.EntityVariant+query.ProviderSuffix)
	require.NoError(t, err, "the variant provider must be registered")

	filters := map[string]any{"ids": ids}
	if channels != nil {
		filters[service.FilterSalesChannelIDs] = channels
	}

	records, err := provider.List(context.Background(), query.ListOptions{
		Fields:  []string{query.IDField},
		Filters: filters,
	})
	require.NoError(t, err)

	out := make([]string, 0, len(records))
	for i := range records {
		id, ok := records[i][query.IDField].(string)
		require.True(t, ok, "a record must carry an ID: %v", records[i])
		out = append(out, id)
	}
	return out
}

// TestVariantVisibilityFollowsProductChannels verifies in REAL SQL that the
// variant's scope derives from the product's channels.
//
// This is the very question the write path that adds a line to the cart asks,
// and it cannot be proven with a fake repository: the condition is an
// EXISTS/NOT EXISTS that looks at the link table through the variant's
// product_id, and the table is created by core/link at run time.
func TestVariantVisibilityFollowsProductChannels(t *testing.T) {
	fx := newChannelFixture(t)

	assignedID, assignedVariant := fx.seedPublishedWithVariant(t, uniqueHandle("channel-variant"))
	fx.assign(t, assignedID, fx.channelA)
	_, unassignedVariant := fx.seedPublishedWithVariant(t, uniqueHandle("unassigned-variant"))

	all := []string{assignedVariant, unassignedVariant}

	assert.ElementsMatch(t, all, fx.variantIDsInChannels(t, all, []string{fx.channelA}),
		"a variant must show up in the channel its product is assigned to")
	assert.Equal(t, []string{unassignedVariant}, fx.variantIDsInChannels(t, all, []string{fx.channelB}),
		"in a FOREIGN channel only the unassigned product's variant may remain; if it does not, "+
			"the write path is unscoped and another storefront's product can enter the cart")
	assert.Equal(t, []string{unassignedVariant}, fx.variantIDsInChannels(t, all, []string{}),
		"a principal without channels is the EMPTY SET: only the unassigned product's variant is visible")
	assert.ElementsMatch(t, all, fx.variantIDsInChannels(t, all, nil),
		"when no filter is asked for at all, no scope must be applied")
}

// TestVariantVisibilityMatchesStoreListing verifies that the write path's
// answer is the SAME as the storefront list's.
//
// The two surfaces diverging in the same setup is the very class of bug this
// change tries to close: when the rule is applied in one place and not in the
// other, a product hidden in the storefront stays sellable in the cart. The
// test puts the two side by side and makes the divergence visible.
func TestVariantVisibilityMatchesStoreListing(t *testing.T) {
	fx := newChannelFixture(t)

	hiddenID, hiddenVariant := fx.seedPublishedWithVariant(t, uniqueHandle("hidden"))
	fx.assign(t, hiddenID, fx.channelA)

	handles, _ := fx.list(t, []string{fx.channelB})
	require.Empty(t, handles, "the product MUST NOT SHOW UP in the foreign storefront (read surface)")

	assert.Empty(t, fx.variantIDsInChannels(t, []string{hiddenVariant}, []string{fx.channelB}),
		"the variant of a product hidden in the storefront MUST NOT SHOW UP on the write path either; "+
			"if it does, the hiding is merely cosmetic")
}
