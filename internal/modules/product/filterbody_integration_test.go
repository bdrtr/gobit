//go:build integration

package product_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// This file proves, against a real database, that the product listing's filter
// body still selects the set it claims to.
//
// It exists because of what changed on 2026-09-06: the body used to be one
// fixed string in which every criterion was written as "($n IS NULL OR ...)",
// and it is now assembled per request so that a criterion nobody asked for
// writes no clause at all (saleschannel.go, "An absent criterion writes NO
// clause"). The construction is pinned without a database in
// repository/saleschannel_internal_test.go; what a database adds, and what
// nothing else can add, is that the assembled text is VALID SQL for every
// combination and that it selects the right rows.
//
// The failure mode being guarded is not a crash. A criterion whose clause stops
// being written returns MORE rows than it should — a category page listing the
// whole shop, or a storefront showing another sales channel's catalog — and a
// wrong parameter number sends a category id into the handle comparison and
// returns nothing. Both answer HTTP 200.
//
// The tests run against the REPOSITORY rather than the HTTP surface on purpose:
// the store endpoint cannot set the handle and the admin one cannot set the
// sales channels, so no single surface can drive all seven criteria, and the
// claim is about the seven together.

// filterFixture is a small isolated catalog with a known answer for every
// criterion.
//
// The tests share one database, so every product here carries the fixture's own
// COLLECTION and the assertions filter by it; without that, products left
// behind by unrelated tests join the result and every count assertion becomes
// meaningless. That is also why the ids are built from [uniqueHandle].
type filterFixture struct {
	repo *repository.Repo

	collectionID string
	categoryID   string
	tagID        string
	channelA     string
	channelB     string

	// The four products, by the criteria they satisfy.
	//
	//	both:    the category, the tag and channel A
	//	catOnly: the category, no tag, no channel assignment
	//	tagOnly: the tag, no category, channel B
	//	plain:   nothing at all, and therefore visible in EVERY channel
	both, catOnly, tagOnly, plain string
}

// newFilterFixture writes the catalog described on [filterFixture].
func newFilterFixture(t *testing.T) filterFixture {
	t.Helper()
	ctx := context.Background()

	repo := repository.New(testPool.Pool())

	collection, err := repo.CreateCollection(ctx, models.Collection{
		ID: "pcol_" + uniqueHandle("filter"), Title: "Filter", Handle: uniqueHandle("filter-collection"),
	})
	require.NoError(t, err)

	category, err := repo.CreateCategory(ctx, models.Category{
		ID: "pcat_" + uniqueHandle("filter"), Name: "Filter", Handle: uniqueHandle("filter-category"),
	})
	require.NoError(t, err)

	tag, err := repo.CreateTag(ctx, models.Tag{
		ID: "ptag_" + uniqueHandle("filter"), Value: uniqueHandle("filter-tag"),
	})
	require.NoError(t, err)

	fx := filterFixture{
		repo:         repo,
		collectionID: collection.ID,
		categoryID:   category.ID,
		tagID:        tag.ID,
		channelA:     "sc_" + uniqueHandle("a"),
		channelB:     "sc_" + uniqueHandle("b"),
	}

	fx.both = fx.seed(t, "both")
	fx.catOnly = fx.seed(t, "cat-only")
	fx.tagOnly = fx.seed(t, "tag-only")
	fx.plain = fx.seed(t, "plain")

	require.NoError(t, repo.SetProductCategories(ctx, fx.both, []string{fx.categoryID}))
	require.NoError(t, repo.SetProductCategories(ctx, fx.catOnly, []string{fx.categoryID}))
	require.NoError(t, repo.SetProductTags(ctx, fx.both, []string{fx.tagID}))
	require.NoError(t, repo.SetProductTags(ctx, fx.tagOnly, []string{fx.tagID}))

	fx.assign(t, fx.both, fx.channelA)
	fx.assign(t, fx.tagOnly, fx.channelB)

	return fx
}

