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

// The panel's half of a telephone order (ADR 0290).

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
	surface := api.NewTelephoneSurface(api.Flows{Opening: opening})

	id, err := surface.OpenCart(operator(), "TR", "cus_1", "caller@example.com")

	require.NoError(t, err)
	assert.Equal(t, "cart_phone", id)
	assert.Equal(t, "TR", opening.gotCountry)
	assert.Equal(t, "cus_1", opening.gotCustomerID)
	assert.Equal(t, "caller@example.com", opening.gotEmail)
	assert.Empty(t, opening.gotAddsTo, "a telephone order adds to no order")
}

// TestThePanelPricesALineInTheChannelItNames: the line reaches the pricing
// flow with the operator's identity scoped to the named channel, so the
// catalog the flow reads is that shopfront's.
func TestThePanelPricesALineInTheChannelItNames(t *testing.T) {
	pricing := &channelPricing{fakePricing: fakePricing{lineID: "line_1"}}
	surface := api.NewTelephoneSurface(api.Flows{Pricing: pricing})

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
	surface := api.NewTelephoneSurface(api.Flows{Pricing: pricing})

	_, err := surface.AddLine(operator(), "cart_phone", "  ", "variant_1", 1)

	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, pricing.calls)
}

// TestTheSurfaceWithoutItsFlowsRefuses: an installation whose flows are not
// bound answers an error rather than a cart nobody priced.
func TestTheSurfaceWithoutItsFlowsRefuses(t *testing.T) {
	surface := api.NewTelephoneSurface(api.Flows{})

	_, err := surface.OpenCart(operator(), "TR", "", "caller@example.com")
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))

	_, err = surface.AddLine(operator(), "cart_phone", "sc_shop", "variant_1", 1)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}
