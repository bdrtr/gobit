package service_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// The warehouses and regions of the region narrowing tests (ADR 0422).
const (
	regionTR       = "reg_tr"
	regionEU       = "reg_eu"
	warehouseTR    = "sloc_tr"
	warehouseEU    = "sloc_eu"
	warehouseAll   = "sloc_all"
	regionChannel  = "sc_region"
	regionFixtureQ = 4
)

// fakeRanker ranks as fulfillment does (ADR 0010): a warehouse bound to no
// region serves every region, one bound to some serves those alone, and a
// call whose candidates all fall out is refused with the code the checkout
// reads.
type fakeRanker struct {
	mu    sync.Mutex
	bonds map[string][]string
	err   error
	calls [][]string
}

func (f *fakeRanker) RankLocations(_ context.Context, region string, candidates []string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, slices.Clone(candidates))
	if f.err != nil {
		return nil, f.err
	}
	var ranked []string
	for _, id := range candidates {
		if bound := f.bonds[id]; len(bound) == 0 || slices.Contains(bound, region) {
			ranked = append(ranked, id)
		}
	}
	if len(ranked) == 0 {
		return nil, coreerrors.Conflict(service.CodeNoServiceableLocation, "no warehouse serves the region %s", region)
	}

	return ranked, nil
}

// regionStockFixture builds one published, counted variant whose stock sits
// in the given warehouses and whose restock forecast is the one given, read
// through a service that ranks by the fake.
func regionStockFixture(
	t *testing.T, byLocation map[string]int64, forecast map[string]time.Time,
) (*service.Service, *fakeGraph, *fakeLinker, *fakeRanker, models.Product) {
	t.Helper()

	graph := &fakeGraph{}
	linker := newFakeLinker()
	store := newMemStore()
	store.links = linker
	ranker := &fakeRanker{bonds: map[string][]string{
		warehouseTR: {regionTR},
		warehouseEU: {regionEU},
	}}
	svc, err := service.New(service.Options{Repo: store, Links: linker, Query: graph, Ranker: ranker})
	require.NoError(t, err)

	product := seedProductInput(t, svc, service.CreateProductInput{
		Handle: "region-stock", Title: "Region Stock", Status: models.StatusPublished,
		Variants: []service.CreateVariantInput{{
			Title: "Only variant", ManageInventory: ptr(true), AllowBackorder: ptr(false),
		}},
	})
	var total int64
	for _, quantity := range byLocation {
		total += quantity
	}
	graph.records = []query.Record{{
		"id":                product.Variants[0].ID,
		"inventory_item":    query.Record{"id": "invitem_region", "available_quantity": total},
		"inventory_stock":   query.Record{"available_by_location": byLocation},
		"inventory_restock": query.Record{"restock_by_location": forecast},
	}}

	return svc, graph, linker, ranker, product
}

// regionBadge lists the storefront for a region and the channels given and
// answers the only variant.
func regionBadge(t *testing.T, svc *service.Service, region string, channels ...string) service.StoreVariant {
	t.Helper()

	opts := service.StoreListOptions{RegionID: region}
	if channels != nil {
		opts.SalesChannelIDs = channels
	}
	result, err := svc.ListStoreProducts(context.Background(), opts)
	require.NoError(t, err)
	require.Len(t, result.Items, 1)
	require.Len(t, result.Items[0].Variants, 1)

	return result.Items[0].Variants[0]
}

// TestTheBadgeCountsTheWarehousesThatServeTheRegion is gap D267 (ADR 0422): the
// units sit in a warehouse bound to the EU, and a TR shopper's checkout ranks
// none of them, so a read naming TR must not say in stock; one naming the EU,
// or no region at all, still does.
func TestTheBadgeCountsTheWarehousesThatServeTheRegion(t *testing.T) {
	t.Parallel()

	svc, graph, _, ranker, _ := regionStockFixture(t, map[string]int64{warehouseEU: regionFixtureQ}, nil)

	assert.False(t, regionBadge(t, svc, regionTR).InStock,
		"a TR shopper's till ranks no warehouse holding a unit")
	assert.True(t, regionBadge(t, svc, regionEU).InStock, "the EU warehouse serves the EU")
	calls := len(ranker.calls)
	assert.True(t, regionBadge(t, svc, "").InStock, "a read naming no region counts as before")
	assert.Len(t, ranker.calls, calls, "a read naming no region asks fulfillment nothing")

	asked := false
	for _, expansion := range graph.specs[0].Expand {
		asked = asked || expansion.As == "inventory_stock"
	}
	assert.True(t, asked, "a read naming a region reads the breakdown even when no channel binds warehouses")
}