// seed writes one published product into the fixture's collection.
//
// The title carries the same marker as the handle so the "q" criterion has
// something to match that is not shared with the neighbouring tests.
func (f filterFixture) seed(t *testing.T, marker string) string {
	t.Helper()

	handle := uniqueHandle("filter-" + marker)
	product, err := f.repo.CreateProduct(context.Background(), models.Product{
		ID:           "prod_" + handle,
		Handle:       handle,
		Title:        "Filter " + handle,
		Status:       models.StatusPublished,
		CollectionID: &f.collectionID,
	})
	require.NoError(t, err)
	return product.ID
}

// assign binds a product to a sales channel.
//
// The row is written directly rather than through core/link, because what is
// under test is the SQL that reads this table and the fixture must not depend
// on the link service agreeing with it. The table name comes from the
// repository's own [repository.SalesChannelLinkTable] constant, so a rename
// cannot leave this file writing into a table nobody reads.
func (f filterFixture) assign(t *testing.T, productID, channelID string) {
	t.Helper()

	_, err := testPool.Pool().Exec(context.Background(),
		`INSERT INTO `+repository.SalesChannelLinkTable+` (from_id, to_id) VALUES ($1, $2)`,
		productID, channelID)
	require.NoError(t, err)
}

// filter produces a filter that is scoped to this fixture's collection.
//
// Every case starts from here, which is what keeps the assertions independent
// of whatever else the shared database holds.
func (f filterFixture) filter() repository.ProductFilter {
	collection := f.collectionID
	return repository.ProductFilter{CollectionID: &collection, Limit: 100}
}

// ids runs the listing and the count and returns the ids the listing produced.
//
// The count is asserted against the listing's length on EVERY case rather than
// in a test of its own. The two statements share one filter body, and the fault
// that sharing prevents — a count over a different set from the page — is a
// pagination envelope that sends the client after pages which never fill. A
// separate test would exercise the agreement for one filter; asserting it here
// exercises it for all of them.
func (f filterFixture) ids(t *testing.T, filter repository.ProductFilter) []string {
	t.Helper()
	ctx := context.Background()

	products, err := f.repo.ListProducts(ctx, filter)
	require.NoError(t, err)

	count, err := f.repo.CountProducts(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, len(products), count,
		"the count and the listing must answer the same question")

	ids := make([]string, 0, len(products))
	for _, p := range products {
		ids = append(ids, p.ID)
	}
	return ids
}

// TestEachCriterionAloneSelectsItsOwnSet is the "each filter alone" claim.
func TestEachCriterionAloneSelectsItsOwnSet(t *testing.T) {
	fx := newFilterFixture(t)

	t.Run("none at all", func(t *testing.T) {
		assert.ElementsMatch(t, []string{fx.both, fx.catOnly, fx.tagOnly, fx.plain},
			fx.ids(t, fx.filter()))
	})

	t.Run("category", func(t *testing.T) {
		f := fx.filter()
		f.CategoryID = &fx.categoryID
		assert.ElementsMatch(t, []string{fx.both, fx.catOnly}, fx.ids(t, f))
	})

	t.Run("tag", func(t *testing.T) {
		f := fx.filter()
		f.TagID = &fx.tagID
		assert.ElementsMatch(t, []string{fx.both, fx.tagOnly}, fx.ids(t, f))
	})

	t.Run("sales channel", func(t *testing.T) {
		f := fx.filter()
		f.SalesChannelIDs = []string{fx.channelA}
		// tagOnly is assigned to channel B and is therefore hidden; plain and
		// catOnly have no assignment at all and are visible everywhere.
		assert.ElementsMatch(t, []string{fx.both, fx.catOnly, fx.plain}, fx.ids(t, f))
	})

	t.Run("handle", func(t *testing.T) {
		f := fx.filter()
		handle := handleOf(t, fx, fx.plain)
		f.Handle = &handle
		assert.Equal(t, []string{fx.plain}, fx.ids(t, f))
	})

	t.Run("search", func(t *testing.T) {
		f := fx.filter()
		term := handleOf(t, fx, fx.tagOnly)
		f.Search = &term
		assert.Equal(t, []string{fx.tagOnly}, fx.ids(t, f))
	})

	t.Run("status", func(t *testing.T) {
		f := fx.filter()
		draft := string(models.StatusDraft)
		f.Status = &draft
		assert.Empty(t, fx.ids(t, f), "the fixture holds no draft product")

		published := string(models.StatusPublished)
		f.Status = &published
		assert.Len(t, fx.ids(t, f), 4)
	})
}

