package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
)

// TestListStoreRegionsBatchesReads vitrin listesinin sorgu sayısının bölge
// sayısından BAĞIMSIZ olduğunu kanıtlar.
//
// Bölge başına para birimi/ülke okuması yapmak N+1 demek olurdu ve vitrin
// listesi tam da en çok kaydın döndüğü yerdir. İddia sayaçla kanıtlanır:
// üç bölge için de toplam üç okuma yapılır.
func TestListStoreRegionsBatchesReads(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)

	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "JPY")
	newRegion(t, svc, "USD")

	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, first.ID, "DE")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, second.ID, "JP")
	require.NoError(t, err)

	repo.resetCalls()
	page, err := svc.ListStoreRegions(ctx, 0, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 3)
	assert.Equal(t, int64(3), page.Count)

	assert.Equal(t, 1, repo.callCount("ListRegions"))
	assert.Equal(t, 1, repo.callCount("GetCurrenciesByCodes"),
		"para birimleri TEK toplu okumayla alınmalı")
	assert.Equal(t, 1, repo.callCount("ListCountriesByRegions"),
		"ülkeler TEK toplu okumayla alınmalı")
	assert.Zero(t, repo.callCount("GetCurrency"), "bölge başına para birimi okunmamalı")
	assert.Zero(t, repo.callCount("ListCountries"), "bölge başına ülke okunmamalı")

	byID := map[string]StoreRegion{}
	for _, item := range page.Items {
		byID[item.Region.ID] = item
	}

	tr := byID[first.ID]
	require.NotNil(t, tr.Currency)
	assert.Equal(t, "TRY", tr.Currency.Code)
	assert.Equal(t, int32(2), tr.Currency.DecimalDigits)
	require.Len(t, tr.Countries, 2)
	assert.Equal(t, "DE", tr.Countries[0].Code, "ülkeler koda göre sıralı olmalı")
	assert.Equal(t, "TR", tr.Countries[1].Code)

	jp := byID[second.ID]
	require.NotNil(t, jp.Currency)
	assert.Equal(t, int32(0), jp.Currency.DecimalDigits, "JPY ondalıksızdır")
	require.Len(t, jp.Countries, 1)
}

// TestListStoreRegionsEmptyCountriesAreSlices ülkesi olmayan bir bölgenin nil
// değil boş dilim döndürdüğünü kanıtlar.
//
// JSON'da null yerine [] görünmesi, tüketicinin tek biçimli bir yüzey görmesi
// demektir.
func TestListStoreRegionsEmptyCountriesAreSlices(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	newRegion(t, svc, "USD")

	page, err := svc.ListStoreRegions(ctx, 0, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.NotNil(t, page.Items[0].Countries)
	assert.Empty(t, page.Items[0].Countries)
}

// TestGetStoreRegion tek bölge okumasının da para birimi ve ülkeleri
// taşıdığını kanıtlar.
func TestGetStoreRegion(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	region := newRegion(t, svc, "KWD")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "US")
	require.NoError(t, err)

	item, err := svc.GetStoreRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Equal(t, region.ID, item.Region.ID)
	require.NotNil(t, item.Currency)
	assert.Equal(t, int32(3), item.Currency.DecimalDigits, "KWD üç basamaklıdır")
	require.Len(t, item.Countries, 1)
	assert.Equal(t, "US", item.Countries[0].Code)

	_, err = svc.GetStoreRegion(ctx, "reg_YOK")
	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestStoreRegionCurrencyMissingIsNil referans tablosunda bulunamayan bir para
// biriminin sıfır değerle DEĞİL nil ile temsil edildiğini kanıtlar.
//
// Sıfır değer, ondalık basamağı 0 göstererek tutarları yanlış ölçekte
// gösterirdi; nil ise tüketiciye "bilinmiyor" der.
func TestStoreRegionCurrencyMissingIsNil(t *testing.T) {
	ctx := context.Background()
	svc, repo := newTestService(t)
	region := newRegion(t, svc, "TRY")

	// Foreign key nedeniyle gerçekte oluşamaz; sahte depoda elle kurulur.
	repo.mu.Lock()
	delete(repo.currencies, "TRY")
	repo.mu.Unlock()

	item, err := svc.GetStoreRegion(ctx, region.ID)
	require.NoError(t, err)
	assert.Nil(t, item.Currency)
	assert.Equal(t, "TRY", item.Region.CurrencyCode, "kod yine de görünmeli")
}
