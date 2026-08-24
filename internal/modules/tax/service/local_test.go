package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestRateTableVarsayilanOranKurallarindanEtkilenmez elle yazılmış bir kuralın
// varsayılan oranın kapsamını DARALTMADIĞINI doğrular.
//
// Servis ve depo katmanı varsayılan bir orana kural yazılmasını reddeder; bu
// test doğrudan SQL ile açılmış bir kaydın hesabı bozmadığını gösterir.
func TestRateTableVarsayilanOranKurallarindanEtkilenmez(t *testing.T) {
	table := newRateTable(
		[]string{trRegionID},
		[]models.TaxRate{{ID: rateA, TaxRegionID: trRegionID, RateBps: 2000, IsDefault: true}},
		[]models.TaxRateRule{{ID: ruleA, TaxRateID: rateA, Reference: models.ReferenceProduct, ReferenceID: "prod_1"}},
	)

	rate, ok := table.selectRate(nil)
	require.True(t, ok, "kuralsız bir kalem yine de varsayılana düşmeli")
	assert.Equal(t, rateA, rate.ID)
}

// TestRateTableIkinciVarsayilanKucukKimlikKazanir bozuk bir veri kümesinde bile
// sonucun BELİRLENİMCİ kaldığını doğrular.
func TestRateTableIkinciVarsayilanKucukKimlikKazanir(t *testing.T) {
	for range 20 {
		table := newRateTable(
			[]string{trRegionID},
			[]models.TaxRate{
				{ID: rateC, TaxRegionID: trRegionID, RateBps: 1000, IsDefault: true},
				{ID: rateA, TaxRegionID: trRegionID, RateBps: 2000, IsDefault: true},
			},
			nil,
		)

		rate, ok := table.selectRate(nil)
		require.True(t, ok)
		assert.Equal(t, rateA, rate.ID, "kimliği küçük varsayılan korunmalı")
	}
}

// TestRateTableZincirEnOzeldenGeneleYurur zincir sırasının sonucu belirlediğini
// doğrular.
func TestRateTableZincirEnOzeldenGeneleYurur(t *testing.T) {
	rates := []models.TaxRate{
		{ID: rateA, TaxRegionID: trRegionID, RateBps: 2000, IsDefault: true},
		{ID: rateB, TaxRegionID: trIstanbul, RateBps: 800, IsDefault: true},
	}

	ozelOnce := newRateTable([]string{trIstanbul, trRegionID}, rates, nil)
	rate, ok := ozelOnce.selectRate(nil)
	require.True(t, ok)
	assert.Equal(t, rateB, rate.ID, "zincirin başı kazanmalı")

	genelOnce := newRateTable([]string{trRegionID, trIstanbul}, rates, nil)
	rate, ok = genelOnce.selectRate(nil)
	require.True(t, ok)
	assert.Equal(t, rateA, rate.ID, "sıra tersine dönerse sonuç da dönmeli")
}

// TestRateTableEslesmeYoksaOranBulunmaz hiç oran vermeyen bir tablonun sıfır
// vergi ürettiğini doğrular.
func TestRateTableEslesmeYoksaOranBulunmaz(t *testing.T) {
	table := newRateTable(
		[]string{trRegionID},
		[]models.TaxRate{{ID: rateB, TaxRegionID: trRegionID, RateBps: 100}},
		[]models.TaxRateRule{{ID: ruleA, TaxRateID: rateB, Reference: models.ReferenceProduct, ReferenceID: "prod_1"}},
	)

	_, ok := table.selectRate([]matchKey{{models.ReferenceProduct, "prod_baska"}})
	assert.False(t, ok)

	applied, err := table.applyTo([]matchKey{{models.ReferenceProduct, "prod_baska"}}, "li_1", 10_000)
	require.NoError(t, err)
	assert.Equal(t, ProviderItemTax{ID: "li_1"}, applied, "oran yoksa vergi sıfır ve kimlik boş olmalı")
}

// TestRateTableReferansTuruEslesmesiKatidir aynı kimliğin farklı referans
// türünde eşleşmediğini doğrular.
//
// Bu, ürün kimliği ile kargo seçeneği kimliğinin kazara çakışması durumunda
// yanlış oranın uygulanmasını engelleyen kuraldır.
func TestRateTableReferansTuruEslesmesiKatidir(t *testing.T) {
	table := newRateTable(
		[]string{trRegionID},
		[]models.TaxRate{{ID: rateB, TaxRegionID: trRegionID, RateBps: 100}},
		[]models.TaxRateRule{{ID: ruleA, TaxRateID: rateB, Reference: models.ReferenceShippingOption, ReferenceID: "x_1"}},
	)

	_, ok := table.selectRate([]matchKey{{models.ReferenceProduct, "x_1"}})
	assert.False(t, ok, "ürün anahtarı kargo kuralıyla eşleşmemeli")

	_, ok = table.selectRate([]matchKey{{models.ReferenceShippingOption, "x_1"}})
	assert.True(t, ok)
}

