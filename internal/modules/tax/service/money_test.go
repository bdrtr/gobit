package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
)

// TestTaxOfBazPuanAritmetigi baz puan aritmetiğini ve yuvarlama YÖNÜNÜ
// doğrular.
func TestTaxOfBazPuanAritmetigi(t *testing.T) {
	tests := []struct {
		name    string
		base    int64
		rateBps int32
		want    int64
	}{
		{"sıfır oran", 100_000, 0, 0},
		{"sıfır taban", 0, 2000, 0},
		{"tam bölünen", 10_000, 2000, 2000},
		{"yüzde yüz tabanı verir", 12_345, 10_000, 12_345},
		{"bir baz puan", 10_000, 1, 1},
		{"bir baz puanın altı sıfırlanır", 9_999, 1, 0},
		{"kesir aşağı iner", 1_999, 1800, 359},
		{"tam yarım aşağı iner", 5, 5000, 2},
		{"kalanlı büyük taban", 123_456_789, 1850, 22_839_505},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TaxOf(tt.base, tt.rateBps)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestTaxOfBolmeyiOneAlmakSonucuDegistirmez "önce böl" optimizasyonunun
// matematiksel olarak eşdeğer olduğunu, taşmayan tabanlar üzerinde doğrudan
// çarpımla KARŞILAŞTIRARAK kanıtlar.
//
// Doğrudan çarpım yalnızca burada güvenlidir: seçilen tabanlar küçüktür ve
// base × rate int64'e sığar. Gerçek uygulamada taban 10^18'e kadar
// çıkabildiği için o yol kullanılamaz — testin varlık sebebi tam olarak budur.
func TestTaxOfBolmeyiOneAlmakSonucuDegistirmez(t *testing.T) {
	bases := []int64{0, 1, 7, 99, 100, 4_999, 10_000, 10_001, 123_456, 999_999_999}
	rates := []int32{0, 1, 18, 100, 725, 1800, 2000, 9_999, 10_000}

	for _, base := range bases {
		for _, rate := range rates {
			got, err := TaxOf(base, rate)
			require.NoError(t, err)
			assert.Equal(t, base*int64(rate)/BpsScale, got,
				"taban=%d oran=%d", base, rate)
		}
	}
}

// TestTaxOfTavandakiTabanTasmaz en büyük tabanın en büyük oranla bile
// taşmadığını doğrular.
//
// Doğrudan base × rate hesabı burada 10^22 üretir ve int64'ü aşardı; testin
// yeşil kalması, bölmenin öne alınmış olmasının kanıtıdır.
func TestTaxOfTavandakiTabanTasmaz(t *testing.T) {
	got, err := TaxOf(MaxTaxableAmount, models.MaxRateBps)
	require.NoError(t, err)
	assert.Equal(t, MaxTaxableAmount, got)
	assert.Positive(t, got, "taşan bir çarpım negatif ya da sıfır üretirdi")
}

// TestTaxOfSozlesmeDisiGirdiReddedilir sınır dışı taban ve oranın
// sınıflandırılmasını doğrular.
func TestTaxOfSozlesmeDisiGirdiReddedilir(t *testing.T) {
	t.Run("negatif taban iç hatadır", func(t *testing.T) {
		_, err := TaxOf(-1, 2000)
		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindInternal),
			"negatif taban çağıranın değil kodun hatasıdır")
	})

	t.Run("tavanı aşan taban istemci hatasıdır", func(t *testing.T) {
		_, err := TaxOf(MaxTaxableAmount+1, 2000)
		require.Error(t, err)
		assert.True(t, errors.IsInvalid(err))
		assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
	})

	t.Run("aralık dışı oran", func(t *testing.T) {
		for _, rate := range []int32{-1, models.MaxRateBps + 1, math.MaxInt32} {
			_, err := TaxOf(1000, rate)
			require.Error(t, err, "oran=%d", rate)
			assert.Equal(t, CodeRateOutOfRange, errors.CodeOf(err))
		}
	})
}

// TestAddAmountTasmaReddedilir toplamanın sessizce sarmadığını doğrular.
func TestAddAmountTasmaReddedilir(t *testing.T) {
	sum, err := addAmount(MaxTaxableAmount-1, 1)
	require.NoError(t, err)
	assert.Equal(t, MaxTaxableAmount, sum)

	_, err = addAmount(MaxTaxableAmount, 1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = addAmount(math.MaxInt64, math.MaxInt64)
	require.Error(t, err, "int64 tavanında sarma değil hata beklenir")

	_, err = addAmount(-1, 1)
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindInternal))
}

// TestCheckTaxableAmountSinirlari taban denetimini doğrular.
func TestCheckTaxableAmountSinirlari(t *testing.T) {
	require.NoError(t, checkTaxableAmount("taban", 0))
	require.NoError(t, checkTaxableAmount("taban", MaxTaxableAmount))

	err := checkTaxableAmount("taban", -1)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "negatif taban sessizce sıfıra çekilmemeli")

	err = checkTaxableAmount("taban", MaxTaxableAmount+1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
}
