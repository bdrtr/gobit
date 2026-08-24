package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// taxOfItem sonuçtaki bir kalemi kimliğiyle bulur.
func taxOfItem(t *testing.T, result CalculateTaxResult, id string) ItemTax {
	t.Helper()

	for i := range result.Items {
		if result.Items[i].ID == id {
			return result.Items[i]
		}
	}
	t.Fatalf("%q kalemi sonuçta yok: %+v", id, result.Items)
	return ItemTax{}
}

// requireTotalIdentity toplam kimliğini doğrular: TaxTotal = Σ kalem + kargo.
//
// Kimlik her testte ayrıca denetlenir çünkü toplam sağlayıcıdan alınmaz,
// serviste yeniden toplanır; toplama hatası tek bir testte değil HER senaryoda
// görünmelidir.
func requireTotalIdentity(t *testing.T, result CalculateTaxResult) {
	t.Helper()

	var sum int64
	for i := range result.Items {
		sum += result.Items[i].TaxAmount
	}
	sum += result.Shipping.TaxAmount
	require.Equal(t, sum, result.TaxTotal, "toplam vergi, kalem vergilerinin toplamı olmalı")
}

// TestCalculateTaxVarsayilanOranUygulanir kuralsız bir kalemin bölgenin
// varsayılan oranına düştüğünü doğrular.
func TestCalculateTaxVarsayilanOranUygulanir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 3000}},
	})
	require.NoError(t, err)

	assert.True(t, result.RegionFound)
	assert.Equal(t, trRegionID, result.RegionID)
	assert.Equal(t, LocalProviderID, result.ProviderID)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int32(2000), line.RateBps)
	assert.Equal(t, rateA, line.RateID)
	assert.Equal(t, int64(3000), line.TaxableAmount)
	assert.Equal(t, int64(600), line.TaxAmount)
	assert.Equal(t, int64(600), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxKuralliOranYalnizcaEslesenKaleme kurallı oranın kapsamının
// gerçekten daraldığını doğrular.
func TestCalculateTaxKuralliOranYalnizcaEslesenKaleme(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 100)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_kitap")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_kitap", ProductID: "prod_kitap", Amount: 10_000},
			{ID: "li_diger", ProductID: "prod_diger", Amount: 10_000},
		},
	})
	require.NoError(t, err)

	kitap := taxOfItem(t, result, "li_kitap")
	assert.Equal(t, int32(100), kitap.RateBps, "kuralla eşleşen kalem indirimli orana düşmeli")
	assert.Equal(t, int64(100), kitap.TaxAmount)

	diger := taxOfItem(t, result, "li_diger")
	assert.Equal(t, int32(2000), diger.RateBps, "eşleşmeyen kalem varsayılana düşmeli")
	assert.Equal(t, int64(2000), diger.TaxAmount)

	assert.Equal(t, int64(2100), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxUrunKuraliUrunTipiKuraliniYener belirginlik sırasını
// doğrular.
func TestCalculateTaxUrunKuraliUrunTipiKuraliniYener(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	// Kimliği BÜYÜK olan oran ürün kuralını taşır: kazanmasının tek sebebi
	// belirginlik olmalı, sıra değil.
	repo.seedRuledRate(rateB, trRegionID, 800)
	repo.seedRule(ruleA, rateB, models.ReferenceProductType, "ptyp_gida")
	repo.seedRuledRate(rateC, trRegionID, 100)
	repo.seedRule(ruleB, rateC, models.ReferenceProduct, "prod_ekmek")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", ProductID: "prod_ekmek", ProductTypeID: "ptyp_gida", Amount: 10_000},
		},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int32(100), line.RateBps, "ürün kuralı ürün tipi kuralını yenmeli")
	assert.Equal(t, rateC, line.RateID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxEsitBelirginliktKucukKimlikKazanir eşitlik bozma kuralının
// BELİRLENİMCİ olduğunu doğrular.
func TestCalculateTaxEsitBelirginliktKucukKimlikKazanir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedRuledRate(rateB, trRegionID, 1000)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")
	repo.seedRuledRate(rateC, trRegionID, 2000)
	repo.seedRule(ruleB, rateC, models.ReferenceProduct, "prod_1")

	// Aynı girdi defalarca çalıştırılır: harita dolaşım sırasına bağlı bir
	// seçim burada kararsız sonuç verirdi.
	for i := range 20 {
		result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", ProductID: "prod_1", Amount: 10_000}},
		})
		require.NoError(t, err, "tur %d", i)
		assert.Equal(t, rateB, taxOfItem(t, result, "li_1").RateID,
			"tur %d: kimliği küçük oran kazanmalı", i)
	}
}

