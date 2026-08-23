package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// newTestProvider sahte depo üzerinde çalışan bir sağlayıcı, servisi ve
// deposunu döner.
func newTestProvider(t *testing.T) (*QueryProvider, *Service, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	return NewQueryProvider(svc), svc, repo
}

// TestProviderEntityMatchesRegistrationName sağlayıcının entity adının
// container'a kaydedilen adla uyuştuğunu kanıtlar.
//
// Query, bir genişletmenin hedefini "<entity>.query" adıyla arar; ad
// uyuşmazsa errors.NotFound döner ve hata yalnızca çalışma zamanında görünürdü.
func TestProviderEntityMatchesRegistrationName(t *testing.T) {
	provider, _, _ := newTestProvider(t)

	assert.Equal(t, "region", provider.Entity())
	assert.Equal(t, "region.query", provider.Entity()+query.ProviderSuffix)
}

// TestProviderListReturnsFullRecords varsayılan alan kümesinin bölgeyi, para
// birimini ve ülkelerini taşıdığını kanıtlar.
func TestProviderListReturnsFullRecords(t *testing.T) {
	ctx := context.Background()
	provider, svc, _ := newTestProvider(t)
	region := newRegion(t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "JP")
	require.NoError(t, err)

	records, err := provider.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, region.ID, record[query.IDField])
	assert.Equal(t, region.Name, record["name"])
	assert.Equal(t, "JPY", record["currency_code"])
	assert.Equal(t, int32(2000), record["tax_rate"])
	assert.Equal(t, true, record["automatic_taxes"])

	currency, ok := record["currency"].(map[string]any)
	require.True(t, ok, "para birimi alt kaydı olmalı")
	assert.Equal(t, "JPY", currency["code"])
	assert.Equal(t, int32(0), currency["decimal_digits"],
		"vitrin bölme çarpanını bu alandan öğrenir")

	countries, ok := record["countries"].([]map[string]any)
	require.True(t, ok, "ülke alt kayıtları olmalı")
	require.Len(t, countries, 1)
	assert.Equal(t, "JP", countries[0]["code"])
}

// TestProviderFetchByIDsBatchesReads genişletme başına SABİT sayıda okuma
// yapıldığını kanıtlar (ADR 0004'ün N+1 yasağı).
func TestProviderFetchByIDsBatchesReads(t *testing.T) {
	ctx := context.Background()
	provider, svc, repo := newTestProvider(t)

	first := newRegion(t, svc, "TRY")
	second := newRegion(t, svc, "USD")
	third := newRegion(t, svc, "JPY")
	_, err := svc.AddCountryToRegion(ctx, first.ID, "TR")
	require.NoError(t, err)
	_, err = svc.AddCountryToRegion(ctx, second.ID, "US")
	require.NoError(t, err)

	repo.resetCalls()
	records, err := provider.FetchByIDs(ctx, []string{first.ID, second.ID, third.ID}, nil)
	require.NoError(t, err)
	assert.Len(t, records, 3)

	assert.Equal(t, 1, repo.callCount("GetRegionsByIDs"))
	assert.Equal(t, 1, repo.callCount("GetCurrenciesByCodes"))
	assert.Equal(t, 1, repo.callCount("ListCountriesByRegions"))
	assert.Zero(t, repo.callCount("GetCurrency"), "kayıt başına para birimi okunmamalı")
	assert.Zero(t, repo.callCount("ListCountries"), "kayıt başına ülke okunmamalı")
}

// TestProviderSkipsUnrequestedJoins istenmeyen alt kayıtlar için hiç sorgu
// yapılmadığını kanıtlar.
//
// Alan seçimi yalnızca yanıtı küçültmek için değildir; istenmeyen bir
// genişletmenin MALİYETİ de ödenmemelidir.
func TestProviderSkipsUnrequestedJoins(t *testing.T) {
	ctx := context.Background()
	provider, svc, repo := newTestProvider(t)
	region := newRegion(t, svc, "TRY")
	_, err := svc.AddCountryToRegion(ctx, region.ID, "TR")
	require.NoError(t, err)

	repo.resetCalls()
	records, err := provider.FetchByIDs(ctx, []string{region.ID}, []string{"currency_code"})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Zero(t, repo.callCount("GetCurrenciesByCodes"), "para birimi istenmedi, okunmamalı")
	assert.Zero(t, repo.callCount("ListCountriesByRegions"), "ülkeler istenmedi, okunmamalı")
	assert.NotContains(t, records[0], "currency")
	assert.NotContains(t, records[0], "countries")
	assert.Contains(t, records[0], query.IDField, "kimlik istenmese de eklenmeli")
}

