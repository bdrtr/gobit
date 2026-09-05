package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/region/models"
)

// TestNormalizeCurrencyCode ISO 4217 kodunun kabul ve ret kurallarını
// kanıtlar.
func TestNormalizeCurrencyCode(t *testing.T) {
	t.Run("kabul edilenler", func(t *testing.T) {
		cases := []struct{ input, want string }{
			{input: "TRY", want: "TRY"},
			{input: "try", want: "TRY"},
			{input: "TrY", want: "TRY"},
			{input: "  usd  ", want: "USD"},
			{input: "\tjpy\n", want: "JPY"},
		}
		for _, tc := range cases {
			got, err := NormalizeCurrencyCode(tc.input)
			require.NoError(t, err, "girdi: %q", tc.input)
			assert.Equal(t, tc.want, got, "girdi: %q", tc.input)
		}
	})

	t.Run("reddedilenler", func(t *testing.T) {
		// "₺₺₺" üç RUNE'dur ama üç bayt değildir; uzunluk bayt üzerinden
		// ölçülseydi burada uzunluk denetimi geçer, harf denetiminde takılır
		// ve mesaj yanlış sebebi gösterirdi.
		//
		// "ıls" ve "ſek" ASCII DIŞI harflerdir ama Unicode'un basit büyük harf
		// eşlemesi onları "ILS" ve "SEK"e — tohumdaki iki GERÇEK para
		// birimine — taşır. ASCII denetimi çevirmeden sonra yapılsaydı ikisi
		// de sessizce geçer, fonksiyonun sözleşmesi bozulurdu.
		for _, input := range []string{
			"", "TR", "TRYX", "TR1", "T RY", "₺₺₺", "TR-", "123", "ıls", "ſek",
		} {
			_, err := NormalizeCurrencyCode(input)
			require.Error(t, err, "girdi: %q", input)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "girdi: %q", input)
			assert.Equal(t, CodeInvalidInput, errors.CodeOf(err), "girdi: %q", input)
		}
	})
}

// TestNormalizeCountryCode ISO 3166-1 alpha-2 kodunun kabul ve ret kurallarını
// kanıtlar.
func TestNormalizeCountryCode(t *testing.T) {
	t.Run("kabul edilenler", func(t *testing.T) {
		cases := []struct{ input, want string }{
			{input: "TR", want: "TR"},
			{input: "tr", want: "TR"},
			{input: " de ", want: "DE"},
			{input: "Us", want: "US"},
		}
		for _, tc := range cases {
			got, err := NormalizeCountryCode(tc.input)
			require.NoError(t, err, "girdi: %q", tc.input)
			assert.Equal(t, tc.want, got, "girdi: %q", tc.input)
		}
	})

	t.Run("reddedilenler", func(t *testing.T) {
		// "ÇĞ" büyük harfe çevrildikten sonra da ASCII değildir, dolayısıyla
		// denetimin sırasından bağımsız olarak reddedilir. "ıs" ve "ſe" ise
		// tam tersidir: basit büyük harf eşlemesi onları "IS" (İzlanda) ve
		// "SE" (İsveç) yapar, yani denetim çevirmeden sonra yapılsaydı geçerli
		// birer ISO koduna dönüşür ve tohumdaki gerçek ülkeleri çözerlerdi.
		for _, input := range []string{"", "T", "TUR", "T1", "1R", "ÇĞ", "T-", "ıs", "ſe"} {
			_, err := NormalizeCountryCode(input)
			require.Error(t, err, "girdi: %q", input)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "girdi: %q", input)
		}
	})
}

// TestNormalizeName bölge adının kırpıldığını, boş ve kontrol karakterli
// adların reddedildiğini kanıtlar.
func TestNormalizeName(t *testing.T) {
	got, err := normalizeName("  Avrupa Birliği  ")
	require.NoError(t, err)
	assert.Equal(t, "Avrupa Birliği", got)

	for _, input := range []string{"", "   ", "\t\n", "Ad\nSatır", "Ad\x00"} {
		_, err := normalizeName(input)
		require.Error(t, err, "girdi: %q", input)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "girdi: %q", input)
	}

	_, err = normalizeName(strings.Repeat("a", maxNameLen+1))
	require.Error(t, err, "aşırı uzun ad reddedilmeli")
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestValidateTaxRate vergi oranı sınırlarını kanıtlar.
func TestValidateTaxRate(t *testing.T) {
	for _, rate := range []int32{models.MinTaxRate, 1, 2000, models.MaxTaxRate} {
		require.NoError(t, validateTaxRate(rate), "oran: %d", rate)
	}
	for _, rate := range []int32{-1, models.MaxTaxRate + 1, 1 << 20} {
		err := validateTaxRate(rate)
		require.Error(t, err, "oran: %d", rate)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "oran: %d", rate)
	}
}

// TestRequireRegionID kimlik doğrulamasının önek, boşluk ve uzunluk
// kurallarını kanıtlar.
func TestRequireRegionID(t *testing.T) {
	require.NoError(t, requireRegionID(models.RegionIDPrefix+"01ABCDEF"))

	for _, id := range []string{
		"",
		" reg_1",
		"reg_1 ",
		"prod_1",
		"cust_1",
		"1",
		models.RegionIDPrefix + strings.Repeat("a", maxIDLen),
	} {
		err := requireRegionID(id)
		require.Error(t, err, "kimlik: %q", id)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err), "kimlik: %q", id)
	}
}

// TestClampToInt32 int32 aralığına sıkıştırmanın sarma üretmediğini kanıtlar.
//
// Query katmanının limit alanı int'tir; 64 bit bir platformda oradan gelen
// devasa bir değer doğrudan dönüştürülseydi negatif bir limite SARARDI.
func TestClampToInt32(t *testing.T) {
	assert.Equal(t, int32(5), clampToInt32(5))
	assert.Equal(t, int32(2147483647), clampToInt32(1<<40))
	assert.Equal(t, int32(-2147483648), clampToInt32(-(1 << 40)))
}

// TestNormalizePaging sayfalama sınırlarının uygulandığını kanıtlar.
func TestNormalizePaging(t *testing.T) {
	limit, offset, err := normalizePaging(0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit)
	assert.Zero(t, offset)

	limit, _, err = normalizePaging(-5, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit, "negatif limit varsayılana düşmeli")

	limit, _, err = normalizePaging(MaxLimit+1, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, limit)

	limit, offset, err = normalizePaging(10, 20)
	require.NoError(t, err)
	assert.Equal(t, int32(10), limit)
	assert.Equal(t, int32(20), offset)

	_, _, err = normalizePaging(10, -1)
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}
