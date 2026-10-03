package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// newTestProvider builds a provider with its prices in place, and its
// repository.
func newTestProvider(t *testing.T) (*QueryProvider, *stubRepo) {
	t.Helper()

	repo := newStubRepo()
	repo.getPriceSetsByIDsFn = func(_ context.Context, ids []string) ([]models.PriceSet, error) {
		sets := make([]models.PriceSet, 0, len(ids))
		for _, id := range ids {
			if id == "pset_missing" {
				continue
			}
			sets = append(sets, models.PriceSet{ID: id, CreatedAt: testNow, UpdatedAt: testNow})
		}
		return sets, nil
	}
	repo.listCandidatesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
		out := map[string][]models.PriceCandidate{}
		for _, id := range ids {
			out[id] = []models.PriceCandidate{{Price: models.Price{
				ID:           "price_" + id,
				PriceSetID:   id,
				CurrencyCode: "TRY",
				Amount:       19900,
				MinQuantity:  1,
			}}}
		}
		return out, nil
	}
	return NewQueryProvider(newTestService(repo)), repo
}

// TestProviderEntity proves the entity name the provider is registered under.
// If the name changes, product's expansion breaks at run time.
func TestProviderEntity(t *testing.T) {
	provider, _ := newTestProvider(t)
	assert.Equal(t, "price_set", provider.Entity())
	assert.Equal(t, "price_set.query", Entity+query.ProviderSuffix)
}

// TestProviderFetchByIDsIncludesPrices proves that records come back WITH THEIR
// PRICES; product's store listing rests on this.
func TestProviderFetchByIDsIncludesPrices(t *testing.T) {
	provider, _ := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{"pset_1"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "pset_1", records[0][query.IDField])

	prices, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok, "the prices field has to be a slice of sub-records")
	require.Len(t, prices, 1)
	assert.Equal(t, "TRY", prices[0]["currency_code"])
	assert.Equal(t, int64(19900), prices[0]["amount"])
}

// TestProviderFetchByIDsIsBatched proves that the number of calls stays
// constant and does NOT grow with the number of ids; that is the Query layer's
// N+1 ban (ADR 0004).
func TestProviderFetchByIDsIsBatched(t *testing.T) {
	provider, repo := newTestProvider(t)

	var gotIDs []string
	repo.listCandidatesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
		gotIDs = ids
		return map[string][]models.PriceCandidate{}, nil
	}

	ids := []string{"pset_1", "pset_2", "pset_3", "pset_4", "pset_5"}
	records, err := provider.FetchByIDs(context.Background(), ids, nil)

	require.NoError(t, err)
	assert.Len(t, records, 5)
	assert.Equal(t, 1, repo.calls["GetPriceSetsByIDs"], "the containers have to be read in one query")
	assert.Equal(t, 1, repo.calls["ListPriceCandidatesBySets"], "the prices have to be read in one query")
	assert.Equal(t, ids, gotIDs, "every id has to be passed in one call")
}

// TestProviderFetchByIDsSkipsMissing proves that an id that is not found means
// a missing record, NOT an error (the ADR 0004 contract).
func TestProviderFetchByIDsSkipsMissing(t *testing.T) {
	provider, _ := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{"pset_1", "pset_missing"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "pset_1", records[0][query.IDField])
}

// TestProviderFetchByIDsEmpty proves that an empty set of ids never reaches the
// repository.
func TestProviderFetchByIDsEmpty(t *testing.T) {
	provider, repo := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), nil, nil)

	require.NoError(t, err)
	assert.Empty(t, records)
	assert.NotNil(t, records, "an empty result has to be an empty slice, not nil")
	assert.Empty(t, repo.calls)
}

// TestProviderFieldSelection proves that the field selection is applied and
// that the id field is added even when it is not requested.
func TestProviderFieldSelection(t *testing.T) {
	provider, repo := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{"pset_1"},
		[]string{"created_at"})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], query.IDField, "the join key has to always be present")
	assert.Contains(t, records[0], "created_at")
	assert.NotContains(t, records[0], "prices", "a field that was not requested must not be written")
	assert.NotContains(t, records[0], "updated_at")
	assert.Zero(t, repo.calls["ListPriceCandidatesBySets"], "if the price was not requested, no query may be opened")
}

// TestProviderRejectsUnknownField proves that an unrecognized field returns
// errors.Invalid (ADR 0004: field validation belongs to the provider).
func TestProviderRejectsUnknownField(t *testing.T) {
	provider, repo := newTestProvider(t)

	_, err := provider.FetchByIDs(context.Background(), []string{"pset_1"},
		[]string{"secret_margin"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, repo.calls)
}

// TestProviderListFilters proves that the "id" filter is supported and any
// other is rejected.
func TestProviderListFilters(t *testing.T) {
	t.Run("a single string id", func(t *testing.T) {
		provider, repo := newTestProvider(t)

		records, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"id": "pset_1"}})

		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Zero(t, repo.calls["ListPriceSets"], "with an id filter, no paging query may be opened")
	})

	t.Run("a string slice of ids", func(t *testing.T) {
		provider, _ := newTestProvider(t)

		records, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"id": []string{"pset_1", "pset_2"}}})

		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("an unsupported filter is rejected", func(t *testing.T) {
		provider, repo := newTestProvider(t)

		_, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"currency_code": "TRY"}})

		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		assert.Empty(t, repo.calls)
	})

	t.Run("an id of the wrong type is rejected", func(t *testing.T) {
		provider, _ := newTestProvider(t)

		_, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"id": 42}})

		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestProviderListAppliesPagingLimits proves that a zero limit means the