// TestCalculateTaxEyaletUlkeyiEzer eyalet varsayılanının ülke oranını
// tamamen değiştirdiğini ve oranların TOPLANMADIĞINI doğrular.
func TestCalculateTaxEyaletUlkeyiEzer(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
	repo.seedDefaultRate(rateA, usRegionID, 2000)
	repo.seedDefaultRate(rateB, trIstanbul, 725)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode:  "US",
		ProvinceCode: "CA",
		Items:        []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	assert.Equal(t, trIstanbul, result.RegionID, "en özel bölge sonuçta görünmeli")
	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int32(725), line.RateBps, "eyalet oranı ülkeyi EZMELİ")
	assert.Equal(t, int64(725), line.TaxAmount, "oranlar TOPLANMAMALI (2725 değil)")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxEyaletOranVermezseUlkeyeDuser üst halkaya geçişi doğrular.
func TestCalculateTaxEyaletOranVermezseUlkeyeDuser(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
	repo.seedDefaultRate(rateA, usRegionID, 2000)
	// Eyalette YALNIZCA tek bir ürüne yazılmış kurallı oran var.
	repo.seedRuledRate(rateB, trIstanbul, 0)
	repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_muaf")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode:  "US",
		ProvinceCode: "CA",
		Items: []TaxableItem{
			{ID: "li_muaf", ProductID: "prod_muaf", Amount: 10_000},
			{ID: "li_normal", ProductID: "prod_normal", Amount: 10_000},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(0), taxOfItem(t, result, "li_muaf").RateBps,
		"eyaletin kuralı eşleşen kaleme uygulanmalı")
	assert.Equal(t, int32(2000), taxOfItem(t, result, "li_normal").RateBps,
		"eyalet oran vermeyen kalem ülkeye düşmeli")
	assert.Equal(t, int64(2000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxBilinmeyenEyaletUlkeyeDuser tanımlanmamış bir eyalet
// kodunun hata değil ülke oranı ürettiğini doğrular.
func TestCalculateTaxBilinmeyenEyaletUlkeyeDuser(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(usRegionID, "US")
	repo.seedDefaultRate(rateA, usRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode:  "US",
		ProvinceCode: "ZZ",
		Items:        []TaxableItem{{ID: "li_1", Amount: 5000}},
	})
	require.NoError(t, err)

	assert.Equal(t, usRegionID, result.RegionID)
	assert.Equal(t, int64(1000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxBolgeYoksaSifirVergi bölge bulunamadığında hata DEĞİL sıfır
// vergi döndüğünü ve durumun sonuçta GÖRÜNÜR olduğunu doğrular.
func TestCalculateTaxBolgeYoksaSifirVergi(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "DE",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err, "yapılandırılmamış ülke sepeti düşürmemeli")

	assert.False(t, result.RegionFound, "çağıran yapılandırma eksiğini görebilmeli")
	assert.Empty(t, result.RegionID)
	assert.Empty(t, result.ProviderID)
	assert.Equal(t, int64(0), result.TaxTotal)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(10_000), line.TaxableAmount, "taban yine de raporlanmalı")
	assert.Equal(t, int64(0), line.TaxAmount)
	assert.Empty(t, line.RateID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxBolgeVarOranYoksaSifirVergi bölgesi olan ama hiç oranı
// olmayan bir ülkenin sıfır vergi ürettiğini doğrular.
//
// Bölge YOK durumundan ayrıdır: RegionFound true kalır.
func TestCalculateTaxBolgeVarOranYoksaSifirVergi(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.NoError(t, err)

	assert.True(t, result.RegionFound)
	assert.Equal(t, int64(0), result.TaxTotal)
	assert.Empty(t, taxOfItem(t, result, "li_1").RateID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxSifirTaban sıfır tabanın sıfır vergi ürettiğini ve oranın
// yine de raporlandığını doğrular.
func TestCalculateTaxSifirTaban(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 0}},
	})
	require.NoError(t, err)

	line := taxOfItem(t, result, "li_1")
	assert.Equal(t, int64(0), line.TaxAmount)
	assert.Equal(t, int32(2000), line.RateBps, "sıfır taban oranı görünmez yapmamalı")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxKalemsizSepet kalemi olmayan bir hesabın geçerli olduğunu
// doğrular.
func TestCalculateTaxKalemsizSepet(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{CountryCode: "TR"})
	require.NoError(t, err)

	assert.Empty(t, result.Items)
	assert.Equal(t, int64(0), result.TaxTotal)
	assert.Equal(t, ShippingLineID, result.Shipping.ID)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxKargoVarsayilanOlarakVergilenmez sepet akışının bugünkü
// sözleşmesinin korunduğunu doğrular.
func TestCalculateTaxKargoVarsayilanOlarakVergilenmez(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
		Shipping:    ShippingInput{OptionID: "sopt_1", Amount: 5000},
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), result.Shipping.TaxAmount, "kargo tabana GİRMEMELİ")
	assert.Equal(t, int64(0), result.Shipping.TaxableAmount)
	assert.Equal(t, int64(2000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxKargoIstenirseVergilenir açık talebin kargoyu tabana
// soktuğunu ve varsayılan orana düştüğünü doğrular.
func TestCalculateTaxKargoIstenirseVergilenir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
		Shipping:    ShippingInput{OptionID: "sopt_1", Amount: 5000, Taxable: true},
	})
	require.NoError(t, err)

	assert.Equal(t, ShippingLineID, result.Shipping.ID)
	assert.Equal(t, int64(5000), result.Shipping.TaxableAmount)
	assert.Equal(t, int64(1000), result.Shipping.TaxAmount)
	assert.Equal(t, int64(3000), result.TaxTotal)
	requireTotalIdentity(t, result)
}

// TestCalculateTaxKargoKendiOraniniSecer "shipping_option" kuralının kargo
// satırına uygulandığını ve KALEMLERE uygulanmadığını doğrular.
func TestCalculateTaxKargoKendiOraniniSecer(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)
	repo.seedRuledRate(rateB, trRegionID, 800)
	repo.seedRule(ruleA, rateB, models.ReferenceShippingOption, "sopt_hizli")

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", ProductID: "sopt_hizli", Amount: 10_000}},
		Shipping:    ShippingInput{OptionID: "sopt_hizli", Amount: 10_000, Taxable: true},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(800), result.Shipping.RateBps, "kargo kendi kuralına düşmeli")
	assert.Equal(t, int32(2000), taxOfItem(t, result, "li_1").RateBps,
		"kargo kuralı, aynı kimliği taşıyan bir ÜRÜNE uygulanmamalı")
	requireTotalIdentity(t, result)
}

