package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// queryKurulum Query sağlayıcısını ve iki seçenekli bir katalog hazırlar.
func queryKurulum(t *testing.T) (*service.QueryProvider, testKurulum, string) {
	t.Helper()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	secenekID := kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Standart kargo",
		ShippingProfileID: profilID,
		Amount:            2_000,
		RegionID:          "reg_tr",
	})
	kurulum.secenekAc(t, service.CreateOptionInput{
		Name:              "Hesaplanan kargo",
		ShippingProfileID: profilID,
		PriceType:         "calculated",
	})
	return service.NewQueryProvider(kurulum.svc), kurulum, secenekID
}

// TestQuerySaglayicisiEntityAdi ADR 0004'ün ad örtüşmesini kanıtlar.
func TestQuerySaglayicisiEntityAdi(t *testing.T) {
	t.Parallel()

	provider, _, _ := queryKurulum(t)
	assert.Equal(t, "shipping_option", provider.Entity())
	assert.Equal(t, service.EntityName, provider.Entity())
}

// TestQueryListSuzgecUygular desteklenen süzgecin çalıştığını kanıtlar.
func TestQueryListSuzgecUygular(t *testing.T) {
	t.Parallel()

	provider, _, secenekID := queryKurulum(t)

	kayitlar, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"region_id": "reg_tr"},
		Fields:  []string{"id", "name", "amount"},
	})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, secenekID, kayitlar[0]["id"])
	assert.Equal(t, int64(2_000), kayitlar[0]["amount"])
	assert.Len(t, kayitlar[0], 3, "yalnızca istenen alanlar dönmeli")
}

// TestQueryTaninmayanSuzgecReddedilir ADR 0004'ün şartını kanıtlar.
func TestQueryTaninmayanSuzgecReddedilir(t *testing.T) {
	t.Parallel()

	provider, _, _ := queryKurulum(t)

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"tracking_number": "TK-1"},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)

	_, err = provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"region_id": 42},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "metin olmayan süzgeç değeri reddedilmeli: %v", err)
}

// TestQueryTaninmayanAlanReddedilir sunulmayan bir alanın istenemeyeceğini
// kanıtlar.
func TestQueryTaninmayanAlanReddedilir(t *testing.T) {
	t.Parallel()

	provider, _, _ := queryKurulum(t)

	_, err := provider.List(context.Background(), query.ListOptions{Fields: []string{"data"}})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)

	_, err = provider.FetchByIDs(context.Background(), []string{"sopt_1"}, []string{"metadata"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
}

// TestQuerySaglayiciIcVerisiniSunmaz "data" ve "metadata" alanlarının okuma
// yüzeyinde OLMADIĞINI kanıtlar.
//
// data sağlayıcının iç yapılandırmasıdır; modüller arası okuma yüzeyinde hiç
// görünmemelidir.
func TestQuerySaglayiciIcVerisiniSunmaz(t *testing.T) {
	t.Parallel()

	provider, _, _ := queryKurulum(t)

	kayitlar, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, kayitlar)
	assert.NotContains(t, kayitlar[0], "data", "sağlayıcı yapılandırması sunulmamalı")
	assert.NotContains(t, kayitlar[0], "metadata", "şemasız serbest veri sunulmamalı")
	assert.Contains(t, kayitlar[0], "admin_only", "modüller arası okuma admin_only'yi görmeli")
}

// TestQueryFetchByIDsBatchDoner N+1 yasağının karşılığını kanıtlar:
// bulunamayan kimlik hata değildir, yalnızca kayıt dönmez.
func TestQueryFetchByIDsBatchDoner(t *testing.T) {
	t.Parallel()

	provider, _, secenekID := queryKurulum(t)

	kayitlar, err := provider.FetchByIDs(context.Background(),
		[]string{secenekID, "sopt_YOKBOYLE"}, []string{"id"})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, secenekID, kayitlar[0]["id"])

	bos, err := provider.FetchByIDs(context.Background(), nil, nil)
	require.NoError(t, err)
	assert.Empty(t, bos)
}

// TestQuerySinirsizLimitTavanaKirpilir çekirdeğin "0 = sınırsız" sözleşmesinin
// bu sağlayıcıda tavana çevrildiğini kanıtlar.
//
// Sınırsız bir kök sorgu tüm seçenek tablosunu belleğe alırdı.
func TestQuerySinirsizLimitTavanaKirpilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	profilID := kurulum.profilAc(t, "varsayilan")
	for i := range int(service.MaxLimit) + 5 {
		kurulum.secenekAc(t, service.CreateOptionInput{
			Name:              "Kargo " + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			ShippingProfileID: profilID,
			Amount:            int64(1_000 + i),
		})
	}
	provider := service.NewQueryProvider(kurulum.svc)

	kayitlar, err := provider.List(context.Background(), query.ListOptions{Limit: 0})
	require.NoError(t, err)
	assert.Len(t, kayitlar, int(service.MaxLimit), "sınırsız istek tavana kırpılmalı")
}
