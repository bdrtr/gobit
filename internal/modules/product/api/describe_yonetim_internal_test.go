package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Test DAHİLİ pakettedir çünkü yönetim uçlarının GÖVDELERİ dışa kapalıdır
// ([createProductRequest], [deleted], [productSalesChannels] …). Dışarıdan
// sınamanın tek yolu tipleri dışa açmak olurdu; belgeyi sınamak uğruna modülün
// yüzeyini genişletmek, sınanan şeyin kendisini bozardı. Vitrin uçlarının testi
// dışa açık tiplerle çalıştığı için AYRI dosyada ve api_test paketindedir.

// yonetimBelgesi Describe'ın çıktısını GERÇEK route ağacına karşı üretip
// JSON'dan geri okunmuş hâlini döner.
//
// Doğrudan [openapi.Doc.Build] çıktısına bakmak yetmezdi: işlemler orada Go
// struct'ıdır ve incelenen davranış tam olarak alanların JSON'a yazılıp
// yazılmadığıdır. Router da gerçek olmalıdır — açıklamanın yolu route'unkinden
// ayrıştığı an test düşsün, üretimde /openapi.json'a bakan biri değil.
func yonetimBelgesi(t *testing.T) (yollar, bilesenler map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil, graph.Options{}).Routes(r)

	ham, err := doc.Build(r)
	require.NoError(t, err,
		"belge üretilemedi; iki tipin aynı bileşen adını istemesi de bu hatayı verir")
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

// yonetimIslemi belgeden tek bir yol+metod işlemini döner.
func yonetimIslemi(t *testing.T, yollar map[string]any, metod, yol string) map[string]any {
	t.Helper()

	yolIslemleri, ok := yollar[yol].(map[string]any)
	require.True(t, ok, "%s belgede olmalı", yol)

	op, ok := yolIslemleri[strings.ToLower(metod)].(map[string]any)
	require.True(t, ok, "%s %s belgede olmalı", metod, yol)

	return op
}

