//go:build integration

package cart_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestTheQueryListsTheCartsAnOperatorOpened holds ADR 0296's filter on the real
// query: the page and its count keep the carts an operator opened, or the
// shoppers', alongside the completion filter. The region is the test's own, so
// the shared database's other carts stay out of the count, and the shoppers'
// carts outnumber the operator's, so a count that inverted the filter would
// not come out the same.
func TestTheQueryListsTheCartsAnOperatorOpened(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	region := "reg_OPENED_BY"

	byOperator, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: region, CurrencyCode: testCurrency, OpenedBy: "usr_operator",
	})
	require.NoError(t, err)
	var byShopper []string
	for range 2 {
		cart, err := svc.CreateCart(ctx, service.CreateCartInput{
			RegionID: region, CurrencyCode: testCurrency,
		})
		require.NoError(t, err)
		byShopper = append(byShopper, cart.ID)
	}

	read, err := svc.GetCart(ctx, byOperator.ID)
	require.NoError(t, err)
	assert.Equal(t, "usr_operator", read.OpenedBy, "the opener is stored, not only echoed")

	for _, tc := range []struct {
		flag bool
		want []string
	}{
		{flag: true, want: []string{byOperator.ID}},
		{flag: false, want: byShopper},
	} {
		page, err := svc.ListCarts(ctx, service.ListCartsInput{
			RegionID: &region, OpenedByOperator: &tc.flag, Completed: new(false),
		})
		require.NoError(t, err)
		var listed []string
		for i := range page.Items {
			listed = append(listed, page.Items[i].ID)
		}
		assert.ElementsMatch(t, tc.want, listed, "opened_by_operator=%v", tc.flag)
		assert.Equal(t, int64(len(tc.want)), page.Count,
			"the count applies the filter the page does, opened_by_operator=%v", tc.flag)
	}

	page, err := svc.ListCarts(ctx, service.ListCartsInput{RegionID: &region})
	require.NoError(t, err)
	assert.Equal(t, int64(3), page.Count, "no filter keeps all three")

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{byOperator.ID},
		[]string{service.FieldID, service.FieldOpenedBy})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, query.Record{service.FieldID: byOperator.ID, service.FieldOpenedBy: "usr_operator"}, records[0])
}

// TestTheDatabaseRefusesABlankOpener holds the column's CHECK without the
// service: a blank opener is refused by the server, and NULL, a shopper's cart,
// is not.
func TestTheDatabaseRefusesABlankOpener(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx, `UPDATE carts SET opened_by = '  ' WHERE id = $1`, cart.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "carts_opened_by_not_blank"`)

	_, err = testPool.Pool().Exec(ctx, `UPDATE carts SET opened_by = NULL WHERE id = $1`, cart.ID)
	require.NoError(t, err)
}
