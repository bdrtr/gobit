package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
)

// saglayiciDepo aktif ve aktif olmayan promosyonlarla dolu bir depo üretir.
func saglayiciDepo() *memRepo {
	repo := newMemRepo()
	seedPromotion(repo, models.Promotion{
		ID: "promo_1", Code: "AKTIF", Status: models.PromotionActive, IsAutomatic: true,
	}, nil)
	seedPromotion(repo, models.Promotion{
		ID: "promo_2", Code: "TASLAK", Status: models.PromotionDraft,
	}, nil)
	seedPromotion(repo, models.Promotion{
		ID: "promo_3", Code: "PASIF", Status: models.PromotionInactive,
	}, nil)
	return repo
}

func TestQueryProviderEntityAdi(t *testing.T) {
	provider := NewQueryProvider(newTestService(newMemRepo()))

	assert.Equal(t, "promotion", provider.Entity())
	assert.Equal(t, "promotion.query", provider.Entity()+query.ProviderSuffix,
		"Query sağlayıcıyı bu adla arar")
}

func TestQueryProviderYalnizcaAktifPromosyonlariListeler(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	kayitlar, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)

	require.Len(t, kayitlar, 1, "taslak ve pasif promosyonlar okuma yüzeyinden SIZMAZ")
	assert.Equal(t, "promo_1", kayitlar[0]["id"])
	assert.Equal(t, "AKTIF", kayitlar[0]["code"])
}

func TestQueryProviderFetchByIDsDeAyniSuzgeciUygular(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	kayitlar, err := provider.FetchByIDs(context.Background(),
		[]string{"promo_1", "promo_2", "promo_yok"}, nil)
	require.NoError(t, err)

	require.Len(t, kayitlar, 1,
		"kural TEK olmalı; iki yüzeyin ayrışması taslak bir kuponu link üzerinden açardı")
	assert.Equal(t, "promo_1", kayitlar[0]["id"])
}

func TestQueryProviderHassasAlanlariSunmaz(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	kayitlar, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)

	for _, alan := range []string{"usage_count", "usage_limit", "metadata", "rules", "application_method"} {
		assert.NotContains(t, kayitlar[0], alan, "%q okuma yüzeyinden sızmamalı", alan)
	}
}

func TestQueryProviderAlanSecimi(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	kayitlar, err := provider.FetchByIDs(context.Background(), []string{"promo_1"}, []string{"code"})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)

	assert.Equal(t, "AKTIF", kayitlar[0]["code"])
	assert.Contains(t, kayitlar[0], "id",
		"Query kayıtları kimlik üzerinden birleştirir; kimlik istenmese de eklenir")
	assert.NotContains(t, kayitlar[0], "status")
}

func TestQueryProviderTanimsizAlanReddedilir(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	_, err := provider.FetchByIDs(context.Background(), []string{"promo_1"}, []string{"usage_count"})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err),
		"ADR 0004: alan doğrulaması sağlayıcıya aittir")
}

func TestQueryProviderKimlikFiltresi(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	kayitlar, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"id": []string{"promo_1"}},
	})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, "promo_1", kayitlar[0]["id"])

	bos, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"id": []string{}},
	})
	require.NoError(t, err)
	assert.Empty(t, bos, "boş kimlik kümesi 'hiçbiri' demektir, 'süzme' değil")
}

func TestQueryProviderDesteklenmeyenFiltre(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"code": "AKTIF"},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

func TestQueryProviderFiltreTipiDogrulanir(t *testing.T) {
	provider := NewQueryProvider(newTestService(saglayiciDepo()))

	_, err := provider.List(context.Background(), query.ListOptions{
		Filters: map[string]any{"id": 42},
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

func TestQueryProviderSinirsizListeyiEngeller(t *testing.T) {
	repo := newMemRepo()
	for i := range int(MaxLimit) + 20 {
		id := models.NewPromotionID(testNow.Add(-1))
		seedPromotion(repo, models.Promotion{
			ID: id, Code: models.NewPromotionID(testNow) + string(rune('a'+i%26)),
			Status: models.PromotionActive, IsAutomatic: true,
		}, nil)
	}
	provider := NewQueryProvider(newTestService(repo))

	kayitlar, err := provider.List(context.Background(), query.ListOptions{})
	require.NoError(t, err)

	assert.LessOrEqual(t, len(kayitlar), int(MaxLimit),
		"sınırsız bir kök listesi tek istekte tüm tabloyu belleğe alırdı")
}
