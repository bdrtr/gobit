package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// providerFixture is the shared setup of the provider tests.
type providerFixture struct {
	store    *memStore
	products query.Provider
	variants query.Provider
	seeded   models.Product
}

// newProviderFixture builds a setup that has one product and one variant.
func newProviderFixture(t *testing.T) providerFixture {
	t.Helper()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	seeded := seedProduct(t, svc, "shirt", "Shirt")

	return providerFixture{
		store:    store,
		products: service.NewProductProvider(store),
		variants: service.NewVariantProvider(store),
		seeded:   seeded,
	}
}

// TestProviderEntityNamesMatchRegistration verifies that the entity names the
// providers offer coincide with the registration names.
//
// Query verifies the Entity() value of the provider it resolved under
// "<entity>.query"; a mismatch becomes an error instead of silently pulling data
// from the wrong module.
func TestProviderEntityNamesMatchRegistration(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	assert.Equal(t, "product", fx.products.Entity())
	assert.Equal(t, "variant", fx.variants.Entity())
	assert.Equal(t, service.EntityProduct, fx.products.Entity())
	assert.Equal(t, service.EntityVariant, fx.variants.Entity())
}

// TestProductProviderListReturnsRecords verifies that the root list comes back
// as records and that the id field is present.
func TestProductProviderListReturnsRecords(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)

	records, err := fx.products.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, fx.seeded.ID, records[0][query.IDField],
		"the record should carry the %q field Query joins on", query.IDField)
	assert.Equal(t, "shirt", records[0]["handle"])
}

// TestProductProviderFilters verifies that the supported filters are applied.
func TestProductProviderFilters(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"status": "published"},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"status": "draft"},
	})
	require.NoError(t, err)
	assert.Empty(t, records, "the status filter should be applied")

	records, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"handle": "missing"},
	})
	require.NoError(t, err)
	assert.Empty(t, records)
}

// taxonomyFixture is the setup of the catalog taxonomy tests.
//
// It is laid out so that every assertion below can FAIL. There are two
// categories and two tags rather than one of each, so a filter that ignored its
// value would be caught; there is a product in NEITHER, so a filter that matched
// everything would be caught; and the second member of the shirt category is a
// DRAFT, so dropping the status from the combination shows up as a count of two.
type taxonomyFixture struct {
	products query.Provider
	shirts   models.Category
	summer   models.Category
	sale     models.Tag
	fresh    models.Tag
	// listed is published, sits in BOTH categories and carries the "sale" tag.
	listed models.Product
	// draft sits in the shirt category and is not published.
	draft models.Product
}

// newTaxonomyFixture builds the products, categories and tags of the taxonomy
// tests.
func newTaxonomyFixture(t *testing.T) taxonomyFixture {
	t.Helper()

	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	shirts, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Shirts", Handle: "shirts"})
	require.NoError(t, err)
	summer, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Summer", Handle: "summer"})
	require.NoError(t, err)
	sale, err := svc.CreateTag(ctx, "sale")
	require.NoError(t, err)
	fresh, err := svc.CreateTag(ctx, "new")
	require.NoError(t, err)

	listed := seedProductInput(t, svc, service.CreateProductInput{
		Handle:      "listed-shirt",
		Title:       "Listed Shirt",
		Status:      models.StatusPublished,
		CategoryIDs: []string{shirts.ID, summer.ID},
		TagIDs:      []string{sale.ID},
	})
	draft := seedProductInput(t, svc, service.CreateProductInput{
		Handle:      "draft-shirt",
		Title:       "Draft Shirt",
		Status:      models.StatusDraft,
		CategoryIDs: []string{shirts.ID},
	})
	// The product that belongs to nothing. Without it a filter that was dropped
	// on the floor would return the same two rows as a filter that was applied.
	seedProductInput(t, svc, service.CreateProductInput{
		Handle: "loose-hat",
		Title:  "Loose Hat",
		Status: models.StatusPublished,
	})

	return taxonomyFixture{
		products: service.NewProductProvider(store),
		shirts:   shirts,
		summer:   summer,
		sale:     sale,
		fresh:    fresh,
		listed:   listed,
		draft:    draft,
	}
}

