package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The warehouses and the storefront of the narrowing tests.
const (
	// servedWarehouse is the warehouse the channel ships from.
	servedWarehouse = "sloc_east"
	// otherWarehouse holds stock the channel may not sell.
	otherWarehouse = "sloc_west"
	// badgeChannel is the storefront the read is scoped to.
	badgeChannel = "sc_web"
)

// channelStockFixture builds one published, COUNTED variant whose stock sits in
// the given warehouses, and a linker that can bind them to a channel.
//
// The variant is counted on purpose: an uncounted one is sellable by the first
// clause of ADR 0040 alone, so a fixture built out of those could not tell a
// badge that reads the warehouses from one that never looks at them.
func channelStockFixture(
	t *testing.T, byLocation map[string]int64,
) (storeFixture, *fakeLinker) {
	t.Helper()

	graph := &fakeGraph{}
	linker := newFakeLinker()
	svc := newService(t, newMemStore(), linker, graph)

	product := seedProductInput(t, svc, service.CreateProductInput{
		Handle: "channel-stock", Title: "Channel Stock", Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{
			Title: "Only variant", ManageInventory: ptr(true), AllowBackorder: ptr(false),
		}},
	})
	require.Len(t, product.Variants, 1)

	var total int64
	for _, quantity := range byLocation {
		total += quantity
	}

	graph.records = []query.Record{{
		"id": product.Variants[0].ID,
		// What the storefront publishes: the item record, with the total over
		// EVERY warehouse — which is exactly the number that must stop being
		// the badge's answer once a channel narrows the read.
		"inventory_item": query.Record{
			"id": "invitem_1", "available_quantity": total,
		},
		// The second expansion's record, under a key of its own.
		"inventory_stock": query.Record{
			"available_by_location": byLocation,
		},
	}}

	return storeFixture{svc: svc, graph: graph, products: []models.Product{product}},
		linker
}

// bindWarehouseToChannel binds through the fake link service.
func bindWarehouseToChannel(t *testing.T, linker *fakeLinker, warehouseID, channelID string) {
	t.Helper()

	// The cardinality comes from the INVENTORY module's declaration, which this
	// package cannot read; it is set by hand so the fake enforces the real
	// constraint rather than a stricter one it invented.
	linker.cardinality[service.LinkStockLocationSalesChannel] = link.ManyToMany
	require.NoError(t, linker.Create(context.Background(),
		service.LinkStockLocationSalesChannel, warehouseID, channelID))
}

// TestTheBadgeCountsOnlyTheChannelsWarehouses is the inconsistency ADR 0092
// left behind, closed.
//
// The units exist and the storefront may not sell them: they sit in a warehouse
// the channel does not ship from, and the checkout refuses an order for exactly
// that reason. A badge that still said "in stock" would send the shopper all
// the way to the last step to be turned down.
func TestTheBadgeCountsOnlyTheChannelsWarehouses(t *testing.T) {
	t.Parallel()

	fx, linker := channelStockFixture(t, map[string]int64{otherWarehouse: 7})
	bindWarehouseToChannel(t, linker, servedWarehouse, badgeChannel)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		SalesChannelIDs: []string{badgeChannel},
	})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Len(t, result.Items[0].Variants, 1)

	assert.False(t, result.Items[0].Variants[0].InStock,
		"seven units in a warehouse this storefront cannot ship from are not seven units "+
			"the shopper can buy")
	assert.False(t, result.Items[0].InStock,
		"a one-variant product carries its variant's answer")
}

// TestTheBadgeCountsTheServedWarehouse is the other direction: the same stock,
// in the warehouse the channel ships from.
func TestTheBadgeCountsTheServedWarehouse(t *testing.T) {
	t.Parallel()

	fx, linker := channelStockFixture(t, map[string]int64{
		servedWarehouse: 2, otherWarehouse: 7,
	})
	bindWarehouseToChannel(t, linker, servedWarehouse, badgeChannel)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		SalesChannelIDs: []string{badgeChannel},
	})
	require.NoError(t, err)
	require.True(t, result.Items[0].Variants[0].InStock)
}

// TestAReadWithNoChannelCountsEveryWarehouse keeps the administrative and
// unscoped reads exactly as they were.
func TestAReadWithNoChannelCountsEveryWarehouse(t *testing.T) {
	t.Parallel()

	fx, linker := channelStockFixture(t, map[string]int64{otherWarehouse: 7})
	bindWarehouseToChannel(t, linker, servedWarehouse, badgeChannel)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{})
	require.NoError(t, err)

	assert.True(t, result.Items[0].Variants[0].InStock,
		"with no channel in the request there is nothing to narrow to")

	require.NotEmpty(t, fx.graph.specs)
	for _, expansion := range fx.graph.specs[0].Expand {
		assert.NotEqual(t, "inventory_stock", expansion.As,
			"an unnarrowed read must not pay for the breakdown at all")
	}
}

// TestAChannelBoundToNoWarehouseNarrowsNothing is the reading that keeps every
// existing shop working.
//
// A channel with no warehouse bound is one nobody has configured, not one that
// ships from nowhere — the same sentence the checkout is built on.
func TestAChannelBoundToNoWarehouseNarrowsNothing(t *testing.T) {
	t.Parallel()

	fx, _ := channelStockFixture(t, map[string]int64{otherWarehouse: 7})

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		SalesChannelIDs: []string{badgeChannel},
	})
	require.NoError(t, err)
	assert.True(t, result.Items[0].Variants[0].InStock)
}

// TestTheWarehouseTopologyNeverReachesTheResponse holds the field inside the
// module.
//
// [service.StoreVariant.InventoryItem] IS published to the shopper. Which
// warehouse holds how many units is the shop's business and nobody else's, so
// the breakdown arrives under a key of its own and only the badge reads it.
func TestTheWarehouseTopologyNeverReachesTheResponse(t *testing.T) {
	t.Parallel()

	fx, linker := channelStockFixture(t, map[string]int64{servedWarehouse: 2})
	bindWarehouseToChannel(t, linker, servedWarehouse, badgeChannel)

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		SalesChannelIDs: []string{badgeChannel},
	})
	require.NoError(t, err)

	published := result.Items[0].Variants[0].InventoryItem
	require.NotNil(t, published, "the item record is still published")
	assert.NotContains(t, published, "available_by_location",
		"the warehouse breakdown must not leave the module")
	assert.Contains(t, published, "available_quantity",
		"and the total the storefront has always published still does")
}

// TestAnUnreadableBindingDegradesTheBadgeInsteadOfTheCatalog names the
// asymmetry with the checkout deliberately.
//
// Here the narrowing makes a badge more honest; there it decides where goods
// come from. So a link service that cannot be reached costs a slightly generous
// badge and a line in the log, rather than a catalog nobody can read — and the
// order that would follow is still refused by the checkout.
func TestAnUnreadableBindingDegradesTheBadgeInsteadOfTheCatalog(t *testing.T) {
	t.Parallel()

	fx, linker := channelStockFixture(t, map[string]int64{otherWarehouse: 7})
	bindWarehouseToChannel(t, linker, servedWarehouse, badgeChannel)
	linker.listErr = errors.New("the link service is down")

	result, err := fx.svc.ListStoreProducts(context.Background(), service.StoreListOptions{
		SalesChannelIDs: []string{badgeChannel},
	})

	require.NoError(t, err, "a display concern must not take the catalog down")
	assert.True(t, result.Items[0].Variants[0].InStock,
		"with no answer the badge falls back to the total it published before")
}
