//go:build integration

package cart_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// This file proves the cart listing's KEYSET pagination against a real
// database.
//
// The risk it carries is not the query but the route the parameters take: the
// cursor travels as pgtype values, and one that names no position has to arrive
// as SQL NULL for the COALESCE sentinels to mean "start at the top". A zero
// TIME sent instead would make the first page come back empty with no error
// anywhere.

// cartWalkSize is how many carts a walk covers.
//
// Five rows in pages of two leaves the last page partial, which is where an
// off-by-one in the trim would show.
const cartWalkSize = 5

// cartPageSize is the page size the walk asks for.
const cartPageSize = 2

// TestACartCursorWalkVisitsEveryRowExactlyOnce covers both halves: that a
// request without a cursor really is the first page, and that the id half of
// the key keeps a page boundary from repeating or skipping a row.
//
// Every row is forced onto the same created_at, because carts created one call
// at a time get distinct timestamps and the id half would never be reached —
// the test would pass while proving half of what it claims.
func TestACartCursorWalkVisitsEveryRowExactlyOnce(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)

	customer := "cus_cursor_" + time.Now().Format("150405.000000")
	ids := make([]string, 0, cartWalkSize)

	for range cartWalkSize {
		created, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID:     testRegionID,
			CurrencyCode: testCurrency,
			CustomerID:   customer,
		})
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE carts SET created_at = $1 WHERE id = ANY($2::text[])`, at, ids)
	require.NoError(t, err)

	want := slices.Clone(ids)
	slices.Sort(want)
	slices.Reverse(want)

	var (
		seen   []string
		cursor page.Cursor
		pages  int
	)

	for {
		pages++
		require.Less(t, pages, cartWalkSize+3, "the walk does not terminate")

		result, listErr := svc.ListCarts(ctx, service.ListCartsInput{
			CustomerID: &customer,
			Page:       service.Page{Limit: cartPageSize, After: cursor},
		})
		require.NoError(t, listErr)

		for i := range result.Items {
			seen = append(seen, result.Items[i].ID)
		}

		if result.NextCursor == "" {
			break
		}

		cursor, listErr = page.Decode(service.CartListing, result.NextCursor)
		require.NoError(t, listErr)
	}

	assert.Equal(t, want, seen, "the walk has to produce every row exactly once, in order")
	assert.Equal(t, 3, pages, "five rows in pages of two is three requests: 2+2+1")
}
