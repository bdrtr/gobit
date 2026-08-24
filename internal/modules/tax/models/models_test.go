package models_test

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestNewIDBicimVeSira kimliklerin biçimini ve ZAMAN SIRALI olduğunu
// doğrular.
//
// Sıralanabilirlik bir süs değildir: vergi hesabındaki eşitlik bozma kuralı
// ("kimliği küçük oran kazanır") tam olarak bu sıraya dayanır ve kimlikler
// sıralanabilir olmasaydı kural "rastgele biri kazanır" demek olurdu.
func TestNewIDBicimVeSira(t *testing.T) {
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

	var ids []string
	for i := range 50 {
		ids = append(ids, models.NewTaxRateID(base.Add(time.Duration(i)*time.Millisecond)))
	}

	for _, id := range ids {
		assert.True(t, strings.HasPrefix(id, models.TaxRateIDPrefix), "önek: %s", id)
		assert.Len(t, id, len(models.TaxRateIDPrefix)+models.IDBodyLength())
	}

	assert.True(t, sort.StringsAreSorted(ids),
		"artan zamanla üretilen kimlikler sözlüksel olarak da artmalı")

	tekil := map[string]bool{}
	for _, id := range ids {
		require.False(t, tekil[id], "kimlik tekrar etti: %s", id)
		tekil[id] = true
	}
}

// TestNewIDOneklerFarkli her kaydın kendi önekini taşıdığını doğrular.
func TestNewIDOneklerFarkli(t *testing.T) {
	now := time.Now()

	assert.True(t, strings.HasPrefix(models.NewTaxRegionID(now), "taxreg_"))
	assert.True(t, strings.HasPrefix(models.NewTaxRateID(now), "taxrate_"))
	assert.True(t, strings.HasPrefix(models.NewTaxRateRuleID(now), "taxrule_"))

	// Oran ile bölge önekleri birbirinin ÖN EKİ OLMAMALIDIR; olsaydı önek
	// denetimi yanlış türde bir kimliği kabul ederdi.
	assert.False(t, strings.HasPrefix(models.TaxRateIDPrefix, models.TaxRegionIDPrefix))
	assert.False(t, strings.HasPrefix(models.TaxRegionIDPrefix, models.TaxRateIDPrefix))
	assert.False(t, strings.HasPrefix(models.TaxRateRuleIDPrefix, models.TaxRateIDPrefix))
}

