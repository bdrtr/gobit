package arch_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// semaCoz "$ref" atıflarını belgedeki bileşene çözer.
func semaCoz(t *testing.T, doc *openapi.Doc, sema map[string]any) map[string]any {
	t.Helper()

	ref, refli := sema["$ref"].(string)
	if !refli {
		return sema
	}

	hedef, var_ := doc.Schemas()[strings.TrimPrefix(ref, "#/components/schemas/")]
	require.True(t, var_, "%q bileşeni kayıtlı olmalı", ref)

	m, ok := hedef.(map[string]any)
	require.True(t, ok, "%q bileşeni nesne olmalı", ref)

	return m
}

// semaOzellikleri şemanın "properties" haritasını döner.
func semaOzellikleri(t *testing.T, doc *openapi.Doc, sema map[string]any) map[string]any {
	t.Helper()

	ozellikler, ok := semaCoz(t, doc, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	return ozellikler
}

// anahtarlar bir haritanın anahtarlarını döner.
func anahtarlar[T any](m map[string]T) []string {
	adlar := make([]string, 0, len(m))
	for ad := range m {
		adlar = append(adlar, ad)
	}

	return adlar
}

// jsonAnahtarKumesi değeri encoding/json ile kodlayıp anahtarlarını döner.
func jsonAnahtarKumesi(t *testing.T, v any) []string {
	t.Helper()

	ham, err := json.Marshal(v)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	return anahtarlar(cozulmus)
}

// TestVitrinUrunSemasiGercekTipiAnlatir yansıma katmanının GERÇEK bir
// modül tipi üzerinde encoding/json ile aynı sonucu verdiğini doğrular.
//
// Çekirdeğin kendi testleri modülleri import EDEMEZ (Prensip 2.4) ve şeklin
// kopyasıyla çalışır. Kopya, kopyalandığı gün doğrudur; gerçek tip
// değiştiğinde ise sessizce eskir. Bu test o boşluğu kapatır ve arch
// paketinde yaşar çünkü test-only olan tek yer burasıdır: hem çekirdeği hem
// modülleri import edebilir.
//
// [productsvc.StoreProduct] bilinçli seçildi: gömülü models.Product'ı taşır
// ve onun Variants alanını GÖLGELER. encoding/json gölgelenen alanı yazmaz;
// şema da yazmamalıdır. Yazsaydı istemci üreteci varyantları fiyat/stok
// bilgisi OLMAYAN tiple üretir ve vitrin istemcisi fiyatı hiç göremezdi.
func TestVitrinUrunSemasiGercekTipiAnlatir(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	sema := doc.SchemaOf(productsvc.StoreProduct{})
	ozellikler := semaOzellikleri(t, doc, sema)

	// Sıfır değerde omitempty alanları JSON'a hiç yazılmaz; geriye kalan
	// anahtarlar tam olarak "her zaman yazılanlar"dır, yani "required".
	zorunlu, ok := semaCoz(t, doc, sema)["required"].([]string)
	require.True(t, ok, "şemada required olmalı")

	// Boş bir anahtar kümesine karşı AYRICA bir koruma yazılmadı ve bu bilinçli:
	// tip alanlarını kaybetse bile yukarıdaki "şemada required olmalı" iddiası
	// düşer (ölçüldü — boş bir yapının şemasında required anahtarı hiç
	// üretilmiyor), yani iki boş kümeyi sessizce eşleştiren bir yol yok. İkinci
	// bir kapı, kapalı bir kapının önüne konmuş olurdu.
	yazilanlar := jsonAnahtarKumesi(t, productsvc.StoreProduct{})
	assert.ElementsMatch(t, yazilanlar, zorunlu,
		"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")

	for _, ad := range yazilanlar {
		assert.Contains(t, ozellikler, ad, "%q alanı şemada olmalı", ad)
	}
}

// TestVitrinUrunVaryantlariGolgeleyenTipiTasir gölgelenmenin TİP düzeyinde de
// doğru olduğunu doğrular.
//
// Anahtar kümesi karşılaştırması burada yetmez: gölgelenen models.Product
// alanı da gölgeleyen StoreProduct alanı da "variants" adını taşır, yani
// yanlış olanı seçmek anahtar kümesini bozmaz. Ayıran tek şey öğe tipidir:
// yalnızca zenginleştirilmiş varyant fiyat ve stok taşır.
func TestVitrinUrunVaryantlariGolgeleyenTipiTasir(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	ozellikler := semaOzellikleri(t, doc, doc.SchemaOf(productsvc.StoreProduct{}))

	varyantlar, ok := ozellikler["variants"].(map[string]any)
	require.True(t, ok, "vitrin ürünü varyant taşımalı")
	assert.Equal(t, "array", varyantlar["type"])

	oge, ok := varyantlar["items"].(map[string]any)
	require.True(t, ok)

	ogeAlanlari := semaOzellikleri(t, doc, oge)
	assert.Contains(t, ogeAlanlari, "price_set",
		"varyant şeması zenginleştirilmiş tipten gelmeli; gölgelenen models.Variant fiyat taşımaz")
	assert.Contains(t, ogeAlanlari, "inventory_item")

	// Gömülü models.Variant'ın alanları da DÜZLEŞTİRİLMİŞ olmalı; yalnızca
	// eklerin görünmesi, temel varyant bilgisinin kaybolduğu anlamına gelirdi.
	assert.Contains(t, ogeAlanlari, "sku")
}
