//go:build integration

package product_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheProductRecordCarriesItsCategoryTree is ADR 0259 on the real schema:
// a product filed under "Linen" and "Shirts" of "Apparel > Shirts > Linen"
// publishes the three once each, the nearest first, while `category_ids`
// keeps the two it was filed under; a product in no category publishes an
// empty tree.
func TestTheProductRecordCarriesItsCategoryTree(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	provider := service.NewProductProvider(repository.New(testPool.Pool()))

	apparel, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Apparel", Handle: uniqueHandle("tree-apparel")})
	require.NoError(t, err)
	shirts, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Shirts", Handle: uniqueHandle("tree-shirts"), ParentID: &apparel.ID})
	require.NoError(t, err)
	linen, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Linen", Handle: uniqueHandle("tree-linen"), ParentID: &shirts.ID})
	require.NoError(t, err)

	filed, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("tree-filed"), Title: "Linen shirt", Status: models.StatusPublished,
		CategoryIDs: []string{linen.ID, shirts.ID},
	})
	require.NoError(t, err)
	unfiled, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("tree-unfiled"), Title: "Loose", Status: models.StatusPublished,
	})
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{filed.ID, unfiled.ID},
		[]string{query.IDField, "category_ids", service.FieldCategoryTreeIDs})
	require.NoError(t, err)
	require.Len(t, records, 2)
	byID := map[string]query.Record{}
	for _, record := range records {
		id, ok := record[query.IDField].(string)
		require.True(t, ok, "a record carries its id as text")
		byID[id] = record
	}

	direct, ok := byID[filed.ID]["category_ids"].([]string)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{linen.ID, shirts.ID}, direct, "the direct memberships are unchanged")

	// The tree follows the direct memberships in the order the record gives
	// them, each followed by its ancestors, nearest first, every id once.
	lineage := map[string][]string{
		linen.ID:  {linen.ID, shirts.ID, apparel.ID},
		shirts.ID: {shirts.ID, apparel.ID},
	}
	want := []string{}
	for _, id := range direct {
		for _, ancestor := range lineage[id] {
			if !slices.Contains(want, ancestor) {
				want = append(want, ancestor)
			}
		}
	}
	assert.Equal(t, want, byID[filed.ID][service.FieldCategoryTreeIDs],
		"the categories and their ancestors, each once, the nearest first")
	assert.Len(t, want, 3)
	assert.Equal(t, []string{}, byID[unfiled.ID][service.FieldCategoryTreeIDs])
}

// TestTheReadLayerListsACategoryWithItsSubcategories is ADR 0282 on the real
// schema: the product provider's category_tree_id keeps a product filed only
// under a subcategory, as the storefront's does, while category_id does not.
func TestTheReadLayerListsACategoryWithItsSubcategories(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	provider := service.NewProductProvider(repository.New(testPool.Pool()))

	parent, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Outdoor", Handle: uniqueHandle("reads-outdoor")})
	require.NoError(t, err)
	child, err := svc.CreateCategory(ctx, service.CreateCategoryInput{
		Name: "Tents", Handle: uniqueHandle("reads-tents"), ParentID: &parent.ID})
	require.NoError(t, err)
	tent, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("reads-tent"), Title: "Tent", Status: models.StatusPublished,
		CategoryIDs: []string{child.ID},
	})
	require.NoError(t, err)

	tree, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_tree_id": parent.ID}, Fields: []string{query.IDField},
	})
	require.NoError(t, err)
	require.Len(t, tree, 1)
	assert.Equal(t, tent.ID, tree[0][query.IDField])

	direct, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"category_id": parent.ID}, Fields: []string{query.IDField},
	})
	require.NoError(t, err)
	assert.Empty(t, direct, "category_id is the category alone")
}
