package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// TestTheProviderIsHandedTheCartTheRequestNames is ADR 0445 on this side: the
// cart the cart flow names reaches a calculated option's provider as it was
// sent, and a request naming none hands it nil.
func TestTheProviderIsHandedTheCartTheRequestNames(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	profileID := setup.createProfile(t, "default")
	setup.createOption(t, service.CreateOptionInput{
		Name: "By the goods", ShippingProfileID: profileID, PriceType: "calculated",
	})

	request := map[string]any{
		"currency_code":        "TRY",
		"shipping_profile_ids": []string{profileID},
		"cart": map[string]any{
			"id": "cart_1", "currency_code": "TRY",
			"lines": []map[string]any{{
				"id": "cali_1", "variant_id": "variant_1", "product_id": "prod_1",
				"quantity": 2, "unit_price": 1500, "subtotal": 2700,
				"variant_weight": 0, "product_weight": 1200,
				"product_length": 30, "product_width": 20, "product_height": 10,
			}},
		},
	}
	body, err := json.Marshal(request)
	require.NoError(t, err)
	_, err = interop.ListOptionsJSON(context.Background(), body)
	require.NoError(t, err)

	assert.Equal(t, &coreprovider.QuoteCart{
		ID: "cart_1", CurrencyCode: "TRY",
		Lines: []coreprovider.QuoteLine{{
			ID: "cali_1", VariantID: "variant_1", ProductID: "prod_1",
			Quantity: 2, UnitPrice: 1500, Subtotal: 2700, ProductWeight: 1200,
			ProductLength: 30, ProductWidth: 20, ProductHeight: 10,
		}},
	}, setup.provider.lastQuoteInput().Cart)

	delete(request, "cart")
	body, err = json.Marshal(request)
	require.NoError(t, err)
	_, err = interop.ListOptionsJSON(context.Background(), body)
	require.NoError(t, err)
	assert.Nil(t, setup.provider.lastQuoteInput().Cart, "a request naming no cart hands the provider none")
}

// TestAFractionalQuantityInTheCartIsRefused holds the cart's numbers to the
// request's rule: an integer, or the request is refused.
func TestAFractionalQuantityInTheCartIsRefused(t *testing.T) {
	t.Parallel()

	setup := newSetup(t)
	interop := service.NewInterop(setup.svc)
	_, err := interop.ListOptionsJSON(context.Background(), json.RawMessage(
		`{"currency_code":"TRY","cart":{"id":"cart_1","lines":[{"id":"cali_1","quantity":1.5}]}}`))
	require.Error(t, err)
}
