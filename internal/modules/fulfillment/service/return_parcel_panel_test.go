package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// The tests here are about a return's parcel on the panel (ADR 0413): the read
// layer names the return a parcel brings back and finds a parcel by its id, and
// the panel's surface opens one on a return option and lists those options.

// TestAShipmentNamesTheReturnItBringsBack is the read the order page makes: the
// return_id field and filter answer one return's parcels and none of the
// order's outgoing ones, though both carry the order as their reference.
func TestAShipmentNamesTheReturnItBringsBack(t *testing.T) {
	ctx := context.Background()
	setup := newSetup(t)
	setup.bound.returnLines = map[string]int64{"oli_1": 2}
	back, err := setup.svc.CreateFulfillment(ctx, returnParcel(returnOption(t, setup), "key-back", 1))
	require.NoError(t, err)
	out, err := setup.svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
		Reference: "order_1", ShippingOptionID: readyOption(t, setup), IdempotencyKey: "key-out",
		Items: []service.FulfillmentItemInput{{LineItemID: "oli_1", Quantity: 1}},
	})
	require.NoError(t, err)

	provider := service.NewShipmentQueryProvider(setup.svc)
	records, err := provider.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldShipmentID, service.FieldShipmentReturnID},
		Filters: map[string]any{service.FieldShipmentReturnID: "ret_A"},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, records, 1, "the return's parcel and not the order's outgoing one")
	assert.Equal(t, back.ID, records[0][service.FieldShipmentID])
	assert.Equal(t, "ret_A", records[0][service.FieldShipmentReturnID])

	records, err = provider.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldShipmentID, service.FieldShipmentReturnID},
		Filters: map[string]any{service.FieldShipmentID: out.ID},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, records, 1, "a parcel is read by its id")
	assert.Equal(t, out.ID, records[0][service.FieldShipmentID])
	assert.Equal(t, "", records[0][service.FieldShipmentReturnID], "an outgoing parcel brings no return back")

	_, err = provider.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldShipmentID},
		Filters: map[string]any{service.FieldShipmentID: out.ID, service.FieldReference: "order_1"},
	})
	require.Error(t, err, "the id filter stands alone")
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
}

// TestThePanelOpensAReturnsParcelOnce is ADR 0413: the surface opens a parcel on
// a return option naming the return, holding the units given per line; the same
// key answers the same parcel and says so; an outgoing option is refused by the
// module's direction check, and a list whose two halves differ in length is
// refused before anything is asked.
func TestThePanelOpensAReturnsParcelOnce(t *testing.T) {
	ctx := context.Background()
	setup := newSetup(t)
	setup.bound.returnLines = map[string]int64{"oli_1": 2, "oli_2": 1}
	surface := service.NewAdminSurface(setup.svc)
	optionID := returnOption(t, setup)
	outgoing := readyOption(t, setup)

	id, already, err := surface.OpenReturnParcel(ctx, "order_1", "ret_A", optionID, "panel-k",
		[]string{"oli_1", "oli_2"}, []int64{2, 1})
	require.NoError(t, err)
	assert.False(t, already, "the first press opens it")
	parcel, err := setup.svc.GetFulfillment(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ret_A", parcel.ReturnID, "the parcel names the return it brings back")
	assert.Equal(t, "order_1", parcel.Reference)
	require.Len(t, parcel.Items, 2)

	again, already, err := surface.OpenReturnParcel(ctx, "order_1", "ret_A", optionID, "panel-k",
		[]string{"oli_1", "oli_2"}, []int64{2, 1})
	require.NoError(t, err)
	assert.True(t, already, "a second press of the same form opens nothing")
	assert.Equal(t, id, again)

	_, _, err = surface.OpenReturnParcel(ctx, "order_1", "ret_A", outgoing, "panel-out",
		[]string{"oli_1"}, []int64{1})
	require.Error(t, err)
	assert.Equal(t, service.CodeOptionDirectionMismatch, coreerrors.CodeOf(err), "%v", err)

	_, _, err = surface.OpenReturnParcel(ctx, "order_1", "ret_A", optionID, "panel-uneven",
		[]string{"oli_1", "oli_2"}, []int64{1})
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)

	// A blank return on an outgoing option would be an outgoing parcel: the
	// surface opens a return's parcel or nothing.
	_, _, err = surface.OpenReturnParcel(ctx, "order_1", " ", outgoing, "panel-blank",
		[]string{"oli_1"}, []int64{1})
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "%v", err)
	_, err = setup.store.FulfillmentByIdempotencyKey(ctx, "panel-blank")
	assert.Error(t, err, "nothing was opened under the key")
}

// TestThePanelListsAReturnsOptions is the open form's choice: the region's
// return options, an admin-only one included, and none of its outgoing ones.
func TestThePanelListsAReturnsOptions(t *testing.T) {
	ctx := context.Background()
	setup := newSetup(t)
	surface := service.NewAdminSurface(setup.svc)
	back := returnOption(t, setup)
	profileID := setup.createProfile(t, "counter returns")
	counter := setup.createOption(t, service.CreateOptionInput{
		Name: "Return at the counter", ShippingProfileID: profileID, Amount: 0, IsReturn: true, AdminOnly: true,
	})
	readyOption(t, setup)

	raw, err := surface.ReturnOptionsJSON(ctx, "", "TRY")
	require.NoError(t, err)
	var options []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	require.NoError(t, json.Unmarshal(raw, &options))
	ids := make([]string, 0, len(options))
	for _, option := range options {
		ids = append(ids, option.ID)
	}
	assert.ElementsMatch(t, []string{back, counter}, ids, "return options only, admin-only ones included")
}
