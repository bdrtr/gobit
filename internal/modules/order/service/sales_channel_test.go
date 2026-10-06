package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// TestAnOrderKeepsTheChannelItWasPlacedIn follows the sales channel from the
// checkout's wire name to the record, the listing and the read layer's field
// and filter (ADR 0410). An order from a cart that named no channel records
// none and is in no channel's list.
func TestAnOrderKeepsTheChannelItWasPlacedIn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	interop := service.NewInterop(e.svc)

	placeIn := func(channel, key string) string {
		t.Helper()
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(snapshotJSON), &body))
		if channel != "" {
			body["sales_channel_id"] = channel
		}
		body["idempotency_key"] = key
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		id, err := interop.PlaceOrderJSON(ctx, raw)
		require.NoError(t, err)
		return id
	}
	inStore := placeIn("sc_store", "wf_CHANNEL_STORE")
	inWeb := placeIn("sc_web", "wf_CHANNEL_WEB")
	inNone := placeIn("", "wf_CHANNEL_NONE")

	for id, want := range map[string]string{inStore: "sc_store", inWeb: "sc_web", inNone: ""} {
		detail, err := e.svc.GetOrder(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, want, detail.SalesChannelID, "order %s", id)
	}

	channel := "sc_store"
	page, err := e.svc.ListOrders(ctx, service.ListOrdersInput{SalesChannelID: &channel})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "one order was placed in the store channel")
	assert.Equal(t, inStore, page.Items[0].ID)
	assert.Equal(t, int64(1), page.Count)

	provider := service.NewQueryProvider(e.svc)
	records, err := provider.List(ctx, query.ListOptions{
		Fields:  []string{query.IDField, service.FieldSalesChannelID},
		Filters: map[string]any{service.FieldSalesChannelID: "sc_web"},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, inWeb, records[0][query.IDField])
	assert.Equal(t, "sc_web", records[0][service.FieldSalesChannelID])

	records, err = provider.List(ctx, query.ListOptions{
		Fields: []string{query.IDField, service.FieldSalesChannelID},
	})
	require.NoError(t, err)
	byID := map[any]any{}
	for _, record := range records {
		byID[record[query.IDField]] = record[service.FieldSalesChannelID]
	}
	assert.Equal(t, "", byID[inNone], "an order whose cart named no channel records none")

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{service.FieldSalesChannelID: 7}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestABlankChannelPlacesAndListsNoOrder refuses a channel that names nothing,
// on the write and on the filter, rather than storing or matching it as none.
func TestABlankChannelPlacesAndListsNoOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	for _, channel := range []string{" ", "sc_store "} {
		in := validInput()
		in.SalesChannelID = channel
		_, err := e.svc.CreateOrder(ctx, in)
		require.Error(t, err, "%q", channel)
		assert.True(t, errors.IsInvalid(err), "%q", channel)

		_, err = e.svc.ListOrders(ctx, service.ListOrdersInput{SalesChannelID: &channel})
		require.Error(t, err, "filter %q", channel)
		assert.True(t, errors.IsInvalid(err), "filter %q", channel)
	}
}