// TestItemKeysBosKimlikleriAtlar boş alanların anahtar üretmediğini doğrular.
func TestItemKeysBosKimlikleriAtlar(t *testing.T) {
	assert.Empty(t, itemKeys(TaxableItem{ID: "li_1"}))
	assert.Equal(t,
		[]matchKey{{models.ReferenceProduct, "p"}},
		itemKeys(TaxableItem{ID: "li_1", ProductID: "p"}))
	assert.Equal(t,
		[]matchKey{{models.ReferenceProduct, "p"}, {models.ReferenceProductType, "t"}},
		itemKeys(TaxableItem{ID: "li_1", ProductID: "p", ProductTypeID: "t"}))
	assert.Nil(t, shippingKeys(ShippingInput{Amount: 100, Taxable: true}))
}

// TestLocalProviderBolgeYoksaSorguYapmaz zincir boşken hiç okuma yapılmadığını
// doğrular.
func TestLocalProviderBolgeYoksaSorguYapmaz(t *testing.T) {
	repo := newMemRepo()
	provider := NewLocalProvider(repo)

	result, err := provider.Calculate(context.Background(), ProviderInput{
		CountryCode: "DE",
		Items:       []TaxableItem{{ID: "li_1", Amount: 1000}},
	})
	require.NoError(t, err)

	require.Len(t, result.Items, 1)
	assert.Equal(t, ProviderItemTax{ID: "li_1"}, result.Items[0])
	assert.Zero(t, repo.callCount("ListTaxRatesByRegions"))
	assert.Zero(t, repo.callCount("ListTaxRateRulesByRates"))
}

// TestLocalProviderKuralsizBolgedeKuralSorgusuYapilmaz yalnızca varsayılan
// oranı olan bir bölgede ikinci turun atlandığını doğrular.
func TestLocalProviderKuralsizBolgedeKuralSorgusuYapilmaz(t *testing.T) {
	repo := newMemRepo()
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	provider := NewLocalProvider(repo)

	_, err := provider.Calculate(context.Background(), ProviderInput{
		RegionIDs:   []string{trRegionID},
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 1000}},
	})
	require.NoError(t, err)

	assert.Equal(t, 1, repo.callCount("ListTaxRatesByRegions"))
	assert.Zero(t, repo.callCount("ListTaxRateRulesByRates"),
		"kurallı oran yoksa kural sorgusu hiç yapılmamalı")
}

// TestLocalProviderOranKaynagiHatasiYukselir okuma hatasının yutulmadığını
// doğrular.
func TestLocalProviderOranKaynagiHatasiYukselir(t *testing.T) {
	repo := newMemRepo()
	repo.seedRootRegion(trRegionID, "TR")
	repo.failOn["ListTaxRatesByRegions"] = errors.Unavailable("db_down", "veritabanı erişilemez")
	provider := NewLocalProvider(repo)

	_, err := provider.Calculate(context.Background(), ProviderInput{
		RegionIDs:   []string{trRegionID},
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 1000}},
	})
	require.Error(t, err)
	assert.Equal(t, "db_down", errors.CodeOf(err))
}

// TestProviderRegistryKayitVeCozum sağlayıcı kaydının sözleşmesini doğrular.
func TestProviderRegistryKayitVeCozum(t *testing.T) {
	registry := NewProviderRegistry()
	local := NewLocalProvider(newMemRepo())
	require.NoError(t, registry.Register(local))

	t.Run("aynı kimlik ikinci kez reddedilir", func(t *testing.T) {
		err := registry.Register(NewLocalProvider(newMemRepo()))
		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, CodeProviderExists, errors.CodeOf(err))

		got, getErr := registry.Get(LocalProviderID)
		require.NoError(t, getErr)
		assert.Same(t, local, got, "çakışmada MEVCUT sağlayıcı korunmalı")
	})

	t.Run("boş kimlik yerel sağlayıcıya düşer", func(t *testing.T) {
		got, err := registry.Get("")
		require.NoError(t, err)
		assert.Equal(t, LocalProviderID, got.ID())
	})

	t.Run("bilinmeyen kimlik NotFound", func(t *testing.T) {
		_, err := registry.Get("avalara")
		require.Error(t, err)
		assert.True(t, errors.IsNotFound(err))
		assert.Contains(t, err.Error(), LocalProviderID, "mesaj kayıtlı kimlikleri yazmalı")
	})

	t.Run("nil ve boş kimlikli sağlayıcı reddedilir", func(t *testing.T) {
		require.Error(t, registry.Register(nil))

		err := registry.Register(&stubProvider{id: "   "})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
	})

	t.Run("kimlik listesi sıralıdır", func(t *testing.T) {
		r := NewProviderRegistry()
		require.NoError(t, r.Register(&stubProvider{id: "zeta"}))
		require.NoError(t, r.Register(&stubProvider{id: "alfa"}))
		assert.Equal(t, []string{"alfa", "zeta"}, r.IDs())
	})
}
