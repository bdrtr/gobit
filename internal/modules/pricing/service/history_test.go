package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// The ladder's past, rebuilt from snapshots (ADR 0167).
//
// Every case below is a way the price a shopper was charged changes: a write to
// the set, a write to a list, and a list's window opening or closing with no
// write at all. The ladder that decides each moment is [selectPrice], the one
// the present uses, so what these tests pin is the MOMENTS and the SNAPSHOT that
// stood at each.

// day is the timeline tests' unit of time, counted from historyStart.
func day(n int) time.Time { return historyStart.AddDate(0, 0, n) }

// historyStart is when the fixture's history begins.
var historyStart = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// snapshot is a set snapshot of the given prices at the given day.
func snapshot(at time.Time, prices ...models.Price) models.PriceSetSnapshot {
	return models.PriceSetSnapshot{PriceSetID: "pset_h", RecordedAt: at, Prices: prices}
}

// priced is a rule-less price at quantity one and up.
func priced(id, currency string, amount int64, list *string) models.Price {
	return models.Price{ID: id, PriceSetID: "pset_h", PriceListID: list, CurrencyCode: currency,
		Amount: amount, MinQuantity: 1, Rules: []models.PriceRule{}}
}

// listState is a list snapshot.
func listState(at time.Time, id string, kind models.PriceListType, status models.PriceListStatus,
	starts, ends *time.Time, deleted bool,
) models.PriceListSnapshot {
	return models.PriceListSnapshot{RecordedAt: at, Deleted: deleted, Info: models.PriceListInfo{
		ID: id, Type: kind, Status: status, StartsAt: starts, EndsAt: ends,
	}}
}

// amounts reads a timeline as (from-day, amount) pairs, -1 for unpriced.
func amounts(t *testing.T, timeline models.PriceTimeline) []int64 {
	t.Helper()

	out := make([]int64, 0, len(timeline.Stretches))
	for _, stretch := range timeline.Stretches {
		if !stretch.Priced {
			out = append(out, -1)

			continue
		}
		out = append(out, stretch.Amount)
	}

	return out
}

func TestAReplacedPriceIsStillThePriceThatApplied(t *testing.T) {
	t.Parallel()

	sets := []models.PriceSetSnapshot{
		snapshot(day(0), priced("price_a", "TRY", 10_000, nil)),
		snapshot(day(10), priced("price_b", "TRY", 8_000, nil)),
	}

	timeline := buildTimeline("pset_h", "TRY", sets, nil, day(0), day(20))

	require.Equal(t, []int64{10_000, 8_000}, amounts(t, timeline))
	assert.Equal(t, day(10), timeline.Stretches[1].From, "the change is at the write")
	lowest, ok := timeline.Lowest(day(0), day(20))
	require.True(t, ok)
	assert.Equal(t, int64(8_000), lowest.Amount)
	lowest, ok = timeline.Lowest(day(0), day(10))
	require.True(t, ok)
	assert.Equal(t, int64(10_000), lowest.Amount, "the window ends before the second price applied")
}

func TestASaleWindowOpensAndClosesWithoutAWrite(t *testing.T) {
	t.Parallel()

	sale := "plist_sale"
	sets := []models.PriceSetSnapshot{snapshot(day(0),
		priced("price_base", "TRY", 10_000, nil),
		priced("price_sale", "TRY", 7_000, &sale),
	)}
	lists := map[string][]models.PriceListSnapshot{sale: {
		listState(day(0), sale, models.PriceListSale, models.PriceListActive, ptr(day(5)), ptr(day(8)), false),
	}}

	timeline := buildTimeline("pset_h", "TRY", sets, lists, day(0), day(20))

	require.Equal(t, []int64{10_000, 7_000, 10_000}, amounts(t, timeline))
	assert.Equal(t, day(5), timeline.Stretches[1].From, "the sale applies from its starts_at")
	assert.Equal(t, day(8).Add(time.Nanosecond), timeline.Stretches[2].From,
		"a window's end is inclusive, so the base price returns just after it")
	assert.Equal(t, models.PriceListSale, timeline.Stretches[1].PriceListType)
	assert.Equal(t, &sale, timeline.Stretches[1].PriceListID)
}

