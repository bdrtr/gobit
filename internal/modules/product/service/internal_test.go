package service

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya paketin İÇİNDEDİR: doğrulanan şeyler (slug üretimi, kimlik gövdesi,
// sayfalama kırpması) dışarıya açılmayan yardımcılardır ve yalnızca buradan
// görülebilir. Dışa açık davranış service_test paketinde sınanır.

// TestSlugify serbest metinden handle üretimini doğrular.
func TestSlugify(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Tişört":              "tisort",
		"Şık Tişört  Mavi":    "sik-tisort-mavi",
		"İstanbul Ceketi":     "istanbul-ceketi",
		"  boşluklu  ":        "bosluklu",
		"ÇOK-ÜRÜN":            "cok-urun",
		"a__b..c//d":          "a-b-c-d",
		"---kenar---":         "kenar",
		"Ürün #1 (yeni)":      "urun-1-yeni",
		"ğüşiöç":              "gusioc",
		"":                    "",
		"%%%":                 "",
		"Yaz 2026 Koleksiyon": "yaz-2026-koleksiyon",
	}

	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, want, slugify(input))
		})
	}
}

// TestSlugifyIsIdempotent üretilen slug'ın yeniden slug'landığında değişmediğini
// doğrular.
//
// Bu özellik validateHandle'ın temelidir: handle doğrulaması "slugify(h) == h"
// karşılaştırmasıyla yapılır, dolayısıyla üretilen her handle doğrulamadan
// geçmek zorundadır.
func TestSlugifyIsIdempotent(t *testing.T) {
	t.Parallel()

	inputs := []string{"Tişört", "İstanbul Ceketi", "Ürün #1 (yeni)", "a__b..c//d", "ÇOK-ÜRÜN"}
	for _, input := range inputs {
		once := slugify(input)
		assert.Equal(t, once, slugify(once), "%q için slug kararlı olmalı", input)
		if once != "" {
			_, err := validateHandle(once)
			assert.NoError(t, err, "üretilen handle doğrulamadan geçmeli: %q", once)
		}
	}
}

// TestValidateHandleRejectsBadShapes handle biçiminin zorlandığını doğrular.
func TestValidateHandleRejectsBadShapes(t *testing.T) {
	t.Parallel()

	bad := []string{"Büyük", "boş luk", "-bas", "son-", "iki--tire", "tişört", "UPPER"}
	for _, handle := range bad {
		_, err := validateHandle(handle)
		assert.Error(t, err, "%q reddedilmeliydi", handle)
		assert.True(t, errors.IsInvalid(err), "%q için doğrulama hatası bekleniyordu", handle)
	}

	good := []string{"tisort", "tisort-mavi", "urun-1", "a"}
	for _, handle := range good {
		got, err := validateHandle(handle)
		require.NoError(t, err, "%q kabul edilmeliydi", handle)
		assert.Equal(t, handle, got)
	}
}

// TestValidateHandleLengthLimit çok uzun handle'ın reddedildiğini doğrular.
func TestValidateHandleLengthLimit(t *testing.T) {
	t.Parallel()

	_, err := validateHandle(strings.Repeat("a", maxHandleLen+1))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err))
}

// TestRequireIDRejectsWhitespace kimliklerin KIRPILMADIĞINI, boşluk taşıyan
// kimliğin reddedildiğini doğrular.
//
// Sessiz kırpma, gönderilen kimlikle saklanan kimliği ayırır; fark ancak veri
// bozulduktan sonra görünür (bkz. core/link kimlik sözleşmesi).
func TestRequireIDRejectsWhitespace(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", " ", "prod_1\n", " prod_1", "prod_1 "} {
		_, err := requireID("id", id)
		assert.Error(t, err, "%q reddedilmeliydi", id)
	}

	got, err := requireID("id", "prod_1")
	require.NoError(t, err)
	assert.Equal(t, "prod_1", got)
}

// TestNormalizePaging sayfalama kurallarını doğrular.
func TestNormalizePaging(t *testing.T) {
	t.Parallel()

	limit, offset, err := normalizePaging(0, 0)
	require.NoError(t, err)
	assert.Equal(t, DefaultLimit, limit)
	assert.Zero(t, offset)

	limit, _, err = normalizePaging(MaxLimit+1, 0)
	require.NoError(t, err)
	assert.Equal(t, MaxLimit, limit, "sınırı aşan limit kırpılmalı")

	limit, offset, err = normalizePaging(5, 10)
	require.NoError(t, err)
	assert.Equal(t, 5, limit)
	assert.Equal(t, 10, offset)

	_, _, err = normalizePaging(-1, 0)
	assert.Error(t, err)
	_, _, err = normalizePaging(0, -1)
	assert.Error(t, err)
}

