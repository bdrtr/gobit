package graph_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// fakeStorefront is the fake of the storefront service the resolvers call.
//
// It RECORDS the call options: most of the claims in this file are not about
// the data returned but about WHAT WAS PASSED to the service (such as the sales
// channel filter).
//
// The lock is a real need: gqlgen resolves root query fields CONCURRENTLY, that
// is, two aliased queries in one request mean two goroutines.
type fakeStorefront struct {
	mu sync.Mutex

	listOptions     []service.StoreListOptions
	singleSelectors []string
	singleChannels  [][]string

	list   service.ListResult[service.StoreProduct]
	single service.StoreProduct
	err    error
}

// ListStoreProducts records the options of the call and returns the prepared
// result.
//
// On the count field the REAL service's contract is imitated: nil if SkipCount
// is asked for, filled in if it is not. Returning a fixed result would not have
// been enough — the "count: Int!" in the schema does not accept nil and every
// test that SELECTS the count would fail with "null which the schema does not
// allow" because of the fake. That is, a fake that does not carry the contract
// would produce a failure unrelated to the behavior under test.
func (s *fakeStorefront) ListStoreProducts(
	_ context.Context,
	opts service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.listOptions = append(s.listOptions, opts)

	if s.err != nil {
		return service.ListResult[service.StoreProduct]{}, s.err
	}

	list := s.list

	switch {
	case opts.SkipCount:
		list.Count = nil
	case list.Count == nil:
		list.Count = ptr(len(list.Items))
	}

	return list, nil
}

// GetStoreProduct records the selector and the channels of the call.
func (s *fakeStorefront) GetStoreProduct(
	_ context.Context,
	idOrHandle string,
	salesChannelIDs []string,
) (service.StoreProduct, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.singleSelectors = append(s.singleSelectors, idOrHandle)
	s.singleChannels = append(s.singleChannels, salesChannelIDs)

	if s.err != nil {
		return service.StoreProduct{}, s.err
	}

	return s.single, nil
}

// lastList returns the last recorded listing options.
func (s *fakeStorefront) lastList(t *testing.T) service.StoreListOptions {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	require.Len(t, s.listOptions, 1, "the service should have been called EXACTLY once")

	return s.listOptions[0]
}

