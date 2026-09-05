//go:build integration

package order_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// This file proves the order listing's KEYSET pagination against a real
// database.
//
// The order listing reaches the database through sqlc, so the risk it carries
// is not the one the product listing carries: the cursor parameters travel as
// pgtype values, and an absent cursor has to arrive as SQL NULL for the
// COALESCE sentinels to mean "start at the top". A pgtype value that went as a
// zero TIME instead would make the first page come back empty — with no error
// anywhere.

// orderWalkSize is how many orders a walk covers.
//
// Five rows in pages of two makes the last page a partial one, which is where
// an off-by-one in the trim would show.
const orderWalkSize = 5

// orderPageSize is the page size the walk asks for.
const orderPageSize = 2

// TestAnOrderCursorWalkVisitsEveryRowExactlyOnce covers both halves: that an
// absent cursor really is the first page, and that the id in the key keeps a
// page boundary from repeating or skipping a row.
//
// Every row is forced onto the same created_at, because orders created one call
// at a time get distinct timestamps and the id half of the key would never be
// reached — the walk would pass while proving only half of what it claims.
func TestAnOrderCursorWalkVisitsEveryRowExactlyOnce(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	customer := "cus_cursor_" + time.Now().Format("150405.000000")
	ids := make([]string, 0, orderWalkSize)

	for range orderWalkSize {
		in := validInput()
		in.CustomerID = customer

		created, err := svc.CreateOrder(ctx, in)
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	_, err := testPool.Pool().Exec(ctx,
		`UPDATE orders SET created_at = $1 WHERE id = ANY($2::text[])`, at, ids)
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
		require.Less(t, pages, orderWalkSize+3, "the walk does not terminate")

		result, err := svc.ListOrders(ctx, service.ListOrdersInput{
			CustomerID: &customer,
			Page:       service.Page{Limit: orderPageSize, After: cursor},
		})
		require.NoError(t, err)

		for i := range result.Items {
			seen = append(seen, result.Items[i].ID)
		}

		if result.NextCursor == "" {
			break
		}

		cursor, err = page.Decode(service.OrderListing, result.NextCursor)
		require.NoError(t, err)
	}

	assert.Equal(t, want, seen, "the walk has to produce every row exactly once, in order")
	assert.Equal(t, 3, pages, "five rows in pages of two is three requests: 2+2+1")
}