// TestAWarehouseBoundToNoRegionServesEveryRegion is the checkout's other half:
// the unbound warehouse is ranked for TR, so its units count there.
func TestAWarehouseBoundToNoRegionServesEveryRegion(t *testing.T) {
	t.Parallel()

	svc, _, _, _, _ := regionStockFixture(t, map[string]int64{warehouseAll: 1, warehouseEU: regionFixtureQ}, nil)

	assert.True(t, regionBadge(t, svc, regionTR).InStock, "the unbound warehouse serves TR")
}

// TestTheRegionNarrowsTheChannelsWarehousesOnly holds the order of the two
// narrowings: the region chooses among the warehouses the channel ships from,
// so a warehouse serving the region but outside the channel is not counted.
func TestTheRegionNarrowsTheChannelsWarehousesOnly(t *testing.T) {
	t.Parallel()

	svc, _, linker, _, _ := regionStockFixture(t, map[string]int64{warehouseEU: regionFixtureQ}, nil)
	bindWarehouseToChannel(t, linker, warehouseTR, regionChannel)

	assert.False(t, regionBadge(t, svc, regionEU, regionChannel).InStock,
		"the EU warehouse serves the EU, and the channel does not ship from it")
}

// TestARankerFailureKeepsTheChannelsAnswer: fulfillment's ranking failing for
// another reason leaves the read as if it named no region, as a binding that
// cannot be read does, rather than taking every product off sale.
func TestARankerFailureKeepsTheChannelsAnswer(t *testing.T) {
	t.Parallel()

	svc, _, _, ranker, _ := regionStockFixture(t, map[string]int64{warehouseEU: regionFixtureQ}, nil)
	ranker.err = coreerrors.Unavailable("fulfillment_down", "the ranking is down")

	assert.True(t, regionBadge(t, svc, regionTR).InStock, "a failed ranking keeps the channel's answer")
}

// TestAnInstallationWithoutFulfillmentNarrowsNothing: no ranking bound is no
// bond to read, so a region narrows nothing.
func TestAnInstallationWithoutFulfillmentNarrowsNothing(t *testing.T) {
	t.Parallel()

	svc, _, _, ranker, _ := regionStockFixture(t, map[string]int64{warehouseEU: regionFixtureQ}, nil)
	ranker.err = coreerrors.Unavailable(service.CodeRankerUnavailable, "not bound")

	assert.True(t, regionBadge(t, svc, regionTR).InStock)
}

// TestTheInStockFilterFollowsTheRegion: the filter reads the badge the read
// computed, so in_stock=true and a TR region leave the variant out.
func TestTheInStockFilterFollowsTheRegion(t *testing.T) {
	t.Parallel()

	svc, _, _, _, _ := regionStockFixture(t, map[string]int64{warehouseEU: regionFixtureQ}, nil)
	inStock := true

	tr, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{InStock: &inStock, RegionID: regionTR})
	require.NoError(t, err)
	assert.Empty(t, tr.Items, "nothing sellable to a TR shopper")

	eu, err := svc.ListStoreProducts(context.Background(), service.StoreListOptions{InStock: &inStock, RegionID: regionEU})
	require.NoError(t, err)
	assert.Len(t, eu.Items, 1)
}