// TestCalculateTaxYuvarlamaAsagi yuvarlama yönünün müşteri lehine olduğunu
// doğrular.
func TestCalculateTaxYuvarlamaAsagi(t *testing.T) {
	tests := []struct {
		name    string
		base    int64
		rateBps int32
		want    int64
	}{
		// 1999 × %18 = 359,82 -> 359
		{"kesirli kalan aşağı iner", 1999, 1800, 359},
		// 1 × %20 = 0,2 -> 0
		{"bir kuruşun altı sıfırlanır", 1, 2000, 0},
		// 5 × %50 = 2,5 -> 2 (yakına yuvarlama 3 verirdi)
		{"tam yarım aşağı iner", 5, 5000, 2},
		// 10000 × %20 = 2000 -> tam
		{"tam bölünen değişmez", 10_000, 2000, 2000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")
			repo.seedDefaultRate(rateA, trRegionID, tt.rateBps)

			result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
				CountryCode: "TR",
				Items:       []TaxableItem{{ID: "li_1", Amount: tt.base}},
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, taxOfItem(t, result, "li_1").TaxAmount)
			requireTotalIdentity(t, result)
		})
	}
}

// TestCalculateTaxKalemBasinaYuvarlamaFarki belgelenen AYRIŞMAYI sayısal
// olarak sabitler.
//
// Üç kalemin her biri 333 ve oran %20: kalem başına 66 (333×0,2 = 66,6),
// toplam 198. Sepetin tamamı üzerinden hesaplansaydı 999×0,2 = 199,8 -> 199
// olurdu. Fark tam 1 minor unit'tir ve MÜŞTERİ LEHİNE kalır.
func TestCalculateTaxKalemBasinaYuvarlamaFarki(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items: []TaxableItem{
			{ID: "li_1", Amount: 333},
			{ID: "li_2", Amount: 333},
			{ID: "li_3", Amount: 333},
		},
	})
	require.NoError(t, err)

	for _, id := range []string{"li_1", "li_2", "li_3"} {
		assert.Equal(t, int64(66), taxOfItem(t, result, id).TaxAmount)
	}
	assert.Equal(t, int64(198), result.TaxTotal)

	tekSeferde, err := TaxOf(999, 2000)
	require.NoError(t, err)
	assert.Equal(t, int64(199), tekSeferde)
	assert.Less(t, result.TaxTotal, tekSeferde,
		"kalem başına hesap, sepet tabanı üzerinden hesaptan KÜÇÜK ya da eşit olmalı")
}

