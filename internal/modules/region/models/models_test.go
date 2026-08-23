package models_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestMinorUnitFactor ondalık basamak sayısının bölme çarpanına doğru
// çevrildiğini kanıtlar.
//
// Bu tablonun tamamı gerçek para birimlerinden gelir ve modülün varlık
// sebebini sınar: tutarlar minor unit TAM SAYI saklandığı için sabit bir 100
// çarpanı varsayan bir sunum katmanı yen tutarlarını yüz kat küçük, dinar
// tutarlarını on kat büyük gösterirdi.
func TestMinorUnitFactor(t *testing.T) {
	cases := []struct {
		code   string
		digits int32
		factor int64
		// amount minor unit tutar, major ise onun tam kısmıdır.
		amount int64
		major  int64
	}{
		{code: "JPY", digits: 0, factor: 1, amount: 1999, major: 1999},
		{code: "TRY", digits: 2, factor: 100, amount: 1999, major: 19},
		{code: "USD", digits: 2, factor: 100, amount: 100, major: 1},
		{code: "KWD", digits: 3, factor: 1000, amount: 1999, major: 1},
		{code: "UYW", digits: 4, factor: 10_000, amount: 19_999, major: 1},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			currency := models.Currency{Code: tc.code, DecimalDigits: tc.digits}

			assert.Equal(t, tc.factor, currency.MinorUnitFactor())
			assert.Equal(t, tc.major, tc.amount/currency.MinorUnitFactor())
		})
	}
}

// TestMinorUnitFactorOutOfRangeIsSafe aralık dışı bir basamak sayısının sıfır
// değil BİR döndürdüğünü kanıtlar.
//
// Sıfır dönseydi çağıranda sıfıra bölme oluşurdu; bir dönmek en kötü ihtimalle
// ölçeği bozar ama süreci düşürmez.
func TestMinorUnitFactorOutOfRangeIsSafe(t *testing.T) {
	for _, digits := range []int32{-1, models.MaxDecimalDigits + 1, 100} {
		currency := models.Currency{Code: "XXX", DecimalDigits: digits}

		assert.Equal(t, int64(1), currency.MinorUnitFactor(), "basamak: %d", digits)
	}
}

// TestTaxRatePercent baz puanın tam sayı yüzde ve kalan olarak ayrıldığını
// kanıtlar.
//
// Yüzde float dönmez: 2050 baz puan "%20 ve 50 baz puan"dır ve iki tam sayı
// hâlinde taşınır (plan Bölüm 8).
func TestTaxRatePercent(t *testing.T) {
	cases := []struct {
		rate      int32
		percent   int32
		remainder int32
	}{
		{rate: 0, percent: 0, remainder: 0},
		{rate: 1, percent: 0, remainder: 1},
		{rate: 800, percent: 8, remainder: 0},
		{rate: 2000, percent: 20, remainder: 0},
		{rate: 2050, percent: 20, remainder: 50},
		{rate: models.MaxTaxRate, percent: 100, remainder: 0},
	}

	for _, tc := range cases {
		region := models.Region{TaxRate: tc.rate}

		percent, remainder := region.TaxRatePercent()
		assert.Equal(t, tc.percent, percent, "oran: %d", tc.rate)
		assert.Equal(t, tc.remainder, remainder, "oran: %d", tc.rate)
	}
}

// TestRegionPatchedAppliesOnlyGivenFields yamanın yalnızca dolu alanları
// uyguladığını ve alıcıyı DEĞİŞTİRMEDİĞİNİ kanıtlar.
func TestRegionPatchedAppliesOnlyGivenFields(t *testing.T) {
	original := models.Region{
		ID:             "reg_1",
		Name:           "Türkiye",
		CurrencyCode:   "TRY",
		AutomaticTaxes: true,
		TaxRate:        2000,
	}

	name := "Yeni"
	patched := original.Patched(models.RegionPatch{Name: &name})

	assert.Equal(t, "Yeni", patched.Name)
	assert.Equal(t, "TRY", patched.CurrencyCode, "verilmeyen alan değişmemeli")
	assert.True(t, patched.AutomaticTaxes)
	assert.Equal(t, int32(2000), patched.TaxRate)
	assert.Equal(t, "Türkiye", original.Name, "alıcı değişmemeli")
}

