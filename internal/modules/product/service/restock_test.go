package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// This file holds the storefront half of ADR 0399: a counted variant with
// nothing to sell at the request's warehouses carries the first moment
// inventory expects units on sale there again.

// The moments inventory's forecast answers in these tests.
var (
	restockSoon  = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	restockLater = time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)
)

// restockSpec is one variant of the restock fixture.
type restockSpec struct {
	backorder bool
	// uncounted is a variant whose stock is not counted (ADR 0040).
	uncounted bool
	// available is the item's sellable total; byLocation its breakdown.
	available  int64
	byLocation map[string]int64
	// forecast is what inventory's restock field answers for the item.
	forecast map[string]time.Time
}

// restockFixture builds one published, counted variant per spec, each linked
// to an item, read through a fake graph that answers every expansion.
func restockFixture(t *testing.T, specs ...restockSpec) (*service.Service, *fakeGraph, *fakeLinker, []string) {
	t.Helper()

	graph := &fakeGraph{}
	linker := newFakeLinker()
	svc := newService(t, newMemStore(), linker, graph)

	ids := make([]string, 0, len(specs))
	for i, spec := range specs {
		product := seedProductInput(t, svc, service.CreateProductInput{
			Handle: "restock-" + string(rune('a'+i)), Title: "Restock", Status: models.StatusPublished,
			Variants: []service.CreateVariantInput{{
				Title: "Only", ManageInventory: ptr(!spec.uncounted), AllowBackorder: ptr(spec.backorder),
			}},
		})
		id := product.Variants[0].ID
		ids = append(ids, id)
		graph.records = append(graph.records, query.Record{
			"id":                id,
			"inventory_item":    query.Record{"id": "invitem_" + id, "available_quantity": spec.available},
			"inventory_stock":   query.Record{"available_by_location": spec.byLocation},
			"inventory_restock": query.Record{"restock_by_location": spec.forecast},
		})
	}

	return svc, graph, linker, ids
}

// variantsByID lists the storefront and indexes its variants.
func variantsByID(t *testing.T, svc *service.Service, channels ...string) map[string]service.StoreVariant {
	t.Helper()

	result, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{SalesChannelIDs: channels})
	require.NoError(t, err)
	out := map[string]service.StoreVariant{}
	for i := range result.Items {
		for j := range result.Items[i].Variants {
			out[result.Items[i].Variants[j].ID] = result.Items[i].Variants[j]
		}
	}

	return out
}

// TestARestockDateIsTheEarliestAtTheServedWarehouses: a narrowed read takes the
// earliest date over the channel's warehouses and ignores another warehouse's
// earlier one; an unnarrowed read takes the earliest anywhere.
func TestARestockDateIsTheEarliestAtTheServedWarehouses(t *testing.T) {
	t.Parallel()

	svc, _, linker, ids := restockFixture(t, restockSpec{
		forecast: map[string]time.Time{servedWarehouse: restockLater, otherWarehouse: restockSoon},
	})
	bindWarehouseToChannel(t, linker, servedWarehouse, badgeChannel)

	narrowed := variantsByID(t, svc, badgeChannel)[ids[0]]
	require.NotNil(t, narrowed.RestockExpectedAt, "nothing to sell, and units on their way")
	assert.Equal(t, restockLater, *narrowed.RestockExpectedAt,
		"units on their way to a warehouse the channel cannot ship from are not its restock")

	everywhere := variantsByID(t, svc)[ids[0]]
	require.NotNil(t, everywhere.RestockExpectedAt)
	assert.Equal(t, restockSoon, *everywhere.RestockExpectedAt, "an unnarrowed read counts every warehouse")
}

