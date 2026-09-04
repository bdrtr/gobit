//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves, with the REAL stack, that the product ↔ sales channel bond
// is reflected in the storefront:
//
//	The same store endpoint, called with two different publishable keys,
//	returns two DIFFERENT catalogs.
//
// # Why it goes through HTTP
//
// The claim itself is not a service claim. For the filter to work correctly
// three separate layers have to line up: auth has to resolve the key's channel,
// the core's protection stack has to put it into the context, and the product
// handler has to read the channel from that identity and NOT from the QUERY
// STRING. A test that called the service directly would stay green even if the
// handler never read the channel at all — the filter would then NEVER run in
// production, but the module tests could not see it.
//
// # Why a second key
//
// With a single key the observation "the product is not visible" proves nothing
// on its own: the product may have been deleted, it may have been left as a
// draft, or the query may be broken. The second key SEEING the SAME product is
// the only observation that says the reason for the hiding is exactly the
// channel.
//
// # Do new products break the shared ground
//
// They do not: none of the existing tests in the package make a claim that
// rests on the NUMBER of products (they look at status codes and at the records
// they set up themselves). The other direction — the shared ground breaking
// THIS test — is a real risk, and it is closed off by collection isolation
// (see [channelCatalogFixture]).

// The fixture constants of the second storefront.
//
// The names and the handles are FIXED (they are not produced by the fixture
// counter): setup runs once per process, and fixed names make it readable at a
// glance which record an error message is talking about.
const (
	// secondChannelName is the name of the second sales channel, separate from
	// the shared fixture channel ([testChannelName]).
	secondChannelName = "e2e-second-storefront"
	// channelCatalogCollectionHandle is the handle of the collection that
	// separates the three fixture products from the shared catalog.
	channelCatalogCollectionHandle = "e2e-channel-catalog"
)

// channelCatalogProduct holds the fields of the fixture product that the test
// needs.
//
// The handle is carried as well, because the single-product storefront endpoint
// can be called with either address, and a filter that worked ONLY on the
// identity would mean the storefront addresses (the handles) were left
// unfiltered.
type channelCatalogProduct struct {
	id     string
	handle string
}

// channelCatalog is the set-up ground of the two-storefront scenario.
type channelCatalog struct {
	// secondChannelID is the second sales channel, separate from [testChannelID].
	secondChannelID string
	// ikinciAnahtar is the publishable key bound ONLY to the second channel.
	ikinciAnahtar string
	// koleksiyonID is the collection all three products belong to.
	koleksiyonID string
	// firstChannelProduct is assigned to the shared fixture channel only.
	firstChannelProduct channelCatalogProduct
	// secondChannelProduct is assigned to [channelCatalog.secondChannelID] only.
	secondChannelProduct channelCatalogProduct
	// unassignedProduct is assigned to no channel at all and by the rule has to be
	// visible in BOTH.
	unassignedProduct channelCatalogProduct
}

// The state needed so that the fixture is set up only once.
var (
	// channelCatalogOnce makes sure the ground is set up only once.
	channelCatalogOnce sync.Once
	// channelCatalogGround is the set-up ground.
	channelCatalogGround channelCatalog
	// channelCatalogErr carries the setup error to the tests.
	channelCatalogErr error
)

// channelCatalogFixture sets up the two-storefront ground and returns it.
//
// # Why once
//
// The sales channel name and the product handle are UNIQUE; setting them up
// again in every test would collide on the second call. Setup therefore lives
// inside a [sync.Once] and the error is carried outwards (see
// [yetkisizYoneticiJetonu], the same pattern).
//
// # Why it is isolated with a collection
//
// The shared ground holds dozens of fixture products and NONE of them has a
// channel assignment; by the rule they all show up in EVERY storefront. So
// "filtering by my own channel" does not isolate this test — the three products
// would be lost among foreign products and the counter claim (does "count"
// reflect the filtered set) could never be made against a fixed number. The
// three products are therefore put into a COLLECTION that only this file sets
// up, and every request is narrowed with collection_id.
//
// The isolation has no cost, and it has a side benefit: the collection filter
// and the channel filter are ANDed in the same query, so at the same time the
// test proves that the two do not override each other. Giving up the counter
// claim altogether (the alternative) would have left the task's actual question
// unanswered.
func channelCatalogFixture(t *testing.T) channelCatalog {
	t.Helper()

	channelCatalogOnce.Do(func() {
		// The setup context is NOT t.Context(): the ground is shared between
		// tests and the first test's context is cancelled when that test ends.
		// Even though setup completes here, keeping that context would leave
		// the door open for a step added later to run with a cancelled
		// context.
		channelCatalogGround, channelCatalogErr = setUpChannelCatalog(context.Background())
	})
	require.NoError(t, channelCatalogErr, "the channel catalog fixture could not be set up")

	return channelCatalogGround
}

