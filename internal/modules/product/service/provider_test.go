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
