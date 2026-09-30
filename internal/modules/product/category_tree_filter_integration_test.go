//go:build integration

package product_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// TestTheTreeFilterListsWhatTheTreeIDsName is ADR 0261's equivalence on the
// real schema: for every category of a tree, a deleted one, one below the
// deleted one and one that does not exist, the storefront listing filtered by
// category_tree_id holds exactly the products whose record's category_tree_ids
// (ADR 0259) name it, each once, and the count agrees.
//
// The expected set is read off the records rather than written here, so the
// two walks, up for the record and down for the filter, are held to each other
// and not to my reading of either.
func TestTheTreeFilterListsWhatTheTreeIDsName(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	provider := service.NewProductProvider(repository.New(testPool.Pool()))

	category := func(name string, parent *models.Category) models.Category {
		in := service.CreateCategoryInput{Name: name, Handle: uniqueHandle("subtree-" + name)}
		if parent != nil {
			in.ParentID = &parent.ID
		}
		created, err := svc.CreateCategory(ctx, in)
		require.NoError(t, err)
		return created
	}
	root := category("root", nil)
	mid := category("mid", &root)
	leaf := category("leaf", &mid)
	side := category("side", &root)
	gone := category("gone", &mid)
	below := category("below", &gone)
	// The service refuses to delete a category that has children, and the
	// schema does not: the row is written as a deleted parent is left by any
	// other path, and the walk has to stop at it both ways.
	_, err := testPool.Pool().Exec(ctx, `UPDATE product_category SET deleted_at = now() WHERE id = $1`, gone.ID)
	require.NoError(t, err)

	product := func(name string, categories ...models.Category) string {
		ids := make([]string, 0, len(categories))
		for i := range categories {
			ids = append(ids, categories[i].ID)
		}
		created, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Handle: uniqueHandle("subtree-" + name), Title: name, Status: models.StatusPublished,
			CategoryIDs: ids,
		})
		require.NoError(t, err)
		return created.ID
	}
	products := []string{
		product("in-leaf", leaf),
		product("in-side-and-leaf", side, leaf),
		product("in-root", root),
		product("in-below", below),
		product("loose"),
	}

	records, err := provider.FetchByIDs(ctx, products, []string{query.IDField, service.FieldCategoryTreeIDs})
	require.NoError(t, err)
	require.Len(t, records, len(products))

	named := func(categoryID string) []string {
		out := []string{}
		for _, record := range records {
			tree, ok := record[service.FieldCategoryTreeIDs].([]string)
			require.True(t, ok, "a record carries its tree as a list of ids")
			if slices.Contains(tree, categoryID) {
				id, _ := record[query.IDField].(string)
				out = append(out, id)
			}
		}
		slices.Sort(out)
		return out
	}

	walked := 0
	for name, id := range map[string]string{
		"the root": root.ID, "a middle category": mid.ID, "a leaf": leaf.ID, "a sibling": side.ID,
		"a deleted category": gone.ID, "a category under a deleted one": below.ID,
		"a category that does not exist": "pcat_" + uniqueHandle("absent"),
	} {
		t.Run(name, func(t *testing.T) {
			result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{CategoryTreeID: &id, Limit: 100})
			require.NoError(t, err)

			listed := make([]string, 0, len(result.Items))
			for i := range result.Items {
				listed = append(listed, result.Items[i].ID)
			}
			slices.Sort(listed)
			assert.Equal(t, named(id), listed, "the listing and the records disagree about %s", name)
			require.NotNil(t, result.Count)
			assert.Equal(t, len(listed), *result.Count, "the count answers the listing's question")
			walked += len(listed)
		})
	}

	// The fixture is not vacuous: the root holds three products, one of them
	// filed twice inside it and listed once, and the one under the deleted
	// category is in no tree above it.
	assert.Len(t, named(root.ID), 3)
	assert.Equal(t, []string{products[3]}, named(below.ID))
	assert.Positive(t, walked)
}

