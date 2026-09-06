package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The tests in this file pin the four deletes that did not exist until
// 2026-09-06.
//
// What was wrong is worth stating once, because it is not a missing feature but
// a schema that lied: product_collection, product_category, product_tag and
// product_option_value have each carried a deleted_at and a partial unique
// index built to free their key on delete since the very first migration, every
// read has filtered on that column, and NOTHING had ever written it. The column
// audit found all four on the day it started binding a write to its own table
// (docs/gaps.md D18).
//
// The claims that belong to the DATABASE — the freed handle, the join that
// hides a deleted tag from its products — are proven in the module's
// integration tests. What is proven here is what the SERVICE decides: which
// deletes are refused and what else each one touches.

// TestDeletingACollectionReleasesItsProducts pins the one side effect a
// taxonomy delete has.
//
// The column says "ON DELETE SET NULL" and a soft delete never triggers it, so
// without this the products would keep pointing at a collection nobody can see
// — and the storefront listing, which filters by collection_id without joining
// the collection, would keep serving them under it.
func TestDeletingACollectionReleasesItsProducts(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	collection, err := svc.CreateCollection(ctx, service.CreateCollectionInput{Title: "Summer 2026"})
	require.NoError(t, err)

	inside, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:        "Linen shirt",
		CollectionID: &collection.ID,
	})
	require.NoError(t, err)
	outside, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Wool coat"})
	require.NoError(t, err)

	require.NoError(t, svc.DeleteCollection(ctx, collection.ID))

	_, err = svc.GetCollection(ctx, collection.ID)
	assert.True(t, errors.IsNotFound(err), "a deleted collection must not be readable: %v", err)

	page, err := svc.ListCollections(ctx, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Items, "a deleted collection must not be listed")
	require.NotNil(t, page.Count)
	assert.Zero(t, *page.Count, "the count must apply the same predicate as the listing")

	released, err := svc.GetProduct(ctx, inside.ID)
	require.NoError(t, err)
	assert.Nil(t, released.CollectionID, "the product must be released from the deleted collection")

	untouched, err := svc.GetProduct(ctx, outside.ID)
	require.NoError(t, err)
	assert.Nil(t, untouched.CollectionID, "a product of another collection must not be touched")
}

// TestDeletingAnUnknownCollectionTouchesNoProduct verifies that the delete
// stops at the id check.
//
// The order inside the transaction is what this pins: the collection's own
// delete runs FIRST, so a wrong id can never reach the statement that clears
// products.
func TestDeletingAnUnknownCollectionTouchesNoProduct(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	err := svc.DeleteCollection(ctx, "pcol_missing")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "a not found was expected: %v", err)
	assert.Zero(t, store.callCount("ClearCollectionProducts"),
		"an unknown id must not reach the statement that changes products")
}

// TestACategoryWithSubcategoriesIsRefused is the guard that keeps a subtree
// from disappearing.
//
// Deleting a parent leaves its children pointing at a dead node: the tree is
// walked downward by parent_id, so the whole subtree falls out of every listing
// while its rows stay live — invisible and unreachable.
func TestACategoryWithSubcategoriesIsRefused(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{Name: "Clothing"})
	require.NoError(t, err)
	child, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name:     "Shirts",
		ParentID: &parent.ID,
	})
	require.NoError(t, err)

	err = svc.DeleteCategory(ctx, parent.ID)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict was expected: %v", err)

	// The refusal must leave the parent exactly where it was; a transaction
	// that stamped the row and then reported a conflict would delete the
	// category it refused to delete.
	alive, err := svc.GetCategory(ctx, parent.ID)
	require.NoError(t, err, "the refused category must still be readable")
	assert.Nil(t, alive.DeletedAt)

	// With the child gone the same call succeeds: the guard is about the
	// children, not about the node.
	require.NoError(t, svc.DeleteCategory(ctx, child.ID))
	require.NoError(t, svc.DeleteCategory(ctx, parent.ID))

	_, err = svc.GetCategory(ctx, parent.ID)
	assert.True(t, errors.IsNotFound(err), "a deleted category must not be readable: %v", err)
}