// setUpChannelCatalog prepares the second channel, the second key and the three
// products.
//
// The channel and the key are set up FROM THE SERVICE while the channel
// ASSIGNMENTS are set up FROM THE ADMIN ENDPOINT, and that split is deliberate:
// the first two are not the subject of this test (the identity ground is
// already set up, see [setUpIdentityFixture]), whereas the third is exactly the
// thing under test. Doing the assignment from the service would not prove that
// the admin endpoint writes THE VERY bond the storefront reads — the endpoint
// could write on behalf of some other link and the test would still stay green.
func setUpChannelCatalog(ctx context.Context) (channelCatalog, error) {
	var ground channelCatalog

	channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        secondChannelName,
		Description: "the second storefront of the end-to-end test",
	})
	if err != nil {
		return ground, fmt.Errorf("the second sales channel could not be set up: %w", err)
	}
	ground.secondChannelID = channel.ID

	if _, ground.ikinciAnahtar, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeyPublishable,
		Title:     "e2e second publishable key",
		CreatedBy: adminID,
		// The key is bound ONLY to the second channel; had it carried both
		// channels at once the whole test would have become meaningless.
		SalesChannelIDs: []string{channel.ID},
	}); err != nil {
		return ground, fmt.Errorf("the second publishable key could not be set up: %w", err)
	}

	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E Channel Catalog",
		Handle: channelCatalogCollectionHandle,
	})
	if err != nil {
		return ground, fmt.Errorf("the isolation collection could not be set up: %w", err)
	}
	ground.koleksiyonID = collection.ID

	if ground.firstChannelProduct, err = setUpChannelCatalogProduct(ctx, collection.ID, "first"); err != nil {
		return ground, err
	}
	if ground.secondChannelProduct, err = setUpChannelCatalogProduct(ctx, collection.ID, "second"); err != nil {
		return ground, err
	}
	if ground.unassignedProduct, err = setUpChannelCatalogProduct(ctx, collection.ID, "unassigned"); err != nil {
		return ground, err
	}

	if err := bindChannel(ground.firstChannelProduct.id, testChannelID); err != nil {
		return ground, err
	}
	if err := bindChannel(ground.secondChannelProduct.id, channel.ID); err != nil {
		return ground, err
	}

	return ground, nil
}

// setUpChannelCatalogProduct creates a PUBLISHED product bound to the
// collection.
//
// The status is [productmodels.StatusPublished]: a draft product is already
// invisible in the storefront, and in that case what the test measured would
// not be the channel filter but the publication filter.
func setUpChannelCatalogProduct(ctx context.Context, collectionID, name string) (channelCatalogProduct, error) {
	handle := "e2e-channel-catalog-" + name

	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:       handle,
		Title:        "E2E Channel Catalog " + name,
		Status:       productmodels.StatusPublished,
		CollectionID: &collectionID,
	})
	if err != nil {
		return channelCatalogProduct{}, fmt.Errorf("the %q product could not be set up: %w", handle, err)
	}

	return channelCatalogProduct{id: product.ID, handle: product.Handle}, nil
}

// adminRequestWithBody makes an admin write request with the secret key.
//
// It stands apart from [adminRequest] because the two are called from different
// places: that helper wants a *testing.T, whereas this one is called from the
// fixture's [sync.Once] body — that is, from a place where we are inside no
// test at all — and it has to return the error.
//
// If body is given as nil the request goes out WITHOUT A BODY (not with a
// "null" body): the endpoint that removes the channel bond carries the identity
// IN THE PATH and never reads its body, so putting a JSON value there would
// give the reader the impression that the body meant something.
func adminRequestWithBody(method, path string, body any) (*httptest.ResponseRecorder, error) {
	content := io.Reader(http.NoBody)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("the request body could not be encoded: %w", err)
		}
		content = bytes.NewReader(raw)
	}

	request := httptest.NewRequest(method, path, content)
	request.Header.Set("Authorization", "Bearer "+secretKey)
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	testRouter.ServeHTTP(recorder, request)

	return recorder, nil
}