// graphQLResponse is the decoded form of a single GraphQL response.
type graphQLResponse struct {
	Data   map[string]any `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

// runQuery POSTs the document to the endpoint and decodes the response.
//
// The request goes through the REAL handler (transport, parsing, validation,
// resolver): calling the resolver directly would test only the body of the
// function, not that the channel is read from the context.
func runQuery(
	t *testing.T,
	ctx context.Context,
	svc graph.Storefront,
	document string,
) (response graphQLResponse, status int) {
	t.Helper()

	return runQueryWithOptions(t, ctx, svc, document, graph.Options{})
}

// runQueryWithOptions POSTs the document to an endpoint set up with the given
// limits.
func runQueryWithOptions(
	t *testing.T,
	ctx context.Context,
	svc graph.Storefront,
	document string,
	opts graph.Options,
) (response graphQLResponse, status int) {
	t.Helper()

	rec := doRequest(t, ctx, svc, document, opts)

	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response), "body: %s", rec.Body.String())

	return response, rec.Code
}

// doRequest POSTs the document to the endpoint and returns the RAW response.
//
// The raw body is there so a question the decoded response hides can be asked:
// that a text appears NOWHERE in the response — not in the message, not in the
// extensions, not in the path — can only be claimed by looking at the bytes
// (see handler_test.go).
func doRequest(
	t *testing.T,
	ctx context.Context,
	svc graph.Storefront,
	document string,
	opts graph.Options,
) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(map[string]any{"query": document})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, graph.Path, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	graph.NewHandler(svc, opts).ServeHTTP(rec, req)

	return rec
}

// identityWith builds a store identity carrying the given sales channels.
func identityWith(channels []string) context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID:              "pk_test",
		Kind:            "api_key",
		SalesChannelIDs: channels,
	})
}

// TestListTakesChannelsFromTheIdentity verifies that the filter comes from the
// request's IDENTITY.
//
// Could the channel be taken from the query, the filter would stop being an
// authorization and turn into a display preference: a client arriving with any
// publishable key it happened to hold would read another storefront's catalog.
func TestListTakesChannelsFromTheIdentity(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, status := runQuery(t, identityWith([]string{"sc_1", "sc_2"}), svc,
		`{ products { count } }`)

	require.Empty(t, response.Errors)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"sc_1", "sc_2"}, svc.lastList(t).SalesChannelIDs)
}

// TestIdentityWithoutChannelsPassesTheEmptySet verifies that an identity
// without channels IS NOT THE SAME THING as "no filtering".
//
// nil means "the request carries no channel identity at all" and turns the
// filter off. Turning an identity without channels into nil would open the
// catalog of ALL channels to that identity; the empty set, on the other hand,
// applies the rule and only products with no assignment become visible.
func TestIdentityWithoutChannelsPassesTheEmptySet(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith(nil), svc, `{ products { count } }`)

	require.Empty(t, response.Errors)

	channels := svc.lastList(t).SalesChannelIDs
	assert.NotNil(t, channels, "an identity without channels must pass the empty set, not nil")
	assert.Empty(t, channels)
}

// TestRequestWithoutIdentityAppliesNoFilter verifies that the storefront does
// not empty out in a deployment where store authentication is not wired up.
//
// product can also be deployed on its own; in that deployment there is no
// channel identity at all and the filter must not be applied.
func TestRequestWithoutIdentityAppliesNoFilter(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, context.Background(), svc, `{ products { count } }`)

	require.Empty(t, response.Errors)
	assert.Nil(t, svc.lastList(t).SalesChannelIDs)
}

// TestSingleEndpointPassesTheChannels verifies that the single-item query is
// subject to the SAME filter.
//
// Showing a product hidden from the list through the single-item query would
// make hiding entirely pointless; because storefront addresses carry the handle
// this is exactly the guessable query.
func TestSingleEndpointPassesTheChannels(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{single: service.StoreProduct{
		Product: models.Product{ID: "prod_1", Handle: "t-shirt"},
	}}

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ product(handle: "t-shirt") { id } }`)

	require.Empty(t, response.Errors)
	assert.Equal(t, []string{"t-shirt"}, svc.singleSelectors)
	assert.Equal(t, [][]string{{"sc_1"}}, svc.singleChannels)
}

// TestQueryCannotAskForASalesChannel verifies that an argument that is not in
// the schema is rejected IN VALIDATION and never reaches the service.
//
// The schema test states the absence of the argument; this test shows what that
// absence means AT RUN TIME: the request is rejected, the catalog is not read.
func TestQueryCannotAskForASalesChannel(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products(salesChannelIds: ["sc_other"]) { count } }`)

	require.NotEmpty(t, response.Errors, "an unknown argument must be rejected")
	assert.Contains(t, response.Errors[0].Message, "salesChannelIds")
	assert.Empty(t, svc.listOptions, "a rejected query must never reach the service")
}

// TestListArgumentsReachTheService verifies that the arguments read are passed
// into the options as they are.
func TestListArgumentsReachTheService(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products(limit: 5, offset: 10, q: "t-shirt", collectionId: "pcol_1") { count } }`)

	require.Empty(t, response.Errors)

	opts := svc.lastList(t)
	assert.Equal(t, 5, opts.Limit)
	assert.Equal(t, 10, opts.Offset)
	require.NotNil(t, opts.Search)
	assert.Equal(t, "t-shirt", *opts.Search)
	require.NotNil(t, opts.CollectionID)
	assert.Equal(t, "pcol_1", *opts.CollectionID)
}

