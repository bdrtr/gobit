package service

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
)

// TestNormalizeCurrency para birimi doğrulamasının her dalını kanıtlar.
func TestNormalizeCurrency(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{"büyük harf korunur", "TRY", "TRY", true},
		{"küçük harf büyütülür", "try", "TRY", true},
		{"karışık harf büyütülür", "TrY", "TRY", true},
		{"baş/son boşluk kırpılır", "  eur  ", "EUR", true},
		{"boş reddedilir", "", "", false},
		{"iki harf reddedilir", "TR", "", false},
		{"dört harf reddedilir", "TRYX", "", false},
		{"rakam reddedilir", "TR1", "", false},
		{"simge reddedilir", "TR$", "", false},
		{"iç boşluk reddedilir", "T R", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeCurrency(tc.input)
			if !tc.ok {
				require.Error(t, err)
				assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestValidateAmount tutarın minor unit sınırlarını kanıtlar.
//
// Üst sınır bir tercihtir değil bir gerekliliktir: MaxAmount × MaxQuantity
// int64'e sığmalıdır. Test bu değişmezi de doğrular.
func TestValidateAmount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		amount int64
		ok     bool
	}{
		{"sıfır kabul edilir", 0, true},
		{"pozitif kabul edilir", 1999, true},
		{"üst sınırda kabul edilir", models.MaxAmount, true},
		{"negatif reddedilir", -1, false},
		{"çok negatif reddedilir", math.MinInt64, false},
		{"üst sınırın üstü reddedilir", models.MaxAmount + 1, false},
		{"çok büyük reddedilir", math.MaxInt64, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAmount(tc.amount)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	t.Run("sınırlar taşmayı imkânsız kılar", func(t *testing.T) {
		assert.Less(t, models.MaxAmount, math.MaxInt64/int64(models.MaxQuantity),
			"tutar × adet çarpımı int64'e sığmalı")
	})
}

// TestNormalizeQuantityRange adet aralığı doğrulamasının her dalını kanıtlar.
func TestNormalizeQuantityRange(t *testing.T) {
	t.Run("sıfır asgari adet bire çekilir", func(t *testing.T) {
		minQty, maxQty, err := normalizeQuantityRange(0, nil)
		require.NoError(t, err)
		assert.Equal(t, int32(1), minQty)
		assert.Nil(t, maxQty)
	})

	t.Run("üst sınır kopyalanır", func(t *testing.T) {
		original := int32(10)
		_, maxQty, err := normalizeQuantityRange(1, &original)
		require.NoError(t, err)
		require.NotNil(t, maxQty)

		original = 99
		assert.Equal(t, int32(10), *maxQty, "çağıranın işaretçisi paylaşılmamalı")
	})

	for _, tc := range []struct {
		name   string
		minQty int32
		maxQty *int32
	}{
		{"negatif asgari", -1, nil},
		{"asgari sınırın üstünde", models.MaxQuantity + 1, nil},
		{"azami sıfır", 1, ptr(int32(0))},
		{"azami negatif", 1, ptr(int32(-5))},
		{"azami sınırın üstünde", 1, ptr(models.MaxQuantity + 1)},
		{"azami asgariden küçük", 10, ptr(int32(5))},
	} {
		t.Run(tc.name+" reddedilir", func(t *testing.T) {
			_, _, err := normalizeQuantityRange(tc.minQty, tc.maxQty)
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	t.Run("azami asgariye eşit olabilir", func(t *testing.T) {
		_, _, err := normalizeQuantityRange(5, ptr(int32(5)))
		assert.NoError(t, err)
	})
}

// TestValidateRule kural doğrulamasının her dalını kanıtlar.
func TestValidateRule(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   RuleInput
		ok   bool
	}{
		{"eq tek değerle geçerli",
			RuleInput{Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"}}, true},
		{"in çok değerle geçerli",
			RuleInput{Attribute: "grp", Operator: models.OpIn, Values: []string{"a", "b"}}, true},
		{"gt sayısal değerle geçerli",
			RuleInput{Attribute: "yas", Operator: models.OpGt, Values: []string{"18"}}, true},
		{"alan adı boş reddedilir",
			RuleInput{Attribute: "  ", Operator: models.OpEq, Values: []string{"a"}}, false},
		{"işleç tanımsız reddedilir",
			RuleInput{Attribute: "k", Operator: models.RuleOperator("regex"), Values: []string{"a"}}, false},
		{"değer yok reddedilir",
			RuleInput{Attribute: "k", Operator: models.OpEq}, false},
		{"tek değerli işlece iki değer reddedilir",
			RuleInput{Attribute: "k", Operator: models.OpEq, Values: []string{"a", "b"}}, false},
		{"boş değer reddedilir",
			RuleInput{Attribute: "k", Operator: models.OpIn, Values: []string{"a", ""}}, false},
		{"sayısal işlece metin reddedilir",
			RuleInput{Attribute: "k", Operator: models.OpLte, Values: []string{"onsekiz"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRule(tc.in)
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}
}

// TestRequireID kimlik doğrulamasının her dalını kanıtlar.
func TestRequireID(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		ok   bool
	}{
		{"doğru önek geçerli", "pset_ABC", true},
		{"boş reddedilir", "", false},
		{"yanlış önek reddedilir", "variant_ABC", false},
		{"öneksiz reddedilir", "ABC", false},
		{"baş boşluk reddedilir", " pset_ABC", false},
		{"son boşluk reddedilir", "pset_ABC ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireID(tc.id, models.PriceSetIDPrefix, "price set kimliği")
			if tc.ok {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
		})
	}

	t.Run("aşırı uzun kimlik reddedilir", func(t *testing.T) {
		long := models.PriceSetIDPrefix
		for len(long) <= maxIDLen {
			long += "A"
		}
		err := requireID(long, models.PriceSetIDPrefix, "price set kimliği")
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestNormalizePaging sayfalama kırpmasının her dalını kanıtlar.
func TestNormalizePaging(t *testing.T) {
	t.Run("limit yoksa varsayılan uygulanır", func(t *testing.T) {
		limit, offset, err := normalizePaging(0, 0)
		require.NoError(t, err)
		assert.Equal(t, DefaultLimit, limit)
		assert.Equal(t, int32(0), offset)
	})

	t.Run("negatif limit varsayılana düşer", func(t *testing.T) {
		limit, _, err := normalizePaging(-5, 0)
		require.NoError(t, err)
		assert.Equal(t, DefaultLimit, limit)
	})

	t.Run("aşırı limit azamiye kırpılır", func(t *testing.T) {
		limit, _, err := normalizePaging(MaxLimit+1, 0)
		require.NoError(t, err)
		assert.Equal(t, MaxLimit, limit)
	})

	t.Run("geçerli limit korunur", func(t *testing.T) {
		limit, offset, err := normalizePaging(7, 21)
		require.NoError(t, err)
		assert.Equal(t, int32(7), limit)
		assert.Equal(t, int32(21), offset)
	})

	t.Run("negatif offset reddedilir", func(t *testing.T) {
		_, _, err := normalizePaging(10, -1)
		require.Error(t, err)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	})
}

// TestClampToInt32 int -> int32 dönüşümünün sarmadığını kanıtlar.
func TestClampToInt32(t *testing.T) {
	assert.Equal(t, int32(42), clampToInt32(42))
	assert.Equal(t, int32(math.MaxInt32), clampToInt32(math.MaxInt32))

	if math.MaxInt > math.MaxInt32 {
		assert.Equal(t, int32(math.MaxInt32), clampToInt32(math.MaxInt32+1))
		assert.Equal(t, int32(math.MinInt32), clampToInt32(math.MinInt32-1))
	}
}

// TestWithIndexAddsDetail toplu yazmada hangi girdinin reddedildiğinin hataya
// eklendiğini kanıtlar.
func TestWithIndexAddsDetail(t *testing.T) {
	err := withIndex(errors.Invalid("x", "bozuk"), detailIndex, 3)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 3, typed.Details["index"])
}

// TestWithIndexKeepsNestedLevels iç içe iki indeksin birbirini EZMEDİĞİNİ
// kanıtlar.
//
// Aynı anahtar iki kez kullanılsaydı errors.WithDetails ikincisiyle birincisini
// ezer ve dıştaki fiyat indeksi içteki kural indeksini yok ederdi.
func TestWithIndexKeepsNestedLevels(t *testing.T) {
	inner := withIndex(errors.Invalid("x", "bozuk"), detailRuleIndex, 3)
	err := withIndex(inner, detailIndex, 7)

	var typed *errors.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, 7, typed.Details[detailIndex])
	assert.Equal(t, 3, typed.Details[detailRuleIndex], "kural indeksi korunmalı")
}