// TestNewIDShape üretilen kimliğin önek + 26 karakter gövde biçimini
// taşıdığını doğrular.
func TestNewIDShape(t *testing.T) {
	t.Parallel()

	id := newID(prefixProduct)
	assert.True(t, strings.HasPrefix(id, prefixProduct), "önek taşımalı: %s", id)

	body := strings.TrimPrefix(id, prefixProduct)
	assert.Len(t, body, 26, "gövde 16 baytın Crockford Base32 karşılığı olmalı: %s", body)
	assert.Equal(t, strings.ToUpper(body), body, "alfabe büyük harf ve rakamdan oluşur: %s", body)
	for _, r := range body {
		assert.NotContains(t, "ILOU", string(r),
			"Crockford alfabesi karıştırılabilir harfleri (I, L, O, U) içermez: %s", body)
	}
}

// TestNewIDIsUnique aynı milisaniyede üretilen kimliklerin çakışmadığını
// doğrular.
func TestNewIDIsUnique(t *testing.T) {
	t.Parallel()

	const count = 2000
	seen := make(map[string]struct{}, count)
	for range count {
		id := newID(prefixVariant)
		_, dup := seen[id]
		require.False(t, dup, "the id repeated: %s", id)
		seen[id] = struct{}{}
	}
}

// TestIDBytesAreTimeOrdered kimliğin zaman sıralı olduğunu doğrular.
//
// Sıralanabilirlik plan Bölüm 8'in şartıdır: kimlik kabaca oluşturma sırasını
// taşır, böylece kayıtlar birincil anahtar indeksinde de doğal sırada durur.
func TestIDBytesAreTimeOrdered(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	earlier := idEncoding.EncodeToString(idBytes(base))
	later := idEncoding.EncodeToString(idBytes(base.Add(time.Millisecond)))
	muchLater := idEncoding.EncodeToString(idBytes(base.Add(72 * time.Hour)))

	assert.Less(t, earlier, later, "1 ms sonraki kimlik sözlüksel olarak sonra gelmeli")
	assert.Less(t, later, muchLater, "3 gün sonraki kimlik sözlüksel olarak sonra gelmeli")
}

// TestIDBytesClampsPreEpoch 1970 öncesi zamanın sıralamayı bozmadığını
// doğrular.
func TestIDBytesClampsPreEpoch(t *testing.T) {
	t.Parallel()

	old := idEncoding.EncodeToString(idBytes(time.Date(1969, 1, 1, 0, 0, 0, 0, time.UTC)))
	now := idEncoding.EncodeToString(idBytes(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	assert.Less(t, old, now, "tabana çekilen zaman damgası sıralamayı bozmamalı")
}

// TestInt32From daraltmanın işaret değiştirmediğini doğrular.
func TestInt32From(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(0), int32From(-5), "negatif değer sıfıra çekilmeli")
	assert.Equal(t, int32(7), int32From(7))
	assert.Equal(t, int32(2147483647), int32From(1<<40), "sınırı aşan değer üst sınıra çekilmeli")
}

// TestTrimOptionalEmptyBecomesNil boşalan isteğe bağlı alanın nil olduğunu
// doğrular.
func TestTrimOptionalEmptyBecomesNil(t *testing.T) {
	t.Parallel()

	value := "   "
	got, err := trimOptional(&value, "subtitle", 10)
	require.NoError(t, err)
	assert.Nil(t, got)

	value = "  alt başlık "
	got, err = trimOptional(&value, "subtitle", 100)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "alt başlık", *got)

	long := strings.Repeat("a", 11)
	_, err = trimOptional(&long, "subtitle", 10)
	assert.Error(t, err)
}

// TestUniqueIDsPreservesOrder tekilleştirmenin sırayı koruduğunu doğrular.
func TestUniqueIDsPreservesOrder(t *testing.T) {
	t.Parallel()

	got, err := uniqueIDs("tag_ids", []string{"ptag_2", "ptag_1", "ptag_2"})
	require.NoError(t, err)
	assert.Equal(t, []string{"ptag_2", "ptag_1"}, got)
}
