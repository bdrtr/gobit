//go:build integration

// The tests in this file need a real PostgreSQL instance (and therefore
// Docker); they run behind the `integration` tag with the rest. To run them:
// make test-integration
//
// What is proven here is the STOREFRONT'S VOCABULARY: the collection, tag and
// category reads a shop uses to turn the word a shopper clicked into a catalog
// filter. Until this file existed, ListCollections, ListTags, CountCollections,
// CountTags, CountChildCategories, GetCollectionByHandle and GetCategoryByHandle
// had never been executed by any test at all.
//
// Every claim below lives in a WHERE clause or an ORDER BY, that is, in SQL and
// nowhere else. The service layer's in-memory store (memstore_test.go) answers
// all of these questions in Go, and it would keep answering them correctly on
// the day the predicate fell out of the statement — which is the one failure a
// unit test here could not see.
package product_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// newIsolatedService builds a service on a database of ITS OWN.
//
// The vocabulary counters (CountCollections, CountTags) count the WHOLE table:
// there is no filter to narrow them to the rows one test wrote. On the shared
// database every other test's leftovers are inside the number, so "the deleted
// row is not counted" could only be asserted as a difference between two
// readings — and a difference is exactly what stays right when the predicate is
// removed, because both readings move together. The claim is only worth making
// against a table whose contents the test knows completely.
func newIsolatedService(ctx context.Context, t *testing.T) *service.Service {
	t.Helper()

	dsn := newDatabase(ctx, t)
	mod := product.New(product.Options{})
	require.NoError(t, db.Migrate(ctx, dsn, mod.Migrations(), mod.Name()),
		"the product schema has to be applied to the test's own database")

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	svc, err := service.New(service.Options{Repo: repository.New(pool.Pool())})
	require.NoError(t, err)
	return svc
}

// TestADeletedCollectionLeavesBothTheListingAndTheCount verifies that a
// soft-deleted collection disappears from the storefront's collection
// vocabulary AND from its counter.
//
// The two halves are one claim, not two. A listing that hides the row while the
// counter still counts it is worse than either mistake on its own: the client
// is told there are three collections, receives two, and pages forward looking
// for a third that no read will ever return. That is a shop menu with a page
// button that leads nowhere, and no error is raised anywhere along the way.
//
// The delete is a soft delete, so the row is physically still in the table and
// the only thing separating it from the live ones is the "deleted_at IS NULL"
// in the two statements. Nothing but a real database can show that both
// statements carry it.
func TestADeletedCollectionLeavesBothTheListingAndTheCount(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	var ids []string
	for _, title := range []string{"summer", "winter", "outlet"} {
		collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
			Title:  title,
			Handle: uniqueHandle(title),
		})
		require.NoError(t, err)
		ids = append(ids, collection.ID)
	}

	require.NoError(t, svc.DeleteCollection(ctx, ids[1]))

	result, err := svc.ListCollections(ctx, 50, 0)
	require.NoError(t, err)

	require.NotNil(t, result.Count, "the collection listing always counts")
	assert.Equal(t, 2, *result.Count,
		"the counter must not count the deleted collection; a count larger than the page sends "+
			"the client after a page that will never arrive")
	require.Len(t, result.Items, 2, "the deleted collection must not be in the page")

	listed := make([]string, 0, len(result.Items))
	for _, collection := range result.Items {
		listed = append(listed, collection.ID)
	}
	assert.NotContains(t, listed, ids[1], "the deleted collection must not be listed")
	assert.Contains(t, listed, ids[0])
	assert.Contains(t, listed, ids[2])
}

// TestADeletedTagLeavesBothTheListingAndTheCount verifies the same for the tag
// vocabulary.
//
// It is not a copy of the collection test: tags are a separate table with a
// separate pair of statements, and the reason both are written out is that this
// is precisely the kind of predicate that gets added to one of a group and
// forgotten on the others. A tag also outlives its own deletion in a way a
// collection does not — the product_tag_map bindings stay — so a listing that
// stopped hiding the row would put a deleted tag back on the storefront with
// products still hanging off it.
func TestADeletedTagLeavesBothTheListingAndTheCount(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	kept, err := svc.CreateTag(ctx, "sale")
	require.NoError(t, err)
	removed, err := svc.CreateTag(ctx, "clearance")
	require.NoError(t, err)

	require.NoError(t, svc.DeleteTag(ctx, removed.ID))

	result, err := svc.ListTags(ctx, 50, 0)
	require.NoError(t, err)

	require.NotNil(t, result.Count, "the tag listing always counts")
	assert.Equal(t, 1, *result.Count, "the deleted tag must not be counted")
	require.Len(t, result.Items, 1, "the deleted tag must not be listed")
	assert.Equal(t, kept.ID, result.Items[0].ID)
}

