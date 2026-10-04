package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// shippingRow is the id of the order's current shipping address row.
func shippingRow(t *testing.T, e env, orderID string) string {
	t.Helper()

	detail, err := e.svc.GetOrder(context.Background(), orderID)
	require.NoError(t, err)
	require.NotNil(t, detail.ShippingAddress)

	return detail.ShippingAddress.ID
}

// shippingRows counts the order's shipping address rows, closed ones included.
func shippingRows(t *testing.T, e env, orderID string) int {
	t.Helper()

	all, err := e.store.OrderAddressesByOrderIDs(context.Background(), []string{orderID})
	require.NoError(t, err)
	rows := 0
	for i := range all[orderID] {
		if all[orderID][i].Type == models.AddressShipping {
			rows++
		}
	}

	return rows
}

// TestACorrectionDrawnFromAnOldRowIsRefused is ADR 0388: a correction naming
// the row it was drawn from goes through and keeps that row's metadata, which
// the form does not show; a second one drawn from the same row is refused
// when it says something else, and goes through writing nothing when it says
// what the order holds now.
func TestACorrectionDrawnFromAnOldRowIsRefused(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	placed := placedAddress()
	placed.Metadata = map[string]any{"gate_code": "4411"}
	order := shippedTo(t, e, placed)
	read := shippingRow(t, e, order.ID)

	typed := correctedAddress()
	typed.Metadata = map[string]any{"typed": "by the form"}
	current, err := e.svc.CorrectShippingAddressFrom(ctx, order.ID, typed, read)
	require.NoError(t, err)
	assert.Equal(t, "12 Right St", current.Address1)
	assert.Equal(t, map[string]any{"gate_code": "4411"}, current.Metadata, "the row's metadata is kept")
	assert.NotEqual(t, read, current.ID)
	require.Equal(t, 2, shippingRows(t, e, order.ID))

	again := correctedAddress()
	current, err = e.svc.CorrectShippingAddressFrom(ctx, order.ID, again, read)
	require.NoError(t, err, "the same form sent twice says what the order holds")
	assert.Equal(t, "12 Right St", current.Address1)
	assert.Equal(t, 2, shippingRows(t, e, order.ID), "and writes nothing")

	other := correctedAddress()
	other.Address2 = "Flat 3"
	_, err = e.svc.CorrectShippingAddressFrom(ctx, order.ID, other, read)
	require.Error(t, err)
	assert.True(t, errors.IsConflict(err), "%v", err)
	assert.Equal(t, service.CodeAddressRevised, errors.CodeOf(err), "%v", err)
	assert.Contains(t, err.Error(), "draw the page again")
	assert.Equal(t, 2, shippingRows(t, e, order.ID), "a refused correction writes nothing")

	_, err = e.svc.CorrectShippingAddressFrom(ctx, order.ID, other, shippingRow(t, e, order.ID))
	require.NoError(t, err, "drawn from the row the order holds, it goes through")
	assert.Equal(t, 3, shippingRows(t, e, order.ID))
}

// TestACorrectionNamingNoRowIsTheAPIs keeps the API's correction as it was:
// no row compared, and the metadata as given.
func TestACorrectionNamingNoRowIsTheAPIs(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	placed := placedAddress()
	placed.Metadata = map[string]any{"gate_code": "1"}
	order := shippedTo(t, e, placed)

	_, err := e.svc.CorrectShippingAddress(ctx, order.ID, correctedAddress())
	require.NoError(t, err)
	current, err := e.svc.CorrectShippingAddressFrom(ctx, order.ID, models.OrderAddress{
		Address1: "1 Other St", Metadata: map[string]any{"gate_code": "2"},
	}, "")
	require.NoError(t, err, "no row read, nothing compared")
	assert.Equal(t, map[string]any{"gate_code": "2"}, current.Metadata, "the metadata as given")
}

// TestTheInteropHandsOnTheRowRead: the flow's correction reaches the service
// with the row it names, so a form drawn from a closed row is refused there
// too (ADR 0388).
func TestTheInteropHandsOnTheRowRead(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	order := shippedTo(t, e, placedAddress())
	read := shippingRow(t, e, order.ID)
	interop := service.NewInterop(e.svc)

	_, err := interop.CorrectShippingAddressJSON(ctx, order.ID, json.RawMessage(`{"address_1":"1 First St"}`), read)
	require.NoError(t, err)
	_, err = interop.CorrectShippingAddressJSON(ctx, order.ID, json.RawMessage(`{"address_1":"2 Second St"}`), read)
	assert.Equal(t, service.CodeAddressRevised, errors.CodeOf(err), "%v", err)
}
