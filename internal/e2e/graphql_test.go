//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file exercises the SECOND read surface (GraphQL) against real modules, a
// real Postgres and the PRODUCTION guard stack. The single sentence it proves is
// this:
//
//	The storefront's visibility rule does not change with the surface.
//
// # Why a second surface is exercised separately
//
// Every bug found in this repository was of the same class: "the rule is defined
// in one place, not applied in another". The sales channel was validated but not
// read; search could skip the catalog filter; authorization was enforced only in
// auth. A second read surface exercises exactly that class: a second
// implementation of the same rule drifts silently, and what shows up on the day
// it drifts is not an error message but ANOTHER storefront's catalog.
//
// # Why over HTTP, why this router
//
// A unit test of the resolver has the test ITSELF put the channel into the
// context; here the production guard stack puts it there (see e2e_test.go,
// corehttp.APIGuards). The difference between the two is the difference between
// "the resolver reads the context correctly" and "the endpoint really filters in
// production": the publishable key's channel will be resolved out of auth, the
// core will put it on the Principal, and the graph resolver will take the channel
// from that identity and NOT from the query. All three links of the chain can be
// observed at the same time only here.
//
// # Why it sets up its own products
//
// The fixture pattern is the same as [channelCatalogFixture]'s (two channels,
// three products) but it does NOT SHARE the ground: this file's products have a
// variant, a price and stock (only that way can the GraphQL surface be seen
// returning the variant tree and the enrichment arriving from other modules), and
// the neighbouring file's count assertions would break under those additions. The
// reverse direction is protected too: because the products here are isolated in
// their own collection, they do not fall into the neighbouring tests' catalogs.

// Fixture constants of the GraphQL ground.
//
// The names are FIXED (they are not generated from a fixture counter): setup runs
// once per process, and fixed names make it readable at a glance which record an
// error message means (see [channelCatalogFixture], same rationale).
const (
	// gqlChannelName is the name of the SECOND sales channel this file sets up.
	//
	// It is a channel separate from [secondChannelName]: a channel name is unique,
	// and borrowing the neighbouring file's channel would have gathered the
	// products of two files into a single storefront.
	gqlChannelName = "e2e-graphql-storefront"
	// gqlCollectionHandle is the handle of the collection that separates the
	// three fixture products from the shared catalog.
	gqlCollectionHandle = "e2e-graphql-catalog"
	// gqlMissingHandle is a handle that belongs to NO product; it is used to
	// compare against the hidden product's error.
	gqlMissingHandle = "e2e-graphql-no-such-product"
	// gqlSKU is the tracking code of the fixture's inventory item.
	gqlSKU = "E2E-GRAPHQL-SKU"
	// gqlCurrency is the currency of the fixture price.
	gqlCurrency = "TRY"
	// gqlUnitPrice is the amount of the fixture price in minor units.
	gqlUnitPrice = 12_900
	// gqlStock is the physical quantity of the fixture variant.
	gqlStock = 7
)

// gqlProduct holds the fields of the fixture product that the test needs.
//
// The handle is carried too because the single-product query can be called with
// either address, and a filter that worked ONLY with the id would mean that the
// storefront addresses (the handles) stayed unfiltered.
type gqlProduct struct {
	id      string
	handle  string
	variant string
}

// gqlStage is the prepared ground of the two-storefront GraphQL scenario.
type gqlStage struct {
	// secondChannelID is the second sales channel, separate from
	// [testChannelID].
	secondChannelID string
	// secondKey is the publishable key bound ONLY to the second channel.
	secondKey string
	// collectionID is the isolation collection all three products belong to.
	collectionID string
	// firstChannelProduct is assigned to the shared fixture channel
	// ([testChannelID]) and is the only product that HAS a price and stock.
	firstChannelProduct gqlProduct
	// secondChannelProduct is assigned only to [gqlStage.secondChannelID].
	secondChannelProduct gqlProduct
	// unassignedProduct is assigned to no channel and by the rule must be visible
	// in BOTH.
	unassignedProduct gqlProduct
	// priceSetID is the price set bound to the first product's variant.
	priceSetID string
	// inventoryItemID is the inventory item bound to the first product's variant.
	inventoryItemID string
}

// The state needed so that the fixture is set up once.
var (
	// gqlSetupOnce makes sure the ground is set up only once.
	gqlSetupOnce sync.Once
	// gqlSetupStage is the prepared ground.
	gqlSetupStage gqlStage
	// gqlSetupErr carries the setup error over to the tests.
	gqlSetupErr error
)

