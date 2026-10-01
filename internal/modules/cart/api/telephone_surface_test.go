package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
)

// The panel's telephone order (ADR 0290, ADR 0291).

// surfaceOver builds the surface over a handler with the given flows and cart
// service, as the module builds it over the API's.
func surfaceOver(flows api.Flows, carts *fakeCarts) *api.TelephoneSurface {
	return api.NewTelephoneSurface(api.New(carts, flows, nil, false))
}

// channelPricing records the principal the line write reached the flow with.
type channelPricing struct {
	fakePricing
	principal corehttp.Principal
	found     bool
}

func (f *channelPricing) AddPricedLineItem(
	ctx context.Context, cartID, variantID string, quantity int64,
	metadata json.RawMessage, properties map[string]string, addOns json.RawMessage,
) (string, error) {
	f.principal, f.found = corehttp.PrincipalFromContext(ctx)

	return f.fakePricing.AddPricedLineItem(ctx, cartID, variantID, quantity, metadata, properties, addOns)
}

// operator is the panel's signed-in operator.
func operator() context.Context {
	return corehttp.WithPrincipal(context.Background(), corehttp.Principal{
		ID: "usr_panel", Kind: "user", Scopes: []string{"cart:write"},
	})
}

// TestThePanelOpensACartThroughTheOpeningFlow: the country, the customer and
// the e-mail reach the flow the API holds, and its cart comes back.
func TestThePanelOpensACartThroughTheOpeningFlow(t *testing.T) {
	opening := &fakeOpening{cartID: "cart_phone"}
	surface := surfaceOver(api.Flows{Opening: opening}, operatorsCarts())

	id, err := surface.OpenCart(operator(), "TR", "cus_1", "caller@example.com")

	require.NoError(t, err)
	assert.Equal(t, "cart_phone", id)
	assert.Equal(t, "TR", opening.gotCountry)
	assert.Equal(t, "cus_1", opening.gotCustomerID)
	assert.Equal(t, "caller@example.com", opening.gotEmail)
	assert.Empty(t, opening.gotAddsTo, "a telephone order adds to no order")
	assert.Equal(t, "usr_panel", opening.gotOpenedBy, "the cart names the signed-in operator (ADR 0296)")
}

// TestThePanelOpensNoCartForNobody: a context with no operator in it is a
// wiring fault, and no cart is opened that names nobody (ADR 0296).
func TestThePanelOpensNoCartForNobody(t *testing.T) {
	opening := &fakeOpening{cartID: "cart_phone"}
	surface := surfaceOver(api.Flows{Opening: opening}, operatorsCarts())

	for name, ctx := range map[string]context.Context{
		"no principal": context.Background(),
		"no id": corehttp.WithPrincipal(context.Background(), corehttp.Principal{
			Kind: "user", Scopes: []string{"cart:write"},
		}),
	} {
		_, err := surface.OpenCart(ctx, "TR", "", "caller@example.com")
		require.Error(t, err, name)
		assert.True(t, errors.HasKind(err, errors.KindInternal), name)
	}
	assert.Zero(t, opening.calls, "no cart is opened without its operator")
}

// TestThePanelPricesALineInTheChannelItNames: the line reaches the pricing
// flow with the operator's identity scoped to the named channel, so the
// catalog the flow reads is that shopfront's.
func TestThePanelPricesALineInTheChannelItNames(t *testing.T) {
	pricing := &channelPricing{fakePricing: fakePricing{lineID: "line_1"}}
	surface := surfaceOver(api.Flows{Pricing: pricing}, operatorsCarts())

	id, err := surface.AddLine(operator(), "cart_phone", " sc_shop ", "variant_1", 2)

	require.NoError(t, err)
	assert.Equal(t, "line_1", id)
	assert.Equal(t, "cart_phone", pricing.gotCartID)
	assert.Equal(t, "variant_1", pricing.gotVariantID)
	assert.Equal(t, int64(2), pricing.gotQuantity)
	require.True(t, pricing.found)
	assert.Equal(t, []string{"sc_shop"}, pricing.principal.SalesChannelIDs)
	assert.Equal(t, "usr_panel", pricing.principal.ID, "the audit still names the operator")
}

// TestALineWithoutAChannelIsRefused: no channel is a refusal, not the catalog
// of products assigned to none, and the flow is not reached.
func TestALineWithoutAChannelIsRefused(t *testing.T) {
	pricing := &channelPricing{}
	surface := surfaceOver(api.Flows{Pricing: pricing}, operatorsCarts())

	_, err := surface.AddLine(operator(), "cart_phone", "  ", "variant_1", 1)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, pricing.calls)
}