// TestCalculateTaxTasmaReddedilir int64 sınırlarının hem kalem hem toplam
// düzeyinde uygulandığını doğrular.
func TestCalculateTaxTasmaReddedilir(t *testing.T) {
	t.Run("kalem tabanı tavanı aşarsa", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedDefaultRate(rateA, trRegionID, 2000)

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", Amount: MaxTaxableAmount + 1}},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
		assert.Zero(t, repo.callCount("ResolveTaxRegions"), "girdi hatası için veritabanına gidilmemeli")
	})

	t.Run("kalem tabanı negatifse", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", Amount: -1}},
		})
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Zero(t, repo.callCount("ResolveTaxRegions"))
	})

	t.Run("vergi toplamı tavanı aşarsa", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedDefaultRate(rateA, trRegionID, models.MaxRateBps)

		// Her kalem tek başına geçerli; %100 oranla toplam vergi tavanı aşar.
		items := make([]TaxableItem, 0, 3)
		for i := range 3 {
			items = append(items, TaxableItem{
				ID:     fmt.Sprintf("li_%d", i),
				Amount: MaxTaxableAmount / 2,
			})
		}

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR",
			Items:       items,
		})
		require.Error(t, err)
		assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
	})
}

// TestCalculateTaxGecersizGirdiReddedilir girdi doğrulamasının veritabanına
// GİTMEDEN yapıldığını doğrular.
func TestCalculateTaxGecersizGirdiReddedilir(t *testing.T) {
	tests := map[string]CalculateTaxInput{
		"ülke kodu boş":       {Items: []TaxableItem{{ID: "li_1", Amount: 1}}},
		"ülke kodu üç harf":   {CountryCode: "TUR"},
		"ülke kodu rakamlı":   {CountryCode: "T1"},
		"eyalet kodu tireyle": {CountryCode: "US", ProvinceCode: "-CA"},
		"eyalet kodu uzun":    {CountryCode: "US", ProvinceCode: "CALIFORNIAAA"},
		"kalem kimliği boş": {
			CountryCode: "TR",
			Items:       []TaxableItem{{Amount: 1}},
		},
		"kalem kimliği tekrar": {
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: "li_1", Amount: 1}, {ID: "li_1", Amount: 2}},
		},
		"kalem kimliği kargoya ayrılmış": {
			CountryCode: "TR",
			Items:       []TaxableItem{{ID: ShippingLineID, Amount: 1}},
		},
		"kargo tutarı negatif": {
			CountryCode: "TR",
			Shipping:    ShippingInput{Amount: -5, Taxable: true},
		},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			svc, repo := newTestService(t)
			repo.seedRootRegion(trRegionID, "TR")

			_, err := svc.CalculateTax(context.Background(), in)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata: %v", err)
			assert.Zero(t, repo.callCount("ResolveTaxRegions"))
		})
	}
}