// TestTheTagVocabularyIsOrderedByValueNotByCreation verifies that the tag
// listing comes back in ALPHABETICAL order.
//
// A tag list is a vocabulary shown to a shopper, and the order is the only
// thing that makes a long one usable: "amber, blue, crimson" can be scanned,
// and the same three in the order the merchant happened to type them cannot.
// Creation order is what an ORDER BY dropped from the statement falls back to
// in practice, and it is invisible in any test that writes its rows in
// alphabetical order to begin with — so the rows here are created in an order
// that is deliberately NOT the expected one.
func TestTheTagVocabularyIsOrderedByValueNotByCreation(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	for _, value := range []string{"pear", "apple", "mango"} {
		_, err := svc.CreateTag(ctx, value)
		require.NoError(t, err)
	}

	result, err := svc.ListTags(ctx, 50, 0)
	require.NoError(t, err)

	values := make([]string, 0, len(result.Items))
	for _, tag := range result.Items {
		values = append(values, tag.Value)
	}
	assert.Equal(t, []string{"apple", "mango", "pear"}, values,
		"the tag vocabulary has to be alphabetical; in creation order a shopper cannot find a word in it")
}

// TestAParentCategoryBecomesDeletableOnceItsChildrenAre verifies that the child
// count behind the category delete counts only the LIVE children.
//
// The refusal itself is the rule the service states: a category with children
// cannot be deleted, because the children keep pointing at a node that is gone
// and the whole subtree disappears from every listing while its rows stay live.
// What is proven here is the OTHER half, the half that only the database can
// answer: after the child is deleted the parent becomes deletable again. Had
// CountChildCategories forgotten its "deleted_at IS NULL", the count would
// never fall back to zero and a parent whose children were all removed would be
// undeletable FOREVER — with an error message naming subcategories the merchant
// cannot see anywhere in the panel.
func TestAParentCategoryBecomesDeletableOnceItsChildrenAre(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name:   "Clothing",
		Handle: uniqueHandle("clothing"),
	})
	require.NoError(t, err)

	child, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name:     "Shirts",
		Handle:   uniqueHandle("shirts"),
		ParentID: &parent.ID,
	})
	require.NoError(t, err)

	err = svc.DeleteCategory(ctx, parent.ID)
	require.Error(t, err, "a category with a live child must not be deleted")
	assert.True(t, coreerrors.IsConflict(err),
		"the refusal is a conflict, not a validation error: the request is well formed and the "+
			"tree is what refuses it (got %v)", err)

	require.NoError(t, svc.DeleteCategory(ctx, child.ID))

	assert.NoError(t, svc.DeleteCategory(ctx, parent.ID),
		"once the child is deleted the parent has to become deletable; a child count that also "+
			"counts deleted rows would lock the parent in place forever")
}

// TestTheByHandleReadsDoNotSeeADeletedRow verifies that the two by-handle
// lookups hide soft-deleted rows.
//
// # These two queries have NO Go caller
//
// GetCollectionByHandle and GetCategoryByHandle are two of the nine SELECTs in
// the tree that nothing calls (the count is recorded in
// internal/arch/columns_test.go). They are exercised here through the generated
// package directly, which no other test in this module does, and that is a
// deliberate exception rather than a pattern: the queries exist for the
// storefront's handle addressing ("/collections/summer"), the caller has not
// been written yet, and a statement nobody has ever run is a statement nobody
// knows the behavior of.
//
// The claim is the one the handle promise rests on. The unique index on the
// handle is PARTIAL — it only covers live rows — so deleting a collection frees
// its handle for a new one. That promise is only kept if the READ hides the
// dead row too: with the filter gone, a new collection created under a freed
// handle would resolve to the DELETED row, and the storefront address would
// point at a collection the merchant deleted rather than the one they just
// made.
func TestTheByHandleReadsDoNotSeeADeletedRow(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	queries := productdb.New(testPool.Pool())

	collectionHandle := uniqueHandle("summer")
	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{
		Title:  "Summer",
		Handle: collectionHandle,
	})
	require.NoError(t, err)

	row, err := queries.GetCollectionByHandle(ctx, collectionHandle)
	require.NoError(t, err, "a live collection has to be reachable by its handle")
	assert.Equal(t, collection.ID, row.ID)

	require.NoError(t, svc.DeleteCollection(ctx, collection.ID))

	_, err = queries.GetCollectionByHandle(ctx, collectionHandle)
	require.Error(t, err,
		"a deleted collection must not answer to its handle; the handle is freed for a new "+
			"collection and would otherwise resolve to the deleted one")

	categoryHandle := uniqueHandle("shirts")
	category, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name:   "Shirts",
		Handle: categoryHandle,
	})
	require.NoError(t, err)

	categoryRow, err := queries.GetCategoryByHandle(ctx, categoryHandle)
	require.NoError(t, err, "a live category has to be reachable by its handle")
	assert.Equal(t, category.ID, categoryRow.ID)

	require.NoError(t, svc.DeleteCategory(ctx, category.ID))

	_, err = queries.GetCategoryByHandle(ctx, categoryHandle)
	require.Error(t, err, "a deleted category must not answer to its handle")
}