// gqlFixture sets up the two-storefront GraphQL ground and returns it.
//
// The channel name, the key and the product handles are UNIQUE; setting them up
// again in every test would collide on the second call. Setup is therefore inside
// a [sync.Once] and the error is carried outwards (see [channelCatalogFixture],
// same pattern).
func gqlFixture(t *testing.T) gqlStage {
	t.Helper()

	gqlSetupOnce.Do(func() {
		// The setup context is NOT t.Context(): the ground is shared between
		// tests and the first test's context is cancelled when that test ends.
		gqlSetupStage, gqlSetupErr = gqlSetUpStage(context.Background())
	})
	require.NoError(t, gqlSetupErr, "the GraphQL fixture could not be set up")

	return gqlSetupStage
}

// gqlSetUpStage prepares the second channel, the second key and the three
// products.
//
// The channel and the key are set up FROM THE SERVICE, while the channel
// ASSIGNMENTS are set up FROM THE ADMIN ENDPOINT (see [bindChannel]): the first
// two are not the subject of this test, the third is the very link the GraphQL
// surface reads. Doing the assignment from the service would not have proven that
// the admin endpoint writes the link the storefront reads.
func gqlSetUpStage(ctx context.Context) (gqlStage, error) {
	var stage gqlStage

	channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        gqlChannelName,
		Description: "the second storefront of the GraphQL end-to-end test",
	})
	if err != nil {
		return stage, fmt.Errorf("the second sales channel could not be set up: %w", err)
	}
	stage.secondChannelID = channel.ID

	if _, stage.secondKey, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeyPublishable,
		Title:     "e2e graphql publishable key",
		CreatedBy: adminID,
		// The key is bound ONLY to the second channel; had it carried both
		// channels, the whole filter assertion would have become meaningless.
		SalesChannelIDs: []string{channel.ID},
	}); err != nil {
		return stage, fmt.Errorf("the second publishable key could not be set up: %w", err)
	}

	collection, err := productSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E GraphQL Catalog",
		Handle: gqlCollectionHandle,
	})
	if err != nil {
		return stage, fmt.Errorf("the isolation collection could not be set up: %w", err)
	}
	stage.collectionID = collection.ID

	if stage.firstChannelProduct, err = gqlSetUpProduct(ctx, collection.ID, "first"); err != nil {
		return stage, err
	}
	if stage.secondChannelProduct, err = gqlSetUpProduct(ctx, collection.ID, "second"); err != nil {
		return stage, err
	}
	if stage.unassignedProduct, err = gqlSetUpProduct(ctx, collection.ID, "unassigned"); err != nil {
		return stage, err
	}

	if stage.priceSetID, stage.inventoryItemID, err =
		gqlEnrichVariant(ctx, stage.firstChannelProduct.variant); err != nil {
		return stage, err
	}

	if err := bindChannel(stage.firstChannelProduct.id, testChannelID); err != nil {
		return stage, err
	}
	if err := bindChannel(stage.secondChannelProduct.id, channel.ID); err != nil {
		return stage, err
	}

	return stage, nil
}

// gqlSetUpProduct creates a PUBLISHED product bound to the collection and its
// single variant.
//
// The status is [productmodels.StatusPublished]: a draft product is invisible in
// the storefront anyway, and in that state what would be measured is not the
// channel filter but the publication filter.
//
// The variant is as mandatory as the product itself: the GraphQL surface's claim
// is "it returns the tree the client asked for", and a product without a variant
// would never show the second level of that tree.
func gqlSetUpProduct(ctx context.Context, collectionID, name string) (gqlProduct, error) {
	handle := "e2e-graphql-" + name

	product, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:       handle,
		Title:        "E2E GraphQL " + name,
		Status:       productmodels.StatusPublished,
		CollectionID: &collectionID,
	})
	if err != nil {
		return gqlProduct{}, fmt.Errorf("the %q product could not be set up: %w", handle, err)
	}

	variant, err := productSvc.CreateVariant(ctx, product.ID, productsvc.CreateVariantInput{
		Title: "E2E GraphQL " + name + " variant",
	})
	if err != nil {
		return gqlProduct{}, fmt.Errorf("the %q variant could not be set up: %w", handle, err)
	}

	return gqlProduct{id: product.ID, handle: product.Handle, variant: variant.ID}, nil
}