// recordIDs returns the id field of every record.
func recordIDs(t *testing.T, records []query.Record) []string {
	t.Helper()

	out := make([]string, 0, len(records))
	for _, rec := range records {
		id, ok := rec[query.IDField].(string)
		require.True(t, ok, "the record carries no readable %q field: %#v", query.IDField, rec)
		out = append(out, id)
	}
	return out
}

// TestProductProviderTaxonomyFilters verifies that the read layer answers the
// two filters the STOREFRONT has always been able to ask.
//
// Until these existed the panel — which reaches the catalog only through this
// provider — could not narrow the catalog by category while the shop's customers
// could. The names are the storefront's own, so the two surfaces stay one
// vocabulary.
func TestProductProviderTaxonomyFilters(t *testing.T) {
	t.Parallel()

	fx := newTaxonomyFixture(t)
	ctx := context.Background()

	byCategory, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": fx.shirts.ID},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.listed.ID, fx.draft.ID}, recordIDs(t, byCategory),
		"the category filter returned the wrong set")

	byOtherCategory, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": fx.summer.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.listed.ID}, recordIDs(t, byOtherCategory),
		"the filter is not reading its VALUE; the two categories hold different sets")

	byTag, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"tag_id": fx.sale.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.listed.ID}, recordIDs(t, byTag))

	// A tag nobody carries. An "IS NULL OR" predicate written the wrong way
	// round degrades into no filter at all, which looks like a wide catalog and
	// says nothing.
	byUnusedTag, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"tag_id": fx.fresh.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, byUnusedTag, "a tag no product carries returned products")
}

// TestProductProviderCombinesTheTaxonomyFiltersWithTheOthers verifies that the
// new filters NARROW alongside the old ones instead of replacing them.
//
// The combination is where a filter dispatch goes wrong quietly: a case that
// overwrote the filter struct, or a status that stopped being carried, would
// still return a plausible-looking page.
func TestProductProviderCombinesTheTaxonomyFiltersWithTheOthers(t *testing.T) {
	t.Parallel()

	fx := newTaxonomyFixture(t)
	ctx := context.Background()

	both, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": fx.shirts.ID, "tag_id": fx.sale.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.listed.ID}, recordIDs(t, both),
		"category AND tag together should narrow, not widen")

	// The category of one product together with the tag of another: the
	// intersection is empty, and an implementation that applied only the last
	// filter it read would return a record here.
	crossed, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": fx.summer.ID, "tag_id": fx.fresh.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, crossed)

	withStatus, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": fx.shirts.ID, "status": "published"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.listed.ID}, recordIDs(t, withStatus),
		"the draft product in the same category should have been left out")
}

// TestProductProviderPagesTheTaxonomyResult verifies that the limit still binds
// on the filtered path.
//
// The limit reaches SQL as a LIMIT and the "0 means unlimited" of the Query
// contract is translated by [providerLimit]. A filter that pushed the paging
// aside would return the whole catalog to a caller that asked for one row.
func TestProductProviderPagesTheTaxonomyResult(t *testing.T) {
	t.Parallel()

	fx := newTaxonomyFixture(t)
	ctx := context.Background()
	filters := map[string]any{"category_id": fx.shirts.ID}

	unlimited, err := fx.products.List(ctx, query.ListOptions{Filters: filters})
	require.NoError(t, err)
	require.Len(t, unlimited, 2, "a limit of zero means unlimited")

	first, err := fx.products.List(ctx, query.ListOptions{Filters: filters, Limit: 1})
	require.NoError(t, err)
	assert.Len(t, first, 1)

	second, err := fx.products.List(ctx, query.ListOptions{Filters: filters, Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.NotEqual(t, recordIDs(t, first), recordIDs(t, second),
		"the offset returned the same row again")

	past, err := fx.products.List(ctx, query.ListOptions{Filters: filters, Limit: 1, Offset: 2})
	require.NoError(t, err)
	assert.Empty(t, past)
}

// TestProductProviderRefusesATaxonomyFilterWithIDs pins the DECISION taken for
// the id path.
//
// On that path the provider reads the products by id and re-checks the rest of
// the criteria in Go, and the category/tag membership is not on the record it
// holds. The three answers were: check the empty relation slices anyway (every
// query silently returns nothing), fetch the memberships and check honestly (a
// second copy of a predicate that lives in SQL, over a TREE), or refuse. The
// refusal is what is implemented, and it is what this test holds in place.
func TestProductProviderRefusesATaxonomyFilterWithIDs(t *testing.T) {
	t.Parallel()

	fx := newTaxonomyFixture(t)
	ctx := context.Background()

	// The id and the category MATCH: this product really is in that category.
	// The refusal must not depend on the data — a filter that failed only when
	// the answer would have been empty is a filter that works sometimes, and the
	// caller cannot tell which time it got.
	_, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.listed.ID, "category_id": fx.shirts.ID},
	})
	require.Error(t, err, "a silently empty page is the failure this refusal exists to prevent")
	assert.True(t, errors.IsInvalid(err), "the refusal should be a validation error: %v", err)
	assert.Contains(t, err.Error(), "category_id",
		"the message should name the filter to drop, not the id")

	_, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []string{fx.listed.ID}, "tag_id": fx.sale.ID},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the ids shape should be refused as well: %v", err)

	// The id filter ON ITS OWN is untouched by the refusal: it is the panel's
	// product detail page and it runs on every product view.
	alone, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.listed.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.listed.ID}, recordIDs(t, alone))
}

