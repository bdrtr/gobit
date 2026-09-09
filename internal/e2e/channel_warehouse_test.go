//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// channelWarehouseGround is a storefront of its own, with its own key.
type channelWarehouseGround struct {
	channelID string
	key       string
	east      string
	west      string
}

// newChannelWarehouseGround builds a channel nobody else uses and two
// warehouses.
//
// The channel is NOT the shared one: binding a warehouse to it would narrow
// every other scenario in this package that completes a cart, and the failure
// would land in a test that never mentions warehouses.
func newChannelWarehouseGround(ctx context.Context, t *testing.T) channelWarehouseGround {
	t.Helper()

	channel, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        "E2E Channel Warehouse " + t.Name(),
		Description: "a storefront that ships from one warehouse",
	})
	require.NoError(t, err, "the fixture sales channel could not be created")

	_, key, err := authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:            models.APIKeyPublishable,
		Title:           "e2e channel warehouse key",
		CreatedBy:       adminID,
		SalesChannelIDs: []string{channel.ID},
	})
	require.NoError(t, err, "the fixture publishable key could not be created")

	return channelWarehouseGround{
		channelID: channel.ID,
		key:       key,
		east:      newWarehouse(ctx, t, "E2E Channel East"),
		west:      newWarehouse(ctx, t, "E2E Channel West"),
	}
}

// bindWarehouseToChannel binds through the ADMIN ENDPOINT rather than through
// the link service.
//
// The split is the one the channel-catalog fixture makes for the same reason:
// writing the binding from the service would not prove that the endpoint writes
// THE VERY link the checkout reads. It could write another one and this test
// would stay green.
func bindWarehouseToChannel(t *testing.T, warehouseID, channelID string) {
	t.Helper()

	bound, err := adminRequestWithBody(http.MethodPost,
		"/admin/v1/stock-locations/"+warehouseID+"/sales-channels",
		map[string]any{"sales_channel_id": channelID})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, bound.Code,
		"the binding could not be written; body: %s", bound.Body.String())
}

// TestAnOrderIsReservedOnlyFromTheWarehousesItsChannelShipsFrom is the whole
// point of the binding, over HTTP.
//
// The stock is in the WEST warehouse and the storefront ships from the EAST
// one. Everything else is in working order — the units exist, the cart is
// priced, the payment provider answers — so the only thing that can refuse the
// order is the binding, and the only thing that can let it through afterwards
// is the second binding.
//
// If it stops holding, the shop sells from a warehouse it meant to keep for
// another storefront, and finds out when the parcel is picked.
func TestAnOrderIsReservedOnlyFromTheWarehousesItsChannelShipsFrom(t *testing.T) {
	ctx := t.Context()
	ground := newChannelWarehouseGround(ctx, t)

	variantID, stockItemID := variantAcrossWarehouses(ctx, t, "E2E Channel Bound Product",
		map[string]int64{taxedCurrency: happyUnitPrice},
		map[string]int64{ground.west: happyInitialStock})

	bindWarehouseToChannel(t, ground.east, ground.channelID)

	customerID, _ := newCustomer(ctx, t)
	cartID, totals := prepareCart(ctx, t, customerID, variantID, happyQuantity)

	// --- 1) the BADGE already says so, before anyone tries to buy ---
	//
	// This is the half that keeps the shopper out of a doomed checkout: the
	// listing is read with the same key the order will carry, and it counts the
	// warehouses that key's channel ships from.
	assert.False(t, badgeInStock(t, ground, variantID),
		"the storefront must not offer units it cannot ship; the stock is in the west "+
			"warehouse and this channel ships from the east")

	// --- 2) the units exist, in the wrong warehouse ---
	refused := keyedStorefrontRequest(t, ground.key, http.MethodPost,
		"/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, totals.Total))

	require.Equal(t, http.StatusConflict, refused.Code,
		"an order whose stock sits outside its channel's warehouses must be REFUSED; "+
			"body: %s", refused.Body.String())
	assert.Contains(t, refused.Body.String(), checkoutwf.CodeChannelHasNoStock,
		"the refusal has to say WHICH of the two it is: the shop is not out of stock, the "+
			"stock is in a warehouse this channel may not ship from")

	assert.Equal(t, happyInitialStock, sellableQuantity(ctx, t, stockItemID),
		"a refused order must reserve nothing")

	// --- 3) the merchant binds the warehouse that holds the units ---
	bindWarehouseToChannel(t, ground.west, ground.channelID)

	// --- 4) and the badge turns with it, from the same binding ---
	assert.True(t, badgeInStock(t, ground, variantID),
		"the badge and the checkout read the SAME binding; if they disagree, one of them "+
			"is lying to the shopper")

	done := keyedStorefrontRequest(t, ground.key, http.MethodPost,
		"/store/v1/carts/"+cartID+"/complete",
		storefrontCompletionBody(t, totals.Total))
	require.Equal(t, http.StatusOK, done.Code,
		"with the warehouse bound the same cart goes through; body: %s", done.Body.String())

	assert.Equal(t, happyInitialStock-happyQuantity,
		sellableQuantity(ctx, t, stockItemID),
		"the units left the warehouse the channel now ships from")

	// The sale is CONFIRMED by the end of the saga, so what the west warehouse
	// shows is a smaller physical count rather than a held one — and the count
	// that moved names the warehouse the units really left.
	west := warehouseLevel(ctx, t, stockItemID, ground.west)
	assert.Equal(t, happyInitialStock-happyQuantity, west.StockedQuantity,
		"the units left the WEST warehouse, which is the one that was bound second")
	assert.Zero(t, west.ReservedQuantity,
		"a completed order holds nothing: the promise became a deduction")
}

// badgeInStock reads the storefront listing with the ground's own key and
// reports whether the variant is offered as sellable.
//
// It goes over HTTP, through the channel-scoped path, because that is where the
// answer is assembled: the handler puts the request's channels into the read
// and the badge is computed from the warehouses they ship from.
func badgeInStock(t *testing.T, ground channelWarehouseGround, variantID string) bool {
	t.Helper()

	recorder := magazaIstegi(t, catalogPath(ground.channelID, "/products"), ground.key)
	require.Equal(t, http.StatusOK, recorder.Code,
		"the storefront listing must answer 200; body: %s", recorder.Body.String())

	var envelope struct {
		Data []struct {
			Variants []struct {
				ID      string `json:"id"`
				InStock bool   `json:"in_stock"`
			} `json:"variants"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope),
		"the listing could not be decoded; body: %s", recorder.Body.String())

	for _, product := range envelope.Data {
		for _, variant := range product.Variants {
			if variant.ID == variantID {
				return variant.InStock
			}
		}
	}

	t.Fatalf("variant %s is not in the channel's listing at all", variantID)

	return false
}
