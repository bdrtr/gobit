//go:build integration

package order_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestTheOperatorFilterOnTheRealQuery is ADR 0298's column and filter against
// a real PostgreSQL, the listing and its count together. The orders are one
// customer's, because the database is shared, and the shoppers' outnumber the
// operator's, so a filter read the wrong way round would not count the same.
func TestTheOperatorFilterOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	customer := fmt.Sprintf("cus_placed_by_%d", time.Now().UnixNano())

	place := func(placedBy string) string {
		t.Helper()

		in := validInput()
		in.CustomerID = customer
		in.PlacedBy = placedBy
		order, err := svc.CreateOrder(ctx, in)
		require.NoError(t, err)
		return order.ID
	}
	byOperator := place("usr_operator")
	byShoppers := []string{place(""), place("")}

	detail, err := svc.GetOrder(ctx, byOperator)
	require.NoError(t, err)
	assert.Equal(t, "usr_operator", detail.PlacedBy, "the operator is stored, not only echoed")

	for _, tc := range []struct {
		flag bool
		want []string
	}{
		{flag: true, want: []string{byOperator}},
		{flag: false, want: byShoppers},
	} {
		page, err := svc.ListOrders(ctx, service.ListOrdersInput{
			CustomerID: &customer, PlacedByOperator: &tc.flag, Page: service.Page{Limit: 50},
		})
		require.NoError(t, err)
		ids := make([]string, 0, len(page.Items))
		for i := range page.Items {
			ids = append(ids, page.Items[i].ID)
		}
		assert.ElementsMatch(t, tc.want, ids, "placed_by_operator=%v", tc.flag)
		assert.Equal(t, int64(len(tc.want)), page.Count,
			"the count applies the filter the page does, placed_by_operator=%v", tc.flag)
	}
}

// TestTheDatabaseRefusesABlankPlacer holds the column's CHECK without the
// service: a blank operator is refused by the server, and NULL, a shopper's
// order, is not.
func TestTheDatabaseRefusesABlankPlacer(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	order, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `UPDATE orders SET placed_by = '  ' WHERE id = $1`, order.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "orders_placed_by_not_blank"`)

	_, err = testPool.Pool().Exec(ctx, `UPDATE orders SET placed_by = NULL WHERE id = $1`, order.ID)
	require.NoError(t, err)
}