// bindChannel binds the product to the sales channel FROM THE ADMIN ENDPOINT.
//
// The up-to-date list the endpoint returns is checked here as well: if the
// write request returned 200 without ever establishing the bond, the fault
// would show up not in the fixture but in every test that uses it, and in the
// wrong place.
func bindChannel(productID, channelID string) error {
	recorder, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/products/"+productID+"/sales-channels",
		map[string]string{"sales_channel_id": channelID})
	if err != nil {
		return err
	}
	if recorder.Code != http.StatusOK {
		return fmt.Errorf("the channel bond could not be established (%d): %s",
			recorder.Code, recorder.Body.String())
	}

	ids, err := decodeChannelList(recorder)
	if err != nil {
		return err
	}
	if !slices.Contains(ids, channelID) {
		return fmt.Errorf("the admin endpoint did not report the %q bond; returned list: %v", channelID, ids)
	}

	return nil
}

// decodeChannelList extracts the channel identities out of the sales channel
// response.
func decodeChannelList(recorder *httptest.ResponseRecorder) ([]string, error) {
	var envelope struct {
		Data struct {
			SalesChannelIDs []string `json:"sales_channel_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("the sales channel response could not be decoded: %w (body: %s)", err, recorder.Body.String())
	}

	return envelope.Data.SalesChannelIDs, nil
}

// vitrinZarfi is the test-side counterpart of the store list response.
//
// The envelope's "offset" and "limit" fields are DELIBERATELY not decoded: this
// test's claim is which products come back and how many are counted, not
// whether the pagination parameters are echoed. Of the product, only the
// identity and the handle are read; the correctness of its fields is other
// tests' business.
type vitrinZarfi struct {
	Data []struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	} `json:"data"`
	Count int `json:"count"`
}

// kimlikler returns the product identities in the envelope.
func (e vitrinZarfi) kimlikler() []string {
	out := make([]string, 0, len(e.Data))
	for _, product := range e.Data {
		out = append(out, product.ID)
	}

	return out
}

// vitrinKatalogu calls the store list with the given publishable key.
func vitrinKatalogu(t *testing.T, key string, query url.Values) vitrinZarfi {
	t.Helper()

	recorder := magazaIstegi(t, "/store/v1/products?"+query.Encode(), key)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the store list must return 200; body: %s", recorder.Body.String())

	var envelope vitrinZarfi
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the store list could not be decoded; body: %s", recorder.Body.String())

	return envelope
}

// koleksiyonSorgusu produces the query string that narrows down to a
// collection.
func koleksiyonSorgusu(collectionID string) url.Values {
	return url.Values{"collection_id": {collectionID}}
}

// TestTheStorefrontCatalogIsFilteredByTheRequestsSalesChannel verifies that two
// keys get two different catalogs FROM THE SAME endpoint.
//
// All three products are published and are in the same collection; the ONLY
// difference between them is the channel assignment. So there is no other
// explanation for the lists diverging.
func TestTheStorefrontCatalogIsFilteredByTheRequestsSalesChannel(t *testing.T) {
	ground := channelCatalogFixture(t)
	query := koleksiyonSorgusu(ground.koleksiyonID)

	first := vitrinKatalogu(t, publishableKey, query).kimlikler()
	assert.ElementsMatch(t,
		[]string{ground.firstChannelProduct.id, ground.unassignedProduct.id}, first,
		"the first storefront must see its own product and the UNASSIGNED product")
	assert.NotContains(t, first, ground.secondChannelProduct.id,
		"a product assigned to another channel MUST NOT be visible in this storefront; "+
			"if it is, the filter is not looking at the request's identity at all")

	second := vitrinKatalogu(t, ground.ikinciAnahtar, query).kimlikler()
	assert.ElementsMatch(t,
		[]string{ground.secondChannelProduct.id, ground.unassignedProduct.id}, second,
		"the second storefront must see its own product and the UNASSIGNED product")
	assert.NotContains(t, second, ground.firstChannelProduct.id,
		"the first channel's product MUST NOT be visible in the second storefront")

	// The same product being visible in one storefront and not in the other is
	// the only observation that says the reason for the hiding is the channel:
	// had the product been deleted or left as a draft it would be invisible in
	// BOTH.
	assert.Contains(t, second, ground.secondChannelProduct.id,
		"the product hidden in the first storefront must be visible in the storefront it belongs to")
}

// TestTheStorefrontCounterReflectsTheFilteredSet verifies that the envelope's
// "count" field gives the FILTERED total rather than the unpaginated TOTAL.
//
// Had the counter shown the unfiltered set, the storefront client would ask for
// pages that never fill up and would print "3 results" while showing 2
// products. The admin list sees THREE products in the same collection, and that
// comparison anchors the claim: the reason the two storefronts see 2 is not
// that products are missing but the filter itself.
func TestTheStorefrontCounterReflectsTheFilteredSet(t *testing.T) {
	ground := channelCatalogFixture(t)
	query := koleksiyonSorgusu(ground.koleksiyonID)

	first := vitrinKatalogu(t, publishableKey, query)
	assert.Equal(t, 2, first.Count,
		"the counter must count the filtered set; number of body rows: %d", len(first.Data))
	assert.Len(t, first.Data, first.Count,
		"in a result that fits on a single page the counter and the row count must not diverge")

	second := vitrinKatalogu(t, ground.ikinciAnahtar, query)
	assert.Equal(t, 2, second.Count)
	assert.Len(t, second.Data, second.Count)

	// The admin list is NOT subject to the channel filter: the admin identity
	// has no sales channel and has to see the catalog as a whole.
	admin := adminCatalog(t, query)
	assert.Equal(t, 3, admin.Count,
		"the admin list must count all three products; if it does not, the filter has leaked into the wrong place")
	assert.ElementsMatch(t,
		[]string{ground.firstChannelProduct.id, ground.secondChannelProduct.id, ground.unassignedProduct.id},
		admin.kimlikler())
}

// adminCatalog calls the admin product list with the secret key.
func adminCatalog(t *testing.T, query url.Values) vitrinZarfi {
	t.Helper()

	recorder := adminRequest(t, http.MethodGet, "/admin/v1/products?"+query.Encode(),
		"Bearer "+secretKey)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the admin list must return 200; body: %s", recorder.Body.String())

	var envelope vitrinZarfi
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the admin list could not be decoded; body: %s", recorder.Body.String())

	return envelope
}

// TestTheSingleProductStorefrontEndpointIsFilteredToo verifies that a product
// hidden from the list cannot be fetched from the single-product endpoint
// either.
//
// The endpoint returns 404, and that is the code the application chose: for an
// invisible product the service produces the SAME error (errors.NotFound) as
// for an unpublished one, and the core turns it into a 404 (see
// service.Service.GetStoreProduct). Had the single-product endpoint not been
// filtered, the hiding would be entirely pointless — because storefront
// addresses carry the handle, this is the easiest endpoint of all to guess;
// that is why both the identity and the handle are tried.
func TestTheSingleProductStorefrontEndpointIsFilteredToo(t *testing.T) {
	ground := channelCatalogFixture(t)

	cases := map[string]struct {
		key      string
		address  string
		expected int
	}{
		"a foreign channel's product by identity": {
			publishableKey, ground.secondChannelProduct.id, http.StatusNotFound,
		},
		"a foreign channel's product by handle": {
			publishableKey, ground.secondChannelProduct.handle, http.StatusNotFound,
		},
		"its own channel's product": {
			publishableKey, ground.firstChannelProduct.id, http.StatusOK,
		},
		"the unassigned product in the first storefront": {
			publishableKey, ground.unassignedProduct.handle, http.StatusOK,
		},
		"the hidden product in its own storefront": {
			ground.ikinciAnahtar, ground.secondChannelProduct.id, http.StatusOK,
		},
		"the first channel's product in the second storefront": {
			ground.ikinciAnahtar, ground.firstChannelProduct.handle, http.StatusNotFound,
		},
		"the unassigned product in the second storefront": {
			ground.ikinciAnahtar, ground.unassignedProduct.id, http.StatusOK,
		},
	}

	for name, tt := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := magazaIstegi(t, "/store/v1/products/"+tt.address, tt.key)

			assert.Equal(t, tt.expected, recorder.Code,
				"the single-product storefront endpoint must return the expected code; body: %s", recorder.Body.String())
		})
	}
}

// TestAHiddenProductDoesNotRevealItselfViaTheErrorCode verifies that the 404 of
// a hidden product cannot be told apart from the 404 of a product that never
// existed at all.
//
// Had there been a difference the hiding would be pierced: with the publishable
// key in hand, a competitor could learn one by one which handles are sold in
// ANOTHER channel.
//
// The comparison is over the error CODE only; because the message echoes the
// requested address ("product not found: %s") it already differs between the
// two requests, and the message differing is not a leak — the leak is the CLASS
// that changes the client's decision.
func TestAHiddenProductDoesNotRevealItselfViaTheErrorCode(t *testing.T) {
	ground := channelCatalogFixture(t)

	hidden := magazaIstegi(t, "/store/v1/products/"+ground.secondChannelProduct.handle, publishableKey)
	missing := magazaIstegi(t, "/store/v1/products/e2e-no-such-product-exists", publishableKey)

	require.Equal(t, http.StatusNotFound, hidden.Code, "body: %s", hidden.Body.String())
	require.Equal(t, http.StatusNotFound, missing.Code, "body: %s", missing.Body.String())

	assert.Equal(t, errorSummary(t, missing)[0], errorSummary(t, hidden)[0],
		"the hidden product and the missing product must return the SAME error code")
}

// TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString verifies that the
// client cannot pick the channel itself.
//
// Had the handler read the channel from the query string, the filter would stop
// being an authorization and would turn into a display preference: a client
// arriving with any publishable key in hand would read ANOTHER storefront's
// catalog just by writing the channel identity. The claim exists in the
// handler's unit test too, but there the identity is put in place by the test
// itself; here it is put there by the protection stack that runs in production.
func TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString(t *testing.T) {
	ground := channelCatalogFixture(t)

	query := koleksiyonSorgusu(ground.koleksiyonID)
	query.Set("sales_channel_id", ground.secondChannelID)

	catalog := vitrinKatalogu(t, publishableKey, query)

	assert.NotContains(t, catalog.kimlikler(), ground.secondChannelProduct.id,
		"the channel identity in the query string MUST BE IGNORED; if it is not, the key's "+
			"owner is able to read another storefront's catalog")
	assert.ElementsMatch(t,
		[]string{ground.firstChannelProduct.id, ground.unassignedProduct.id}, catalog.kimlikler(),
		"the catalog must stay bound to the key's OWN channel")
	assert.Equal(t, 2, catalog.Count)
}

// TestRemovingTheLastChannelBondShowsTheProductInEveryStorefront verifies that
// removing the bond does not hide the product but opens it up to ALL
// storefronts.
//
// It is the direct consequence of the rule ("a product with no assignment is
// visible everywhere") and it is nailed down end to end because it is
// surprising: an admin deleting the last bond in order to REMOVE the product
// from a storefront does the exact opposite.
//
// The test sets up its own collection and its own product; had it changed the
// shared fixture, every counter claim running after it would have become
// dependent on the order of the tests.
func TestRemovingTheLastChannelBondShowsTheProductInEveryStorefront(t *testing.T) {
	ground := channelCatalogFixture(t)
	ctx := t.Context()

	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E Channel Bond Removal",
		Handle: "e2e-channel-bond-removal",
	})
	require.NoError(t, err, "the isolation collection could not be set up")

	product, err := setUpChannelCatalogProduct(ctx, collection.ID, "removal")
	require.NoError(t, err)
	require.NoError(t, bindChannel(product.id, ground.secondChannelID))

	query := koleksiyonSorgusu(collection.ID)
	require.Empty(t, vitrinKatalogu(t, publishableKey, query).kimlikler(),
		"at first the product must be in the second storefront only")
	require.Equal(t, []string{product.id}, vitrinKatalogu(t, ground.ikinciAnahtar, query).kimlikler())

	recorder, err := adminRequestWithBody(http.MethodDelete,
		"/admin/v1/products/"+product.id+"/sales-channels/"+ground.secondChannelID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the channel bond must be removed; body: %s", recorder.Body.String())

	remaining, err := decodeChannelList(recorder)
	require.NoError(t, err)
	require.Empty(t, remaining, "after the last bond is removed the channel list must become empty")

	assert.Equal(t, []string{product.id}, vitrinKatalogu(t, publishableKey, query).kimlikler(),
		"a product left with no assignment must be visible in the FIRST storefront too")
	assert.Equal(t, []string{product.id}, vitrinKatalogu(t, ground.ikinciAnahtar, query).kimlikler(),
		"a product left with no assignment must keep being visible in its own old storefront too")
}
