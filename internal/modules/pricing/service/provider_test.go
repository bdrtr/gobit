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
	repo.listCandidatesBySetsFn = func(_ context.Context, ids []string) (map[string][]models.PriceCandidate, error) {
		gotIDs = ids
		return map[string][]models.PriceCandidate{}, nil
	}

	ids := []string{"pset_1", "pset_2", "pset_3", "pset_4", "pset_5"}
	records, err := provider.FetchByIDs(context.Background(), ids, nil)

	require.NoError(t, err)
	assert.Len(t, records, 5)
	assert.Equal(t, 1, repo.calls["GetPriceSetsByIDs"], "kaplar tek sorguda okunmalı")
	assert.Equal(t, 1, repo.calls["ListPriceCandidatesBySets"], "fiyatlar tek sorguda okunmalı")
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
	assert.Zero(t, repo.calls["ListPriceCandidatesBySets"], "fiyat istenmediyse sorgu açılmamalı")
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

// TestProviderExcludesConditionalPrices sağlayıcının YALNIZCA koşulsuz ve o an
// geçerli fiyatları döndürdüğünü kanıtlar.
//
// Sağlayıcı bir okuma yüzeyidir ve hesaplama bağlamı taşımaz; taşımadığı bir
// bağlama koşullu fiyatı dönerse vitrin, pricing'in kendisinin GEÇERSİZ saydığı
// bir fiyatı gösterir. Elenmesi gereken dört durum da ayrı ayrı kurulur:
// yayınlanmamış (draft) liste, pencere dışı liste, SİLİNMİŞ liste ve kuralli
// fiyat.
func TestProviderExcludesConditionalPrices(t *testing.T) {
	ended := testNow.Add(-time.Hour)
	deletedListID := "plist_silinmis"

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
			base := basePrice("price_taban", "TRY", 10000, 1, nil)
			base.Price.PriceSetID = id

			yayinda := withList(basePrice("price_yayinda", "TRY", 9000, 1, nil), "plist_aktif",
				activeList("plist_aktif", models.PriceListSale))
			yayinda.Price.PriceSetID = id

			taslak := withList(basePrice("price_taslak", "TRY", 1, 1, nil), "plist_taslak",
				&models.PriceListInfo{ID: "plist_taslak", Type: models.PriceListSale, Status: models.PriceListDraft})
			taslak.Price.PriceSetID = id

			pencereDisi := withList(basePrice("price_pencere_disi", "TRY", 2, 1, nil), "plist_bitmis",
				&models.PriceListInfo{
					ID:     "plist_bitmis",
					Type:   models.PriceListSale,
					Status: models.PriceListActive,
					EndsAt: &ended,
				})
			pencereDisi.Price.PriceSetID = id

			// Liste kimliği dolu ama üstverisi yok: liste SİLİNMİŞTİR.
			silinmisListe := basePrice("price_silinmis_liste", "TRY", 3, 1, nil)
			silinmisListe.Price.PriceSetID = id
			silinmisListe.Price.PriceListID = &deletedListID

			kuralli := withRules(basePrice("price_kuralli", "TRY", 4, 1, nil),
				rule("customer_group_id", models.OpEq, "vip"))
			kuralli.Price.PriceSetID = id

			out[id] = []models.PriceCandidate{
				base, yayinda, taslak, pencereDisi, silinmisListe, kuralli,
			}
		}
		return out, nil
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
	assert.Equal(t, []string{"price_taban", "price_yayinda"}, got,
		"yalnızca koşulsuz ve o an geçerli fiyatlar dönmeli")
}