// TestACategorySwitchedOffCanBeSwitchedBackOn is the defect this write was
// missing for.
//
// Measured 2026-09-09: the only UPDATE on product_category in the whole tree was
// the soft delete, is_active was written by the INSERT and by nothing else, and
// the admin surface bound POST, GET and DELETE. So a category created with
// is_active false stayed off for the life of the installation — while the
// listing's own godoc said the admin surface "is the only way the merchant can
// turn a category back on", describing a write that did not exist.
//
// The round trip is asserted through the STOREFRONT's own predicate, because
// that is where the flag is read: a category the shopper cannot see, then can.
func TestACategorySwitchedOffCanBeSwitchedBackOn(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	off := false
	category, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Winter", Handle: "winter", IsActive: &off,
	})
	require.NoError(t, err)
	require.False(t, category.IsActive)

	public, err := svc.ListCategories(ctx, service.ListCategoriesOptions{PublicOnly: true})
	require.NoError(t, err)
	assert.Empty(t, public.Items, "a switched-off category must not reach the storefront")

	on := true
	updated, err := svc.UpdateCategory(ctx, category.ID, service.UpdateCategoryInput{IsActive: &on})
	require.NoError(t, err)
	assert.True(t, updated.IsActive)

	public, err = svc.ListCategories(ctx, service.ListCategoriesOptions{PublicOnly: true})
	require.NoError(t, err)
	require.Len(t, public.Items, 1,
		"the category was switched back on and still does not reach the storefront")
	assert.Equal(t, category.ID, public.Items[0].ID)
}

// TestACategoryCannotBeMovedUnderItsOwnDescendant proves the guard that lives in
// the statement.
//
// The tree is A -> B -> C. Moving A under C would close a ring: C's ancestry
// runs C, B, A, so A would become its own descendant. Nothing in the reads
// would notice today — every category read walks ONE level — which is exactly
// why the refusal has to be written rather than assumed. The day a recursive
// read arrives it would not terminate.
func TestACategoryCannotBeMovedUnderItsOwnDescendant(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	top, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "A", Handle: "cat-a"})
	require.NoError(t, err)
	middle, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "B", Handle: "cat-b", ParentID: &top.ID,
	})
	require.NoError(t, err)
	bottom, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "C", Handle: "cat-c", ParentID: &middle.ID,
	})
	require.NoError(t, err)

	_, err = svc.UpdateCategory(ctx, top.ID, service.UpdateCategoryInput{ParentID: &bottom.ID})

	require.Error(t, err, "moving the root under its own grandchild has to be refused")
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.Equal(t, "product_category_cycle", coreerrors.CodeOf(err))

	// The refusal left the tree as it was: a guard that answered an error while
	// writing the row would be worse than no guard.
	unchanged, err := svc.GetCategory(ctx, top.ID)
	require.NoError(t, err)
	assert.Nil(t, unchanged.ParentID, "the refused move must not have been applied")

	// A category may not become its own parent either, and it is the same walk:
	// the ancestry starts AT the new parent.
	_, err = svc.UpdateCategory(ctx, middle.ID, service.UpdateCategoryInput{ParentID: &middle.ID})
	require.Error(t, err, "a category may not be its own parent")
	assert.Equal(t, "product_category_cycle", coreerrors.CodeOf(err))
}