// TestProductProviderIDFilterRechecksTheScalarCriteria covers the branch the
// refusal above rests on.
//
// The claim is that status, handle and collection_id ARE re-checked on the id
// path, in memory, against the record the batch read returned. Nothing tested
// that branch before — so the assertion that a taxonomy filter cannot be
// answered there stood beside three siblings whose behavior was equally
// unobserved.
func TestProductProviderIDFilterRechecksTheScalarCriteria(t *testing.T) {
	t.Parallel()

	fx := newTaxonomyFixture(t)
	ctx := context.Background()

	published, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []string{fx.listed.ID, fx.draft.ID}, "status": "published"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.listed.ID}, recordIDs(t, published),
		"the status was not applied to the id set")

	byHandle, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.listed.ID, "handle": "draft-shirt"},
	})
	require.NoError(t, err)
	assert.Empty(t, byHandle, "the handle of another product should match nothing")

	// No product in the fixture has a collection, so the criterion is a genuine
	// discriminator: a re-check that skipped a nil column would return the row.
	byCollection, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.listed.ID, "collection_id": "pcol_missing"},
	})
	require.NoError(t, err)
	assert.Empty(t, byCollection)
}

// searchFixture is the setup of the free-text search tests.
//
// The titles are chosen so that every assertion below CAN fail. Two of them
// carry "shirt" in DIFFERENT cases, so a match that folded only one side (or
// neither) shows up as a shorter set; the word sits at the END of one title and
// in the MIDDLE of the other, so a filter that anchored the pattern is caught;
// one product carries none of the searched words at all, so a filter dropped on
// the floor returns three rows where it should return two; and the two shirts
// differ in STATUS and one of them is out of the category, so the combinations
// narrow instead of widening.
//
// Everything here is ASCII on purpose. The fake store folds case with
// strings.ToLower and the database folds it with ILIKE, and the two agree only
// over ASCII (see [productProvider.List], "Why the SEARCH cannot be combined
// with id/ids"): a non-ASCII fixture would make this package's answer depend on
// the collation of a cluster it never talks to.
type searchFixture struct {
	products query.Provider
	linen    models.Category
	// blue is published, sits in the linen category and spells the word in
	// UPPER case at the end of its title.
	blue models.Product
	// red is a draft, sits in the same category and spells the word in lower
	// case in the middle of its title.
	red models.Product
	// hat matches none of the searched words.
	hat models.Product
}