// TestOnlyAVariantWithNothingToSellAsks: a variant in stock gets no date, one
// sold past zero with nothing on the shelf does, and the date lives on the
// variant rather than in the published inventory record.
func TestOnlyAVariantWithNothingToSellAsks(t *testing.T) {
	t.Parallel()

	forecast := map[string]time.Time{servedWarehouse: restockSoon}
	svc, graph, _, ids := restockFixture(t,
		restockSpec{available: 3, forecast: forecast},
		restockSpec{backorder: true, forecast: forecast},
	)

	variants := variantsByID(t, svc)
	assert.Nil(t, variants[ids[0]].RestockExpectedAt, "a variant with something to sell has no restock")
	backordered := variants[ids[1]]
	assert.True(t, backordered.InStock, "sold past zero is in stock by ADR 0040")
	require.NotNil(t, backordered.RestockExpectedAt, "and still has nothing on the shelf")
	assert.Equal(t, restockSoon, *backordered.RestockExpectedAt)
	assert.NotContains(t, backordered.InventoryItem, "restock_by_location", "the forecast is not published")

	require.Equal(t, 2, graph.callCount())
	asked, ok := graph.lastSpec(t).Filters["ids"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{ids[1]}, asked, "only the variant with nothing to sell asks")
	require.Len(t, graph.lastSpec(t).Expand, 1)
	assert.Equal(t, []string{"restock_by_location"}, graph.lastSpec(t).Expand[0].Fields,
		"the forecast is named, never a default field")
}

// TestAPageInStockPaysNothing: a page where every variant has something to
// sell makes no second graph call.
func TestAPageInStockPaysNothing(t *testing.T) {
	t.Parallel()

	svc, graph, _, _ := restockFixture(t, restockSpec{available: 1}, restockSpec{available: 4})

	variantsByID(t, svc)
	assert.Equal(t, 1, graph.callCount(), "no variant asked, so no call")
}

// TestAFailedForecastLeavesTheDateOutAndTheCatalogUp: the date is a display
// concern, so a forecast that cannot be read costs the date and nothing else.
func TestAFailedForecastLeavesTheDateOutAndTheCatalogUp(t *testing.T) {
	t.Parallel()

	svc, graph, _, ids := restockFixture(t, restockSpec{forecast: map[string]time.Time{servedWarehouse: restockSoon}})
	graph.errOnCall, graph.callErr = 2, errors.New("inventory is unreachable")

	variants := variantsByID(t, svc)
	require.Contains(t, variants, ids[0], "the catalog still answers")
	assert.Nil(t, variants[ids[0]].RestockExpectedAt)
	assert.Equal(t, 2, graph.callCount())
}

// restockAsks is every id the graph calls asked a restock date for.
func restockAsks(t *testing.T, graph *fakeGraph) []string {
	t.Helper()

	graph.mu.Lock()
	defer graph.mu.Unlock()
	var out []string
	for _, spec := range graph.specs {
		for _, expansion := range spec.Expand {
			if expansion.As != "inventory_restock" {
				continue
			}
			ids, ok := spec.Filters["ids"].([]string)
			require.True(t, ok)
			out = append(out, ids...)
		}
	}

	return out
}

// TestAnUncountedVariantAsksNothing: a variant whose stock is not counted is
// in stock by ADR 0040 whatever its linked item holds, so it shows no date and
// is not asked for one.
func TestAnUncountedVariantAsksNothing(t *testing.T) {
	t.Parallel()

	svc, graph, _, ids := restockFixture(t, restockSpec{
		uncounted: true, forecast: map[string]time.Time{servedWarehouse: restockSoon},
	})

	variant := variantsByID(t, svc)[ids[0]]
	assert.True(t, variant.InStock)
	assert.Nil(t, variant.RestockExpectedAt, "an uncounted variant carries no date")
	assert.Empty(t, restockAsks(t, graph))
}

// TestTheBadgeReadAsksNoDate: the stock alert reads the badge alone (ADR 0215),
// so a variant with nothing to sell costs it no forecast.
func TestTheBadgeReadAsksNoDate(t *testing.T) {
	t.Parallel()

	svc, graph, _, ids := restockFixture(t, restockSpec{forecast: map[string]time.Time{servedWarehouse: restockSoon}})

	badges, err := svc.VariantsInStock(context.Background(), ids, nil)
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{ids[0]: false}, badges)
	assert.Empty(t, restockAsks(t, graph), "the badge read asks no restock date")
	assert.Equal(t, 1, graph.callCount())
}

