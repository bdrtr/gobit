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

// categoryFixture is the setup of the category provider tests.
//
// The tree is deliberately mixed: under one parent there is a category a shopper
// may see, one the merchant SWITCHED OFF and one that exists for operators. A
// fixture of three visible categories could not tell the provider's decision
// (show everything unless asked otherwise) from the storefront's (show only the
// public ones) — both would return the same list.
type categoryFixture struct {
	store      *memStore
	categories query.Provider
	// clothing is the root; the three below are its children.
	clothing models.Category
	// shown is active and not internal: the storefront lists it.
	shown models.Category
	// switchedOff is what a merchant turned off and has to be able to turn back
	// on.
	switchedOff models.Category
	// internalOnly exists for operators and was never meant to be browsable.
	internalOnly models.Category
}

// newCategoryFixture builds the category tree of the tests.
func newCategoryFixture(t *testing.T) categoryFixture {
	t.Helper()

	ctx := context.Background()
	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	clothing, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Clothing", Handle: "clothing"})
	require.NoError(t, err)
	shown, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Shirts", Handle: "shirts", ParentID: &clothing.ID,
	})
	require.NoError(t, err)
	switchedOff, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Coming Soon", Handle: "coming-soon", ParentID: &clothing.ID, IsActive: ptr(false),
	})
	require.NoError(t, err)
	internalOnly, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Staff Picks", Handle: "staff-picks", ParentID: &clothing.ID, IsInternal: true,
	})
	require.NoError(t, err)

	return categoryFixture{
		store:        store,
		categories:   service.NewCategoryProvider(store),
		clothing:     clothing,
		shown:        shown,
		switchedOff:  switchedOff,
		internalOnly: internalOnly,
	}
}

// TestCategoryProviderEntityNameMatchesRegistration verifies that the entity
// name the provider offers is the one it is registered under.
//
// Query resolves a provider under "<entity>.query" and then verifies Entity();
// a mismatch is caught there rather than pulling records from the wrong
// provider, so the two have to be one value.
func TestCategoryProviderEntityNameMatchesRegistration(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)
	assert.Equal(t, "category", fx.categories.Entity())
	assert.Equal(t, service.EntityCategory, fx.categories.Entity())
}

// TestCategoryProviderShowsTheOperatorEverything pins the decision that a panel
// is not a storefront.
//
// The storefront's category endpoint passes PublicOnly and never lists a
// switched-off or an internal category. This provider does not, and the reason
// is that a merchant who cannot SEE the category they switched off has no way to
// switch it back on. Were the default flipped "to be safe", the same operator
// would get one list from the admin service and a shorter one from the read
// layer, with nothing to say which.
func TestCategoryProviderShowsTheOperatorEverything(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)

	records, err := fx.categories.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{fx.clothing.ID, fx.shown.ID, fx.switchedOff.ID, fx.internalOnly.ID},
		recordIDs(t, records),
		"the read layer hid a category the operator has to manage")
}

// TestCategoryProviderReportsTheFlags verifies that the record says which
// categories a shopper would see.
//
// This is what makes the wide default honest rather than careless: a list that
// mixes live and switched-off rows without saying which is which is a trap, and
// a panel reading it would render them identically.
func TestCategoryProviderReportsTheFlags(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)

	records, err := fx.categories.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)

	byID := map[string]query.Record{}
	for _, rec := range records {
		id, ok := rec[query.IDField].(string)
		require.True(t, ok)
		byID[id] = rec
	}

	assert.Equal(t, true, byID[fx.shown.ID]["is_active"])
	assert.Equal(t, false, byID[fx.shown.ID]["is_internal"])
	assert.Equal(t, false, byID[fx.switchedOff.ID]["is_active"],
		"a switched-off category came back looking live")
	assert.Equal(t, true, byID[fx.internalOnly.ID]["is_internal"],
		"an operator-only category came back looking browsable")
	assert.Equal(t, fx.clothing.ID, byID[fx.shown.ID]["parent_id"],
		"the tree cannot be rebuilt without the parent")
	assert.Equal(t, "", byID[fx.clothing.ID]["parent_id"],
		"a root category has no parent and the record should say so as an empty value")
}

// TestCategoryProviderPublicOnlyNarrowsToTheShopfrontSet verifies the opt-in.
//
// A storefront-shaped consumer asks for it and gets exactly the set the
// storefront's own endpoint returns: active AND not internal.
func TestCategoryProviderPublicOnlyNarrowsToTheShopfrontSet(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)

	records, err := fx.categories.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"public_only": true},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.clothing.ID, fx.shown.ID}, recordIDs(t, records),
		"public_only must drop the switched-off and the internal category, and nothing else")

	// false is not the same as absent, but it has to mean the same thing here:
	// the flag says "narrow", so not narrowing is the wide list.
	wide, err := fx.categories.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"public_only": false},
	})
	require.NoError(t, err)
	assert.Len(t, wide, 4)
}

// TestCategoryProviderParentFilterWalksOneLevel verifies the tree filter.
//
// A navigation menu asks for one level at a time; without it the whole tree
// comes back flat and the consumer rebuilds the hierarchy itself.
func TestCategoryProviderParentFilterWalksOneLevel(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)
	ctx := context.Background()

	children, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"parent_id": fx.clothing.ID},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{fx.shown.ID, fx.switchedOff.ID, fx.internalOnly.ID},
		recordIDs(t, children),
		"the root itself is not its own child")

	leaves, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"parent_id": fx.shown.ID},
	})
	require.NoError(t, err)
	assert.Empty(t, leaves)
}

