package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// newTestQueryProvider tohumlanmış bir Query sağlayıcısı kurar.
func newTestQueryProvider(t *testing.T) (*QueryProvider, *memRepo) {
	t.Helper()

	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedDefaultRate(rateC, usRegionID, 725)
	return NewQueryProvider(svc), repo
}

// TestQueryProviderEntityAdi sağlayıcının kayıt adıyla tutarlı olduğunu
// doğrular.
//
// Query, sağlayıcıyı "<entity>.query" adıyla arar ve Entity() ile adın
// örtüştüğünü denetler; ikisi ayrışırsa sağlayıcı hiç bulunmaz.
func TestQueryProviderEntityAdi(t *testing.T) {
	provider, _ := newTestQueryProvider(t)
	assert.Equal(t, "tax_region", provider.Entity())
	assert.Equal(t, Entity, provider.Entity())
}

// TestQueryProviderTumAlanlar varsayılan alan kümesini doğrular.
func TestQueryProviderTumAlanlar(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{trRegionID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)

	record := records[0]
	assert.Equal(t, trRegionID, record["id"])
	assert.Equal(t, "TR", record["country_code"])
	assert.Equal(t, "", record["province_code"], "kök bölgede eyalet kodu boş dize olmalı")
	assert.Equal(t, "", record["parent_id"])
	assert.Equal(t, "", record["provider_id"])
	assert.Contains(t, record, "created_at")
	assert.Contains(t, record, "updated_at")

	rates, ok := record["rates"].([]map[string]any)
	require.True(t, ok, "oranlar alt kayıt dilimi olmalı: %#v", record["rates"])
	require.Len(t, rates, 2)
	assert.Equal(t, rateA, rates[0]["id"], "varsayılan oran başta olmalı")
	assert.Equal(t, int32(2000), rates[0]["rate_bps"])
	assert.Equal(t, true, rates[0]["is_default"])
}

// TestQueryProviderAlanSecimi Fields ile alan daraltmayı doğrular.
func TestQueryProviderAlanSecimi(t *testing.T) {
	provider, repo := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(), []string{trRegionID}, []string{"country_code"})
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Contains(t, records[0], "id", "kimlik istenmese de eklenmeli (birleştirme anahtarı)")
	assert.Contains(t, records[0], "country_code")
	assert.NotContains(t, records[0], "rates")
	assert.Zero(t, repo.callCount("ListTaxRatesByRegions"),
		"oranlar istenmediyse oran sorgusu HİÇ yapılmamalı")
}

// TestQueryProviderBilinmeyenAlanReddedilir alan doğrulamasının sağlayıcıya
// ait olduğunu doğrular (ADR 0004).
func TestQueryProviderBilinmeyenAlanReddedilir(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	_, err := provider.FetchByIDs(context.Background(), []string{trRegionID}, []string{"tax_rate"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Contains(t, err.Error(), "tax_region")
}

// TestQueryProviderTopluOkur genişletme başına SABİT sorgu yapıldığını
// doğrular.
func TestQueryProviderTopluOkur(t *testing.T) {
	provider, repo := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{trRegionID, usRegionID, trIstanbul}, nil)
	require.NoError(t, err)
	assert.Len(t, records, 3)

	assert.Equal(t, 1, repo.callCount("GetTaxRegionsByIDs"))
	assert.Equal(t, 1, repo.callCount("ListTaxRatesByRegions"),
		"bölge başına oran sorgusu yapılmamalı (N+1 yok)")
}

// TestQueryProviderBulunamayanKimlikHataDegil eksik kimliğin sessizce
// atlandığını doğrular (ADR 0004).
func TestQueryProviderBulunamayanKimlikHataDegil(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	records, err := provider.FetchByIDs(context.Background(),
		[]string{trRegionID, models.TaxRegionIDPrefix + "YOK"}, nil)
	require.NoError(t, err)
	assert.Len(t, records, 1)

	records, err = provider.FetchByIDs(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, records)
}

// TestQueryProviderListSuzgecleri desteklenen ve desteklenmeyen filtreleri
// doğrular.
func TestQueryProviderListSuzgecleri(t *testing.T) {
	t.Run("kimlik dizesi", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": trRegionID},
		})
		require.NoError(t, err)
		require.Len(t, records, 1)
		assert.Equal(t, trRegionID, records[0]["id"])
	})

	t.Run("kimlik dilimi", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": []string{trRegionID, usRegionID}},
		})
		require.NoError(t, err)
		assert.Len(t, records, 2)
	})

	t.Run("boş kimlik dilimi hiçbir kayıt demektir", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": []string{}},
		})
		require.NoError(t, err)
		assert.Empty(t, records)
	})

	t.Run("ülke kodu", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		records, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"country_code": "TR"},
		})
		require.NoError(t, err)
		assert.Len(t, records, 2, "TR kökü ve eyaleti dönmeli")
	})

	t.Run("desteklenmeyen filtre", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		_, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"province_code": "34"},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})

	t.Run("filtre tipi yanlış", func(t *testing.T) {
		provider, _ := newTestQueryProvider(t)
		_, err := provider.List(context.Background(), query.ListOptions{
			Filters: map[string]any{"id": 42},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})
}

// TestQueryProviderKimlikFiltresiYalnizBasinaKullanilir kimlik filtresinin
// yanına konan daraltmanın SESSİZCE düşürülmediğini doğrular.
//
// Sessiz düşürme, desteklenmeyen bir filtreyi reddeden bu sağlayıcının kendi
// ilkesine aykırıdır: çağıran gönderdiği daraltmanın uygulandığını sanır ve
// istediğinden daha geniş bir kümeyi eline alır.
func TestQueryProviderKimlikFiltresiYalnizBasinaKullanilir(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{
			fieldID:          []string{trRegionID, usRegionID},
			fieldCountryCode: "TR",
		},
	})
	require.Error(t, err, "ülke süzgeci sessizce yok sayılmamalı")
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, CodeInvalidInput, errors.CodeOf(err))
}

// TestQueryProviderListSayfalar limit sıfırken sınırsız DEĞİL varsayılan
// sayfa boyunun uygulandığını doğrular.
func TestQueryProviderListSayfalar(t *testing.T) {
	provider, _ := newTestQueryProvider(t)

	records, err := provider.List(context.Background(), query.ListOptions{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, records, 1)

	all, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, all, 3, "limit verilmezse varsayılan sayfa boyu uygulanmalı")
}

// TestQueryProviderKurulmamisServis nil depo ile panik yerine tipli hata
// döndüğünü doğrular.
func TestQueryProviderKurulmamisServis(t *testing.T) {
	provider := NewQueryProvider(New(nil, Options{}))

	_, err := provider.List(context.Background(), query.ListOptions{})
	require.Error(t, err)
	assert.Equal(t, CodeUnconfigured, errors.CodeOf(err))

	_, err = provider.FetchByIDs(context.Background(), []string{trRegionID}, nil)
	require.Error(t, err)
}
