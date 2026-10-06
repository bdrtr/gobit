package cart

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheCartsListingCarriesAnOptionsDeliveryDays is ADR 0421: an option the
// fulfillment module answers with business days reaches the cart's listing
// with them, and one answered without carries none.
func TestTheCartsListingCarriesAnOptionsDeliveryDays(t *testing.T) {
	h := shippingHarness(t)
	h.shipping.options = []quotedOption{
		{ID: "so_std", Name: "Standard", Amount: 2500, DeliveryDays: &DeliveryDays{Min: 3, Max: 5}},
		{ID: "so_pick", Name: "Pick up", Amount: 0},
	}

	options, err := h.wf.ShippingOptionsFor(context.Background(), testCartID)
	require.NoError(t, err)

	require.Len(t, options, 2)
	assert.Equal(t, &DeliveryDays{Min: 3, Max: 5}, options[0].DeliveryDays)
	assert.Nil(t, options[1].DeliveryDays, "an option that says none")

	operator, err := h.wf.OperatorShippingOptionsFor(context.Background(), testCartID)
	require.NoError(t, err)
	require.Len(t, operator, 2)
	assert.Equal(t, &DeliveryDays{Min: 3, Max: 5}, operator[0].DeliveryDays, "the operator's listing too")

	raw, err := listedOptionsJSON(testCartID, options)
	require.NoError(t, err)
	assert.JSONEq(t, `{"options":[`+
		`{"id":"so_std","name":"Standard","amount":2500,"currency_code":"`+testCurrency+`","delivery_days":{"min":3,"max":5}},`+
		`{"id":"so_pick","name":"Pick up","amount":0,"currency_code":"`+testCurrency+`"}]}`, string(raw),
		"the interop answer carries the days, and none for an option that says none")
}
