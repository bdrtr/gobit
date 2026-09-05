//go:build integration

package customer_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// This file proves the customer listing's KEYSET pagination against a real
// database.
//
// The risk it carries is not the query but the route the parameters take: the
// cursor travels as pgtype values, and one that names no position has to arrive
// as SQL NULL for the COALESCE sentinels to mean "start at the top". A zero
// TIME sent instead would make the first page come back empty with no error
// anywhere.
//
// It builds its own service and its own customers rather than borrowing the
// module's existing helpers: those are named in Turkish, this file is new and
// is therefore English (ADR 0012), and a new file may not add to the migration
// ledger — the ledger only shrinks.

// customerWalkSize is how many customers a walk covers.
//
// Five rows in pages of two leaves the last page partial, which is where an
// off-by-one in the trim would show.
const customerWalkSize = 5

// customerPageSize is the page size the walk asks for.
const customerPageSize = 2

// TestACustomerCursorWalkVisitsEveryRowExactlyOnce covers both halves: that a
// request without a cursor really is the first page, and that the id half of
// the key keeps a page boundary from repeating or skipping a row.
//
// Every row is forced onto the same created_at, because customers created one
// call at a time get distinct timestamps and the id half would never be
// reached — the test would pass while proving half of what it claims.
func TestACustomerCursorWalkVisitsEveryRowExactlyOnce(t *testing.T) {
	ctx := context.Background()
	svc := service.New(repository.New(testPool.Pool()), service.Options{})

	group, err := svc.CreateGroup(ctx, service.GroupInput{
		Name: "cursor-" + time.Now().Format("150405.000000"),
	})
	require.NoError(t, err)

	ids := make([]string, 0, customerWalkSize)

	for i := range customerWalkSize {
		customer, createErr := svc.CreateCustomer(ctx, service.CustomerInput{
			Email: fmt.Sprintf("cursor%d.%s@example.com", i, group.ID),
		})
		require.NoError(t, createErr)
		require.NoError(t, svc.AddToGroup(ctx, customer.ID, group.ID))
		ids = append(ids, customer.ID)
	}

	at := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	_, err = testPool.Pool().Exec(ctx,
		`UPDATE customer SET created_at = $1 WHERE id = ANY($2::text[])`, at, ids)
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
		require.Less(t, pages, customerWalkSize+3, "the walk does not terminate")

		result, listErr := svc.ListCustomers(ctx, service.ListCustomersInput{
			GroupID: &group.ID,
			Limit:   customerPageSize,
			After:   cursor,
		})
		require.NoError(t, listErr)

		for i := range result.Items {
			seen = append(seen, result.Items[i].ID)
		}

		if result.NextCursor == "" {
			break
		}

		cursor, listErr = page.Decode(service.CustomerListing, result.NextCursor)
		require.NoError(t, listErr)
	}

	assert.Equal(t, want, seen, "the walk has to produce every row exactly once, in order")
	assert.Equal(t, 3, pages, "five rows in pages of two is three requests: 2+2+1")
}