// TestCategoryProviderIDFilterAppliesTheSameCriteria verifies that the id path
// answers like the listing path.
//
// The re-check is done IN GO there, and it is allowed to be — unlike the
// catalog's taxonomy filters, these criteria are scalar columns on the record the
// batch read already returned. Two paths that answered the same filter
// differently would make the answer depend on how the caller happened to ask.
func TestCategoryProviderIDFilterAppliesTheSameCriteria(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)
	ctx := context.Background()

	named, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []string{fx.shown.ID, fx.switchedOff.ID}},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{fx.shown.ID, fx.switchedOff.ID}, recordIDs(t, named))

	narrowed, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{
			"ids":         []string{fx.shown.ID, fx.switchedOff.ID},
			"public_only": true,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.shown.ID}, recordIDs(t, narrowed),
		"public_only was ignored on the id path; the two paths now disagree")

	withParent, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": []string{fx.shown.ID, fx.clothing.ID}, "parent_id": fx.clothing.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.shown.ID}, recordIDs(t, withParent),
		"the root has no parent and should not survive a parent_id filter")

	// The single-string shape as well: the caller should not have to wrap one id
	// in a slice.
	single, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": fx.internalOnly.ID},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{fx.internalOnly.ID}, recordIDs(t, single))
}

// TestCategoryProviderRejectsWhatItDoesNotUnderstand verifies that an unknown
// filter and a mistyped value are REFUSED rather than dropped (ADR 0004).
func TestCategoryProviderRejectsWhatItDoesNotUnderstand(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)
	ctx := context.Background()

	_, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"handle": "shirts"},
	})
	require.Error(t, err, "a filter of a NEIGHBORING entity is still an unknown filter here")
	assert.True(t, errors.IsInvalid(err), "an unrecognized filter should give a validation error: %v", err)

	// The string "true" is not accepted for a boolean. A filter that read two
	// spellings of one value would teach two client dialects, and the values
	// outside the pair would need a rule that could only be guessed at.
	_, err = fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"public_only": "true"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type mismatch should give a validation error: %v", err)

	_, err = fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"parent_id": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type mismatch should give a validation error: %v", err)
}

// TestCategoryProviderProjectsFields verifies the field selection and that a
// field the entity does not offer is refused.
//
// "title" is the interesting negative: it is a real field of the PRODUCT entity,
// and a category has a name instead. Answering it with a zero value would hand
// the consumer a record with an empty heading and no way to tell that from a
// category whose name really is empty.
func TestCategoryProviderProjectsFields(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)
	ctx := context.Background()

	records, err := fx.categories.List(ctx, query.ListOptions{
		Fields:  []string{"id", "name"},
		Filters: map[string]any{"id": fx.shown.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Len(t, records[0], 2, "only the requested fields should come back: %#v", records[0])
	assert.Equal(t, "Shirts", records[0]["name"])

	_, err = fx.categories.List(ctx, query.ListOptions{Fields: []string{"title"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "an unrecognized field should give a validation error: %v", err)
}

// TestCategoryProviderFetchByIDsIsBatchedAndHidesNothing verifies the expansion
// surface.
//
// Two claims at once: whatever the number of ids a SINGLE query is made (the
// N+1 the read layer exists to prevent), and the flags are NOT applied — an
// expansion resolves an id a link already named, and answering "no such record"
// for a switched-off category would leave the consumer holding a dangling
// reference.
func TestCategoryProviderFetchByIDsIsBatchedAndHidesNothing(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)

	before := fx.store.callCount("ListCategoriesByIDs")
	records, err := fx.categories.FetchByIDs(context.Background(),
		[]string{fx.switchedOff.ID, fx.internalOnly.ID, "pcat_missing"}, nil)
	require.NoError(t, err, "an id that is not found is not an error")
	assert.ElementsMatch(t, []string{fx.switchedOff.ID, fx.internalOnly.ID}, recordIDs(t, records))
	assert.Equal(t, before+1, fx.store.callCount("ListCategoriesByIDs"),
		"whatever the number of ids, a single query should be made")
}

// TestCategoryProviderPaging verifies that limit and offset are applied on both
// read paths.
//
// The id path pages in memory and the listing path pages in the query; a limit
// that bound on only one of them would give a caller that walks pages a
// different tree depending on how it asked.
func TestCategoryProviderPaging(t *testing.T) {
	t.Parallel()

	fx := newCategoryFixture(t)
	ctx := context.Background()

	all, err := fx.categories.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, all, 4, "when no limit is given it should count as unlimited")

	page, err := fx.categories.List(ctx, query.ListOptions{Limit: 3})
	require.NoError(t, err)
	assert.Len(t, page, 3)

	rest, err := fx.categories.List(ctx, query.ListOptions{Limit: 3, Offset: 3})
	require.NoError(t, err)
	assert.Len(t, rest, 1)

	named := []string{fx.shown.ID, fx.switchedOff.ID, fx.internalOnly.ID}
	firstOfIDs, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": named}, Limit: 2,
	})
	require.NoError(t, err)
	assert.Len(t, firstOfIDs, 2, "the id path ignored the limit")

	restOfIDs, err := fx.categories.List(ctx, query.ListOptions{
		Filters: map[string]any{"ids": named}, Limit: 2, Offset: 2,
	})
	require.NoError(t, err)
	assert.Len(t, restOfIDs, 1)
}
