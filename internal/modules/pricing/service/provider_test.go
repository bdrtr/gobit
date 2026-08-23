package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// newTestProvider fiyatları hazır bir sağlayıcı ve deposunu üretir.
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
	repo.listPricesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.Price, error) {
		out := map[string][]models.Price{}
		for _, id := range ids {
			out[id] = []models.Price{{
				ID:           "price_" + id,
				PriceSetID:   id,
				CurrencyCode: "TRY",
				Amount:       19900,
				MinQuantity:  1,
			}}
		}
		return out, nil
	}
	return NewQueryProvider(newTestService(repo)), repo
}

// TestProviderEntity sağlayıcının kaydedileceği entity adını kanıtlar.
// Ad değişirse product'ın genişletmesi çalışma zamanında kırılır.
func TestProviderEntity(t *testing.T) {
	provider, _ := newTestProvider(t)
	assert.Equal(t, "price_set", provider.Entity())
	assert.Equal(t, "price_set.query", Entity+query.ProviderSuffix)
}

// TestProviderFetchByIDsIncludesPrices kayıtların FİYATLARIYLA döndüğünü
// kanıtlar; product'ın store listelemesi buna dayanır.
func TestProviderFetchByIDsIncludesPrices(t *testing.T) {
	provider, _ := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{"pset_1"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "pset_1", records[0][query.IDField])

	prices, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok, "prices alanı alt kayıt dilimi olmalı")
	require.Len(t, prices, 1)
	assert.Equal(t, "TRY", prices[0]["currency_code"])
	assert.Equal(t, int64(19900), prices[0]["amount"])
}

// TestProviderFetchByIDsIsBatched çağrı sayısının kimlik sayısıyla DEĞİL sabit
// kaldığını kanıtlar; Query katmanının N+1 yasağı budur (ADR 0004).
func TestProviderFetchByIDsIsBatched(t *testing.T) {
	provider, repo := newTestProvider(t)

	var gotIDs []string
	repo.listPricesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.Price, error) {
		gotIDs = ids
		return map[string][]models.Price{}, nil
	}

	ids := []string{"pset_1", "pset_2", "pset_3", "pset_4", "pset_5"}
	records, err := provider.FetchByIDs(context.Background(), ids, nil)

	require.NoError(t, err)
	assert.Len(t, records, 5)
	assert.Equal(t, 1, repo.calls["GetPriceSetsByIDs"], "kaplar tek sorguda okunmalı")
	assert.Equal(t, 1, repo.calls["ListPricesBySets"], "fiyatlar tek sorguda okunmalı")
	assert.Equal(t, ids, gotIDs, "tüm kimlikler tek çağrıda geçmeli")
}

// TestProviderFetchByIDsSkipsMissing bulunamayan kimliğin hata DEĞİL, eksik
// kayıt anlamına geldiğini kanıtlar (ADR 0004 sözleşmesi).
func TestProviderFetchByIDsSkipsMissing(t *testing.T) {
	provider, _ := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{"pset_1", "pset_missing"}, nil)

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, "pset_1", records[0][query.IDField])
}

// TestProviderFetchByIDsEmpty boş kimlik kümesinin depoya hiç gitmediğini
// kanıtlar.
func TestProviderFetchByIDsEmpty(t *testing.T) {
	provider, repo := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), nil, nil)

	require.NoError(t, err)
	assert.Empty(t, records)
	assert.NotNil(t, records, "boş sonuç nil değil boş dilim olmalı")
	assert.Empty(t, repo.calls)
}

// TestProviderFieldSelection alan seçiminin uygulandığını ve kimlik alanının
// istenmese de eklendiğini kanıtlar.
func TestProviderFieldSelection(t *testing.T) {
	provider, repo := newTestProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{"pset_1"},
		[]string{"created_at"})

	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Contains(t, records[0], query.IDField, "birleştirme anahtarı daima bulunmalı")
	assert.Contains(t, records[0], "created_at")
	assert.NotContains(t, records[0], "prices", "istenmeyen alan yazılmamalı")
	assert.NotContains(t, records[0], "updated_at")
	assert.Zero(t, repo.calls["ListPricesBySets"], "fiyat istenmediyse sorgu açılmamalı")
}

// TestProviderRejectsUnknownField tanınmayan alanın errors.Invalid döndürdüğünü
// kanıtlar (ADR 0004: alan doğrulaması sağlayıcıya aittir).
func TestProviderRejectsUnknownField(t *testing.T) {
	provider, repo := newTestProvider(t)

	_, err := provider.FetchByIDs(context.Background(), []string{"pset_1"},
		[]string{"secret_margin"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Empty(t, repo.calls)
}

// TestProviderListFilters "id" filtresinin desteklendiğini, başkasının
// reddedildiğini kanıtlar.
func TestProviderListFilters(t *testing.T) {
	t.Run("tek dize kimlik", func(t *testing.T) {
		provider, repo := newTestProvider(t)

		records, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"id": "pset_1"}})

		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Zero(t, repo.calls["ListPriceSets"], "kimlik filtresinde sayfalama sorgusu açılmamalı")
	})

	t.Run("dize dilimi kimlik", func(t *testing.T) {
		provider, _ := newTestProvider(t)

		records, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"id": []string{"pset_1", "pset_2"}}})

		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("desteklenmeyen filtre reddedilir", func(t *testing.T) {
		provider, repo := newTestProvider(t)

		_, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"currency_code": "TRY"}})

		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		assert.Empty(t, repo.calls)
	})

	t.Run("yanlış tipte kimlik reddedilir", func(t *testing.T) {
		provider, _ := newTestProvider(t)

		_, err := provider.List(context.Background(),
			query.ListOptions{Filters: map[string]any{"id": 42}})

		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestProviderListAppliesPagingLimits sıfır limitin sınırsız değil varsayılan
// sayfa boyu anlamına geldiğini ve azami sınırın aşılamadığını kanıtlar.
func TestProviderListAppliesPagingLimits(t *testing.T) {
	provider, repo := newTestProvider(t)

	var gotLimit, gotOffset int32
	repo.listPriceSetsFn = func(_ context.Context, limit, offset int32) ([]models.PriceSet, int64, error) {
		gotLimit, gotOffset = limit, offset
		return []models.PriceSet{}, 0, nil
	}

	_, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, gotLimit, "sıfır limit sınırsız DEĞİL varsayılan olmalı")

	_, err = provider.List(context.Background(),
		query.ListOptions{Limit: int(MaxLimit) + 1000, Offset: 5})
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, gotLimit)
	assert.Equal(t, int32(5), gotOffset)
}

// TestProviderSatisfiesQueryProviderInterface somut tipin çekirdeğin arayüzünü
// karşıladığını derleme zamanında kanıtlar (ADR 0001'in sağlayıcı tarafı).
func TestProviderSatisfiesQueryProviderInterface(t *testing.T) {
	provider, _ := newTestProvider(t)

	var iface query.Provider = provider

	assert.Equal(t, Entity, iface.Entity())
}