// TestSeveralCriteriaTogetherIntersect is the "several together" claim.
//
// The intersection is what a conditionally built body can get wrong in a way a
// single criterion cannot: the clauses are written in a fixed order but their
// PARAMETER NUMBERS depend on which of the earlier ones were written, so a
// combination can be wrong while each of its members alone is right.
func TestSeveralCriteriaTogetherIntersect(t *testing.T) {
	fx := newFilterFixture(t)

	t.Run("category and tag", func(t *testing.T) {
		f := fx.filter()
		f.CategoryID = &fx.categoryID
		f.TagID = &fx.tagID
		assert.Equal(t, []string{fx.both}, fx.ids(t, f))
	})

	t.Run("category and channel", func(t *testing.T) {
		f := fx.filter()
		f.CategoryID = &fx.categoryID
		f.SalesChannelIDs = []string{fx.channelA}
		assert.ElementsMatch(t, []string{fx.both, fx.catOnly}, fx.ids(t, f))
	})

	t.Run("tag and the foreign channel", func(t *testing.T) {
		f := fx.filter()
		f.TagID = &fx.tagID
		f.SalesChannelIDs = []string{fx.channelA}
		// tagOnly carries the tag but is sold in channel B only.
		assert.Equal(t, []string{fx.both}, fx.ids(t, f))
	})

	t.Run("all seven", func(t *testing.T) {
		f := fx.filter()
		published := string(models.StatusPublished)
		handle := handleOf(t, fx, fx.both)
		f.Status = &published
		f.Handle = &handle
		f.Search = &handle
		f.CategoryID = &fx.categoryID
		f.TagID = &fx.tagID
		f.SalesChannelIDs = []string{fx.channelA}
		assert.Equal(t, []string{fx.both}, fx.ids(t, f))
	})
}

// TestAnEmptyFilterIsValidSQLOverTheWholeCatalog covers the one shape the
// fixture's collection scope hides.
//
// With no criterion at all the body is the single line "WHERE deleted_at IS
// NULL" and nothing else. That is the shortest statement the builder can
// produce and therefore the one where a stray "AND" or a dangling placeholder
// would show; it is also the admin listing's own shape.
func TestAnEmptyFilterIsValidSQLOverTheWholeCatalog(t *testing.T) {
	fx := newFilterFixture(t)
	ctx := context.Background()

	products, err := fx.repo.ListProducts(ctx, repository.ProductFilter{Limit: 5})
	require.NoError(t, err)
	assert.Len(t, products, 5, "the catalog holds at least the fixture's four products")

	count, err := fx.repo.CountProducts(ctx, repository.ProductFilter{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 4)
}

// TestAnEmptyChannelSliceIsNotTheSameAsNoChannel pins the distinction
// [repository.ProductFilter]'s own documentation calls load-bearing.
//
// nil means "the request carries no channel id, do not filter". An empty but
// non-nil slice means "there is an identity and it has no channel at all", and
// it must keep only the products with no assignment. A builder that decided on
// emptiness rather than on nil-ness would drop the clause here and hand that
// identity the whole catalog.
func TestAnEmptyChannelSliceIsNotTheSameAsNoChannel(t *testing.T) {
	fx := newFilterFixture(t)

	f := fx.filter()
	f.SalesChannelIDs = []string{}
	assert.ElementsMatch(t, []string{fx.catOnly, fx.plain}, fx.ids(t, f),
		"an empty channel list must leave only the unassigned products")

	f.SalesChannelIDs = nil
	assert.Len(t, fx.ids(t, f), 4, "a nil channel list must not filter at all")
}

// handleOf reads a product's handle back out of the database.
//
// The handles are generated inside [filterFixture.seed] and the tests need them
// for the handle and "q" criteria; reading them back is shorter than carrying a
// second map, and it also asserts the product is really there.
func handleOf(t *testing.T, fx filterFixture, id string) string {
	t.Helper()

	product, err := fx.repo.GetProduct(context.Background(), id)
	require.NoError(t, err)
	return product.Handle
}