// TestTheSurfaceWithoutItsFlowsRefuses: an installation whose flows are not
// bound answers an error rather than a cart nobody priced.
func TestTheSurfaceWithoutItsFlowsRefuses(t *testing.T) {
	surface := surfaceOver(api.Flows{}, operatorsCarts())

	_, err := surface.OpenCart(operator(), "TR", "", "caller@example.com")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))

	_, err = surface.AddLine(operator(), "cart_phone", "sc_shop", "variant_1", 1)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}

// TestThePanelWritesTheAddressAndRepricesTheCart: the address keys reach the
// service as the address endpoint's body would, inside the repricing flow.
func TestThePanelWritesTheAddressAndRepricesTheCart(t *testing.T) {
	carts := operatorsCarts()
	repricing := &fakeRepricing{}
	surface := surfaceOver(api.Flows{Repricing: repricing}, carts)

	err := surface.SetShippingAddress(operator(), "cart_phone", map[string]string{
		api.AddressFirstName: "Ada", api.AddressLastName: "Lovelace", api.AddressLine1: "12 Right St",
		api.AddressCity: "Ankara", api.AddressPostalCode: "06000", api.AddressCountryCode: "tr",
		api.AddressPhone: "+90 312 000 00 00",
	})

	require.NoError(t, err)
	assert.Equal(t, "cart_phone", carts.gotCartID)
	assert.False(t, carts.billing, "it is the shipping address")
	assert.Equal(t, "Ada", carts.addressInput.FirstName)
	assert.Equal(t, "Lovelace", carts.addressInput.LastName)
	assert.Equal(t, "12 Right St", carts.addressInput.Address1)
	assert.Equal(t, "Ankara", carts.addressInput.City)
	assert.Equal(t, "06000", carts.addressInput.PostalCode)
	assert.Equal(t, "tr", carts.addressInput.CountryCode)
	assert.Equal(t, "+90 312 000 00 00", carts.addressInput.Phone)
	assert.Equal(t, []string{"cart_phone"}, repricing.repriced, "the tax follows the address")
}

// TestThePanelChoosesTheShippingOptionThroughTheShippingFlow: the option is
// priced by the flow the API holds and its method comes back.
func TestThePanelChoosesTheShippingOptionThroughTheShippingFlow(t *testing.T) {
	shipping := &fakeShipping{methodID: "sm_1"}
	surface := surfaceOver(api.Flows{Shipping: shipping}, operatorsCarts())

	id, err := surface.AddShippingMethod(operator(), "cart_phone", "so_courier")

	require.NoError(t, err)
	assert.Equal(t, "sm_1", id)
	assert.Equal(t, "cart_phone", shipping.gotCartID)
	assert.Equal(t, "so_courier", shipping.gotOptionID)
}

// TestThePanelCompletesWithTheOperatorsClaims: the completion carries the
// channel the operator named, the offline method, the total they read and the
// cart's own e-mail, and asks for an offline method; the order and what it
// owes come back.
func TestThePanelCompletesWithTheOperatorsClaims(t *testing.T) {
	checkout := &fakeCheckout{response: json.RawMessage(
		`{"order_id":"order_1","cart_id":"cart_phone","currency_code":"TRY","amount":60000,"outstanding":60000}`)}
	carts := operatorsCarts()
	carts.detail.Email = "caller@example.com"
	surface := surfaceOver(api.Flows{Checkout: checkout}, carts)

	orderID, outstanding, err := surface.Complete(operator(), "cart_phone", " sc_shop ", "bank_transfer", 60_000)

	require.NoError(t, err)
	assert.Equal(t, "order_1", orderID)
	assert.Equal(t, int64(60_000), outstanding)

	var sent map[string]any
	require.NoError(t, json.Unmarshal(checkout.got, &sent))
	assert.Equal(t, "cart_phone", sent["cart_id"])
	assert.Equal(t, "bank_transfer", sent["payment_provider_id"])
	assert.InDelta(t, 60_000, sent["expected_total"], 0)
	assert.Equal(t, true, sent["offline_only"], "the operator's completion is paid later or not at all")
	assert.Equal(t, []any{"sc_shop"}, sent["sales_channel_ids"])
	assert.Equal(t, "caller@example.com", sent["email"], "the e-mail is the cart's")
	assert.Equal(t, "usr_panel", sent["placed_by"], "the order names the signed-in operator (ADR 0298)")
}

// TestACompletionWithoutAChannelIsRefused: no channel is refused before the
// checkout is reached.
func TestACompletionWithoutAChannelIsRefused(t *testing.T) {
	checkout := &fakeCheckout{}
	surface := surfaceOver(api.Flows{Checkout: checkout}, operatorsCarts())

	_, _, err := surface.Complete(operator(), "cart_phone", "", "bank_transfer", 60_000)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, checkout.calls)
}
