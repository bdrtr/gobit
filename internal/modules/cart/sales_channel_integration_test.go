//go:build integration

package cart_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// TestACartKeepsTheChannelItWasOpenedIn holds ADR 0397's column on the real
// repository: the channel the cart was opened in is stored, read back, sent in
// the snapshot the price rounds read and offered to the read layer; a cart that
// names none stores NULL and sends nothing.
func TestACartKeepsTheChannelItWasOpenedIn(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	interop := service.NewInterop(svc)

	id, err := interop.OpenCart(ctx, testRegionID, testCurrency, "", "", "", "", "sc_stored", nil)
	require.NoError(t, err)

	read, err := svc.GetCart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "sc_stored", read.SalesChannelID, "the channel is stored, not only echoed")

	raw, err := interop.CartSnapshotJSON(ctx, id)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "sc_stored", wire["sales_channel_id"])

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{id},
		[]string{service.FieldID, service.FieldSalesChannelID})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, query.Record{service.FieldID: id, service.FieldSalesChannelID: "sc_stored"}, records[0])

	none := newCart(ctx, t, svc)
	var stored *string
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT sales_channel_id FROM carts WHERE id = $1`, none.ID).Scan(&stored))
	assert.Nil(t, stored, "a cart in no channel stores NULL, not a blank")
}

// TestTheDatabaseRefusesABlankChannel holds the column's CHECK without the
// service: a blank channel is refused by the server, and NULL, a cart priced
// in none, is not.
func TestTheDatabaseRefusesABlankChannel(t *testing.T) {
	ctx := context.Background()
	svc := newService(t)
	cart := newCart(ctx, t, svc)

	_, err := testPool.Pool().Exec(ctx, `UPDATE carts SET sales_channel_id = '  ' WHERE id = $1`, cart.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `check constraint "carts_sales_channel_id_not_blank"`)

	_, err = testPool.Pool().Exec(ctx, `UPDATE carts SET sales_channel_id = NULL WHERE id = $1`, cart.ID)
	require.NoError(t, err)
}