// TestProviderRejectsUnknownField tanınmayan alan için errors.Invalid
// döndüğünü kanıtlar (ADR 0004: alan doğrulaması sağlayıcıya aittir).
func TestProviderRejectsUnknownField(t *testing.T) {
	ctx := context.Background()
	provider, _, repo := newTestProvider(t)

	_, err := provider.FetchByIDs(ctx, []string{"reg_1"}, []string{"tax_rate", "gizli_alan"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Zero(t, repo.callCount("GetRegionsByIDs"), "alan doğrulaması okumadan ÖNCE yapılmalı")
}

// TestProviderIDFilter kimlik filtresinin kabul ettiği ve reddettiği biçimleri
// kanıtlar.
func TestProviderIDFilter(t *testing.T) {
	ctx := context.Background()
	provider, svc, _ := newTestProvider(t)
	first := newRegion(t, svc, "TRY")
	newRegion(t, svc, "USD")

	records, err := provider.List(ctx, query.ListOptions{Filters: map[string]any{"id": first.ID}})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, first.ID, records[0][query.IDField])

	records, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": []string{first.ID}},
	})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	// Boş dilim "hiçbir kimlik" demektir; nil'den ayrı bir anlamdır.
	records, err = provider.List(ctx, query.ListOptions{
		Filters: map[string]any{"id": []string{}},
	})
	require.NoError(t, err)
	assert.Empty(t, records)

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{"name": "X"}})
	require.Error(t, err, "desteklenmeyen filtre reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = provider.List(ctx, query.ListOptions{Filters: map[string]any{"id": 42}})
	require.Error(t, err, "yanlış tipli filtre reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderListAppliesModuleDefaultLimit Query sözleşmesindeki "sınırsız"
// yerine modülün sayfa boyunun uygulandığını kanıtlar.
//
// Sınırsız bir kök listesi tek istekte tüm tabloyu belleğe alırdı.
func TestProviderListAppliesModuleDefaultLimit(t *testing.T) {
	ctx := context.Background()
	provider, svc, repo := newTestProvider(t)
	for range 3 {
		newRegion(t, svc, "TRY")
	}

	records, err := provider.List(ctx, query.ListOptions{Limit: 0})
	require.NoError(t, err)
	assert.Len(t, records, 3)
	limit, offset := repo.lastPaging()
	assert.Equal(t, DefaultLimit, limit, "sınırsız istek modülün varsayılanına düşmeli")
	assert.Zero(t, offset)

	// int32'ye sığmayan bir limit SARMAMALI; sıkıştırıldıktan sonra azami
	// değerle kırpılmalıdır. Sarsaydı negatif bir limit veritabanına giderdi.
	records, err = provider.List(ctx, query.ListOptions{Limit: 1 << 40})
	require.NoError(t, err)
	assert.Len(t, records, 3)
	limit, _ = repo.lastPaging()
	assert.Equal(t, MaxLimit, limit, "limit azami değerle kırpılmalı")

	_, err = provider.List(ctx, query.ListOptions{Offset: -1})
	require.Error(t, err, "negatif offset reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestProviderRecordsAreIndependent döndürülen kayıtların birbirinin durumunu
// paylaşmadığını kanıtlar.
//
// Query genişletme sonucunu kaydın İÇİNE yazar; paylaşılan bir harita, bir
// kaydın genişletmesinin diğerinde de görünmesi demek olurdu.
func TestProviderRecordsAreIndependent(t *testing.T) {
	ctx := context.Background()
	provider, svc, _ := newTestProvider(t)
	newRegion(t, svc, "TRY")
	newRegion(t, svc, "USD")

	records, err := provider.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, records, 2)

	records[0]["ek_alan"] = "x"
	assert.NotContains(t, records[1], "ek_alan")
}