// gqlEnrichVariant binds a price set and an inventory item to the variant.
//
// The enrichment is made of OTHER MODULES' records (pricing, inventory) and
// product does not import them; both are resolved through links over the variant
// id. The GraphQL surface returns these records as JSON scalars, that is, it does
// not interpret their fields — and that is exactly what is being exercised: the
// second read surface takes the enrichment not by its own route but from the SAME
// Query layer as the storefront service.
func gqlEnrichVariant(ctx context.Context, variantID string) (priceSetID, inventoryItemID string, err error) {
	set, err := pricingSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{{
		CurrencyCode: gqlCurrency,
		Amount:       gqlUnitPrice,
		MinQuantity:  1,
	}})
	if err != nil {
		return "", "", fmt.Errorf("the fixture price set could not be created: %w", err)
	}
	if err := productSvc.SetVariantPriceSet(ctx, variantID, set.ID); err != nil {
		return "", "", fmt.Errorf("the variant could not be bound to the price set: %w", err)
	}

	item, err := inventorySvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   gqlSKU,
		Title: "E2E GraphQL inventory item",
	})
	if err != nil {
		return "", "", fmt.Errorf("the fixture inventory item could not be created: %w", err)
	}
	if err := productSvc.SetVariantInventoryItem(ctx, variantID, item.ID); err != nil {
		return "", "", fmt.Errorf("the variant could not be bound to the inventory item: %w", err)
	}
	if _, err := inventorySvc.SetInventoryLevel(ctx, item.ID, stockLocationID, gqlStock); err != nil {
		return "", "", fmt.Errorf("the fixture inventory level could not be written: %w", err)
	}

	return set.ID, item.ID, nil
}

// The query documents.
//
// The documents are written WITH VARIABLES (not with inline values): that is how
// a storefront client writes them too, and the question "can the channel be
// forced with a variable" can only be asked of a real document that carries
// variables.
const (
	// gqlCatalogDocument asks for the storefront list narrowed by the collection.
	//
	// The variant tree and the enrichment fields are asked for DELIBERATELY: had
	// the list returned only the ids, the surface's claim of "it returns the tree
	// the client asked for" would never have been exercised.
	gqlCatalogDocument = `
	  query Catalog($collection: ID) {
	    products(collectionId: $collection) {
	      count
	      offset
	      limit
	      items {
	        id
	        handle
	        title
	        variants { id title priceSet inventoryItem }
	      }
	    }
	  }`

	// gqlSingleDocument asks for a single product by id or by handle.
	//
	// Both are variables and the caller fills in ONLY one; the argument that is
	// not given goes as null and the resolver counts it as "not supplied".
	gqlSingleDocument = `
	  query Product($id: ID, $handle: String) {
	    product(id: $id, handle: $handle) { id handle }
	  }`

	// gqlDeepestDocument is the deepest DATA path the schema allows (5 levels).
	gqlDeepestDocument = `
	  query Deepest($collection: ID) {
	    products(collectionId: $collection) {
	      items { variants { optionValues { optionTitle } } }
	    }
	  }`

	// gqlRichDocument is the document that EXCEEDS the complexity gate while
	// catching on no other gate: the fields are all DIFFERENT (that is, the field
	// repetition is 1) and the page ceiling is 100. Because the cost is computed
	// as root cost + page size × field count, it goes far above the production
	// ceiling (50,000).
	//
	// That the document uses NO aliases is deliberate: piling up with aliases now
	// catches on an earlier and cheaper gate (field repetition) and this test
	// would then NEVER exercise the complexity gate — it would stay green even if
	// one of the gates were removed.
	gqlRichDocument = `
	  query Rich {
	    products(limit: 100) {
	      items {
	        id handle title subtitle description thumbnail isGiftcard
	        discountable weight length height width material originCountry
	        collectionId metadata createdAt updatedAt
	        variants {
	          id productId title sku barcode ean upc manageInventory
	          allowBackorder weight rank metadata createdAt updatedAt
	          priceSet inventoryItem
	          optionValues { id optionId value rank optionTitle }
	        }
	        options { id productId title rank values { id value } }
	        images { id productId url rank metadata }
	        tags { id value }
	        categories { id name handle description parentId isActive rank }
	      }
	      count offset limit
	    }
	  }`
)

// gqlVariantView holds the fields of the variant that are read from the response.
type gqlVariantView struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// PriceSet and InventoryItem are JSON scalars in the schema: pricing's and
	// inventory's records arrive at this module loosely typed and are returned
	// that way.
	PriceSet      map[string]any `json:"priceSet"`
	InventoryItem map[string]any `json:"inventoryItem"`
}

// gqlProductView holds the fields of the product that are read from the response.
type gqlProductView struct {
	ID       string           `json:"id"`
	Handle   string           `json:"handle"`
	Title    string           `json:"title"`
	Variants []gqlVariantView `json:"variants"`
}