// TestTextArgumentsAreTrimmedOnTheWay verifies that a non-empty argument
// arrives trimmed.
//
// That an empty value turns into nil is pinned by a test that walks the SCHEMA
// (see schema_test.go, TestEmptyTextArgumentBuildsNoFilter); what is claimed
// here is the trimming itself: " t-shirt " and "t-shirt" are the same search,
// and counting the two as separate queries would tie the result to a space the
// client cannot see.
func TestTextArgumentsAreTrimmedOnTheWay(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products(q: "  t-shirt  ") { count } }`)

	require.Empty(t, response.Errors)

	opts := svc.lastList(t)
	require.NotNil(t, opts.Search)
	assert.Equal(t, "t-shirt", *opts.Search)
}

// TestPagingDefaultIsLeftToTheService verifies that a limit which is not given
// arrives as 0.
//
// For the service 0 means "apply the default". The resolver picking its own
// default would be a second definition of the same rule and the two read
// surfaces would start returning different page sizes.
func TestPagingDefaultIsLeftToTheService(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, `{ products { count } }`)

	require.Empty(t, response.Errors)

	opts := svc.lastList(t)
	assert.Zero(t, opts.Limit)
	assert.Zero(t, opts.Offset)
}

// TestSingleQueryRejectsTwoSelectors verifies that id and handle cannot be
// given together.
//
// Giving one of them priority would mean silently interpreting a contradictory
// request: the client would think it asked for the handle and would get the
// identity's answer.
func TestSingleQueryRejectsTwoSelectors(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ product(id: "prod_1", handle: "t-shirt") { id } }`)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "product_graphql_bad_argument", response.Errors[0].Extensions["code"])
	assert.Empty(t, svc.singleSelectors, "an invalid request must not reach the service")
}

// TestSingleQueryRejectsAMissingSelector verifies that a query giving neither
// id nor handle is rejected.
func TestSingleQueryRejectsAMissingSelector(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc, `{ product { id } }`)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "product_graphql_bad_argument", response.Errors[0].Extensions["code"])
	assert.Empty(t, svc.singleSelectors)
}

// TestPriceAndInventoryAreReturnedLooselyTyped verifies that other modules'
// records are carried in the schema as JSON.
//
// Typing their fields would mean copying the pricing/inventory schema into this
// module; the record already arrives here loosely typed (see
// service.StoreVariant).
func TestPriceAndInventoryAreReturnedLooselyTyped(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{single: service.StoreProduct{
		Product: models.Product{ID: "prod_1", Handle: "t-shirt"},
		Variants: []service.StoreVariant{{
			Variant:       models.Variant{ID: "var_1", ProductID: "prod_1", Title: "S"},
			PriceSet:      query.Record{"id": "pset_1", "amount": 1990},
			InventoryItem: query.Record{"id": "iitem_1", "stocked_quantity": 7},
		}},
	}}

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ product(id: "prod_1") { variants { id priceSet inventoryItem } } }`)

	require.Empty(t, response.Errors)

	variants := productVariants(t, response)
	require.Len(t, variants, 1)

	variant, ok := variants[0].(map[string]any)
	require.True(t, ok)

	price, ok := variant["priceSet"].(map[string]any)
	require.True(t, ok, "the price set must come back as an object")
	assert.InDelta(t, float64(1990), price["amount"], 0)

	inventory, ok := variant["inventoryItem"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "iitem_1", inventory["id"])
}

// TestMissingPriceProviderReturnsNull verifies that a missing record comes back
// as null.
//
// If pricing is not registered in this deployment the service never fills the
// field in. That is why the schema's field is nullable: a typed and required
// field would have to invent a "product with a price of zero" — showing no
// price at all is better than showing a wrong one.
func TestMissingPriceProviderReturnsNull(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{single: service.StoreProduct{
		Product:  models.Product{ID: "prod_1"},
		Variants: []service.StoreVariant{{Variant: models.Variant{ID: "var_1"}}},
	}}

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ product(id: "prod_1") { variants { priceSet } } }`)

	require.Empty(t, response.Errors)

	variant, ok := productVariants(t, response)[0].(map[string]any)
	require.True(t, ok)
	assert.Nil(t, variant["priceSet"])
}

