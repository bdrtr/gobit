package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestNormalizeCountryCode ülke kodu normalleştirmesini doğrular.
func TestNormalizeCountryCode(t *testing.T) {
	t.Run("kabul edilenler", func(t *testing.T) {
		// Dilim kullanılır, harita değil: girdilerin bir kısmı BİLEREK boşluk
		// taşır (kırpma sınanır) ve boşluklu harita anahtarları okurken
		// yazım hatası gibi görünürdü.
		tests := []struct{ in, want string }{
			{"TR", "TR"},
			{"tr", "TR"},
			{" de ", "DE"},
			{"\tus\n", "US"},
		}
		for _, tt := range tests {
			got, err := NormalizeCountryCode(tt.in)
			require.NoError(t, err, "girdi: %q", tt.in)
			assert.Equal(t, tt.want, got)
		}
	})

	t.Run("reddedilenler", func(t *testing.T) {
		for _, in := range []string{"", "T", "TUR", "T1", "T-", "T R", "TÜ"} {
			_, err := NormalizeCountryCode(in)
			require.Error(t, err, "girdi: %q kabul edilmemeli", in)
			assert.True(t, errors.IsInvalid(err))
		}
	})

	// Bu, gerçek bir tuzaktır: Unicode'un basit büyük harf eşlemesi noktasız
	// "ı"yı ASCII "I"ya taşır. Denetim çevrimden SONRA yapılsaydı "ıs"
	// sessizce "IS" (İzlanda) olurdu.
	t.Run("ASCII dışı harf büyük harfe çevrilerek kaçamaz", func(t *testing.T) {
		for _, in := range []string{"ıs", "ſe", "İs"} {
			_, err := NormalizeCountryCode(in)
			require.Error(t, err, "girdi: %q", in)
		}
	})
}

// TestNormalizeProvinceCode eyalet kodu normalleştirmesini doğrular.
func TestNormalizeProvinceCode(t *testing.T) {
	t.Run("kabul edilenler", func(t *testing.T) {
		tests := []struct{ in, want string }{
			{"", ""},
			{"  ", ""},
			{"ca", "CA"},
			{"34", "34"},
			{"BC-1", "BC-1"},
			{"NL2A", "NL2A"},
			{"ABCDEFGHIJ", "ABCDEFGHIJ"},
		}
		for _, tt := range tests {
			got, err := NormalizeProvinceCode(tt.in)
			require.NoError(t, err, "girdi: %q", tt.in)
			assert.Equal(t, tt.want, got)
		}
	})

	t.Run("reddedilenler", func(t *testing.T) {
		for _, in := range []string{"-CA", "C A", "CA!", "ABCDEFGHIJK", "Kİ"} {
			_, err := NormalizeProvinceCode(in)
			require.Error(t, err, "girdi: %q kabul edilmemeli", in)
			assert.True(t, errors.IsInvalid(err))
		}
	})

	t.Run("veritabanı kısıtıyla uyumlu", func(t *testing.T) {
		assert.Equal(t, 10, models.MaxProvinceCodeLength,
			"sınır migration'daki CHECK ile aynı olmalı; ayrışırsa servis kabul ettiğini yazamaz")
	})
}

// TestRequireIDOnekDenetimi yanlış türde kimliğin doğrulama hatası (404
// değil) ürettiğini doğrular.
func TestRequireIDOnekDenetimi(t *testing.T) {
	require.NoError(t, requireID(trRegionID, models.TaxRegionIDPrefix, "bölge"))

	for _, id := range []string{
		"",
		" " + trRegionID,
		trRegionID + " ",
		rateA,
		"reg_0000",
	} {
		err := requireID(id, models.TaxRegionIDPrefix, "bölge")
		require.Error(t, err, "kimlik: %q", id)
		assert.True(t, errors.IsInvalid(err))
	}

	uzun := models.TaxRegionIDPrefix
	for range maxIDLen {
		uzun += "X"
	}
	require.Error(t, requireID(uzun, models.TaxRegionIDPrefix, "bölge"))
}

// TestRequireReferenceIDOnekDenetlemez yabancı kimliklerin önek şartına tabi
// OLMADIĞINI doğrular.
//
// Kural bilinçlidir: kimlik başka bir modüle aittir ve o modülün önek
// sözleşmesini burada tekrarlamak, önek değiştiğinde tax'ın sessizce kural
// kabul etmemesi demek olurdu.
func TestRequireReferenceIDOnekDenetlemez(t *testing.T) {
	require.NoError(t, requireReferenceID("prod_1"))
	require.NoError(t, requireReferenceID("herhangi-bir-kimlik"))

	require.Error(t, requireReferenceID(""))
	require.Error(t, requireReferenceID(" prod_1"))
}

// TestNormalizeCode mutabakat kodu doğrulamasını doğrular.
func TestNormalizeCode(t *testing.T) {
	got, err := normalizeCode("  KDV20  ")
	require.NoError(t, err)
	assert.Equal(t, "KDV20", got)

	got, err = normalizeCode("   ")
	require.NoError(t, err)
	assert.Empty(t, got, "boşluktan ibaret kod, kodun YOKLUĞU demektir")

	for _, in := range []string{"KDV 20", "KDV\t20", "KDV\n20"} {
		_, err := normalizeCode(in)
		require.Error(t, err, "girdi: %q", in)
	}
}

// TestNormalizePagingSinirlari sayfalama kırpmasını doğrular.
func TestNormalizePagingSinirlari(t *testing.T) {
	limit, offset, err := normalizePaging(0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit)
	assert.Equal(t, int32(0), offset)

	limit, _, err = normalizePaging(MaxLimit+1, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, limit)

	_, _, err = normalizePaging(10, -1)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestClampToInt32 int32 sınırına sıkıştırmayı doğrular.
func TestClampToInt32(t *testing.T) {
	assert.Equal(t, int32(5), clampToInt32(5))
	assert.Equal(t, int32(math.MaxInt32), clampToInt32(math.MaxInt64))
	assert.Equal(t, int32(math.MinInt32), clampToInt32(math.MinInt64))
}
