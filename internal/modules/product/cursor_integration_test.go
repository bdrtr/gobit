//go:build integration

package product_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file proves the KEYSET pagination against a real database.
//
// Neither claim here can be made with a fake repository. Whether a walk visits
// every row exactly once depends on the row comparison Postgres does against
// the real ordering key, and whether the predicate reaches the index at all is
// visible only in a plan.

// cursorWalkSize is how many products a walk covers.
//
// Seven rows in pages of two makes the last page a partial one, which is the
// page where an off-by-one in the trim would show.
const cursorWalkSize = 7

// cursorPageSize is the page size the walk asks for.
const cursorPageSize = 2

// seedWalkProducts writes the products a walk visits and returns their ids in
// the order the listing will produce them.
//
// # Why every row is forced onto the SAME created_at
//
// The claim under test is that the id half of the ordering key keeps a page
// boundary from repeating or skipping a row. Products created one call at a
// time get distinct timestamps — each is its own round trip — so a listing over
// them would be correctly ordered by the timestamp ALONE and the id half would
// never be reached. Dropping the id from the predicate would then change
// nothing and the test would pass while proving nothing; it did, until the
// mutation showed it.
//
// Collapsing the timestamps builds the condition the claim is about: with every
// created_at equal, the id is the ONLY thing separating the rows.
func seedWalkProducts(t *testing.T, svc *service.Service, prefix string) []string {
	t.Helper()

	ctx := context.Background()
	ids := make([]string, 0, cursorWalkSize)

	for range cursorWalkSize {
		handle := uniqueHandle(prefix)
		created, err := svc.CreateProduct(ctx, service.CreateProductInput{
			Title:  handle,
			Handle: handle,
			Status: models.StatusPublished,
		})
		require.NoError(t, err)

		ids = append(ids, created.ID)
	}

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE product SET created_at = $1 WHERE id = ANY($2::text[])`, at, ids)
	require.NoError(t, err)

	// created_at is now equal for all of them, so the listing order
	// (created_at DESC, id DESC) is the ids descending.
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	slices.Reverse(sorted)

	return sorted
}

// TestACursorWalkVisitsEveryRowExactlyOnce is the claim keyset pagination is
// bought for.
//
// A page boundary landing between two rows with the same created_at is where
// this breaks: a bound on the timestamp alone would either repeat the row on
// both sides of the boundary or skip it. The products here are written in one
// loop, so several of them genuinely do land in the same microsecond — which is
// what makes the id half of the key the thing under test rather than a
// decoration.
func TestACursorWalkVisitsEveryRowExactlyOnce(t *testing.T) {
	svc := newService(t, nil, nil)
	want := seedWalkProducts(t, svc, "cursorwalk")

	seen, pages := walkByPrefix(t, svc, "cursorwalk")

	assert.Equal(t, want, seen, "the walk has to produce every row exactly once, in order")
	assert.Equal(t, 4, pages, "seven rows in pages of two is four requests: 2+2+2+1")
}

// walkByPrefix walks the listing filtered to this test's own products and
// returns the ids it saw and how many pages it took.
func walkByPrefix(t *testing.T, svc *service.Service, prefix string) (ids []string, pages int) {
	t.Helper()

	ctx := context.Background()

	var cursor page.Cursor

	for {
		pages++
		require.Less(t, pages, cursorWalkSize+3, "the walk does not terminate")

		result, err := svc.ListProducts(ctx, service.ListProductsOptions{
			Search:    &prefix,
			Limit:     cursorPageSize,
			After:     cursor,
			SkipCount: true,
		})
		require.NoError(t, err)

		for _, product := range result.Items {
			ids = append(ids, product.ID)
		}

		if result.NextCursor == "" {
			return ids, pages
		}

		cursor, err = page.Decode(service.ProductListing, result.NextCursor)
		require.NoError(t, err)
	}
}

// TestTheLastPageCarriesNoCursor is the end-of-listing signal.
//
// A cursor that always came back would make a client walk one extra request
// into an empty page before it could tell it was finished, and a client that
// stops on an empty page instead cannot tell "finished" from "a filter that
// matches nothing".
func TestTheLastPageCarriesNoCursor(t *testing.T) {
	ctx := context.Background()
	svc := newService(t, nil, nil)
	prefix := "cursorend"
	seedWalkProducts(t, svc, prefix)

	result, err := svc.ListProducts(ctx, service.ListProductsOptions{
		Search:    &prefix,
		Limit:     cursorWalkSize + 1,
		SkipCount: true,
	})
	require.NoError(t, err)
	require.Len(t, result.Items, cursorWalkSize)
	assert.Empty(t, result.NextCursor, "a page that exhausted the listing must carry no cursor")
}

// TestTheKeysetPredicateReachesTheIndex is the reason the cursor exists.
//
// A cursor that does not reach the index buys NOTHING: it walks the same rows
// offset walks and merely spells the position differently. The plan is the only
// place that says which of the two is happening, which is why it is read here
// rather than inferred from a timing.
//
// The shape being guarded is specific. Writing the bound as
// "$9 IS NULL OR (created_at, id) < (...)" produces an Index Cond for the first
// five executions of a statement and a Filter on the sixth, when Postgres
// switches to a generic plan — so a test that ran the query once would pass
// while production walked the whole index.
func TestTheKeysetPredicateReachesTheIndex(t *testing.T) {
	ctx := context.Background()

	const explain = `EXPLAIN (FORMAT TEXT)
SELECT id FROM product
WHERE deleted_at IS NULL
  AND (created_at, id) < (COALESCE($1::timestamptz, 'infinity'::timestamptz), COALESCE($2::text, ''))
ORDER BY created_at DESC, id DESC
LIMIT 10`

	rows, err := testPool.Pool().Query(ctx, explain, nil, nil)
	require.NoError(t, err)

	var plan strings.Builder

	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line)
		plan.WriteString("\n")
	}

	require.NoError(t, rows.Err())
	rows.Close()

	assert.Contains(t, plan.String(), "Index Cond",
		"the keyset bound has to become an INDEX CONDITION; as a Filter it walks every row it "+
			"was supposed to skip and the cursor buys nothing.\nplan:\n%s", plan.String())
	assert.NotContains(t, plan.String(), "Seq Scan",
		"the listing must not fall back to a sequential scan.\nplan:\n%s", plan.String())
}