// newSearchFixture builds the products and the category of the search tests.
func newSearchFixture(t *testing.T) searchFixture {
	t.Helper()

	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	linen, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Linen", Handle: "linen"})
	require.NoError(t, err)

	blue := seedProductInput(t, svc, service.CreateProductInput{
		Handle:      "blue-linen-shirt",
		Title:       "Blue Linen SHIRT",
		Status:      models.StatusPublished,
		CategoryIDs: []string{linen.ID},
	})
	red := seedProductInput(t, svc, service.CreateProductInput{
		Handle:      "red-linen-shirt",
		Title:       "Red shirt of linen",
		Status:      models.StatusDraft,
		CategoryIDs: []string{linen.ID},
	})
	hat := seedProductInput(t, svc, service.CreateProductInput{
		Handle: "wool-hat",
		Title:  "Wool Hat",
		Status: models.StatusPublished,
	})

	return searchFixture{
		products: service.NewProductProvider(store),
		linen:    linen,
		blue:     blue,
		red:      red,
		hat:      hat,
	}
}

// TestProductProviderSearchesTheTitle verifies that the read layer answers the
// storefront's free-text filter.
//
// It was the last one of the storefront's set the provider could not answer, so
// until now a shopper could search the catalog and the operator maintaining it
// — who reaches the catalog only through this surface — could not.
func TestProductProviderSearchesTheTitle(t *testing.T) {
	t.Parallel()

	fx := newSearchFixture(t)
	ctx := context.Background()

	// The term is lower case and one of the titles is UPPER case: a match that
	// folded neither side would return one row instead of two.
	lower, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "shirt"},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.blue.ID, fx.red.ID}, recordIDs(t, lower),
		"the search should be case-insensitive, as ILIKE is")

	// And the same question with the cases swapped. One direction passing tells
	// nothing about the other: a fold applied to the TITLE alone answers the
	// first assertion and fails this one.
	upper, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "SHIRT"},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.blue.ID, fx.red.ID}, recordIDs(t, upper),
		"folding one side only is not case-insensitivity")

	// A fragment that is neither a prefix of the title nor a whole word in it.
	// A "starts with" or a word-boundary match returns nothing here, and both
	// are plausible things for an implementation to drift into.
	middle, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "hir"},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.blue.ID, fx.red.ID}, recordIDs(t, middle),
		"the match should be a substring anywhere in the title")

	// The filter reads its VALUE: a different term selects a different product.
	other, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "wool"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.hat.ID}, recordIDs(t, other))

	// A word no title carries. An "IS NULL OR" predicate written the wrong way
	// round degrades into no filter at all, which returns the whole catalog and
	// reads as a wide answer rather than a broken one.
	none, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "cardigan"},
	})
	require.NoError(t, err)
	assert.Empty(t, none, "a word no product carries returned products")
}

// TestProductProviderCombinesTheSearchWithTheOtherFilters verifies that the
// search NARROWS alongside its neighbors instead of replacing them.
//
// The combination is where a filter dispatch goes wrong quietly: a case that
// overwrote the filter struct, or a status that stopped being carried, still
// returns a plausible-looking page.
func TestProductProviderCombinesTheSearchWithTheOtherFilters(t *testing.T) {
	t.Parallel()

	fx := newSearchFixture(t)
	ctx := context.Background()

	withStatus, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "shirt", "status": "published"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.blue.ID}, recordIDs(t, withStatus),
		"the draft shirt matching the same term should have been left out")

	withCategory, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "linen", "category_id": fx.linen.ID},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.blue.ID, fx.red.ID}, recordIDs(t, withCategory),
		"the search and the category should narrow together")

	// A term matching one product and a category holding the others: the
	// intersection is empty, and an implementation applying only the last filter
	// it read would return a record here.
	crossed, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "wool", "category_id": fx.linen.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, crossed)

	// The limit still binds on the searched path: it reaches SQL as a LIMIT.
	page, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "shirt"}, Limit: 1,
	})
	require.NoError(t, err)
	assert.Len(t, page, 1)
}

