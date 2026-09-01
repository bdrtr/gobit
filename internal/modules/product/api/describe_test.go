package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// vitrinBelgesi Describe'ın çıktısını GERÇEK route ağacına karşı üretip
// JSON'dan geri okunmuş hâlini döner.
//
// Doğrudan [openapi.Doc.Build] çıktısına bakmak yetmezdi: işlemler orada Go
// struct'ıdır ve incelenen davranış tam olarak alanların JSON'a yazılıp
// yazılmadığıdır. Router da gerçek olmalıdır — açıklamanın yolu route'unkinden
// ayrıştığı an test düşsün, üretimde /openapi.json'a bakan biri değil.
func vitrinBelgesi(t *testing.T) (yollar, bilesenler map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	api.Describe(doc)

	r := chi.NewRouter()
	api.New(nil, graph.Options{}).Routes(r)

	ham, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"anlatılan her uç bir route ile eşleşmeli; eşleşmeyen kayıt belgeye hiç girmez")

	kodlanmis, err := json.Marshal(ham)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(kodlanmis, &cozulmus))

	var ok bool

	bilesenler, ok = cozulmus["components"].(map[string]any)["schemas"].(map[string]any)
	require.True(t, ok)

	yollar, ok = cozulmus["paths"].(map[string]any)
	require.True(t, ok)

	return yollar, bilesenler
}

// vitrinIslemi belgeden tek bir yol+metod işlemini döner.
func vitrinIslemi(t *testing.T, yollar map[string]any, metod, yol string) map[string]any {
	t.Helper()

	yolIslemleri, ok := yollar[yol].(map[string]any)
	require.True(t, ok, "%s belgede olmalı", yol)

	op, ok := yolIslemleri[strings.ToLower(metod)].(map[string]any)
	require.True(t, ok, "%s %s belgede olmalı", metod, yol)

	return op
}