// TestTheTreeFilterStopsWhereTheLineageStops holds the two walks to the same
// bound: on a chain deeper than sixty-four levels, a product at the bottom is
// in the listing of exactly the ancestors its record names, so the ancestor
// sixty-four levels up lists it and the one sixty-five up does not.
func TestTheTreeFilterStopsWhereTheLineageStops(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	provider := service.NewProductProvider(repository.New(testPool.Pool()))

	// The chain is written directly: the service would walk sixty-six levels
	// of ancestry on every create to learn what this test already knows.
	chain := make([]string, 67)
	for i := range chain {
		chain[i] = "pcat_" + uniqueHandle("chain")
		var parent *string
		if i > 0 {
			parent = &chain[i-1]
		}
		_, err := testPool.Pool().Exec(ctx,
			`INSERT INTO product_category (id, name, handle, parent_id) VALUES ($1, $1, $1, $2)`, chain[i], parent)
		require.NoError(t, err)
	}
	bottom := chain[len(chain)-1]
	created, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Handle: uniqueHandle("chain-bottom"), Title: "Bottom", Status: models.StatusPublished,
		CategoryIDs: []string{bottom},
	})
	require.NoError(t, err)

	records, err := provider.FetchByIDs(ctx, []string{created.ID}, []string{query.IDField, service.FieldCategoryTreeIDs})
	require.NoError(t, err)
	require.Len(t, records, 1)
	tree, ok := records[0][service.FieldCategoryTreeIDs].([]string)
	require.True(t, ok)

	for _, up := range []int{63, 64, 65, 66} {
		ancestor := chain[len(chain)-1-up]
		result, err := svc.ListStoreProducts(ctx, service.StoreListOptions{CategoryTreeID: &ancestor, Limit: 10})
		require.NoError(t, err)
		listed := len(result.Items) == 1 && result.Items[0].ID == created.ID
		assert.Equal(t, slices.Contains(tree, ancestor), listed,
			"%d levels up: the record and the listing disagree", up)
		assert.Equal(t, up <= 64, listed, "%d levels up", up)
	}
}

// TestATreeStatementIsPlannedForItsOwnIDs holds the three statements that
// carry the tree filter to a plan made for the ids they are given (ADR 0261):
// none of them leaves a named statement on the connection, so PostgreSQL has
// no statement to cache a generic plan for.
//
// The single category's listing is run first on the same connection and has
// to leave one, or the probe could not tell a statement it cannot see from
// one that is not there.
func TestATreeStatementIsPlannedForItsOwnIDs(t *testing.T) {
	ctx := context.Background()

	config := db.DefaultConfig(testDSN)
	config.MaxConns, config.MinConns = 1, 0
	pool, err := db.New(ctx, config, nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	repo := repository.New(pool.Pool())

	named := func() []string {
		rows, err := pool.Pool().Query(ctx, `SELECT statement FROM pg_prepared_statements`)
		require.NoError(t, err)
		defer rows.Close()
		var out []string
		for rows.Next() {
			var statement string
			require.NoError(t, rows.Scan(&statement))
			out = append(out, statement)
		}
		require.NoError(t, rows.Err())
		return out
	}
	holding := func(fragment string) int {
		count := 0
		for _, statement := range named() {
			if strings.Contains(statement, fragment) {
				count++
			}
		}
		return count
	}

	category := "pcat_" + uniqueHandle("planned")
	for range 8 {
		_, err := repo.ListProducts(ctx, repository.ProductFilter{CategoryID: &category, Limit: 20})
		require.NoError(t, err)
	}
	require.Positive(t, holding("category_id = $"),
		"the single category's listing left no named statement, so the probe cannot see one")

	tree := repository.ProductFilter{CategoryTreeIDs: []string{category}, Limit: 20, SalesChannelIDs: []string{"sc_planned"}}
	for range 8 {
		_, err := repo.ListProducts(ctx, tree)
		require.NoError(t, err)
		_, err = repo.CountProducts(ctx, tree)
		require.NoError(t, err)
		_, err = repo.AttributeFacets(ctx, tree, []string{"pattr_planned"})
		require.NoError(t, err)
	}
	assert.Zero(t, holding("category_id = ANY("),
		"a statement carrying the tree filter was prepared by name, and after five runs "+
			"PostgreSQL may keep a generic plan for it that cannot see how large the subtree is")
}