// TestNewID1970OncesiTabanaCekilir negatif zaman damgasının sıralamayı
// bozmadığını doğrular.
func TestNewID1970OncesiTabanaCekilir(t *testing.T) {
	eski := models.NewTaxRateID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	yeni := models.NewTaxRateID(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	assert.Less(t, eski, yeni, "tabana çekilen eski damga yine de küçük kalmalı")
}

// TestRuleReferenceGecerlilik tanımlı referans türlerini doğrular.
func TestRuleReferenceGecerlilik(t *testing.T) {
	for _, ref := range []models.RuleReference{
		models.ReferenceProduct, models.ReferenceProductType, models.ReferenceShippingOption,
	} {
		assert.True(t, ref.Valid(), "referans: %s", ref)
		assert.Positive(t, ref.Specificity())
	}

	for _, ref := range []models.RuleReference{"", "variant", "PRODUCT"} {
		assert.False(t, ref.Valid(), "referans: %q", ref)
		assert.Zero(t, ref.Specificity())
	}
}

// TestRuleReferenceBelirginlikSirasi ürün kuralının ürün tipini yendiğini
// doğrular.
func TestRuleReferenceBelirginlikSirasi(t *testing.T) {
	assert.Greater(t, models.ReferenceProduct.Specificity(), models.ReferenceProductType.Specificity(),
		"tek bir ürüne yazılmış kural, tipe yazılmış kuraldan DAHA ÖZELDİR")
	assert.Equal(t, models.ReferenceProductType.Specificity(), models.ReferenceShippingOption.Specificity(),
		"kargo kuralı kalemlerle yarışmaz; derecesi ürün tipiyle aynı kabul edilir")
}

// TestTaxRegionHiyerarsiYardimcilari kök ve eyalet ayrımını doğrular.
func TestTaxRegionHiyerarsiYardimcilari(t *testing.T) {
	root := models.TaxRegion{ID: "taxreg_1", CountryCode: "TR"}
	assert.True(t, root.IsRoot())
	assert.Empty(t, root.Province())
	assert.Empty(t, root.Parent())

	province := "34"
	parent := "taxreg_1"
	child := models.TaxRegion{ID: "taxreg_2", CountryCode: "TR", ProvinceCode: &province, ParentID: &parent}
	assert.False(t, child.IsRoot())
	assert.Equal(t, "34", child.Province())
	assert.Equal(t, "taxreg_1", child.Parent())
}

// TestTaxRatePercentFloatUretmez yüzde gösteriminin iki TAM SAYI olarak
// döndüğünü doğrular.
func TestTaxRatePercentFloatUretmez(t *testing.T) {
	tests := map[int32][2]int32{
		0:      {0, 0},
		100:    {1, 0},
		2000:   {20, 0},
		2050:   {20, 50},
		10_000: {100, 0},
	}

	for bps, want := range tests {
		percent, remainder := models.TaxRate{RateBps: bps}.RatePercent()
		assert.Equal(t, want[0], percent, "bps: %d", bps)
		assert.Equal(t, want[1], remainder, "bps: %d", bps)
	}
}

// TestTaxRatePatchDokunulmayanAlaniKorur kısmi güncellemenin saf bir dönüşüm
// olduğunu doğrular.
func TestTaxRatePatchDokunulmayanAlaniKorur(t *testing.T) {
	code := "KDV20"
	original := models.TaxRate{
		ID: "taxrate_1", Name: "KDV", Code: &code, RateBps: 2000, IsDefault: true,
		Metadata: map[string]any{"a": "b"},
	}

	yeniAd := "KDV Yeni"
	patched := original.Patched(models.TaxRatePatch{Name: &yeniAd})

	assert.Equal(t, "KDV Yeni", patched.Name)
	assert.Equal(t, "KDV20", patched.RateCode())
	assert.Equal(t, int32(2000), patched.RateBps)
	assert.True(t, patched.IsDefault)
	assert.Equal(t, "KDV", original.Name, "alıcı DEĞİŞMEMELİ")
}

// TestTaxRatePatchKodKaldirma boş dizenin kodu sildiğini doğrular.
func TestTaxRatePatchKodKaldirma(t *testing.T) {
	code := "KDV20"
	original := models.TaxRate{Code: &code}

	bos := ""
	assert.Nil(t, original.Patched(models.TaxRatePatch{Code: &bos}).Code)

	yeni := "KDV18"
	assert.Equal(t, "KDV18", original.Patched(models.TaxRatePatch{Code: &yeni}).RateCode())
	assert.Equal(t, "KDV20", original.RateCode(), "alıcı DEĞİŞMEMELİ")
}

// TestTaxRatePatchEmpty boş yamanın tanınmasını doğrular.
func TestTaxRatePatchEmpty(t *testing.T) {
	assert.True(t, models.TaxRatePatch{}.Empty())

	name := "x"
	assert.False(t, models.TaxRatePatch{Name: &name}.Empty())
	assert.False(t, models.TaxRatePatch{Metadata: map[string]any{}}.Empty())
}

// TestOranSinirlariMigrationlaUyumlu sabitlerin veritabanı CHECK'iyle aynı
// olduğunu doğrular.
//
// İkisi ayrışırsa servis kabul ettiği bir değeri yazamaz ve hata, doğrulamadan
// GEÇTİKTEN sonra kısıt ihlali olarak çıkardı.
func TestOranSinirlariMigrationlaUyumlu(t *testing.T) {
	assert.Equal(t, int32(0), models.MinRateBps)
	assert.Equal(t, int32(10_000), models.MaxRateBps, "migration: rate_bps <= 10000")
	// Sabit baz puan ÖLÇEĞİ değil, bir yüzdedeki baz puan sayısıdır; ölçek
	// (10000) service.BpsScale'dir ve ikisi ayrı adlarla durur.
	assert.Equal(t, int32(100), models.BpsPerPercent)
	assert.Equal(t, 2, models.CountryCodeLength)
	assert.Equal(t, 10, models.MaxProvinceCodeLength, "migration: province_code en fazla 10 karakter")
}