// TestServiceErrorKeepsItsType verifies that a service error preserves its kind
// and its code.
func TestServiceErrorKeepsItsType(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{
		err: coreerrors.NotFound("product_not_found", "product not found: prod_missing"),
	}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ product(id: "prod_missing") { id } }`)

	require.NotEmpty(t, response.Errors)
	assert.Equal(t, "product not found: prod_missing", response.Errors[0].Message)
	assert.Equal(t, "product_not_found", response.Errors[0].Extensions["code"])
	assert.Nil(t, response.Data["product"], "a product that is not found comes back as null")
}

// productVariants returns the variant array in the response of a single-item
// query.
func productVariants(t *testing.T, response graphQLResponse) []any {
	t.Helper()

	product, ok := response.Data["product"].(map[string]any)
	require.True(t, ok, "the response must carry a product: %#v", response.Data)

	variants, ok := product["variants"].([]any)
	require.True(t, ok, "the product must carry a variant array: %#v", product)

	return variants
}

// ptr returns the address of the given value.
//
// The count in the envelope is a pointer (nil means "it was not counted", see
// service.ListResult) and that is why the constants the fake storefront returns
// have to be addressable.
func ptr[T any](v T) *T { return &v }

// TestUnselectedCountIsNotComputed verifies that the selection set determines
// the AMOUNT OF WORK.
//
// The gap that was closed was this: "count" is a field in GraphQL and when it
// was not selected it did not show up in the response anyway, but the query was
// still running. Measured in gobit_load — on a catalog of 52,004 products the
// count took 64.07 ms and the rest of the request 0.65 ms; that is, a field the
// client NEVER ASKED FOR was writing 99% of the request.
//
// The claim looks not at the response but at the options passed to the SERVICE:
// "count" is not in the response anyway (the client did not select it) and a
// test looking at that would pass even if the count were still being computed.
func TestUnselectedCountIsNotComputed(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products { items { id } } }`)

	require.Empty(t, response.Errors)
	assert.True(t, svc.lastList(t).SkipCount,
		"no count query may be asked for when count is not selected")
}

// TestSelectedCountIsComputed verifies that when the field IS selected the
// count is still asked for and comes back FILLED IN.
//
// [TestUnselectedCountIsNotComputed] on its own would also pass with a broken
// implementation that never asks for the count. What the two tests say together
// is the condition itself: the work is done when the field is asked for.
//
// The "count: Int!" in the schema does not accept nil, so this test is at the
// same time the proof of a contract violation: had the count been selected and
// not computed, the response would come back with a field error.
func TestSelectedCountIsComputed(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{list: service.ListResult[service.StoreProduct]{
		Items: []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
		Count: ptr(42),
	}}

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products { count items { id } } }`)

	require.Empty(t, response.Errors)
	assert.False(t, svc.lastList(t).SkipCount, "a selected count must be computed")

	list, ok := response.Data["products"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, float64(42), list["count"], 0)
}

// TestSkippedCountIsNotComputed verifies that the decision also listens to the
// @skip directive.
//
// The directive is APPLIED on the server side: a client writing
// `count @skip(if: true)` cannot see that field in the response, so doing work
// for it is wasted work as well. An implementation that walks its own selection
// set by hand would miss this case; gqlgen's FieldRequested does not, and this
// test pins that difference.
func TestSkippedCountIsNotComputed(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{}
	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products { count @skip(if: true) items { id } } }`)

	require.Empty(t, response.Errors)
	assert.True(t, svc.lastList(t).SkipCount,
		"no count query may be asked for a field skipped with @skip")

	list, ok := response.Data["products"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, list, "count", "a skipped field must not be in the response either")
}

// TestCountInsideAFragmentIsComputed verifies that asking for the field through
// a FRAGMENT also makes it count as selected.
//
// Generated clients ask for fields inside a fragment almost every time. An
// implementation looking only at direct selections would silently give the
// wrong answer here: the count would never be computed and, because the schema
// says "Int!", the response would fail with a field error — that is, every
// generated client would break.
func TestCountInsideAFragmentIsComputed(t *testing.T) {
	t.Parallel()

	svc := &fakeStorefront{list: service.ListResult[service.StoreProduct]{
		Items: []service.StoreProduct{{Product: models.Product{ID: "prod_1"}}},
		Count: ptr(3),
	}}

	response, _ := runQuery(t, identityWith([]string{"sc_1"}), svc,
		`{ products { ...page items { id } } } fragment page on ProductList { count }`)

	require.Empty(t, response.Errors, "%#v", response.Errors)
	assert.False(t, svc.lastList(t).SkipCount,
		"a field asked for from inside a fragment IS SELECTED too")

	list, ok := response.Data["products"].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, float64(3), list["count"], 0)
}
