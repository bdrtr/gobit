package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file covers the storefront's four VOCABULARY endpoints: collections,
// categories, tags and option values.
//
// They are the half of the storefront the product listing cannot stand in for.
// The listing takes a collection id, a category id and a tag id; a storefront
// has none of those, it has the word a shopper clicked. Everything that turns
// that word into an id happens here, and until this file existed all four
// endpoints shipped without a single request ever having been made to them.
//
// What is NOT re-proven here: the envelope shape and the empty-list-is-an-array
// rule. All four go through the same writeList as the admin listings, which
// TestListEnvelopeShape and TestEmptyListReturnsArrayNotNull already pin.

// vocabularyCatalog is a catalog whose four vocabulary reads all answer with an
// empty page and record the options they were called with.
//
// The recorders are returned as pointers rather than read off the catalog,
// because what every test in this file asks is the same question: WHAT did the
// handler ask the service for.
type vocabularyCatalog struct {
	catalog      *fakeCatalog
	collections  *[2]int
	categories   *service.ListCategoriesOptions
	tags         *[2]int
	optionValues *service.ListOptionValuesOptions
}

// newVocabularyCatalog builds a catalog that records every vocabulary call.
func newVocabularyCatalog() vocabularyCatalog {
	var (
		collections  [2]int
		categories   service.ListCategoriesOptions
		tags         [2]int
		optionValues service.ListOptionValuesOptions
	)
	return vocabularyCatalog{
		catalog: &fakeCatalog{
			listCollections: func(_ context.Context, limit, offset int) (service.ListResult[models.Collection], error) {
				collections = [2]int{limit, offset}
				return service.ListResult[models.Collection]{
					Items: []models.Collection{{ID: "pcol_1", Title: "Summer", Handle: "summer"}},
					Count: ptr(1), Limit: limit, Offset: offset,
				}, nil
			},
			listCategories: func(
				_ context.Context, opts service.ListCategoriesOptions,
			) (service.ListResult[models.Category], error) {
				categories = opts
				return service.ListResult[models.Category]{
					Items: []models.Category{{ID: "pcat_1", Name: "Shirts", Handle: "shirts", IsActive: true}},
					Count: ptr(1), Limit: opts.Limit, Offset: opts.Offset,
				}, nil
			},
			listTags: func(_ context.Context, limit, offset int) (service.ListResult[models.Tag], error) {
				tags = [2]int{limit, offset}
				return service.ListResult[models.Tag]{
					Items: []models.Tag{{ID: "ptag_1", Value: "sale"}},
					Count: ptr(1), Limit: limit, Offset: offset,
				}, nil
			},
			listOptionValues: func(
				_ context.Context, opts service.ListOptionValuesOptions,
			) (service.ListResult[models.OptionValuePair], error) {
				optionValues = opts
				return service.ListResult[models.OptionValuePair]{
					Items: []models.OptionValuePair{{OptionTitle: "Color", Value: "red"}},
					Count: ptr(1), Limit: opts.Limit, Offset: opts.Offset,
				}, nil
			},
		},
		collections:  &collections,
		categories:   &categories,
		tags:         &tags,
		optionValues: &optionValues,
	}
}

// vocabularyPaths are the four storefront vocabulary endpoints.
//
// They are listed once and walked by the tests that make the SAME claim about
// all four: a claim that held for three of them and was forgotten on the
// fourth is exactly the shape of fault a per-endpoint test lets through.
var vocabularyPaths = []string{
	"/store/v1/collections",
	"/store/v1/categories",
	"/store/v1/tags",
	"/store/v1/option-values",
}