// semaCoz "$ref" atıflarını belgedeki bileşene çözer.
func semaCoz(t *testing.T, bilesenler, sema map[string]any) map[string]any {
	t.Helper()

	ref, refli := sema["$ref"].(string)
	if !refli {
		return sema
	}

	hedef, ok := bilesenler[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	require.True(t, ok, "%q bileşeni kayıtlı olmalı", ref)

	return hedef
}

// yanitSemasi bir yanıt tanımından JSON şemasını çıkarır.
func yanitSemasi(t *testing.T, tanim map[string]any) map[string]any {
	t.Helper()

	icerik, ok := tanim["content"].(map[string]any)
	require.True(t, ok, "yanıt tanımında content olmalı: %#v", tanim)

	json_, ok := icerik["application/json"].(map[string]any)
	require.True(t, ok, "yanıt application/json olmalı")

	sema, ok := json_["schema"].(map[string]any)
	require.True(t, ok, "yanıtın şeması olmalı")

	return sema
}

// ozellik şemanın tek bir alanının şemasını döner.
func ozellik(t *testing.T, bilesenler, sema map[string]any, ad string) map[string]any {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	alan, ok := ozellikler[ad].(map[string]any)
	require.True(t, ok, "%q alanı şemada olmalı", ad)

	return alan
}

// vitrinAlanlari şemanın "properties" anahtarlarını döner.
func vitrinAlanlari(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	return vitrinAnahtarlari(ozellikler)
}

// vitrinZorunlulari şemanın "required" listesini döner.
func vitrinZorunlulari(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	ham, _ := semaCoz(t, bilesenler, sema)["required"].([]any)

	adlar := make([]string, 0, len(ham))
	for _, ad := range ham {
		metin, ok := ad.(string)
		require.True(t, ok)

		adlar = append(adlar, metin)
	}

	return adlar
}

// vitrinAnahtarlari bir haritanın anahtarlarını döner.
func vitrinAnahtarlari[T any](m map[string]T) []string {
	adlar := make([]string, 0, len(m))
	for ad := range m {
		adlar = append(adlar, ad)
	}

	return adlar
}

// vitrinJSONAnahtarlari değeri encoding/json ile kodlayıp anahtarlarını döner.
//
// Karşılaştırmanın diğer ucu budur: şema, tel üzerinde GERÇEKTEN ne olduğunu
// anlatmalıdır ve bunu bilen tek şey encoding/json'un kendisidir.
func vitrinJSONAnahtarlari(t *testing.T, v any) []string {
	t.Helper()

	ham, err := json.Marshal(v)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	return vitrinAnahtarlari(cozulmus)
}

// doluUrun omitempty alanları da yazılan bir vitrin ürünü üretir.
//
// Gömülü models.Product'ın Variants alanı BİLİNÇLİ olarak boş bırakılır:
// gölgelenen alandır ve encoding/json onu hiç yazmaz. Doldurulsaydı test
// gölgelenmenin bozulduğunu göremezdi.
func doluUrun() service.StoreProduct {
	metin := "x"
	sayi := int32(1)
	an := time.Now().UTC()

	return service.StoreProduct{
		Product: models.Product{
			Subtitle:      &metin,
			Description:   &metin,
			Thumbnail:     &metin,
			Weight:        &sayi,
			Length:        &sayi,
			Height:        &sayi,
			Width:         &sayi,
			Material:      &metin,
			OriginCountry: &metin,
			CollectionID:  &metin,
			Metadata:      map[string]any{"k": "v"},
			DeletedAt:     &an,
			Options:       []models.Option{{}},
			Images:        []models.Image{{}},
			Tags:          []models.Tag{{}},
			Categories:    []models.Category{{}},
		},
		Variants: []service.StoreVariant{doluVaryant()},
	}
}

// doluVaryant omitempty alanları da yazılan bir vitrin varyantı üretir.
func doluVaryant() service.StoreVariant {
	metin := "x"
	sayi := int32(1)
	an := time.Now().UTC()

	return service.StoreVariant{
		Variant: models.Variant{
			SKU:          &metin,
			Barcode:      &metin,
			EAN:          &metin,
			UPC:          &metin,
			Weight:       &sayi,
			Metadata:     map[string]any{"k": "v"},
			DeletedAt:    &an,
			OptionValues: []models.OptionValue{{}},
		},
		PriceSet:      query.Record{"id": "pset_1"},
		InventoryItem: query.Record{"id": "iitem_1"},
	}
}

// TestVitrinListesiGovdesiniAnlatir liste ucunun ne döndüğünü söylediğini
// doğrular.
//
// Alan kümesi DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, tipe alan eklendiği gün eksik kalır
// ve test bunu görmezdi.
func TestVitrinListesiGovdesiniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := vitrinBelgesi(t)
	op := vitrinIslemi(t, yollar, http.MethodGet, "/store/v1/products")

	assert.NotEmpty(t, op["summary"])
	assert.NotContains(t, op, "requestBody", "GET ucu gövde almaz")

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	// Liste 200 döner (bkz. writeList); 201 yazmak istemci üretecinde yanlış
	// dallanma üretirdi.
	tanim, ok := yanitlar["200"].(map[string]any)
	require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli")

	zarf := yanitSemasi(t, tanim)
	assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
		vitrinAlanlari(t, bilesenler, zarf), "liste zarfı plan Bölüm 8'deki biçimdir")

	oge, ok := ozellik(t, bilesenler, zarf, "data")["items"].(map[string]any)
	require.True(t, ok, "liste zarfının öğe şeması olmalı")

	urunSemasiniDogrula(t, bilesenler, oge)
}

// TestVitrinTekilUcuGovdesiniAnlatir tekil ucun ne döndüğünü söylediğini
// doğrular.
func TestVitrinTekilUcuGovdesiniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := vitrinBelgesi(t)
	op := vitrinIslemi(t, yollar, http.MethodGet, "/store/v1/products/{id}")

	assert.NotEmpty(t, op["summary"])
	assert.NotContains(t, op, "requestBody", "GET ucu gövde almaz")

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar["200"].(map[string]any)
	require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli")

	zarf := yanitSemasi(t, tanim)
	assert.ElementsMatch(t, []string{"data"}, vitrinAlanlari(t, bilesenler, zarf),
		"tekil yanıtlar {\"data\": …} zarfıyla döner")

	urunSemasiniDogrula(t, bilesenler, ozellik(t, bilesenler, zarf, "data"))
}