func TestAListWriteChangesThePriceAtTheWrite(t *testing.T) {
	t.Parallel()

	sale := "plist_sale"
	sets := []models.PriceSetSnapshot{snapshot(day(0),
		priced("price_base", "TRY", 10_000, nil),
		priced("price_sale", "TRY", 7_000, &sale),
	)}
	lists := map[string][]models.PriceListSnapshot{sale: {
		listState(day(0), sale, models.PriceListSale, models.PriceListDraft, nil, nil, false),
		listState(day(3), sale, models.PriceListSale, models.PriceListActive, nil, nil, false),
		listState(day(6), sale, models.PriceListSale, models.PriceListActive, nil, nil, true),
	}}

	timeline := buildTimeline("pset_h", "TRY", sets, lists, day(0), day(10))

	require.Equal(t, []int64{10_000, 7_000, 10_000}, amounts(t, timeline),
		"a draft offers nothing, an activated list does, a deleted one stops")
	assert.Equal(t, day(3), timeline.Stretches[1].From)
	assert.Equal(t, day(6), timeline.Stretches[2].From)
}

func TestTheStorefrontPriceIgnoresRulesTiersAndOtherCurrencies(t *testing.T) {
	t.Parallel()

	ruled := priced("price_vip", "TRY", 5_000, nil)
	ruled.Rules = []models.PriceRule{{Attribute: "customer_group_id", Operator: models.OpEq, Values: []string{"vip"}}}
	tier := priced("price_bulk", "TRY", 6_000, nil)
	tier.MinQuantity = 10
	sets := []models.PriceSetSnapshot{snapshot(day(0),
		priced("price_base", "TRY", 10_000, nil),
		priced("price_eur", "EUR", 100, nil),
		ruled, tier,
	)}

	timeline := buildTimeline("pset_h", "TRY", sets, nil, day(0), day(10))

	assert.Equal(t, []int64{10_000}, amounts(t, timeline),
		"a rule needs a context the storefront does not carry, a tier needs a quantity "+
			"above one, and a euro price is not a lira one")
}

func TestAnOverrideIsAPriceAndNotAReduction(t *testing.T) {
	t.Parallel()

	override := "plist_override"
	sets := []models.PriceSetSnapshot{snapshot(day(0),
		priced("price_base", "TRY", 10_000, nil),
		priced("price_override", "TRY", 12_000, &override),
	)}
	lists := map[string][]models.PriceListSnapshot{override: {
		listState(day(0), override, models.PriceListOverride, models.PriceListActive, ptr(day(4)), nil, false),
	}}

	timeline := buildTimeline("pset_h", "TRY", sets, lists, day(0), day(10))

	assert.Equal(t, []int64{10_000, 12_000}, amounts(t, timeline),
		"the ladder ranks an override above the base whatever its amount")
	_, reduced := reductionFrom(timeline, ReductionReferenceDays)
	assert.False(t, reduced, "an override is a different price, not a reduction")
}

func TestNothingBeforeTheHistoryIsInvented(t *testing.T) {
	t.Parallel()

	sets := []models.PriceSetSnapshot{snapshot(day(10), priced("price_a", "TRY", 10_000, nil))}

	timeline := buildTimeline("pset_h", "TRY", sets, nil, day(0), day(20))

	require.NotNil(t, timeline.RecordedSince)
	assert.Equal(t, day(10), *timeline.RecordedSince)
	assert.Equal(t, day(10), timeline.Stretches[0].From, "the timeline starts where the history does")
	assert.False(t, timeline.Covers(day(0)))
	assert.True(t, timeline.Covers(day(10)))

	empty := buildTimeline("pset_h", "TRY", nil, nil, day(0), day(20))
	assert.Nil(t, empty.RecordedSince)
	assert.Empty(t, empty.Stretches)
}

// saleAfterBase is the reduction fixture: a base price that moved during the
// reference days, then a sale that began on day 40.
func saleAfterBase() (
	sets []models.PriceSetSnapshot, lists map[string][]models.PriceListSnapshot, sale string,
) {
	sale = "plist_sale"
	sets = []models.PriceSetSnapshot{
		snapshot(day(0), priced("price_base_1", "TRY", 10_000, nil)),
		snapshot(day(20), priced("price_base_2", "TRY", 9_000, nil)),
		snapshot(day(25), priced("price_base_3", "TRY", 11_000, nil)),
		snapshot(day(30),
			priced("price_base_3", "TRY", 11_000, nil),
			priced("price_sale_1", "TRY", 8_000, &sale),
		),
		snapshot(day(45),
			priced("price_base_3", "TRY", 11_000, nil),
			priced("price_sale_2", "TRY", 7_500, &sale),
		),
	}
	lists = map[string][]models.PriceListSnapshot{sale: {
		listState(day(0), sale, models.PriceListSale, models.PriceListActive, ptr(day(40)), nil, false),
	}}

	return sets, lists, sale
}