// yonetimSemaCoz "$ref" atıflarını belgedeki bileşene çözer.
func yonetimSemaCoz(t *testing.T, bilesenler, sema map[string]any) map[string]any {
	t.Helper()

	ref, refli := sema["$ref"].(string)
	if !refli {
		return sema
	}

	hedef, ok := bilesenler[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	require.True(t, ok, "%q bileşeni kayıtlı olmalı", ref)

	return hedef
}

// yonetimGovdeSemasi bir istek ya da yanıt tanımından JSON şemasını çıkarır.
func yonetimGovdeSemasi(t *testing.T, tanim map[string]any) map[string]any {
	t.Helper()

	icerik, ok := tanim["content"].(map[string]any)
	require.True(t, ok, "gövde tanımında content olmalı: %#v", tanim)

	json_, ok := icerik["application/json"].(map[string]any)
	require.True(t, ok, "gövde application/json olmalı")

	sema, ok := json_["schema"].(map[string]any)
	require.True(t, ok, "gövdenin şeması olmalı")

	return sema
}

// yonetimAlanlari şemanın "properties" anahtarlarını döner.
func yonetimAlanlari(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	ozellikler, ok := yonetimSemaCoz(t, bilesenler, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	return yonetimAnahtarlar(ozellikler)
}

// yonetimZorunlulari şemanın "required" listesini döner.
func yonetimZorunlulari(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	ham, _ := yonetimSemaCoz(t, bilesenler, sema)["required"].([]any)

	adlar := make([]string, 0, len(ham))
	for _, ad := range ham {
		metin, ok := ad.(string)
		require.True(t, ok)

		adlar = append(adlar, metin)
	}

	return adlar
}

// yonetimAnahtarlar bir haritanın anahtarlarını döner.
func yonetimAnahtarlar[T any](m map[string]T) []string {
	adlar := make([]string, 0, len(m))
	for ad := range m {
		adlar = append(adlar, ad)
	}

	return adlar
}

// yonetimJSONAnahtarlari değeri encoding/json ile kodlayıp anahtarlarını döner.
//
// Karşılaştırmanın diğer ucu budur: şema, tel üzerinde GERÇEKTEN ne olduğunu
// anlatmalıdır ve bunu bilen tek şey encoding/json'un kendisidir.
func yonetimJSONAnahtarlari(t *testing.T, v any) []string {
	t.Helper()

	ham, err := json.Marshal(v)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	return yonetimAnahtarlar(cozulmus)
}

// yonetimSifirDegeri verilen örneğin tipinin sıfır değerini döner.
//
// Sıfır değerde JSON'a yazılan anahtarlar tam olarak "her zaman yazılanlar"dır,
// yani şemanın "required" kümesi. Örneği elle ikinci kez yazmak yerine tipten
// türetilir: iki örnek arasında bir alan unutulduğunda test yanlış nedenle
// düşerdi.
func yonetimSifirDegeri(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// yonetimUcu anlatılan tek bir /admin/v1 ucunun sözleşmesidir.
type yonetimUcu struct {
	metod string
	yol   string
	// durum başarılı yanıtın GERÇEK status kodudur; handler'ın yazdığı kodla
	// aynı olmalıdır (bkz. admin.go). Yönetim tarafında 204 HİÇ kullanılmaz:
	// silme uçları da 200 ile bir [deleted] kaydı yazar.
	durum string
	// istek istek gövdesinin tipinden bir örnektir; nil ise uç gövde almaz.
	istek any
	// kayit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir.
	kayit any
	// liste yanıtın liste zarfıyla mı (writeList) tekil zarfla mı (writeItem)
	// döndüğünü bildirir; ikisi farklı şemadır ve karıştırmak istemci
	// üretecinde yanlış tip üretirdi.
	liste bool
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u yonetimUcu) anahtar() string { return u.metod + " " + u.yol }

// yonetimUclari anlatılan yönetim uçlarının beklentileridir.
//
// Kayıt örnekleri DOLUDUR: omitempty/omitzero taşıyan her alan sıfırdan farklı
// bir değer alır, çünkü karşılaştırma "şemanın properties kümesi = kodlanan
// anahtar kümesi" biçimindedir ve boş bir örnek o alanları hiç yazmazdı. İstek
// gövdeleri için sıfır değer yeterlidir: hiçbir istek DTO'su omitempty
// taşımaz.
func yonetimUclari() []yonetimUcu {
	return []yonetimUcu{
		{
			metod: http.MethodPost, yol: "/admin/v1/products", durum: "201",
			istek: createProductRequest{}, kayit: doluYonetimUrunu(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/products", durum: "200",
			kayit: doluYonetimUrunu(), liste: true,
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/products/{id}", durum: "200",
			kayit: doluYonetimUrunu(),
		},
		{
			metod: http.MethodPatch, yol: "/admin/v1/products/{id}", durum: "200",
			istek: updateProductRequest{}, kayit: doluYonetimUrunu(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/products/{id}", durum: "200",
			kayit: deleted{},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/products/{id}/variants", durum: "201",
			istek: createVariantRequest{}, kayit: doluYonetimVaryanti(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/products/{id}/variants", durum: "200",
			kayit: doluYonetimVaryanti(), liste: true,
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/variants/{id}", durum: "200",
			kayit: doluYonetimVaryanti(),
		},
		{
			metod: http.MethodPatch, yol: "/admin/v1/variants/{id}", durum: "200",
			istek: updateVariantRequest{}, kayit: doluYonetimVaryanti(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/variants/{id}", durum: "200",
			kayit: deleted{},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/products/{id}/options", durum: "201",
			istek: createOptionRequest{}, kayit: doluYonetimSecenegi(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/products/{id}/options", durum: "200",
			kayit: doluYonetimSecenegi(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/product-options/{id}/values", durum: "201",
			istek: optionValueRequest{}, kayit: doluSecenekDegeri(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/product-options/{id}", durum: "200",
			kayit: deleted{},
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/variants/{id}/price-set", durum: "200",
			istek: linkRequest{}, kayit: doluBaglar(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/variants/{id}/price-set", durum: "200",
			kayit: deleted{},
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/variants/{id}/inventory-item", durum: "200",
			istek: linkRequest{}, kayit: doluBaglar(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/variants/{id}/inventory-item", durum: "200",
			kayit: deleted{},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/variants/{id}/links", durum: "200",
			kayit: doluBaglar(),
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/products/{id}/sales-channels", durum: "200",
			istek: linkSalesChannelRequest{}, kayit: productSalesChannels{},
		},
		{
			metod: http.MethodDelete,
			yol:   "/admin/v1/products/{id}/sales-channels/{sales_channel_id}",
			durum: "200", kayit: productSalesChannels{},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/products/{id}/sales-channels", durum: "200",
			kayit: productSalesChannels{},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/product-collections", durum: "201",
			istek: createCollectionRequest{}, kayit: doluKoleksiyon(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/product-collections", durum: "200",
			kayit: doluKoleksiyon(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/product-categories", durum: "201",
			istek: createCategoryRequest{}, kayit: doluKategori(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/product-categories", durum: "200",
			kayit: doluKategori(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/product-tags", durum: "201",
			istek: createTagRequest{}, kayit: doluEtiket(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/product-tags", durum: "200",
			kayit: doluEtiket(), liste: true,
		},
	}
}

// doluYonetimUrunu omitempty alanları da yazılan bir yönetim ürünü üretir.
//
// Vitrin ürününün aksine ilişkili kayıtlar GÖLGELENMEZ: yönetim yanıtı
// [models.Product]'ın kendisidir ve "variants" alanı zenginleştirilmemiş
// [models.Variant] taşır.
func doluYonetimUrunu() models.Product {
	metin := "x"
	sayi := int32(1)
	an := time.Now().UTC()

	return models.Product{
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
		Variants:      []models.Variant{{}},
		Options:       []models.Option{{}},
		Images:        []models.Image{{}},
		Tags:          []models.Tag{{}},
		Categories:    []models.Category{{}},
	}
}

// doluYonetimVaryanti omitempty alanları da yazılan bir varyant üretir.
func doluYonetimVaryanti() models.Variant {
	metin := "x"
	sayi := int32(1)
	an := time.Now().UTC()

	return models.Variant{
		SKU:          &metin,
		Barcode:      &metin,
		EAN:          &metin,
		UPC:          &metin,
		Weight:       &sayi,
		Metadata:     map[string]any{"k": "v"},
		DeletedAt:    &an,
		OptionValues: []models.OptionValue{{}},
	}
}

// doluYonetimSecenegi omitempty alanları da yazılan bir seçenek üretir.
func doluYonetimSecenegi() models.Option {
	an := time.Now().UTC()

	return models.Option{DeletedAt: &an, Values: []models.OptionValue{{}}}
}

// doluSecenekDegeri omitempty ve omitzero alanları da yazılan bir seçenek
// değeri üretir.
func doluSecenekDegeri() models.OptionValue {
	an := time.Now().UTC()

	return models.OptionValue{
		OptionTitle: "Beden",
		CreatedAt:   an,
		UpdatedAt:   an,
		DeletedAt:   &an,
	}
}

// doluBaglar iki bağı da dolu bir varyant bağ kaydı üretir.
func doluBaglar() service.VariantLinks {
	metin := "x"

	return service.VariantLinks{PriceSetID: &metin, InventoryItemID: &metin}
}

// doluKoleksiyon omitempty alanları da yazılan bir koleksiyon üretir.
func doluKoleksiyon() models.Collection {
	an := time.Now().UTC()

	return models.Collection{Metadata: map[string]any{"k": "v"}, DeletedAt: &an}
}

// doluKategori omitempty ve omitzero alanları da yazılan bir kategori üretir.
func doluKategori() models.Category {
	metin := "x"
	an := time.Now().UTC()

	return models.Category{
		Description: &metin,
		ParentID:    &metin,
		CreatedAt:   an,
		UpdatedAt:   an,
		DeletedAt:   &an,
	}
}

// doluEtiket omitzero alanları da yazılan bir etiket üretir.
func doluEtiket() models.Tag {
	an := time.Now().UTC()

	return models.Tag{CreatedAt: an, UpdatedAt: an, DeletedAt: &an}
}

// TestYonetimUclariGovdeleriniAnlatir her yönetim ucunun ne ALDIĞINI ve ne
// DÖNDÜĞÜNÜ söylediğini doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve şöyle
// başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de gövdesi
// olmayan, dönüş tipi 'void' olan bir metot üretir — yani o istemciyle ürün
// OLUŞTURULAMAZ.
//
// Alan kümeleri DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, tipe alan eklendiği gün eksik kalır
// ve test bunu görmezdi.
func TestYonetimUclariGovdeleriniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := yonetimBelgesi(t)

	for _, uc := range yonetimUclari() {
		t.Run(uc.anahtar(), func(t *testing.T) {
			t.Parallel()

			op := yonetimIslemi(t, yollar, uc.metod, uc.yol)
			assert.NotEmpty(t, op["summary"], "uç tek satırla anlatılmalı")

			istekTanimi, govdeVar := op["requestBody"].(map[string]any)
			require.Equal(t, uc.istek != nil, govdeVar,
				"gövde alan uçta requestBody olmalı, almayanda olmamalı")

			if uc.istek != nil {
				sema := yonetimGovdeSemasi(t, istekTanimi)
				assert.ElementsMatch(t, yonetimJSONAnahtarlari(t, uc.istek),
					yonetimAlanlari(t, bilesenler, sema),
					"istek gövdesinin alanları GERÇEK DTO ile örtüşmeli")
			}

			yanitlar, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			tanim, ok := yanitlar[uc.durum].(map[string]any)
			require.True(t, ok,
				"handler'ın GERÇEKTEN yazdığı kod belgelenmeli: %s", uc.durum)

			kayit := yonetimKaydi(t, bilesenler, yonetimGovdeSemasi(t, tanim), uc.liste)

			assert.ElementsMatch(t, yonetimJSONAnahtarlari(t, uc.kayit),
				yonetimAlanlari(t, bilesenler, kayit),
				"yanıt kaydının alanları GERÇEK tiple örtüşmeli")
			assert.ElementsMatch(t, yonetimJSONAnahtarlari(t, yonetimSifirDegeri(uc.kayit)),
				yonetimZorunlulari(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
}

// yonetimKaydi zarfın içindeki KAYIT şemasını döner ve zarf biçimini doğrular.
//
// Liste ile tekil zarfı ayırmak testin işinin yarısıdır: ikisi de "data"
// taşır, ama listede o alan bir DİZİDİR. Yanlışını yazmak istemci üretecinde
// tek kaydı dizi (ya da diziyi tek kayıt) sanan bir metot üretirdi.
func yonetimKaydi(t *testing.T, bilesenler, zarf map[string]any, liste bool) map[string]any {
	t.Helper()

	beklenen := []string{"data"}
	if liste {
		beklenen = []string{"data", "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, beklenen, yonetimAlanlari(t, bilesenler, zarf),
		"zarf plan Bölüm 8'deki biçimde olmalı")

	ozellikler, ok := yonetimSemaCoz(t, bilesenler, zarf)["properties"].(map[string]any)
	require.True(t, ok)

	veri, ok := ozellikler["data"].(map[string]any)
	require.True(t, ok)

	if !liste {
		return veri
	}

	assert.Equal(t, "array", veri["type"], "liste zarfının data alanı dizidir")

	oge, ok := veri["items"].(map[string]any)
	require.True(t, ok, "liste zarfının öğe şeması olmalı")

	return oge
}

// TestYonetimUclarininTumuAnlatildi anlatılmamış bir yönetim ucu kalmadığını
// doğrular.
//
// Yeni bir uç eklenip anlatılmadığında bu test düşer. Arıza aksi hâlde SESSİZ
// olurdu: uç belgede yolu ve güvenliğiyle görünür, yalnızca ne aldığı ve ne
// döndüğü bilinmez.
func TestYonetimUclarininTumuAnlatildi(t *testing.T) {
	t.Parallel()

	yollar, _ := yonetimBelgesi(t)

	var bulunan []string

	for yol, islemler := range yollar {
		if !strings.HasPrefix(yol, "/admin/v1") {
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

	beklenen := make([]string, 0, len(yonetimUclari()))
	for _, uc := range yonetimUclari() {
		beklenen = append(beklenen, uc.anahtar())
	}

	assert.ElementsMatch(t, beklenen, bulunan,
		"tabloda olmayan bir yönetim ucu sınanmamış demektir")
}

// TestYonetimUclariYalnizcaOkunanParametreleriAnlatir sorgu parametrelerinin
// handler'ın GERÇEKTEN okuduklarıyla aynı olduğunu doğrular.
//
// Okunmayan bir parametreyi şemaya koymak, istemciye ÇALIŞMAYAN bir özellik
// vaat etmektir: üreteç metoda argüman koyar, çağıran doldurur, sunucu sessizce
// yok sayar. Ters yön de aynı derecede pahalıdır — okunan ama anlatılmayan bir
// parametre, istemcinin hiç ulaşamayacağı bir süzgeçtir.
func TestYonetimUclariYalnizcaOkunanParametreleriAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := yonetimBelgesi(t)

	// Beklenen kümeler admin.go'daki okuma çağrılarından gelir; listede olmayan
	// her yönetim ucu sorgu dizesine hiç bakmaz.
	beklenen := map[string][]string{
		"GET /admin/v1/products": {
			"collection_id", "handle", "q", "status", "expand", "limit", "offset",
		},
		"GET /admin/v1/products/{id}/variants": {"limit", "offset"},
		"GET /admin/v1/product-collections":    {"limit", "offset"},
		"GET /admin/v1/product-categories":     {"parent_id", "limit", "offset"},
		"GET /admin/v1/product-tags":           {"limit", "offset"},
	}

	for _, uc := range yonetimUclari() {
		op := yonetimIslemi(t, yollar, uc.metod, uc.yol)

		assert.ElementsMatch(t, beklenen[uc.anahtar()], yonetimParametreAdlari(t, op, "query"),
			"%s sorgu parametreleri handler'ın okuduklarıyla aynı olmalı", uc.anahtar())
	}
}

// yonetimParametreAdlari işlemin verilen yerdeki parametre adlarını döner.
func yonetimParametreAdlari(t *testing.T, op map[string]any, yer string) []string {
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