// urunSemasiniDogrula ürün şemasının vitrin tipiyle örtüştüğünü doğrular.
func urunSemasiniDogrula(t *testing.T, bilesenler, sema map[string]any) {
	t.Helper()

	assert.ElementsMatch(t, vitrinJSONAnahtarlari(t, doluUrun()),
		vitrinAlanlari(t, bilesenler, sema), "ürün alanları vitrin tipiyle örtüşmeli")

	// Sıfır değerde yazılan anahtarlar tam olarak "her zaman yazılanlar"dır.
	assert.ElementsMatch(t, vitrinJSONAnahtarlari(t, service.StoreProduct{}),
		vitrinZorunlulari(t, bilesenler, sema),
		"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
}

// TestVitrinVaryantlariZenginlestirilmisTipiAnlatir gölgelenen alanın şemada
// DOĞRU tiple göründüğünü doğrular.
//
// service.StoreProduct gömülü models.Product'ın Variants alanını GÖLGELER ve
// encoding/json yalnızca gölgeleyeni yazar. Şema gölgelenen models.Variant'ı
// anlatsaydı istemci üreteci varyantları fiyat ve stok bilgisi OLMAYAN tiple
// üretir, yani vitrin istemcisi fiyatı hiç göremezdi — üstelik anahtar kümesi
// bozulmadığı için ("variants" iki tipte de var) bu sessizce olurdu. Ayıran
// tek şey ÖĞE TİPİDİR ve test tam olarak onu karşılaştırır.
func TestVitrinVaryantlariZenginlestirilmisTipiAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := vitrinBelgesi(t)
	op := vitrinIslemi(t, yollar, http.MethodGet, "/store/v1/products/{id}")

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar["200"].(map[string]any)
	require.True(t, ok)

	urun := ozellik(t, bilesenler, yanitSemasi(t, tanim), "data")

	varyantlar := ozellik(t, bilesenler, urun, "variants")
	assert.Equal(t, "array", varyantlar["type"])

	oge, ok := varyantlar["items"].(map[string]any)
	require.True(t, ok)

	alanlar := vitrinAlanlari(t, bilesenler, oge)
	assert.ElementsMatch(t, vitrinJSONAnahtarlari(t, doluVaryant()), alanlar,
		"varyant şeması zenginleştirilmiş tipten gelmeli")
	assert.Contains(t, alanlar, "price_set", "gölgelenen models.Variant fiyat taşımaz")
	assert.Contains(t, alanlar, "inventory_item")
	// Gömülü models.Variant'ın alanları da DÜZLEŞTİRİLMİŞ olmalı; yalnızca
	// eklerin görünmesi, temel varyant bilgisinin kaybolduğu anlamına gelirdi.
	assert.Contains(t, alanlar, "sku")
}

// TestVitrinListesiYalnizcaOkunanParametreleriAnlatir sorgu parametrelerinin
// handler'ın GERÇEKTEN okuduklarıyla aynı olduğunu doğrular.
//
// Okunmayan bir parametreyi şemaya koymak, istemciye ÇALIŞMAYAN bir özellik
// vaat etmektir: üreteç metoda argüman koyar, çağıran doldurur, sunucu sessizce
// yok sayar.
//
// "sales_channel_id"nin yokluğu ayrıca bir GÜVENLİK ifadesidir: kanal isteğin
// publishable anahtarından gelir, sorgu dizesinden değil. Şemaya yazmak,
// elindeki herhangi bir anahtarla gelen bir istemciye başka bir kanalın
// katalogunu isteyebileceğini ima ederdi.
func TestVitrinListesiYalnizcaOkunanParametreleriAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := vitrinBelgesi(t)
	op := vitrinIslemi(t, yollar, http.MethodGet, "/store/v1/products")

	adlar := parametreAdlari(t, op, "query")
	assert.ElementsMatch(t, []string{"collection_id", "q", "limit", "offset"}, adlar,
		"parametreler storeListProducts'ın okuduklarıyla aynı olmalı")
	assert.NotContains(t, adlar, "sales_channel_id",
		"kanal kimlikten gelir; sorgu parametresi olarak duyurulmamalı")
}

