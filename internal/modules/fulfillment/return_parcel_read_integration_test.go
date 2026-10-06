//go:build integration

package fulfillment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestAReturnsParcelsAreFoundByTheReturn runs ADR 0413's read on the real
// schema: the return_id filter lists one return's parcels and counts them,
// through the service and the read provider, and none of the order's outgoing
// parcels or another return's, though every one carries the same reference;
// and a parcel is read by its id with its reference and its return.
func TestAReturnsParcelsAreFoundByTheReturn(t *testing.T) {
	ctx := context.Background()
	svc, _ := newService(t)
	profile := newProfile(ctx, t, svc)
	outgoing := newOption(ctx, t, svc, profile.ID, 1_000)
	back := newReturnOption(ctx, t, svc, profile.ID)
	suffix := models.NewFulfillmentID()
	retA, retB := "ret_A"+suffix, "ret_B"+suffix

	open := func(optionID, returnID string) models.Fulfillment {
		t.Helper()
		ful, err := svc.CreateFulfillment(ctx, service.CreateFulfillmentInput{
			Reference:        testReference,
			ShippingOptionID: optionID,
			IdempotencyKey:   "found-by-return-" + models.NewFulfillmentID(),
			ReturnID:         returnID,
			Items:            []service.FulfillmentItemInput{{LineItemID: generousReturnLine, Quantity: 1}},
		})
		require.NoError(t, err)
		return ful
	}
	open(outgoing.ID, "")
	first := open(back.ID, retA)
	second := open(back.ID, retA)
	open(back.ID, retB)

	list, total, err := svc.ListFulfillments(ctx, service.ListFulfillmentsInput{
		ReturnID: &retA, Page: service.Page{Limit: 10},
	})
	require.NoError(t, err)
	assert.EqualValues(t, 2, total, "the count applies the same filter as the list")
	ids := make([]string, 0, len(list))
	for i := range list {
		ids = append(ids, list[i].ID)
	}
	assert.ElementsMatch(t, []string{first.ID, second.ID}, ids)

	provider := service.NewShipmentQueryProvider(svc)
	records, err := provider.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldShipmentID, service.FieldShipmentReturnID},
		Filters: map[string]any{service.FieldShipmentReturnID: retA},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Len(t, records, 2)
	for _, record := range records {
		assert.Equal(t, retA, record[service.FieldShipmentReturnID])
	}

	records, err = provider.List(ctx, query.ListOptions{
		Fields:  []string{service.FieldShipmentID, service.FieldReference, service.FieldShipmentReturnID},
		Filters: map[string]any{service.FieldShipmentID: first.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, testReference, records[0][service.FieldReference])
	assert.Equal(t, retA, records[0][service.FieldShipmentReturnID])
}