// TestProductProviderTreatsAnEmptySearchAsNoFilter pins the NORMALIZATION
// decision.
//
// The rule is the module's own and it is older than this provider: REST counts
// an empty parameter as not given, GraphQL trims the argument to nil, and
// graph/schema_test.go's TestEmptyTextArgumentBuildsNoFilter holds every text
// argument of the storefront listing to it — explicitly including the ones added
// later. This is the read layer taking the same rule.
//
// What is being kept out are two SILENT answers pointing opposite ways: an empty
// string reaches SQL as ILIKE '%%' and matches every row, a run of spaces
// reaches it as ILIKE '%   %' and matches nothing. The direction chosen is the
// first one — an empty term narrows nothing — because the caller most likely to
// send one is a panel whose operator has just cleared the search box, and
// answering that with an empty shop while the storefront answers with the
// catalog is one question with two answers.
func TestProductProviderTreatsAnEmptySearchAsNoFilter(t *testing.T) {
	t.Parallel()

	fx := newSearchFixture(t)
	ctx := context.Background()

	unfiltered, err := fx.products.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, unfiltered, 3, "the fixture should hold three products")

	empty, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": ""},
	})
	require.NoError(t, err, "an empty term is not an error; it is not a criterion")
	assert.ElementsMatch(t, recordIDs(t, unfiltered), recordIDs(t, empty),
		"an empty term must answer exactly what no term answers")

	blank, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "   "},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, recordIDs(t, unfiltered), recordIDs(t, blank),
		"a whitespace term must not become a pattern that matches nothing")

	// The trim is not only about emptiness. A padded term handed to SQL as it
	// came matches '% shirt %', which finds neither the title that ENDS in the
	// word nor the one that has it followed by a single space.
	padded, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": "  shirt  "},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.blue.ID, fx.red.ID}, recordIDs(t, padded),
		"the term that travels to the query must be the trimmed one")
}

// TestProductProviderRefusesASearchWithIDs pins the DECISION taken for the id
// path.
//
// Unlike a category membership the title IS on the record the id branch holds,
// so the re-check is possible — and it would answer with a DIFFERENT case rule
// than the SQL does, because ILIKE folds the way the cluster folds and a
// C-locale cluster folds ASCII only (see core/db/casefold.go and ADR 0015). Two
// implementations of one filter that cannot be made to agree are refused rather
// than half-answered, and this test is what holds the refusal in place.
func TestProductProviderRefusesASearchWithIDs(t *testing.T) {
	t.Parallel()

	fx := newSearchFixture(t)
	ctx := context.Background()

	// The id and the term MATCH: this product's title really does carry the
	// word. The refusal must not depend on the data — one that fired only when
	// the answer would have been empty is a filter that works sometimes, and the
	// caller cannot tell which time it got.
	_, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.blue.ID, "q": "shirt"},
	})
	require.Error(t, err, "a page silently answering a different case rule is what this refusal prevents")
	assert.True(t, errors.IsInvalid(err), "the refusal should be a validation error: %v", err)
	assert.Contains(t, err.Error(), `"q"`, "the message should name the filter to drop, not the id")
	assert.NotContains(t, err.Error(), "membership",
		"this is not the taxonomy refusal; an explanation that does not fit the request "+
			"sends the reader looking for a membership it never asked about")

	_, err = fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []string{fx.blue.ID}, "q": "shirt"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "the ids shape should be refused as well: %v", err)

	// Normalization runs BEFORE the refusal, and that ordering is the decision:
	// a whitespace term is not a criterion, so an id filter arriving beside a
	// cleared search box is answered. Refusing it would turn an empty box into
	// an error page.
	cleared, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.blue.ID, "q": "   "},
	})
	require.NoError(t, err, "a whitespace term is not part of the request and cannot refuse it")
	assert.Equal(t, []string{fx.blue.ID}, recordIDs(t, cleared))

	// The id filter ON ITS OWN is untouched: it is the panel's product detail
	// page and it runs on every product view.
	alone, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.blue.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.blue.ID}, recordIDs(t, alone))
}

