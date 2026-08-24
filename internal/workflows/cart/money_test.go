package cart

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestTaxOfYuvarlamaVeSinirlar baz puan aritmetiğinin sözleşmesini sabitler.
func TestTaxOfYuvarlamaVeSinirlar(t *testing.T) {
	tests := map[string]struct {
		base    int64
		rateBps int32
		want    int64
	}{
		"tam bölünen":            {base: 1000, rateBps: 2000, want: 200},
		"aşağı yuvarlar":         {base: 101, rateBps: 1850, want: 18},
		"bir eksiği sıfır kalır": {base: 5, rateBps: 1000, want: 0},
		"sıfır oran":             {base: 999_999, rateBps: 0, want: 0},
		"sıfır taban":            {base: 0, rateBps: 2000, want: 0},
		"tam oran":               {base: 12_345, rateBps: MaxTaxRateBps, want: 12_345},
		"en büyük taban":         {base: MaxTotal, rateBps: MaxTaxRateBps, want: MaxTotal},
		"en büyük taban kısmi":   {base: MaxTotal, rateBps: 1, want: MaxTotal / 10_000},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := taxOf(tc.base, tc.rateBps)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestTaxOfBuyukTabandaTasmaz bölmenin çarpmadan ÖNCE yapıldığını kanıtlar.
//
// Doğrudan base × rate hesaplansaydı 10^18 × 10^4 int64'ü taşar ve sonuç
// sessizce negatif çıkardı. Test tam da o değeri sorar.
func TestTaxOfBuyukTabandaTasmaz(t *testing.T) {
	got, err := taxOf(MaxTotal, 2000)
	require.NoError(t, err)

	assert.Positive(t, got, "taşan bir çarpım negatif sonuç üretirdi")
	assert.Equal(t, MaxTotal/10_000*2000, got)
}

// TestTaxOfSozlesmeDisiOranReddedilir aralık dışı oranın hesaplanmadığını
// doğrular.
func TestTaxOfSozlesmeDisiOranReddedilir(t *testing.T) {
	for _, rate := range []int32{-1, MaxTaxRateBps + 1} {
		_, err := taxOf(1000, rate)
		require.Error(t, err)
		assert.Equal(t, CodeTaxRateInvalid, errors.CodeOf(err))
	}
}

// TestTaxOfSinirDisiTabanReddedilir tavanı aşan ya da negatif tabanın
// reddedildiğini doğrular.
func TestTaxOfSinirDisiTabanReddedilir(t *testing.T) {
	_, err := taxOf(MaxTotal+1, 100)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = taxOf(-1, 100)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))
}

// TestAddAmountTasmayiYakalar toplamanın sınırı aşarken hata döndüğünü
// doğrular.
func TestAddAmountTasmayiYakalar(t *testing.T) {
	sum, err := addAmount(MaxTotal-1, 1)
	require.NoError(t, err)
	assert.Equal(t, MaxTotal, sum)

	_, err = addAmount(MaxTotal, 1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	_, err = addAmount(-1, 1)
	require.Error(t, err)
}

// TestMulAmountTasmayiYakalar çarpımın sınırı aşarken hata döndüğünü doğrular.
func TestMulAmountTasmayiYakalar(t *testing.T) {
	product, err := mulAmount(MaxAmount, MaxQuantity)
	require.NoError(t, err)
	assert.Equal(t, MaxTotal, product)

	_, err = mulAmount(MaxAmount, MaxQuantity+1)
	require.Error(t, err)
	assert.Equal(t, CodeAmountOverflow, errors.CodeOf(err))

	zero, err := mulAmount(0, MaxQuantity)
	require.NoError(t, err)
	assert.Zero(t, zero)
}

// TestQuantity32SinirlariUygular adet çevriminin sınırlar dışında hata
// döndüğünü doğrular.
func TestQuantity32SinirlariUygular(t *testing.T) {
	got, err := quantity32(MaxQuantity)
	require.NoError(t, err)
	assert.Equal(t, int32(MaxQuantity), got)

	for _, quantity := range []int64{0, -1, MaxQuantity + 1, 1 << 40} {
		_, err := quantity32(quantity)
		require.Error(t, err, "adet %d kabul edilmemeli", quantity)
		assert.True(t, errors.IsInvalid(err))
	}
}

// TestRequireIDBoslukluKimligiReddeder kimliğin kırpılmadan reddedildiğini
// doğrular.
func TestRequireIDBoslukluKimligiReddeder(t *testing.T) {
	require.NoError(t, requireID("cart_id", "cart_1"))

	for _, value := range []string{"", " cart_1", "cart_1 ", "cart_1\n"} {
		err := requireID("cart_id", value)
		require.Error(t, err, "%q kabul edilmemeli", value)
		assert.True(t, errors.IsInvalid(err))
	}
}