// TestCalculateTaxKalemSayisiSinirli sınırsız bir listenin reddedildiğini
// doğrular.
func TestCalculateTaxKalemSayisiSinirli(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRootRegion(trRegionID, "TR")

	items := make([]TaxableItem, 0, MaxItems+1)
	for i := range MaxItems + 1 {
		items = append(items, TaxableItem{ID: fmt.Sprintf("li_%d", i), Amount: 1})
	}

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       items,
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
	assert.Zero(t, repo.callCount("ResolveTaxRegions"))
}

// TestCalculateTaxSorguSayisiKalemSayisindanBagimsiz N+1 olmadığını
// kanıtlar.
func TestCalculateTaxSorguSayisiKalemSayisindanBagimsiz(t *testing.T) {
	counts := map[int]int{}

	for _, itemCount := range []int{1, 50} {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedProvinceRegion(trIstanbul, "TR", "34", trRegionID)
		repo.seedDefaultRate(rateA, trRegionID, 2000)
		repo.seedRuledRate(rateB, trIstanbul, 100)
		repo.seedRule(ruleA, rateB, models.ReferenceProduct, "prod_1")

		items := make([]TaxableItem, 0, itemCount)
		for i := range itemCount {
			items = append(items, TaxableItem{
				ID: fmt.Sprintf("li_%d", i), ProductID: "prod_1", Amount: 1000,
			})
		}

		_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode: "TR", ProvinceCode: "34", Items: items,
		})
		require.NoError(t, err)

		counts[itemCount] = repo.callCount("ResolveTaxRegions") +
			repo.callCount("ListTaxRatesByRegions") +
			repo.callCount("ListTaxRateRulesByRates")
	}

	assert.Equal(t, 3, counts[1], "bölge + oran + kural: üç sorgu")
	assert.Equal(t, counts[1], counts[50],
		"sorgu sayısı kalem sayısıyla BÜYÜMEMELİ (N+1 yok)")
}

// TestCalculateTaxBilinmeyenSaglayiciKurulumHatasidir bölgenin kayıtlı
// olmayan bir sağlayıcıya işaret etmesinin sessizce yerele DÜŞMEDİĞİNİ
// doğrular.
func TestCalculateTaxBilinmeyenSaglayiciKurulumHatasidir(t *testing.T) {
	svc, repo := newTestService(t)
	repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "avalara"})
	repo.seedDefaultRate(rateA, trRegionID, 2000)

	_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
		CountryCode: "TR",
		Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
	})
	require.Error(t, err)
	assert.Equal(t, CodeProviderMisconfigured, errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"kurulum hatası istemciye 404 olarak değil sunucu hatası olarak dönmeli")
}