// TestStoreCategoryListingAsksOnlyForPublicCategories verifies that the
// storefront category listing asks the service for the PUBLIC categories.
//
// is_active is the merchant's switch for a category that is not ready and
// is_internal marks one that exists for operators; both have been in the schema
// since the first migration and PublicOnly is the only thing that reads them.
// The default of the flag is FALSE, so it is the caller that decides, and the
// storefront is the only caller that has to say true — if this handler stopped
// saying it, the storefront menu would silently list the categories the
// merchant switched off, and the merchant's only clue would be a shopper
// following a link into a category that was not meant to exist yet.
//
// The same request also shows that parent_id is passed through: the tree is
// walked one level at a time, which is what a navigation menu asks for.
func TestStoreCategoryListingAsksOnlyForPublicCategories(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()
	r := newRouter(vocabulary.catalog)

	rec := storeRequest(t, r, "/store/v1/categories?parent_id=pcat_root", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.True(t, vocabulary.categories.PublicOnly,
		"the storefront has to ask for public categories only; otherwise a category the merchant "+
			"switched off (is_active) or kept for operators (is_internal) shows up in the shop menu")
	require.NotNil(t, vocabulary.categories.ParentID, "parent_id has to reach the service")
	assert.Equal(t, "pcat_root", *vocabulary.categories.ParentID,
		"the level being walked is the one the client named")
}

// TestStoreCategoryListingWithoutParentAsksForTheWholeTree verifies that an
// absent parent_id is passed on as nil.
//
// nil and the empty string are two different sentences here: nil means "do not
// narrow to a level" and an empty string would be a filter for categories whose
// parent is "", which no category has. A storefront's top-level menu is the
// request with no parent_id, so getting this wrong empties the menu rather than
// filling it.
func TestStoreCategoryListingWithoutParentAsksForTheWholeTree(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()

	rec := storeRequest(t, newRouter(vocabulary.catalog), "/store/v1/categories", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.Nil(t, vocabulary.categories.ParentID,
		"a parent_id that was not given must not become a filter")
}

// TestStoreOptionVocabularyIsScopedToTheKeysChannels verifies that the option
// vocabulary is narrowed to the sales channels of the REQUEST'S IDENTITY and to
// the published catalog.
//
// This is the one vocabulary endpoint that can leak. A collection, a category
// and a tag are one tree for the whole installation, but an option value exists
// only because some product carries it — so an unscoped vocabulary would name
// the colors and sizes of draft products and of products sold in a channel the
// caller holds no key for. That is, it would tell the caller exactly what the
// product listing on the same key refuses to tell them.
//
// Where the channels come from (the identity, never the query string) is proven
// for the surface as a whole in saleschannel_test.go; what is proven here is
// that THIS handler carries them at all.
func TestStoreOptionVocabularyIsScopedToTheKeysChannels(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()

	rec := storeRequest(t, newRouter(vocabulary.catalog), "/store/v1/option-values",
		&corehttp.Principal{ID: "apk_1", Kind: "api_key", SalesChannelIDs: []string{"sc_a", "sc_b"}})
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	assert.Equal(t, []string{"sc_a", "sc_b"}, vocabulary.optionValues.SalesChannelIDs,
		"the vocabulary has to be narrowed to the key's channels; unscoped, it names the values "+
			"of products the same key cannot list")
	assert.True(t, vocabulary.optionValues.PublicOnly,
		"the vocabulary has to be narrowed to the published catalog; a draft product's colors "+
			"are not the storefront's vocabulary")
}

// TestStoreVocabularyPassesPagingThrough verifies that all four vocabulary
// endpoints hand the client's limit and offset to the service.
//
// A catalog's tag list is not a short list. If the parameters were dropped, the
// endpoint would answer with the service's default page for every request and a
// client walking the pages would receive the FIRST page over and over — an
// infinite menu of the same twenty tags, with no error anywhere to explain it.
func TestStoreVocabularyPassesPagingThrough(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()
	r := newRouter(vocabulary.catalog)

	for _, path := range vocabularyPaths {
		rec := storeRequest(t, r, path+"?limit=7&offset=14", nil)
		require.Equal(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
	}

	assert.Equal(t, [2]int{7, 14}, *vocabulary.collections, "collections: limit and offset")
	assert.Equal(t, [2]int{7, 14}, *vocabulary.tags, "tags: limit and offset")
	assert.Equal(t, 7, vocabulary.categories.Limit, "categories: limit")
	assert.Equal(t, 14, vocabulary.categories.Offset, "categories: offset")
	assert.Equal(t, 7, vocabulary.optionValues.Limit, "option values: limit")
	assert.Equal(t, 14, vocabulary.optionValues.Offset, "option values: offset")
}

// TestStoreVocabularyRefusesANonNumericLimit verifies that a limit that is not
// a number comes back as a validation error on all four endpoints.
//
// Falling back to the default silently is the fault worth naming: the client
// that sent "limit=1O" (a letter O) would receive a page it did not ask for and
// would have no way to tell it apart from a catalog that happens to be that
// size. The service must not be reached at all — the recorders below prove it
// was not.
func TestStoreVocabularyRefusesANonNumericLimit(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()
	r := newRouter(vocabulary.catalog)

	for _, path := range vocabularyPaths {
		rec := storeRequest(t, r, path+"?limit=1O", nil)
		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
			"GET %s has to refuse a non-numeric limit rather than quietly serving the default page: %s",
			path, rec.Body.String())
	}

	assert.Equal(t, [2]int{0, 0}, *vocabulary.collections, "a refused request must not reach the service")
	assert.Equal(t, [2]int{0, 0}, *vocabulary.tags, "a refused request must not reach the service")
	assert.Equal(t, service.ListCategoriesOptions{}, *vocabulary.categories,
		"a refused request must not reach the service")
	assert.Equal(t, service.ListOptionValuesOptions{}, *vocabulary.optionValues,
		"a refused request must not reach the service")
}

// TestStoreVocabularyAsksForNoScope verifies that the four vocabulary endpoints
// answer a request that carries NO identity at all.
//
// The identity of the store surface is the publishable key and that key by
// definition carries no scope, so a scope attached here would be a condition no
// storefront client could ever satisfy: the shop's menu would return 401/403 to
// everyone while the product listing next to it kept working. The failure is
// total and it happens at the first request after the deploy, which is why it
// is pinned rather than left to review.
func TestStoreVocabularyAsksForNoScope(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()
	r := newRouter(vocabulary.catalog)

	for _, path := range vocabularyPaths {
		rec := storeRequest(t, r, path, nil)
		assert.Equal(t, http.StatusOK, rec.Code,
			"GET %s has to answer without an identity; the storefront key carries no scope: %s",
			path, rec.Body.String())
	}
}

// TestStoreVocabularyReturnsWhatTheServiceGave verifies that each endpoint
// returns ITS OWN vocabulary.
//
// The four handlers are near-identical in shape, which is precisely how a
// copy-paste that left the wrong service call behind survives a review: the
// collections endpoint answering with tags is a body that parses, passes every
// envelope check and is wrong. The distinguishing field is asserted for each.
func TestStoreVocabularyReturnsWhatTheServiceGave(t *testing.T) {
	t.Parallel()

	vocabulary := newVocabularyCatalog()
	r := newRouter(vocabulary.catalog)

	cases := map[string]struct {
		path  string
		field string
		value string
	}{
		"collections":   {"/store/v1/collections", "handle", "summer"},
		"categories":    {"/store/v1/categories", "handle", "shirts"},
		"tags":          {"/store/v1/tags", "value", "sale"},
		"option values": {"/store/v1/option-values", "option_title", "Color"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := storeRequest(t, r, tc.path, nil)
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

			items, ok := decodeBody(t, rec)["data"].([]any)
			require.True(t, ok, "the list response has to carry a data array")
			require.Len(t, items, 1)
			item, ok := items[0].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tc.value, item[tc.field],
				"%s has to answer with its own vocabulary, not another endpoint's", tc.path)
		})
	}
}