// TestTwoConcurrentReparentsCannotCloseARingBetweenThem is why the guard is in
// the statement and not in the service.
//
// Two roots, A and B. One caller moves A under B while another moves B under A.
// Read-then-write would let both through: each reads a tree with no ring, each
// then writes, and the ring is closed by the pair rather than by either. The
// guard is inside the UPDATE, so the second writer's ancestry walk sees what the
// first one committed.
//
// The assertion is not "one failed": it is that the TREE has no ring afterwards,
// which is the property the guard exists for. Either outcome of the race is
// allowed — one wins and one is refused — and both being refused is allowed too.
func TestTwoConcurrentReparentsCannotCloseARingBetweenThem(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	first, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "A", Handle: "ring-a"})
	require.NoError(t, err)
	second, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "B", Handle: "ring-b"})
	require.NoError(t, err)

	var start sync.WaitGroup
	start.Add(1)

	var done sync.WaitGroup
	done.Add(2)

	moves := make(chan error, 2)
	for _, move := range [][2]string{{first.ID, second.ID}, {second.ID, first.ID}} {
		go func() {
			defer done.Done()
			parent := move[1]
			start.Wait()
			_, err := svc.UpdateCategory(ctx, move[0], service.UpdateCategoryInput{ParentID: &parent})
			moves <- err
		}()
	}

	start.Done()
	done.Wait()
	close(moves)

	succeeded := 0
	for err := range moves {
		if err == nil {
			succeeded++

			continue
		}
		assert.Equal(t, "product_category_cycle", coreerrors.CodeOf(err),
			"a refused move has to say WHY it was refused: %v", err)
	}
	assert.LessOrEqual(t, succeeded, 1, "both moves were applied, which is the ring itself")

	// The tree is walked by hand, because no read in this module walks it: if A
	// points at B and B points at A, neither is reachable from a root any more.
	afterFirst, err := svc.GetCategory(ctx, first.ID)
	require.NoError(t, err)
	afterSecond, err := svc.GetCategory(ctx, second.ID)
	require.NoError(t, err)

	ring := afterFirst.ParentID != nil && *afterFirst.ParentID == second.ID &&
		afterSecond.ParentID != nil && *afterSecond.ParentID == first.ID
	assert.False(t, ring,
		"the two categories point at each other: the guard did not survive the race")
}

// categoryWalkLimit is the depth the statement's ancestry walk is bounded at.
//
// It is repeated here rather than imported because the number lives in SQL and
// this test is what notices when the two stop agreeing.
const categoryWalkLimit = 64

// TestAMoveIsRefusedWhenTheAncestryIsTooDeepToVerify pins the fail-closed half.
//
// The walk up from a new parent has to be bounded, because an ancestry that
// ALREADY holds a ring would not terminate. A bound on its own would be worse
// than none: an ancestor deeper than the bound would go unseen and the ring it
// closes would be written. So the statement treats reaching the bound as a
// refusal.
//
// The cost is real and is asserted here rather than described: in a tree deeper
// than the bound, a move is refused even when it would have been legitimate.
// Sixty-four levels is far past any catalog a person maintains, which is why
// this is the side to fail on.
func TestAMoveIsRefusedWhenTheAncestryIsTooDeepToVerify(t *testing.T) {
	ctx := context.Background()
	svc := newIsolatedService(ctx, t)

	// One chain, one level deeper than the walk can see.
	var parent *string
	var deepest string
	for level := range categoryWalkLimit + 2 {
		created, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
			Name:     fmt.Sprintf("level %d", level),
			Handle:   fmt.Sprintf("deep-%d", level),
			ParentID: parent,
		})
		require.NoError(t, err)
		deepest = created.ID
		parent = &created.ID
	}

	// A LEAF of its own, with no relation to the chain: moving it under the
	// deepest node closes no ring at all, and it is refused anyway because the
	// statement cannot see far enough to say so.
	loose, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "loose", Handle: "deep-loose",
	})
	require.NoError(t, err)

	_, err = svc.UpdateCategory(ctx, loose.ID, service.UpdateCategoryInput{ParentID: &deepest})

	require.Error(t, err, "a move under an unverifiable ancestry has to be refused")
	assert.Equal(t, "product_category_cycle", coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "too deep",
		"the refusal has to say it is a DEPTH refusal, not a ring")
}