// TestTheSingleReadNarrowsByRegion: the product page takes the region too.
func TestTheSingleReadNarrowsByRegion(t *testing.T) {
	t.Parallel()

	svc, _, _, _, product := regionStockFixture(t, map[string]int64{warehouseEU: regionFixtureQ}, nil)

	tr, err := svc.GetStoreProduct(context.Background(), product.ID, nil, regionTR)
	require.NoError(t, err)
	assert.False(t, tr.Variants[0].InStock)
	eu, err := svc.GetStoreProduct(context.Background(), product.ID, nil, " "+regionEU+" ")
	require.NoError(t, err)
	assert.True(t, eu.Variants[0].InStock, "the id is trimmed")
}

// TestARegionTheRankingCannotJudgeDatesAsTheChannelDoes holds the fallback
// together: a read naming a region fulfillment cannot judge counts the
// channel's answer for its badge, and its restock date must count the same
// set, every warehouse when no channel binds any. The forecast's warehouse is
// outside the breakdown here, which is the case a narrowed read would judge
// in a second call.
func TestARegionTheRankingCannotJudgeDatesAsTheChannelDoes(t *testing.T) {
	t.Parallel()

	svc, _, _, ranker, _ := regionStockFixture(t, map[string]int64{},
		map[string]time.Time{warehouseEU: restockSoon, warehouseTR: restockLater})
	ranker.err = coreerrors.Unavailable(service.CodeRankerUnavailable, "not bound")

	tr := regionBadge(t, svc, regionTR)
	require.NotNil(t, tr.RestockExpectedAt,
		"a region nobody could judge narrows nothing, so the earliest receipt anywhere dates it")
	assert.Equal(t, restockSoon, *tr.RestockExpectedAt)
}

// TestARankingFailureDatesOverTheChannelsWarehouses is the same fallback with
// a channel bound to one warehouse: the date is the channel's, neither every
// warehouse's nor none. It holds whether the breakdown names no warehouse, so
// the first judgment needs no call and the date's own call is the one that
// fails, or names the channel's warehouse with nothing on sale, so the badge's
// call fails first and the read is not narrowed at all.
func TestARankingFailureDatesOverTheChannelsWarehouses(t *testing.T) {
	t.Parallel()

	for name, breakdown := range map[string]map[string]int64{
		"the breakdown names no warehouse":         {},
		"the breakdown names the channel's, empty": {warehouseTR: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			svc, _, linker, ranker, _ := regionStockFixture(t, breakdown,
				map[string]time.Time{warehouseEU: restockSoon, warehouseTR: restockLater})
			bindWarehouseToChannel(t, linker, warehouseTR, regionChannel)
			ranker.err = coreerrors.Unavailable("fulfillment_down", "the ranking is down")

			tr := regionBadge(t, svc, regionEU, regionChannel)
			assert.False(t, tr.InStock, "nothing is on sale at the channel's warehouse")
			require.NotNil(t, tr.RestockExpectedAt, "the channel's warehouse expects units")
			assert.Equal(t, restockLater, *tr.RestockExpectedAt,
				"the channel ships from the TR warehouse alone, whatever region the read named")
		})
	}
}

// TestARestockDateFollowsTheRegion: nothing is on sale, units are expected
// sooner at the EU warehouse and later at the TR one, and neither warehouse is
// in the breakdown (inventory leaves an empty one out). A TR shopper is dated
// by the TR warehouse; an EU one by the EU warehouse.
func TestARestockDateFollowsTheRegion(t *testing.T) {
	t.Parallel()

	svc, _, _, _, _ := regionStockFixture(t, map[string]int64{},
		map[string]time.Time{warehouseEU: restockSoon, warehouseTR: restockLater})

	tr := regionBadge(t, svc, regionTR)
	require.NotNil(t, tr.RestockExpectedAt, "units are on their way to a warehouse serving TR")
	assert.Equal(t, restockLater, *tr.RestockExpectedAt,
		"the EU warehouse's earlier receipt does not reach a TR shopper")

	eu := regionBadge(t, svc, regionEU)
	require.NotNil(t, eu.RestockExpectedAt)
	assert.Equal(t, restockSoon, *eu.RestockExpectedAt)
}