// TestRegionPatchedWritesZeroValues sıfır değerli bir yamanın "dokunma"
// sayılmadığını kanıtlar.
//
// İşaretçi kullanmanın tek sebebi budur: false ve 0 geçerli değerlerdir.
func TestRegionPatchedWritesZeroValues(t *testing.T) {
	original := models.Region{Name: "X", CurrencyCode: "TRY", AutomaticTaxes: true, TaxRate: 2000}

	automatic := false
	rate := int32(0)
	patched := original.Patched(models.RegionPatch{AutomaticTaxes: &automatic, TaxRate: &rate})

	assert.False(t, patched.AutomaticTaxes)
	assert.Zero(t, patched.TaxRate)
}

// TestRegionPatchEmpty boş yamanın tanındığını kanıtlar.
func TestRegionPatchEmpty(t *testing.T) {
	assert.True(t, models.RegionPatch{}.Empty())

	name := "X"
	code := "TRY"
	automatic := false
	rate := int32(0)
	assert.False(t, models.RegionPatch{Name: &name}.Empty())
	assert.False(t, models.RegionPatch{CurrencyCode: &code}.Empty())
	assert.False(t, models.RegionPatch{AutomaticTaxes: &automatic}.Empty(),
		"false bir değerdir, 'verilmedi' değildir")
	assert.False(t, models.RegionPatch{TaxRate: &rate}.Empty(),
		"0 bir değerdir, 'verilmedi' değildir")
}

// TestNewRegionIDIsPrefixedAndSortable kimliğin önekli, doğru uzunlukta,
// tekil ve ZAMAN SIRALI olduğunu kanıtlar.
//
// Sıralanabilirlik iddiası boş değildir: "ORDER BY id" oluşturma sırasını
// verdiği için listeleme sorguları ayrı bir zaman sütununa göre sıralamaz.
func TestNewRegionIDIsPrefixedAndSortable(t *testing.T) {
	base := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	first := models.NewRegionID(base)
	second := models.NewRegionID(base.Add(time.Millisecond))

	require.True(t, strings.HasPrefix(first, models.RegionIDPrefix))
	assert.Len(t, strings.TrimPrefix(first, models.RegionIDPrefix), models.IDBodyLength())
	assert.Less(t, first, second, "sonra üretilen kimlik sözlüksel olarak da sonra gelmeli")

	seen := map[string]struct{}{}
	for range 1000 {
		id := models.NewRegionID(base)
		_, dup := seen[id]
		require.False(t, dup, "aynı milisaniyede üretilen kimlikler tekrar etmemeli: %s", id)
		seen[id] = struct{}{}
	}
}

// TestNewIDClampsPreEpochTime 1970 öncesi bir zaman damgasının sıralamayı
// bozmadığını kanıtlar.
func TestNewIDClampsPreEpochTime(t *testing.T) {
	old := models.NewRegionID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))
	epoch := models.NewRegionID(time.Unix(0, 0))

	assert.Len(t, strings.TrimPrefix(old, models.RegionIDPrefix), models.IDBodyLength())
	// İkisi de tabana çekilir; zaman kısmı aynı olduğu için önek uzunluğunca
	// ortak başlangıç taşırlar.
	const stampPrefixLen = 9 // 48 bitlik damganın Base32 karşılığı
	assert.Equal(t,
		strings.TrimPrefix(epoch, models.RegionIDPrefix)[:stampPrefixLen],
		strings.TrimPrefix(old, models.RegionIDPrefix)[:stampPrefixLen],
	)
}