// TestAProductReadByIDIsDated: search, related products and add-ons publish
// what [service.Service.StoreProductsByIDs] answers, so it carries the date.
func TestAProductReadByIDIsDated(t *testing.T) {
	t.Parallel()

	svc, _, _, ids := restockFixture(t, restockSpec{forecast: map[string]time.Time{servedWarehouse: restockSoon}})
	listed := variantsByID(t, svc)[ids[0]]

	products, err := svc.StoreProductsByIDs(context.Background(), []string{listed.ProductID}, nil)
	require.NoError(t, err)
	require.Len(t, products, 1)
	require.NotNil(t, products[0].Variants[0].RestockExpectedAt)
	assert.Equal(t, restockSoon, *products[0].Variants[0].RestockExpectedAt)
}

// TestAFilteredListingDatesOnlyThePageItReturns: the scan enriches whole
// chunks and keeps what matches, and only the products it returns are asked
// for a date, in one call for the page.
func TestAFilteredListingDatesOnlyThePageItReturns(t *testing.T) {
	t.Parallel()

	forecast := map[string]time.Time{servedWarehouse: restockSoon}
	svc, graph, _, _ := restockFixture(t,
		restockSpec{forecast: forecast}, restockSpec{forecast: forecast}, restockSpec{available: 2})

	inStock := true
	result, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{InStock: &inStock})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	assert.Empty(t, restockAsks(t, graph), "the products the filter dropped are not dated")

	soldOut := false
	result, err = svc.ListStoreProducts(context.Background(), service.StoreListOptions{InStock: &soldOut, Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	shown := result.Items[0].Variants[0]
	require.NotNil(t, shown.RestockExpectedAt, "the product returned is dated")
	assert.Equal(t, []string{shown.ID}, restockAsks(t, graph),
		"only the product returned asks, though the chunk held two with nothing to sell")
}

// TestABundleWithARecordOfItsOwnShowsNoRestock: a bundle's badge is read from
// its parts, so a record of its own -- which a link landing beside the
// composition can leave (ADR 0234) -- dates nothing and asks nothing.
func TestABundleWithARecordOfItsOwnShowsNoRestock(t *testing.T) {
	t.Parallel()

	fx := newBoxFixture(t, stocked(5), stocked(5))
	fx.graph.records = append(fx.graph.records[:0:0], fx.graph.records...)
	fx.graph.records[0] = query.Record{
		"id":             fx.box,
		"inventory_item": query.Record{"id": "invitem_box", "available_quantity": int64(0)},
		"inventory_restock": query.Record{
			"restock_by_location": map[string]time.Time{servedWarehouse: restockSoon},
		},
	}

	variants := variantsByID(t, fx.svc)
	assert.True(t, variants[fx.box].InStock, "its parts are in stock")
	assert.Nil(t, variants[fx.box].RestockExpectedAt, "a bundle carries no date")
	assert.Empty(t, restockAsks(t, fx.graph))
}

// TestABundleShowsNoRestock: a bundle has no item of its own, and its date
// would be its limiting parts' at their quantities, which no forecast answers.
func TestABundleShowsNoRestock(t *testing.T) {
	t.Parallel()

	fx := newBoxFixture(t, stocked(0), stocked(0))
	fx.graph.records = append(fx.graph.records[:0:0], fx.graph.records...)
	for i := range fx.graph.records {
		fx.graph.records[i]["inventory_restock"] = query.Record{
			"restock_by_location": map[string]time.Time{servedWarehouse: restockSoon},
		}
	}

	variants := variantsByID(t, fx.svc)
	assert.Nil(t, variants[fx.box].RestockExpectedAt, "a bundle carries no date")
	require.NotNil(t, variants[fx.towel].RestockExpectedAt, "its part, on its own page, does")
	asked, ok := fx.graph.lastSpec(t).Filters["ids"].([]string)
	require.True(t, ok)
	assert.NotContains(t, asked, fx.box)
}