// TestTheProviderAnswersOnlyTheStorefrontSpellingOfTheSearch verifies that the
// other plausible names for the same filter are still REFUSED.
//
// The value of using the storefront's word is that there is exactly ONE word. A
// provider that quietly also answered "search" would let a second vocabulary
// grow, and the two would drift the day one of them gained a behavior; a
// provider that answered NEITHER would be the ADR 0004 fault. So the refusal is
// what makes the agreement on "q" worth anything.
func TestTheProviderAnswersOnlyTheStorefrontSpellingOfTheSearch(t *testing.T) {
	t.Parallel()

	fx := newSearchFixture(t)
	ctx := context.Background()

	for _, spelling := range []string{"search", "title", "query", "Q"} {
		_, err := fx.products.List(ctx, query.ListOptions{
			Filters: map[string]any{spelling: "shirt"},
		})
		require.Error(t, err, "the filter %q should not be answered", spelling)
		assert.True(t, errors.IsInvalid(err),
			"a second spelling should be refused, not applied: %v", err)
	}

	// The value's TYPE is validated like every other filter's: a number is not a
	// search term, and turning it into one with fmt would invent a criterion the
	// caller never wrote.
	_, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"q": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type mismatch should give a validation error: %v", err)
}

// TestProviderRejectsUnknownFilter verifies that an unrecognized filter is NOT
// SILENTLY IGNORED (ADR 0004).
func TestProviderRejectsUnknownFilter(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	_, err := fx.products.List(ctx, query.ListOptions{
		Filters: map[string]any{"color": "blue"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized filter should give a validation error: %v", err)

	_, err = fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"color": "blue"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized filter should give a validation error: %v", err)
}

// TestProviderRejectsWrongFilterType shows that the type of the filter value is
// validated.
func TestProviderRejectsWrongFilterType(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)

	_, err := fx.products.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"status": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type mismatch should give a validation error: %v", err)
}

// TestProviderProjectsFields verifies that the field selection is applied and
// that an unrecognized field is rejected.
func TestProviderProjectsFields(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.variants.List(ctx, query.ListOptions{Fields: []string{"id", "title"}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2, "only the requested fields should come back: %#v", records[0])
	assert.Contains(t, records[0], "id")
	assert.Contains(t, records[0], "title")

	_, err = fx.variants.List(ctx, query.ListOptions{Fields: []string{"price"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized field should give a validation error: %v", err)
}

// TestVariantProviderFetchByIDsIsBatched verifies that the expansion is resolved
// with a SINGLE call and that an id that is not found is not an error (ADR
// 0004).
func TestVariantProviderFetchByIDsIsBatched(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	variantID := fx.seeded.Variants[0].ID

	before := fx.store.callCount("ListVariantsByIDs")
	records, err := fx.variants.FetchByIDs(context.Background(),
		[]string{variantID, "variant_missing"}, nil)
	require.NoError(t, err, "an id that is not found is not an error")
	require.Len(t, records, 1)
	assert.Equal(t, variantID, records[0][query.IDField])
	assert.Equal(t, before+1, fx.store.callCount("ListVariantsByIDs"),
		"whatever the number of ids, a single query should be made")
}

// TestVariantProviderFiltersByProduct verifies that the variants are filtered by
// product.
func TestVariantProviderFiltersByProduct(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()

	records, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"product_id": fx.seeded.ID},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"product_ids": []string{"prod_missing"}},
	})
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestVariantProviderIDsFilterAcceptsBothShapes verifies that the id filter
// accepts both the single string and the slice shape.
func TestVariantProviderIDsFilterAcceptsBothShapes(t *testing.T) {
	t.Parallel()

	fx := newProviderFixture(t)
	ctx := context.Background()
	variantID := fx.seeded.Variants[0].ID

	single, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": variantID},
	})
	require.NoError(t, err)
	require.Len(t, single, 1)

	many, err := fx.variants.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []any{variantID}},
	})
	require.NoError(t, err)
	require.Len(t, many, 1)
	assert.Equal(t, single[0][query.IDField], many[0][query.IDField])
}

// TestProviderPaging verifies that limit and offset are applied.
func TestProviderPaging(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	seedProduct(t, svc, "one", "One")
	seedProduct(t, svc, "two", "Two")
	seedProduct(t, svc, "three", "Three")
	products := service.NewProductProvider(store)
	ctx := context.Background()

	all, err := products.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 3, "when no limit is given it should count as unlimited")

	page, err := products.List(ctx, query.ListOptions{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, page, 2)

	rest, err := products.List(ctx, query.ListOptions{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, rest, 1)
}
