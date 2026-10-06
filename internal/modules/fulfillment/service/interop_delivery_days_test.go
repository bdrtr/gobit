package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestTheInteropListingCarriesAnOptionsDeliveryDays is ADR 0421 on the
// cross-module surface the cart's listing reads: an option with business days
// carries them under "delivery_days", as the schema in interop.go declares, and
// one without carries no key.
func TestTheInteropListingCarriesAnOptionsDeliveryDays(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	profileID := setup.createProfile(t, "default")
	withDays := setup.createOption(t, service.CreateOptionInput{
		Name: "Standard", ShippingProfileID: profileID, Amount: 900, DeliveryDays: days(3, 5),
	})
	setup.createOption(t, service.CreateOptionInput{
		Name: "Pick up", ShippingProfileID: profileID, Amount: 1_000,
	})

	request, err := json.Marshal(map[string]any{
		"region_id": "reg_tr", "currency_code": "TRY", "shipping_profile_ids": []string{profileID},
	})
	require.NoError(t, err)
	raw, err := interop.ListOptionsJSON(context.Background(), request)
	require.NoError(t, err)

	var response struct {
		Options []map[string]json.RawMessage `json:"options"`
	}
	require.NoError(t, json.Unmarshal(raw, &response))
	require.Len(t, response.Options, 2, string(raw))
	for _, option := range response.Options {
		var id string
		require.NoError(t, json.Unmarshal(option["id"], &id))
		if id == withDays {
			assert.JSONEq(t, `{"min":3,"max":5}`, string(option["delivery_days"]))
			continue
		}
		assert.NotContains(t, option, "delivery_days", "an option that says none: %s", raw)
	}
}