// TestDeletingATagLeavesItsProductsAlone states the difference between a tag
// and the other two.
//
// A tag is a label: no guard, no side effect, and the products that carried it
// are not written to at all — the read simply stops printing it.
func TestDeletingATagLeavesItsProductsAlone(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	tag, err := svc.CreateTag(ctx, "sumemr")
	require.NoError(t, err)

	tagged, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Linen shirt",
		TagIDs: []string{tag.ID},
	})
	require.NoError(t, err)

	before := store.callCount("SetProductTags")
	require.NoError(t, svc.DeleteTag(ctx, tag.ID))
	assert.Equal(t, before, store.callCount("SetProductTags"),
		"deleting a tag must not rewrite the products that carried it")

	page, err := svc.ListTags(ctx, 50, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Items, "a deleted tag must not be listed")
	require.NotNil(t, page.Count)
	assert.Zero(t, *page.Count, "the count must apply the same predicate as the listing")

	_, err = svc.GetProduct(ctx, tagged.ID)
	require.NoError(t, err, "the product must survive the deletion of its tag")

	// The value is free again — which is the whole purpose of the partial
	// unique index this module builds on every handle.
	again, err := svc.CreateTag(ctx, "sumemr")
	require.NoError(t, err, "a deleted tag's value must be reusable")
	assert.NotEqual(t, tag.ID, again.ID)
}

// TestAnOptionValueAVariantCarriesCannotBeDeleted is the guard that keeps two
// variants from becoming indistinguishable.
//
// Every read of a variant's option values joins the value with
// "deleted_at IS NULL", so deleting one in use does not fail — it makes the
// variant show fewer options than it has.
func TestAnOptionValueAVariantCarriesCannotBeDeleted(t *testing.T) {
	t.Parallel()

	svc := newService(t, newMemStore(), newFakeLinker(), nil)
	ctx := context.Background()

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title: "T-shirt",
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M"}},
		},
		Variants: []service.CreateVariantInput{
			{Title: "Small", Options: map[string]string{"Size": "S"}},
		},
	})
	require.NoError(t, err)

	options, err := svc.ListOptions(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, options, 1)
	require.Len(t, options[0].Values, 2)

	var inUse, unused string
	for _, value := range options[0].Values {
		if value.Value == "S" {
			inUse = value.ID
		} else {
			unused = value.ID
		}
	}
	require.NotEmpty(t, inUse)
	require.NotEmpty(t, unused)

	err = svc.DeleteOptionValue(ctx, inUse)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "a conflict was expected: %v", err)

	// The value nothing carries goes without argument.
	require.NoError(t, svc.DeleteOptionValue(ctx, unused))

	after, err := svc.ListOptions(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Len(t, after[0].Values, 1, "the deleted value must fall out of the read")
	assert.Equal(t, inUse, after[0].Values[0].ID)

	// Once the variant carrying it is gone the refusal lifts: the count is of
	// LIVING variants, not of binding rows.
	require.NoError(t, svc.DeleteVariant(ctx, created.Variants[0].ID))
	require.NoError(t, svc.DeleteOptionValue(ctx, inUse),
		"a value whose only carrier was deleted must be deletable")
}

// TestDeletingAnOptionDeletesItsValues pins the cascade that was missing.
//
// It changes nothing a caller can see — the option is gone from every read
// either way — and that is exactly why it went unnoticed until a column audit
// asked who writes product_option_value.deleted_at.
func TestDeletingAnOptionDeletesItsValues(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title: "T-shirt",
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M"}},
			{Title: "Color", Values: []string{"Red"}},
		},
	})
	require.NoError(t, err)

	options, err := svc.ListOptions(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, options, 2)

	var sizeID string
	for _, option := range options {
		if option.Title == "Size" {
			sizeID = option.ID
		}
	}
	require.NotEmpty(t, sizeID)

	require.NoError(t, svc.DeleteOption(ctx, sizeID))
	assert.Zero(t, store.liveOptionValues(sizeID),
		"the deleted option must leave no living value under it")
	assert.Positive(t, store.liveOptionValues(""),
		"the other option's values must be untouched")
}

// TestDeletingAProductDeletesItsOptionValues is the same claim for the delete
// that reaches every child at once.
func TestDeletingAProductDeletesItsOptionValues(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)
	ctx := context.Background()

	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title: "T-shirt",
		Options: []service.CreateOptionInput{
			{Title: "Size", Values: []string{"S", "M"}},
		},
	})
	require.NoError(t, err)
	require.Positive(t, store.liveOptionValues(""))

	require.NoError(t, svc.DeleteProduct(ctx, created.ID))
	assert.Zero(t, store.liveOptionValues(""),
		"a deleted product must leave no living option value behind")
}