// TestCalculateTaxSaglayiciZincirdenDevralinir eyalet bölgesinin ülkenin DIŞ
// vergi sağlayıcısını sessizce YERELE düşürmediğini doğrular.
//
// Senaryo GÖRÜNMEYEN bir para hatasıdır: ülke kökü dış bir otoriteye bağlıyken
// tek bir istisna için açılan eyalet bölgesinin provider_id'si boş kalırsa, o
// eyaletteki her sepet yanlış otoriteyle vergilenir ve fatura hatalı çıkar.
// Sahte sağlayıcılar bilinçli olarak yerel hesabın ÜRETEMEYECEĞİ tutarlar
// döner; sonuçtaki tutar, hesabı kimin yaptığının kanıtıdır.
func TestCalculateTaxSaglayiciZincirdenDevralinir(t *testing.T) {
	const eyaletKodu = "34"

	avalara := &stubProvider{id: "avalara", result: ProviderResult{
		Items: []ProviderItemTax{{ID: "li_1", RateBps: 1000, TaxAmount: 999}},
	}}
	taxjar := &stubProvider{id: "taxjar", result: ProviderResult{
		Items: []ProviderItemTax{{ID: "li_1", RateBps: 500, TaxAmount: 111}},
	}}

	// kur ülkesi "avalara"ya bağlı, eyaleti verilen sağlayıcıyı taşıyan bir
	// servis üretir.
	kur := func(t *testing.T, eyaletSaglayici string) *Service {
		t.Helper()

		repo := newMemRepo()
		repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "avalara"})
		province, parent := eyaletKodu, trRegionID
		repo.seedRegion(models.TaxRegion{
			ID:           trIstanbul,
			CountryCode:  "TR",
			ProvinceCode: &province,
			ParentID:     &parent,
			ProviderID:   eyaletSaglayici,
		})
		// Eyaletin kendi varsayılan oranı vardır: yerele düşen bir hesap 725
		// bulur ve sahte sağlayıcıların tutarlarından ayırt edilir.
		repo.seedDefaultRate(rateA, trIstanbul, 725)

		registry := NewProviderRegistry()
		require.NoError(t, registry.Register(NewLocalProvider(repo)))
		require.NoError(t, registry.Register(avalara))
		require.NoError(t, registry.Register(taxjar))
		return New(repo, Options{Providers: registry, Now: func() time.Time { return testNow }})
	}

	// hesapla eyalet için tek kalemlik bir hesap çalıştırır.
	hesapla := func(t *testing.T, svc *Service) CalculateTaxResult {
		t.Helper()

		result, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
			CountryCode:  "TR",
			ProvinceCode: eyaletKodu,
			Items:        []TaxableItem{{ID: "li_1", Amount: 10_000}},
		})
		require.NoError(t, err)
		return result
	}

	t.Run("boş bırakılan eyalet ülkenin sağlayıcısını devralır", func(t *testing.T) {
		result := hesapla(t, kur(t, ""))

		assert.Equal(t, "avalara", result.ProviderID,
			"eyaletin boş provider_id'si ülkenin dış otoritesini yerele DÜŞÜRMEMELİ")
		line := taxOfItem(t, result, "li_1")
		assert.Equal(t, int64(999), line.TaxAmount, "hesabı ülkenin sağlayıcısı yapmalı")
		assert.Equal(t, int32(1000), line.RateBps)
		assert.Equal(t, trIstanbul, result.RegionID, "hesap yine EN ÖZEL bölgeye dayanır")
		requireTotalIdentity(t, result)
	})

	t.Run("eyalet yerel hesabı AÇIKÇA seçebilir", func(t *testing.T) {
		result := hesapla(t, kur(t, LocalProviderID))

		assert.Equal(t, LocalProviderID, result.ProviderID,
			"devralma, yerelin açıkça seçilmesini engellememeli")
		line := taxOfItem(t, result, "li_1")
		assert.Equal(t, int32(725), line.RateBps)
		assert.Equal(t, int64(725), line.TaxAmount)
		requireTotalIdentity(t, result)
	})

	t.Run("eyaletin kendi sağlayıcısı ülkeyi ezer", func(t *testing.T) {
		result := hesapla(t, kur(t, "taxjar"))

		assert.Equal(t, "taxjar", result.ProviderID)
		assert.Equal(t, int64(111), taxOfItem(t, result, "li_1").TaxAmount)
		requireTotalIdentity(t, result)
	})
}

// TestCalculateTaxSaglayiciSonucuDogrulanir dış bir sağlayıcının sözleşme dışı
// çıktısının sepet toplamına SIZMADIĞINI doğrular.
func TestCalculateTaxSaglayiciSonucuDogrulanir(t *testing.T) {
	tests := map[string]ProviderResult{
		"vergi tabandan büyük": {
			Items: []ProviderItemTax{{ID: "li_1", RateBps: 2000, TaxAmount: 10_001}},
		},
		"vergi negatif": {
			Items: []ProviderItemTax{{ID: "li_1", RateBps: 2000, TaxAmount: -1}},
		},
		"oran aralık dışı": {
			Items: []ProviderItemTax{{ID: "li_1", RateBps: 20_000, TaxAmount: 10}},
		},
		"kalem eksik": {
			Items: []ProviderItemTax{},
		},
		"bilinmeyen kalem": {
			Items: []ProviderItemTax{{ID: "li_yok", RateBps: 0, TaxAmount: 0}},
		},
		"kalem tekrar": {
			Items: []ProviderItemTax{
				{ID: "li_1", RateBps: 0, TaxAmount: 0},
				{ID: "li_1", RateBps: 0, TaxAmount: 0},
			},
		},
		"kargo vergisi vergilenmeyen kargoya yazılmış": {
			Items:    []ProviderItemTax{{ID: "li_1", RateBps: 2000, TaxAmount: 2000}},
			Shipping: ProviderItemTax{ID: ShippingLineID, RateBps: 2000, TaxAmount: 500},
		},
	}

	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			repo := newMemRepo()
			repo.seedRegion(models.TaxRegion{ID: trRegionID, CountryCode: "TR", ProviderID: "sahte"})

			registry := NewProviderRegistry()
			require.NoError(t, registry.Register(&stubProvider{id: "sahte", result: result}))
			svc := New(repo, Options{Providers: registry, Now: func() time.Time { return testNow }})

			_, err := svc.CalculateTax(context.Background(), CalculateTaxInput{
				CountryCode: "TR",
				Items:       []TaxableItem{{ID: "li_1", Amount: 10_000}},
				Shipping:    ShippingInput{Amount: 5000},
			})
			require.Error(t, err)
			assert.Equal(t, CodeProviderInvalidResult, errors.CodeOf(err))
			assert.True(t, errors.HasKind(err, errors.KindInternal))
		})
	}
}