func TestAReductionNamesTheLowestPriceOfTheDaysBeforeIt(t *testing.T) {
	t.Parallel()

	sets, lists, _ := saleAfterBase()
	now := day(50)

	timeline := buildTimeline("pset_h", "TRY", sets, lists, time.Time{}, now.Add(time.Nanosecond))
	reduction, ok := reductionFrom(timeline, ReductionReferenceDays)

	require.True(t, ok)
	assert.Equal(t, "price_sale_2", reduction.PriceID, "the sale price charged now")
	require.NotNil(t, reduction.ReducedSince)
	assert.Equal(t, day(40), *reduction.ReducedSince,
		"a second, deeper cut on day 45 continues the reduction that began on day 40")
	require.NotNil(t, reduction.LowestPrior)
	assert.Equal(t, int64(9_000), *reduction.LowestPrior,
		"the reference is the LOWEST of days 10 to 40 — 9,000 from day 20 — not the "+
			"11,000 the price was raised to five days before the sale")
}

func TestAReferenceTheHistoryCannotReachIsNotAnnounced(t *testing.T) {
	t.Parallel()

	sale := "plist_sale"
	sets := []models.PriceSetSnapshot{snapshot(day(0),
		priced("price_base", "TRY", 10_000, nil),
		priced("price_sale", "TRY", 7_000, &sale),
	)}
	lists := map[string][]models.PriceListSnapshot{sale: {
		listState(day(0), sale, models.PriceListSale, models.PriceListActive, ptr(day(10)), nil, false),
	}}

	timeline := buildTimeline("pset_h", "TRY", sets, lists, time.Time{}, day(12))
	reduction, ok := reductionFrom(timeline, ReductionReferenceDays)

	require.True(t, ok)
	require.NotNil(t, reduction.ReducedSince, "the start is known: the window opened on day 10")
	assert.Nil(t, reduction.LowestPrior,
		"the thirty days before day 10 begin twenty days before the history does")

	running := buildTimeline("pset_h", "TRY",
		[]models.PriceSetSnapshot{snapshot(day(0), priced("price_sale", "TRY", 7_000, &sale))},
		map[string][]models.PriceListSnapshot{sale: {
			listState(day(0), sale, models.PriceListSale, models.PriceListActive, nil, nil, false),
		}}, time.Time{}, day(5))
	reduction, ok = reductionFrom(running, ReductionReferenceDays)
	require.True(t, ok)
	assert.Nil(t, reduction.ReducedSince,
		"a sale already running when the history began began at a moment nobody recorded")
	assert.Nil(t, reduction.LowestPrior)
}

// TestAHistoryThatDisagreesWithTheCatalogAnnouncesNothing is the storefront's
// last check before it states a reference.
//
// The reference is computed from the history and the price it is attached to
// is the one the catalog charges now. If the two disagree — the history's last
// answer is not today's sale price, which a write that skipped its snapshot
// would cause — a reference computed from it describes a catalog that is not
// this one, and it is not announced at all.
func TestAHistoryThatDisagreesWithTheCatalogAnnouncesNothing(t *testing.T) {
	t.Parallel()

	sale := "plist_sale"
	now := day(50)
	info := models.PriceListInfo{ID: sale, Type: models.PriceListSale, Status: models.PriceListActive}

	repo := newStubRepo()
	repo.priceSetHistoryFn = func(context.Context, []string) ([]models.PriceSetSnapshot, error) {
		return []models.PriceSetSnapshot{
			snapshot(day(0), priced("price_base", "TRY", 10_000, nil)),
			snapshot(day(40),
				priced("price_base", "TRY", 10_000, nil),
				priced("price_recorded_sale", "TRY", 8_000, &sale)),
		}, nil
	}
	repo.priceListHistoryFn = func(context.Context, []string) (map[string][]models.PriceListSnapshot, error) {
		return map[string][]models.PriceListSnapshot{sale: {{RecordedAt: day(0), Info: info}}}, nil
	}
	svc := newTestService(repo)

	live := priced("price_live_sale", "TRY", 7_000, &sale)
	candidates := map[string][]models.PriceCandidate{"pset_h": {
		{Price: priced("price_base", "TRY", 10_000, nil)},
		{Price: live, List: &info},
	}}

	prices, err := svc.storePrices(context.Background(), candidates, now)
	require.NoError(t, err)

	var announced *models.StorePrice
	for i := range prices["pset_h"] {
		if prices["pset_h"][i].Price.ID == live.ID {
			announced = &prices["pset_h"][i]
		}
	}
	require.NotNil(t, announced)
	assert.Equal(t, models.PriceListSale, announced.ListType, "it is still a sale price")
	assert.Nil(t, announced.Reduction,
		"the history ends in another sale price than the one charged now, so nothing it "+
			"says about the reduction is about this one")
}
