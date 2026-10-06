//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// regionBadge reads one variant's badge from the channel's listing, naming the
// shopper's region when region is not empty (ADR 0422).
//
// The listing is narrowed to the variant (ADR 0191), so a shared database's
// other products cannot push it off the first page.
func regionBadge(t *testing.T, ground channelWarehouseGround, variantID, region string) bool {
	t.Helper()

	query := url.Values{"variant_id": {variantID}}
	if region != "" {
		query.Set("region_id", region)
	}
	recorder := storeRequest(t, catalogPath(ground.channelID, "/products?"+query.Encode()), ground.key)
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

// TestTheBadgeAndTheTillAgreeOnARegion is gap D267 on the production wiring
// (ADR 0422).
//
// The units exist, in a warehouse bound to another region, and the channel
// narrows nothing. The till of a cart in the taxed region ranks no warehouse
// and refuses the order; the badge of a read naming that region must say so
// before the shopper gets there. A read naming no region keeps counting as it
// did, and one naming the warehouse's own region counts it.
func TestTheBadgeAndTheTillAgreeOnARegion(t *testing.T) {
	ctx := t.Context()
	ground := newChannelWarehouseGround(ctx, t)

	const elsewhere = "reg_e2e_region_badge_elsewhere"
	warehousePolicy(ctx, t, ground.west, 0, elsewhere)
	warehousePolicy(ctx, t, ground.east, 0, taxedRegionID)

	variantID, _ := variantAcrossWarehouses(ctx, t, "E2E Region Badge Product",
		map[string]int64{taxedCurrency: policyPrice},
		map[string]int64{ground.west: policyStockPerWarehouse})

	assert.True(t, regionBadge(t, ground, variantID, ""),
		"a read naming no region counts every warehouse the channel ships from, as before")
	assert.True(t, regionBadge(t, ground, variantID, elsewhere),
		"the stocked warehouse serves the region the read names")
	assert.False(t, regionBadge(t, ground, variantID, taxedRegionID),
		"no warehouse the checkout ranks for the taxed region holds a unit, so the page "+
			"must not offer one there")

	customerID, email := newCustomer(ctx, t)
	cartID, _ := prepareCart(ctx, t, customerID, variantID, policyQuantity)
	_, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             email,
		ExpectedTotal:     policyTotal,
	})
	require.Error(t, err, "the taxed region's till must refuse what no warehouse serving it holds")
	assert.Equal(t, fulfillmentsvc.CodeNoServiceableLocation, errors.CodeOf(err),
		"the till's reason is the one the badge now reads: no warehouse serves the region")
}