// TestDefaultRateForCountry sade yolun davranışını doğrular.
func TestDefaultRateForCountry(t *testing.T) {
	t.Run("varsayılan oran döner", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedDefaultRate(rateA, trRegionID, 2000)
		repo.seedRuledRate(rateB, trRegionID, 100)

		rate, found, err := svc.DefaultRateForCountry(context.Background(), "tr")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, int32(2000), rate, "kurallı oran sade yolda görünmemeli")
	})

	t.Run("bölge yoksa found false", func(t *testing.T) {
		svc, _ := newTestService(t)

		rate, found, err := svc.DefaultRateForCountry(context.Background(), "DE")
		require.NoError(t, err)
		assert.False(t, found)
		assert.Equal(t, int32(0), rate)
	})

	t.Run("varsayılan oran yoksa found false", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedRuledRate(rateB, trRegionID, 100)

		_, found, err := svc.DefaultRateForCountry(context.Background(), "TR")
		require.NoError(t, err)
		assert.False(t, found, "yalnızca kurallı oranı olan bölge sade yolda oran vermemeli")
	})

	t.Run("eyalet oranı sade yolda görünmez", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(usRegionID, "US")
		repo.seedProvinceRegion(trIstanbul, "US", "CA", usRegionID)
		repo.seedDefaultRate(rateA, usRegionID, 2000)
		repo.seedDefaultRate(rateB, trIstanbul, 725)

		rate, found, err := svc.DefaultRateForCountry(context.Background(), "US")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, int32(2000), rate)
	})

	t.Run("sözleşme dışı oran hata döner", func(t *testing.T) {
		svc, repo := newTestService(t)
		repo.seedRootRegion(trRegionID, "TR")
		repo.seedRate(models.TaxRate{
			ID: rateA, TaxRegionID: trRegionID, Name: "bozuk", RateBps: 99_999, IsDefault: true,
		})

		_, _, err := svc.DefaultRateForCountry(context.Background(), "TR")
		require.Error(t, err)
		assert.Equal(t, CodeRateOutOfRange, errors.CodeOf(err))
	})

	t.Run("geçersiz ülke kodu reddedilir", func(t *testing.T) {
		svc, repo := newTestService(t)

		_, _, err := svc.DefaultRateForCountry(context.Background(), "TUR")
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Zero(t, repo.callCount("ResolveTaxRegions"))
	})
}

// stubProvider betiklenmiş bir sonuç dönen sahte sağlayıcıdır.
//
// Amacı, servisin sağlayıcı çıktısını DOĞRULADIĞINI kanıtlamaktır; yerel
// sağlayıcı hiçbir zaman sözleşme dışı çıktı üretmediği için bu yol ancak
// sahteyle sınanabilir.
type stubProvider struct {
	id     string
	result ProviderResult
}

var _ TaxProvider = (*stubProvider)(nil)

// ID sağlayıcının kimliğini döner.
func (p *stubProvider) ID() string { return p.id }

// Calculate betiklenmiş sonucu döner.
func (p *stubProvider) Calculate(_ context.Context, _ ProviderInput) (ProviderResult, error) {
	return p.result, nil
}