// gqlEnvelope is the test-side counterpart of the GraphQL response.
//
// data and errors are decoded TOGETHER: in GraphQL both can be present in the
// same response, and some of these tests ask exactly the question "is there an
// error, and what happened to the data" at the same time (the hidden product:
// product null, errors non-empty).
type gqlEnvelope struct {
	Data struct {
		Products struct {
			Items  []gqlProductView `json:"items"`
			Count  int              `json:"count"`
			Offset int              `json:"offset"`
			Limit  int              `json:"limit"`
		} `json:"products"`
		Product *gqlProductView `json:"product"`
	} `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

// kimlikler returns the ids of the products in the list.
func (z gqlEnvelope) kimlikler() []string {
	out := make([]string, 0, len(z.Data.Products.Items))
	for _, product := range z.Data.Products.Items {
		out = append(out, product.ID)
	}

	return out
}

// errorCode returns the extensions.code field of the first error.
//
// The code comes from the core's error envelope (see graph.NewHandler's error
// presenter): the two read surfaces speak the same vocabulary, and the tests can
// compare it ACROSS the surfaces.
func (z gqlEnvelope) errorCode(t *testing.T) string {
	t.Helper()

	require.NotEmpty(t, z.Errors, "the response should have carried at least one error")

	code, _ := z.Errors[0].Extensions["code"].(string)

	return code
}

// gqlRequest POSTs the document to the GraphQL endpoint and returns the RAW
// response.
//
// If the key is empty the header is NOT added at all: "no header" and "an empty
// header" are different states and the 401 assertion targets the first one (same
// rationale as [magazaIstegi]).
//
// The address is read from [graph.Path] and not typed out by hand: the path is
// the module's constant, and the test's own copy would silently exercise the
// wrong place once the endpoint moved.
func gqlRequest(t *testing.T, key, document string, variables map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	payload := map[string]any{"query": document}
	if variables != nil {
		payload["variables"] = variables
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err, "the GraphQL request body could not be encoded")

	req := httptest.NewRequest(http.MethodPost, graph.Path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set(corehttp.PublishableKeyHeader, key)
	}

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)

	return rec
}

// gqlDecode decodes the response body as a GraphQL envelope.
func gqlDecode(t *testing.T, rec *httptest.ResponseRecorder) gqlEnvelope {
	t.Helper()

	var envelope gqlEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"the GraphQL response could not be decoded; body: %s", rec.Body.String())

	return envelope
}

// gqlQuery runs the document and expects a response WITHOUT errors.
//
// The status code is checked as well: a GraphQL response is 200 and the errors
// are in the body, but if the document NEVER reaches the executor (an
// unauthorized request, a body that does not fit) the response is the core's
// envelope — and in that case the "errors is empty" assertion would pass
// misleadingly.
func gqlQuery(t *testing.T, key, document string, variables map[string]any) gqlEnvelope {
	t.Helper()

	rec := gqlRequest(t, key, document, variables)
	require.Equal(t, http.StatusOK, rec.Code,
		"the GraphQL request should return 200; body: %s", rec.Body.String())

	envelope := gqlDecode(t, rec)
	require.Empty(t, envelope.Errors,
		"the GraphQL response should be free of errors; body: %s", rec.Body.String())

	return envelope
}

// gqlCatalog calls the list narrowed by the collection with the given key.
//
// extra is for adding variables the document does NOT DECLARE (see
// [TestGraphQLChannelCannotBeForcedFromVariables]); if nil is given, only the
// collection variable goes out.
func gqlCatalog(t *testing.T, key, collectionID string, extra map[string]any) gqlEnvelope {
	t.Helper()

	variables := map[string]any{"collection": collectionID}
	for name, value := range extra {
		variables[name] = value
	}

	return gqlQuery(t, key, gqlCatalogDocument, variables)
}

// TestGraphQLEndpointRejectsRequestWithoutPublishableKey verifies that the guard
// stack covers the new endpoint BY ITSELF.
//
// Because the endpoint is placed under /store/v1, authentication is written
// nowhere in its own code; the prefix stack applies the guard. "It should be
// automatic" is a design claim, and as long as it is not verified it is an
// assumption: had the module's endpoint opened under a different prefix, or had
// the stack's prefixes changed, the GraphQL surface would have opened without
// identity while breaking no test at all.
//
// The response is NOT the GraphQL envelope but the core's error envelope, and
// that is checked too: the document never reached the executor, therefore there
// must be no "data" field. Returning 200 + errors to a request without identity
// would tell the client that its query ran but produced an empty result.
func TestGraphQLEndpointRejectsRequestWithoutPublishableKey(t *testing.T) {
	stage := gqlFixture(t)
	variables := map[string]any{"collection": stage.collectionID}

	withoutKey := gqlRequest(t, "", gqlCatalogDocument, variables)
	require.Equal(t, http.StatusUnauthorized, withoutKey.Code,
		"a GraphQL request without a publishable key should be rejected; body: %s",
		withoutKey.Body.String())

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(withoutKey.Body.Bytes(), &raw),
		"the error body could not be decoded: %s", withoutKey.Body.String())
	assert.NotContains(t, raw, "data",
		"a request that did not reach the executor must NOT return a GraphQL envelope")
	assert.Contains(t, raw, "error", "the response should be the core's error envelope")

	// The secret key does not pass in the store header: had it passed, a key
	// embedded inside storefront code would carry admin authority (see
	// [TestGizliAnahtarMagazaBasligindaGecmez], the same claim in REST).
	secret := gqlRequest(t, secretKey, gqlCatalogDocument, variables)
	assert.Equal(t, http.StatusUnauthorized, secret.Code,
		"the secret key should not be accepted at the GraphQL endpoint; body: %s", secret.Body.String())

	// The other side is exercised too: an endpoint that rejects every request
	// would pass the two assertions above as well.
	withKey := gqlRequest(t, publishableKey, gqlCatalogDocument, variables)
	assert.Equal(t, http.StatusOK, withKey.Code,
		"a GraphQL request with the publishable key should pass; body: %s", withKey.Body.String())
}

// TestGraphQLEndpointAcceptsOnlyPOST verifies that the GET transport is not
// opened.
//
// The decision is deliberate (see graph.NewHandler): GET's only benefit is
// intermediate caches, and because the response varies with the publishable key —
// that is, with the sales channel — that benefit does not exist here; its price
// is that the query falls into the URL, into the logs and into the browser
// history. Because the endpoint is registered with chi under POST only, a GET
// gets an honest 405 instead of gqlgen's "transport not supported" 400 — and that
// can be seen only on the REAL router.
func TestGraphQLEndpointAcceptsOnlyPOST(t *testing.T) {
	rec := magazaIstegi(t, graph.Path, publishableKey)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"the GraphQL endpoint should not accept GET; body: %s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), `"data"`,
		"a GET request must NOT BE EXECUTED as a GraphQL operation")
}

// TestGraphQLListReturnsProductsAndVariants verifies that the surface really
// returns the requested tree.
//
// The price and the stock are checked separately: both are records of OTHER
// modules and reach GraphQL not by their own route but from the SAME Query layer
// the REST storefront uses. A unit test running against a fake service cannot see
// this — there the records are filled in by the test itself; here the pricing and
// inventory modules really produce them.
func TestGraphQLListReturnsProductsAndVariants(t *testing.T) {
	stage := gqlFixture(t)

	envelope := gqlCatalog(t, publishableKey, stage.collectionID, nil)

	require.Equal(t, 2, envelope.Data.Products.Count,
		"the first storefront should count its own product and the unassigned product")
	require.Len(t, envelope.Data.Products.Items, 2)

	var first gqlProductView
	for _, product := range envelope.Data.Products.Items {
		if product.ID == stage.firstChannelProduct.id {
			first = product
		}
	}
	require.Equal(t, stage.firstChannelProduct.id, first.ID,
		"the first channel's product should be in the list")
	assert.Equal(t, stage.firstChannelProduct.handle, first.Handle)
	assert.Equal(t, "E2E GraphQL first", first.Title)

	require.Len(t, first.Variants, 1, "the product's variant tree should be returned")
	variant := first.Variants[0]
	assert.Equal(t, stage.firstChannelProduct.variant, variant.ID)

	require.NotNil(t, variant.PriceSet, "the variant's price set should be returned")
	assert.Equal(t, stage.priceSetID, variant.PriceSet["id"],
		"the price set should be the pricing module's record")
	prices, ok := variant.PriceSet["prices"].([]any)
	require.True(t, ok, "the price set should be returned together with its prices: %v", variant.PriceSet)
	require.Len(t, prices, 1)
	price, ok := prices[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, gqlCurrency, price["currency_code"])
	assert.EqualValues(t, gqlUnitPrice, price["amount"])

	require.NotNil(t, variant.InventoryItem, "the variant's inventory item should be returned")
	assert.Equal(t, stage.inventoryItemID, variant.InventoryItem["id"])
	assert.Equal(t, gqlSKU, variant.InventoryItem["sku"])
	assert.EqualValues(t, gqlStock, variant.InventoryItem["available_quantity"],
		"the sellable quantity should come from the inventory module")

	// The pagination envelope is echoed too: the values are applied by the
	// SERVICE (the client gave no limit), the surface does not invent a default
	// of its own.
	assert.Equal(t, 0, envelope.Data.Products.Offset)
	assert.Positive(t, envelope.Data.Products.Limit,
		"the applied page size should be the service's default")
}

// TestGraphQLCatalogIsFilteredBySalesChannel is this file's REAL claim: the same
// document returns two different catalogs with two keys.
//
// All three products are published and are in the same collection; the ONLY
// difference between them is the channel assignment. Therefore there is no other
// explanation for the lists diverging.
//
// The observation "the product is not visible" with a single key proves nothing
// (the product may have been deleted, may have stayed a draft, or the query may
// be broken); the second key SEEING THE SAME product is the only observation that
// says the reason for the hiding is exactly the channel.
func TestGraphQLCatalogIsFilteredBySalesChannel(t *testing.T) {
	stage := gqlFixture(t)

	first := gqlCatalog(t, publishableKey, stage.collectionID, nil).kimlikler()
	assert.ElementsMatch(t,
		[]string{stage.firstChannelProduct.id, stage.unassignedProduct.id}, first,
		"the first storefront should see its own product and the UNASSIGNED product")
	assert.NotContains(t, first, stage.secondChannelProduct.id,
		"a product assigned to another channel must NOT BE VISIBLE in this storefront; if it "+
			"is, the GraphQL surface is not looking at the request's identity at all")

	second := gqlCatalog(t, stage.secondKey, stage.collectionID, nil).kimlikler()
	assert.ElementsMatch(t,
		[]string{stage.secondChannelProduct.id, stage.unassignedProduct.id}, second,
		"the second storefront should see its own product and the UNASSIGNED product")
	assert.NotContains(t, second, stage.firstChannelProduct.id,
		"the first channel's product must NOT BE VISIBLE in the second storefront")

	assert.Contains(t, second, stage.secondChannelProduct.id,
		"the product hidden in the first storefront should be visible in the storefront it belongs to")
}

// TestGraphQLSingleProductQueryIsFilteredToo verifies that the product hidden
// from the list cannot be fetched with the single-product query either.
//
// Had the single-product query not been filtered, the hiding would be entirely
// meaningless: storefront addresses carry the handle, so this is precisely the
// easiest query to guess. That is why both the id and the handle are tried.
//
// The hidden product's error must carry the same code as the error of a handle
// that DOES NOT EXIST AT ALL. Had there been a difference, the hiding would be
// pierced: with a publishable key in hand, a competitor could learn one by one
// which handles are sold in ANOTHER channel.
func TestGraphQLSingleProductQueryIsFilteredToo(t *testing.T) {
	stage := gqlFixture(t)

	own := gqlQuery(t, publishableKey, gqlSingleDocument,
		map[string]any{"id": stage.firstChannelProduct.id})
	require.NotNil(t, own.Data.Product, "the product of its own channel should be returned")
	assert.Equal(t, stage.firstChannelProduct.handle, own.Data.Product.Handle)

	// The hidden product: the error comes back in the GraphQL envelope and the
	// field is null.
	hidden := gqlDecode(t, gqlRequest(t, publishableKey, gqlSingleDocument,
		map[string]any{"handle": stage.secondChannelProduct.handle}))
	assert.Nil(t, hidden.Data.Product,
		"a foreign channel's product should come back null in GraphQL too")

	missing := gqlDecode(t, gqlRequest(t, publishableKey, gqlSingleDocument,
		map[string]any{"handle": gqlMissingHandle}))
	assert.Nil(t, missing.Data.Product)

	assert.Equal(t, missing.errorCode(t), hidden.errorCode(t),
		"the hidden product and the missing product should return the SAME error code")

	// The product is visible in its own storefront: the reason for the 404 is not
	// that the product is absent but the filter itself.
	owner := gqlQuery(t, stage.secondKey, gqlSingleDocument,
		map[string]any{"handle": stage.secondChannelProduct.handle})
	require.NotNil(t, owner.Data.Product, "the hidden product should be visible in its own storefront")
	assert.Equal(t, stage.secondChannelProduct.id, owner.Data.Product.ID)
}

// TestGraphQLChannelCannotBeForcedFromVariables verifies that the client cannot
// pick the channel itself.
//
// All three doors are tried because in GraphQL there are three ways to hand a
// value to the server: the argument, the variable dictionary and the request's
// query string. Had the channel been forceable through even one of them, the
// filter would stop being an authorization and turn into a display preference: a
// client arriving with any publishable key at all would read ANOTHER storefront's
// catalog.
func TestGraphQLChannelCannotBeForcedFromVariables(t *testing.T) {
	stage := gqlFixture(t)
	expected := []string{stage.firstChannelProduct.id, stage.unassignedProduct.id}

	// 1. As an argument: NO such argument exists in the schema and the document
	// is rejected in validation. The response is 422 (gqlgen counts validation
	// errors as protocol errors) and the catalog is never read.
	argument := gqlRequest(t, publishableKey, `
	  query Force($channel: [ID!]) {
	    products(collectionId: "`+stage.collectionID+`", salesChannelIds: $channel) { count }
	  }`, map[string]any{"channel": []string{stage.secondChannelID}})

	assert.Equal(t, http.StatusUnprocessableEntity, argument.Code,
		"an argument that is not in the schema should be rejected in validation; body: %s", argument.Body.String())
	envelope := gqlDecode(t, argument)
	require.NotEmpty(t, envelope.Errors)
	assert.Contains(t, envelope.Errors[0].Message, "salesChannelIds")
	assert.Empty(t, envelope.Data.Products.Items, "a rejected document must not return a catalog")

	// 2. By SMUGGLING it into the variable dictionary: because the document does
	// not declare it, the variable is ignored. Being ignored silently is correct,
	// but the test's question is a different one — had it not been ignored, a
	// "filter from the variable" shortcut added one day would accept this
	// request.
	smuggled := gqlCatalog(t, publishableKey, stage.collectionID,
		map[string]any{"salesChannelIds": []string{stage.secondChannelID}})
	assert.ElementsMatch(t, expected, smuggled.kimlikler(),
		"an undeclared variable must NOT CHANGE the catalog")

	// 3. With the query string: the GraphQL endpoint reads the body, but just as
	// the REST storefront does not take the channel from the query string (see
	// [TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString]) this endpoint must not take it
	// either. The route differs, the trap is the same.
	body, err := json.Marshal(map[string]any{
		"query":     gqlCatalogDocument,
		"variables": map[string]any{"collection": stage.collectionID},
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost,
		graph.Path+"?sales_channel_id="+stage.secondChannelID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(corehttp.PublishableKeyHeader, publishableKey)

	rec := httptest.NewRecorder()
	testRouter.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.ElementsMatch(t, expected, gqlDecode(t, rec).kimlikler(),
		"the channel id in the query string must BE IGNORED")
}

// TestGraphQLAndRESTReturnTheSameSet verifies that the two read surfaces apply
// the SAME visibility rule.
//
// The claim is the most valuable one in this file: if the surfaces drift, the
// drift happens not with an error message but SILENTLY — a product hidden in one
// is visible in the other and nobody notices unless someone puts the two lists
// side by side. That is why the comparison is done with the same key, on the same
// collection and for BOTH keys: had it been done with a single key, the state
// "both surfaces ignore the identity" would pass the test as well.
func TestGraphQLAndRESTReturnTheSameSet(t *testing.T) {
	stage := gqlFixture(t)
	query := koleksiyonSorgusu(stage.collectionID)

	keys := map[string]string{
		"first storefront":  publishableKey,
		"second storefront": stage.secondKey,
	}

	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			rest := vitrinKatalogu(t, key, query)
			gql := gqlCatalog(t, key, stage.collectionID, nil)

			assert.ElementsMatch(t, rest.kimlikler(), gql.kimlikler(),
				"REST and GraphQL should return the same set of products; if they diverge, the "+
					"two surfaces are applying two different visibility rules")
			assert.Equal(t, rest.Count, gql.Data.Products.Count,
				"the counter of both surfaces should count the SAME filtered set")
		})
	}

	// The single-product endpoint is compared too: for the hidden product REST
	// returns 404 while GraphQL returns null + an error. The codes must come from
	// the SAME vocabulary — the core's error envelope is written from a single
	// place on both surfaces (see graph.NewHandler).
	restHidden := magazaIstegi(t,
		"/store/v1/products/"+stage.secondChannelProduct.handle, publishableKey)
	require.Equal(t, http.StatusNotFound, restHidden.Code,
		"REST should return 404 for the hidden product; body: %s", restHidden.Body.String())

	gqlHidden := gqlDecode(t, gqlRequest(t, publishableKey, gqlSingleDocument,
		map[string]any{"handle": stage.secondChannelProduct.handle}))
	require.Nil(t, gqlHidden.Data.Product)

	assert.Equal(t, errorSummary(t, restHidden)[0], gqlHidden.errorCode(t),
		"both surfaces should return the SAME error code for the same product")
}

// gqlAliasPileUp builds the document that repeats the same root query n times
// under aliases.
//
// This is GraphQL's multiplier that has no counterpart in REST: the document
// below is a SINGLE HTTP request, that is, ONE counter for the rate limiter,
// while for the server it is n catalog queries.
func gqlAliasPileUp(n int) string {
	var document strings.Builder

	document.WriteString("{")

	for i := range n {
		fmt.Fprintf(&document, " a%d: products { count }", i)
	}

	document.WriteString(" }")

	return document.String()
}

// TestGraphQLComplexityLimitIsEnforcedInProductionStack verifies that the limits
// are REALLY wired in the production setup.
//
// The behaviour of the limits is exercised in detail in the unit tests; what is
// exercised here is the WIRING: on this ground too the module is set up with
// ZERO-valued options just as in production (see e2e_test.go, the line where the
// product module is added), and that a zero value means "the package default" and
// NOT "unlimited" can be seen only in a real setup. If a copy ever appears on the
// configuration path (if the module picks a default of its own), the unit tests
// stay green and this test turns red.
//
// # Why the depth limit is not here
//
// Today's schema is NOT CYCLIC: the deepest legitimate path is 5 levels
// ([gqlDeepestDocument]) and the default limit is 10, that is, with PRODUCTION
// settings no VALID document exceeding the depth gate can be written — any deeper
// document asks for a field that is not in the schema and dies in validation,
// before the depth is ever measured. The rejecting side of the gate therefore
// belongs to the unit test where the limit can be lowered
// (graph.TestDerinlikSiniriAsilanBelgeyiReddeder); the side that falls here is
// that the gate lets the LEGITIMATE document through, and that is exercised below
// — this is exactly the symptom of a depth limit miscalibrated in production: one
// day the storefront's deepest query silently starts being rejected.
func TestGraphQLComplexityLimitIsEnforcedInProductionStack(t *testing.T) {
	stage := gqlFixture(t)

	rec := gqlRequest(t, publishableKey, gqlRichDocument, nil)

	// Exceeding the limit comes back inside HTTP 200 with errors: gqlgen's
	// complexity error is of the "user error" class and the repository
	// deliberately does not change the errcode registry (that would be a single
	// module changing a process-wide map).
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	envelope := gqlDecode(t, rec)
	require.NotEmpty(t, envelope.Errors, "a page × field product above the ceiling should be rejected")
	assert.Contains(t, envelope.Errors[0].Message, "complexity")
	assert.Equal(t, "COMPLEXITY_LIMIT_EXCEEDED", envelope.Errors[0].Extensions["code"])
	assert.Empty(t, envelope.Data.Products.Items, "a rejected document must not return a catalog")

	// The other side of the calibration: a limit that rejects every document
	// would pass the assertion above too. The schema's deepest legitimate
	// document, enrichment included, MUST PASS with the production settings.
	legitimate := gqlQuery(t, publishableKey, gqlDeepestDocument,
		map[string]any{"collection": stage.collectionID})
	assert.Len(t, legitimate.Data.Products.Items, 2,
		"the deepest legitimate document should work with the production limits")
}

// TestGraphQLFieldRepetitionLimitIsEnforcedInProductionStack verifies that a
// document multiplying the same field under aliases is rejected in the production
// stack.
//
// # Why a test SEPARATE from the complexity gate
//
// The complexity model prices the NUMBER of fields, not the BYTES: a document
// that selects the same heavy field (description, for example) under hundreds of
// aliases can stay BELOW the ceiling and multiply the response a hundredfold.
// When it was measured, an 8 KiB request produced a 191 MiB response and the rate
// limiter counted that as ONE request. That is why the gate is separate and does
// not take the place of complexity.
//
// What is exercised here is not the behaviour exercised in detail in the unit
// tests but that the gate is WIRED in the production setup: on the e2e ground too
// the module is set up with zero-valued options, and that a zero value means "the
// package default" and not "unlimited" can be seen only in a real setup.
func TestGraphQLFieldRepetitionLimitIsEnforcedInProductionStack(t *testing.T) {
	gqlFixture(t)

	rec := gqlRequest(t, publishableKey, gqlAliasPileUp(400), nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	envelope := gqlDecode(t, rec)
	require.NotEmpty(t, envelope.Errors, "a document piled up with aliases should be rejected")
	assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", envelope.Errors[0].Extensions["code"],
		"the repetition gate should catch it BEFORE complexity and more cheaply; if the "+
			"complexity code shows up, the repetition gate is not wired in the production stack")
	assert.Empty(t, envelope.Data.Products.Items, "a rejected document must not return a catalog")
}

// TestGraphQLHugeBodyIsRejectedWithoutParsing verifies that the body limit is
// enforced in the production stack as well.
//
// This gate does the job the others CANNOT do: depth and complexity can only be
// measured AFTER the document has been parsed, that is, by the time they are
// reached the server has already read and parsed the text. The document is a
// flawless GraphQL query; the reason it is rejected is not its shape but its
// SIZE.
//
// The response is not the GraphQL envelope but the CORE's envelope, and the rule
// is this: data/errors belong only to documents that REACHED the executor. The
// same rule drops the unauthorized request into the core envelope at 401 as well;
// an unsupported method gets the router's own 405 — what they have in common is
// that NONE OF THEM RETURNS a GraphQL envelope.
func TestGraphQLHugeBodyIsRejectedWithoutParsing(t *testing.T) {
	document := `{ product(handle: "` + strings.Repeat("x", 128<<10) + `") { id } }`

	rec := gqlRequest(t, publishableKey, document, nil)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"a body that exceeds the limit should be rejected; body: %s", rec.Body.String())

	var envelope corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
		"the error body could not be decoded: %s", rec.Body.String())
	assert.Equal(t, "product_graphql_body_too_large", envelope.Error.Code)
	assert.NotEmpty(t, envelope.Error.RequestID,
		"the core's envelope should carry the request id; there is no separate route for this endpoint either")
}
