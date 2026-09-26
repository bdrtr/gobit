package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestTheCarrierIsHandedTheDestination follows the address from the order's
// wire names to the provider's input, field by field (ADR 0194).
func TestTheCarrierIsHandedTheDestination(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	destination := json.RawMessage(`{"first_name":"Ada","last_name":"Lovelace","company":"Engines",` +
		`"address_1":"12 Main St","address_2":"Floor 3","city":"Springfield","province":"IL",` +
		`"postal_code":"62701","country_code":"US","phone":"+1 555","metadata":{"gate_code":"4411"}}`)
	_, err := service.NewInterop(setup.svc).CreateFulfillment(
		context.Background(), "order_1", optionID, "key-destination", destination)
	require.NoError(t, err)

	assert.Equal(t, &coreprovider.Address{
		FirstName: "Ada", LastName: "Lovelace", Company: "Engines",
		Address1: "12 Main St", Address2: "Floor 3", City: "Springfield", Province: "IL",
		PostalCode: "62701", CountryCode: "US", Phone: "+1 555",
		Metadata: map[string]any{"gate_code": "4411"},
	}, setup.provider.lastCreateInput().Destination)
}

// TestAParcelWithNoDestinationHandsNone keeps an order with no shipping address
// (a download) from reaching the carrier as an empty address.
func TestAParcelWithNoDestinationHandsNone(t *testing.T) {
	t.Parallel()

	for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage("  ")} {
		setup := newSetup(t)
		optionID := readyOption(t, setup)

		_, err := service.NewInterop(setup.svc).CreateFulfillment(
			context.Background(), "order_1", optionID, "key-none", raw)
		require.NoError(t, err)
		assert.Nil(t, setup.provider.lastCreateInput().Destination, "%q", raw)
	}
}

// TestADestinationThisSideCannotReadOpensNoParcel refuses a field the sender
// added and this side does not know, rather than printing a label without it.
func TestADestinationThisSideCannotReadOpensNoParcel(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	optionID := readyOption(t, setup)

	_, err := service.NewInterop(setup.svc).CreateFulfillment(context.Background(),
		"order_1", optionID, "key-unknown", json.RawMessage(`{"country_code":"US","district":"North"}`))

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "%v", err)
	assert.Empty(t, setup.provider.createInputs, "no label was printed")
}