// TestVitrinTekilUcuYolParametresiniAnlatir yol parametresinin handle'ı da
// kabul ettiğinin yazılı olduğunu doğrular.
//
// Türetici desene bakıp bunu söyleyemez: "{id}" adı yalnızca kimliği ima eder,
// oysa vitrin adresleri handle taşır.
func TestVitrinTekilUcuYolParametresiniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := vitrinBelgesi(t)
	op := vitrinIslemi(t, yollar, http.MethodGet, "/store/v1/products/{id}")

	assert.Empty(t, parametreAdlari(t, op, "query"), "tekil uç sorgu dizesini okumaz")
	assert.Equal(t, []string{"id"}, parametreAdlari(t, op, "path"))

	params, ok := op["parameters"].([]any)
	require.True(t, ok)
	require.Len(t, params, 1)

	p, ok := params[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, p["description"], "handle")
}

// parametreAdlari işlemin verilen yerdeki parametre adlarını döner.
func parametreAdlari(t *testing.T, op map[string]any, yer string) []string {
	t.Helper()

	params, _ := op["parameters"].([]any)

	adlar := make([]string, 0, len(params))

	for _, ham := range params {
		p, ok := ham.(map[string]any)
		require.True(t, ok)

		if p["in"] != yer {
			continue
		}

		ad, ok := p["name"].(string)
		require.True(t, ok)

		adlar = append(adlar, ad)
	}

	return adlar
}

// TestVitrinUclarininTumuAnlatildi anlatılmamış bir vitrin ucu kalmadığını
// doğrular.
//
// Yeni bir uç eklenip anlatılmadığında bu test düşer. Arıza aksi hâlde SESSİZ
// olurdu: uç belgede yolu ve güvenliğiyle görünür, yalnızca ne döndüğü
// bilinmez.
func TestVitrinUclarininTumuAnlatildi(t *testing.T) {
	t.Parallel()

	yollar, _ := vitrinBelgesi(t)

	var bulunan []string

	for yol, islemler := range yollar {
		if !strings.HasPrefix(yol, "/store/v1") {
			continue
		}

		islemHaritasi, ok := islemler.(map[string]any)
		require.True(t, ok, "yol girdisi metot haritası olmalı")

		for metod, ham := range islemHaritasi {
			op, ok := ham.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s anlatılmalı", metod, yol)
			bulunan = append(bulunan, strings.ToUpper(metod)+" "+yol)
		}
	}

	assert.ElementsMatch(t, []string{
		"GET /store/v1/products",
		"GET /store/v1/products/{id}",
		// GraphQL ucu da vitrindedir ve anlatılır; OpenAPI onun ŞEMASINI
		// anlatamaz ama yolunu, gövdesini ve sözleşmenin nerede olduğunu
		// anlatır (bkz. api.describeVitrinGraphQL).
		"POST /store/v1/graphql",
	}, bulunan)
}

// TestGraphQLUcuGovdesiniAnlatir GraphQL ucunun istek ve yanıt zarflarını
// anlattığını doğrular.
//
// İddia iki yerden gelir. Birincisi istemci üretecidir: gövdesi anlatılmamış
// bir POST, çağrılamayan bir metoda dönüşür. İkincisi uçtan uca şema testidir
// (internal/e2e): anlatılan HER ucun 2xx gövdesinin şekli bilinmelidir ve o
// test yalnızca Docker'lı koşumda çalışır — burada kırılması, sorunu bir
// konteyner beklemeden gösterir.
//
// "data"nın İÇİ kasten anlatılmaz: biçimini istemcinin sorgusu belirler.
func TestGraphQLUcuGovdesiniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := vitrinBelgesi(t)
	op := vitrinIslemi(t, yollar, http.MethodPost, "/store/v1/graphql")

	assert.NotEmpty(t, op["summary"])

	govde, ok := op["requestBody"].(map[string]any)
	require.True(t, ok, "GraphQL ucu gövde okur ve bunu söylemelidir")

	icerik, ok := govde["content"].(map[string]any)
	require.True(t, ok)

	json_, ok := icerik["application/json"].(map[string]any)
	require.True(t, ok)

	istek, ok := json_["schema"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"query", "operationName", "variables"},
		vitrinAlanlari(t, bilesenler, istek))

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar["200"].(map[string]any)
	require.True(t, ok, "GraphQL, çözümlenen isteğe alan hatalarıyla birlikte 200 döner")

	zarf := yanitSemasi(t, tanim)
	assert.ElementsMatch(t, []string{"data", "errors"},
		vitrinAlanlari(t, bilesenler, zarf), "GraphQL yanıt zarfı bu iki alandır")
}
