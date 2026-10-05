package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// A cart records the sales channel it was opened in (ADR 0397).

// TestTheInteropOpensACartInItsChannel follows the channel through the surface
// the cart flow opens carts with, into the snapshot the flow prices from, the
// cart the API answers with and the read layer's field.
func TestTheInteropOpensACartInItsChannel(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	interop := service.NewInterop(svc)

	id, err := interop.OpenCart(ctx, regionID, currency, "", "caller@example.com", "", "", "sc_A", nil)
	require.NoError(t, err)

	raw, err := interop.CartSnapshotJSON(ctx, id)
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.Equal(t, "sc_A", wire["sales_channel_id"],
		"the snapshot carries the channel under the name the flow reads")

	detail, err := svc.GetCart(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "sc_A", detail.SalesChannelID)

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{id},
		[]string{service.FieldID, service.FieldSalesChannelID})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "sc_A", records[0][service.FieldSalesChannelID])

	none, err := interop.OpenCart(ctx, regionID, currency, "", "", "", "", "", nil)
	require.NoError(t, err)
	raw, err = interop.CartSnapshotJSON(ctx, none)
	require.NoError(t, err)
	wire = map[string]any{}
	require.NoError(t, json.Unmarshal(raw, &wire))
	assert.NotContains(t, wire, "sales_channel_id", "a cart that names no channel sends none")
}

// TestAChannelIsCheckedForShapeOnly refuses a channel id a store would hold
// apart from the one sent, as every id here is refused, and nothing more: the
// channel's existence is the auth module's, which nothing asks (ADR 0146).
func TestAChannelIsCheckedForShapeOnly(t *testing.T) {
	svc, _ := newService(t)

	for _, channel := range []string{" sc ", "sc_A "} {
		_, err := svc.CreateCart(context.Background(), service.CreateCartInput{
			RegionID: regionID, CurrencyCode: currency, SalesChannelID: channel,
		})
		require.Error(t, err, "%q", channel)
		assert.True(t, errors.IsInvalid(err), "%q", channel)
	}

	cart, err := svc.CreateCart(context.Background(), service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, SalesChannelID: "sc_never_created",
	})
	require.NoError(t, err)
	assert.Equal(t, "sc_never_created", cart.SalesChannelID)
}

// cartIn opens a cart in the given channel.
func cartIn(ctx context.Context, t *testing.T, svc *service.Service, channel string) models.Cart {
	t.Helper()

	cart, err := svc.CreateCart(ctx, service.CreateCartInput{
		RegionID: regionID, CurrencyCode: currency, SalesChannelID: channel,
	})
	require.NoError(t, err)

	return cart
}

// TestAMergeKeepsTheTargetsChannel holds the merge to what it moves: lines.
// The target keeps its channel, so the moved lines are priced in it, and two
// carts in different channels, or one in none, merge rather than refuse
// (ADR 0397).
func TestAMergeKeepsTheTargetsChannel(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)

	for name, target := range map[string]string{"a target in none": "", "a target in another": "sc_B"} {
		into := cartIn(ctx, t, svc, target)
		from := cartIn(ctx, t, svc, "sc_A")
		fill(ctx, t, svc, from.ID, variantA, 1)

		merged, err := svc.MergeCart(ctx, from.ID, into.ID)

		require.NoError(t, err, name)
		assert.Equal(t, target, merged.SalesChannelID, name)
		detail, err := svc.GetCart(ctx, into.ID)
		require.NoError(t, err, name)
		assert.Equal(t, target, detail.SalesChannelID, "%s: the stored cart keeps its channel", name)
		assert.Equal(t, map[string]int64{variantA: 1}, quantities(ctx, t, svc, into.ID), name)
	}
}
