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

// TestTheChannelFilterOnTheRealQuery is ADR 0410's column and filter against a
// real PostgreSQL, the listing and its count together. The channels are this
// run's own, because the database is shared, and one channel holds more orders
// than the other, so a filter that read another column or ignored the value
// would not list or count the same.
func TestTheChannelFilterOnTheRealQuery(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	run := time.Now().UnixNano()
	store, web := fmt.Sprintf("sc_store_%d", run), fmt.Sprintf("sc_web_%d", run)

	// Each order is placed by an operator whose id is the OTHER channel's, so
	// a filter that read placed_by for the channel would list the wrong orders.
	place := func(channel, placedBy string) string {
		t.Helper()

		in := validInput()
		in.SalesChannelID = channel
		in.PlacedBy = placedBy
		order, err := svc.CreateOrder(ctx, in)
		require.NoError(t, err)
		return order.ID
	}
	inStore := []string{place(store, web), place(store, web)}
	inWeb := []string{place(web, store)}
	inNone := place("", store)

	detail, err := svc.GetOrder(ctx, inStore[0])
	require.NoError(t, err)
	assert.Equal(t, store, detail.SalesChannelID, "the channel is stored, not only echoed")
	detail, err = svc.GetOrder(ctx, inNone)
	require.NoError(t, err)
	assert.Empty(t, detail.SalesChannelID)

	for _, tc := range []struct {
		channel string
		want    []string
	}{
		{channel: store, want: inStore},
		{channel: web, want: inWeb},
	} {
		page, err := svc.ListOrders(ctx, service.ListOrdersInput{
			SalesChannelID: &tc.channel, Page: service.Page{Limit: 50},
		})
		require.NoError(t, err)
		ids := make([]string, 0, len(page.Items))
		for i := range page.Items {
			ids = append(ids, page.Items[i].ID)
		}
		assert.ElementsMatch(t, tc.want, ids, "sales_channel_id=%s", tc.channel)
		assert.Equal(t, int64(len(tc.want)), page.Count,
			"the count applies the filter the page does, sales_channel_id=%s", tc.channel)
	}
}

// TestTheDatabaseRefusesABlankChannel holds the column's CHECK without the
// service: a blank channel is refused by the server, and NULL, an order whose
// cart named none, is not.
func TestTheDatabaseRefusesABlankChannel(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	order, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)

	_, err = testPool.Pool().Exec(ctx, `UPDATE orders SET sales_channel_id = '  ' WHERE id = $1`, order.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "orders_sales_channel_not_blank"`)

	_, err = testPool.Pool().Exec(ctx, `UPDATE orders SET sales_channel_id = 'sc_set' WHERE id = $1`, order.ID)
	require.NoError(t, err)
	_, err = testPool.Pool().Exec(ctx, `UPDATE orders SET sales_channel_id = NULL WHERE id = $1`, order.ID)
	require.NoError(t, err)
}
