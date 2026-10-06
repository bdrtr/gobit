package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestTheReadLayerOffersAnOptionsDeliveryDays is ADR 0421 on the read layer the
// panel's list reads: an option's business days come as delivery_min_days and
// delivery_max_days, and an option that says none answers nil for both.
func TestTheReadLayerOffersAnOptionsDeliveryDays(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	profileID := setup.createProfile(t, "default")
	withDays := setup.createOption(t, service.CreateOptionInput{
		Name: "Courier", ShippingProfileID: profileID, DeliveryDays: days(2, 7),
	})
	bare := setup.createOption(t, service.CreateOptionInput{Name: "Pick up", ShippingProfileID: profileID})
	provider := service.NewQueryProvider(setup.svc)

	records, err := provider.FetchByIDs(context.Background(), []string{withDays, bare},
		[]string{service.FieldID, service.FieldDeliveryMinDays, service.FieldDeliveryMaxDays})
	require.NoError(t, err)
	require.Len(t, records, 2)
	byID := map[string]map[string]any{}
	for _, record := range records {
		id, _ := record[service.FieldID].(string)
		byID[id] = record
	}
	assert.Equal(t, int32(2), byID[withDays][service.FieldDeliveryMinDays])
	assert.Equal(t, int32(7), byID[withDays][service.FieldDeliveryMaxDays])
	assert.Nil(t, byID[bare][service.FieldDeliveryMinDays], "an option that says none")
	assert.Nil(t, byID[bare][service.FieldDeliveryMaxDays])
}