// default page size, not unbounded, and that the maximum cannot be exceeded.
func TestProviderListAppliesPagingLimits(t *testing.T) {
	provider, repo := newTestProvider(t)

	var gotLimit, gotOffset int32
	repo.listPriceSetsFn = func(_ context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
		gotLimit, gotOffset = limit, offset
		return []models.PriceSet{}, 0, nil
	}

	_, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, gotLimit, "a zero limit has to be the default, NOT unbounded")

	_, err = provider.List(context.Background(),
		query.ListOptions{Limit: int(MaxLimit) + 1000, Offset: 5})
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, gotLimit)
	assert.Equal(t, int32(5), gotOffset)
}

// TestProviderSatisfiesQueryProviderInterface proves at compile time that the
// concrete type satisfies the core's interface (the provider side of ADR 0001).
func TestProviderSatisfiesQueryProviderInterface(t *testing.T) {
	provider, _ := newTestProvider(t)

	var iface query.Provider = provider

	assert.Equal(t, Entity, iface.Entity())
}

// TestProviderExcludesConditionalPrices proves that the provider returns ONLY
// unconditional prices that are valid at that moment.
//
// The provider is a read surface and carries no calculation context; if it
// returns a price that is conditional on a context it does not carry, the
// storefront shows a price that pricing itself counts as INVALID. Each of the
// four cases that have to be eliminated is set up separately: an unpublished
// (draft) list, a list outside its window, a DELETED list and a price with
// rules.
func TestProviderExcludesConditionalPrices(t *testing.T) {
	ended := testNow.Add(-time.Hour)
	deletedListID := "plist_deleted"

	repo := newStubRepo()
	repo.getPriceSetsByIDsFn = func(_ context.Context, ids []string) ([]models.PriceSet, error) {
		sets := make([]models.PriceSet, 0, len(ids))
		for _, id := range ids {
			sets = append(sets, models.PriceSet{ID: id, CreatedAt: testNow, UpdatedAt: testNow})
		}
		return sets, nil
	}
	repo.listCandidatesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
		out := map[string][]models.PriceCandidate{}
		for _, id := range ids {
			base := basePrice("price_base", "TRY", 10000, 1, nil)
			base.Price.PriceSetID = id

			published := withList(basePrice("price_published", "TRY", 9000, 1, nil), "plist_active",
				activeList("plist_active", models.PriceListSale))
			published.Price.PriceSetID = id

			draft := withList(basePrice("price_draft", "TRY", 1, 1, nil), "plist_draft",
				&models.PriceListInfo{ID: "plist_draft", Type: models.PriceListSale, Status: models.PriceListDraft})
			draft.Price.PriceSetID = id

			outOfWindow := withList(basePrice("price_out_of_window", "TRY", 2, 1, nil), "plist_ended",
				&models.PriceListInfo{
					ID:     "plist_ended",
					Type:   models.PriceListSale,
					Status: models.PriceListActive,
					EndsAt: &ended,
				})
			outOfWindow.Price.PriceSetID = id

			// The list id is set but there is no metadata: the list was DELETED.
			deletedList := basePrice("price_deleted_list", "TRY", 3, 1, nil)
			deletedList.Price.PriceSetID = id
			deletedList.Price.PriceListID = &deletedListID

			ruled := withRules(basePrice("price_ruled", "TRY", 4, 1, nil),
				rule("customer_group_id", models.OpEq, "vip"))
			ruled.Price.PriceSetID = id

			out[id] = []models.PriceCandidate{
				base, published, draft, outOfWindow, deletedList, ruled,
			}
		}
		return out, nil
	}

	// The active sale makes the provider ask for the set's history (ADR 0167);
	// this test is about which prices are LISTED, and an empty history
	// announces no reduction.
	repo.priceSetHistoryFn = func(context.Context, []string) ([]models.PriceSetSnapshot, error) {
		return nil, nil
	}
	repo.priceListHistoryFn = func(context.Context, []string) (map[string][]models.PriceListSnapshot, error) {
		return nil, nil
	}

	provider := NewQueryProvider(newTestService(repo))
	records, err := provider.FetchByIDs(context.Background(), []string{"pset_1"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)

	prices, ok := records[0][fieldPrices].([]map[string]any)
	require.True(t, ok)

	got := make([]string, 0, len(prices))
	for _, price := range prices {
		id, isString := price[fieldID].(string)
		require.True(t, isString)
		got = append(got, id)
	}
	assert.Equal(t, []string{"price_base", "price_published"}, got,
		"only unconditional prices valid at that moment may be returned")
}
