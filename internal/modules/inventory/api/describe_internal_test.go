package api

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([createItemRequest],
// [inventoryLevelDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri
// dışa açmak olurdu; belgeyi sınamak uğruna modülün yüzeyini genişletmek,
// sınanan şeyin kendisini bozardı.

// belge Describe'ın çıktısını GERÇEK route ağacına karşı üretip JSON'dan geri
// okunmuş hâlini döner.
//
// Doğrudan [openapi.Doc.Build] çıktısına bakmak yetmezdi: işlemler orada Go
// struct'ıdır ve incelenen davranış tam olarak alanların JSON'a yazılıp
// yazılmadığıdır. Router da gerçek olmalıdır — açıklama ile route'un yolu
// ayrışırsa hata BURADA görünsün, üretimde /openapi.json'a bakan birinde
// değil.
func belge(t *testing.T) (yollar, bilesenler map[string]any) {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	NewHandler(nil).Routes(r)

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

// islem belgeden tek bir yol+metod işlemini döner.
func islem(t *testing.T, yollar map[string]any, metod, yol string) map[string]any {
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

// govdeSemasi bir yanıt ya da istek gövdesi tanımından JSON şemasını çıkarır.
func govdeSemasi(t *testing.T, tanim map[string]any) map[string]any {
	t.Helper()

	icerik, ok := tanim["content"].(map[string]any)
	require.True(t, ok, "gövde tanımında content olmalı: %#v", tanim)

	json_, ok := icerik["application/json"].(map[string]any)
	require.True(t, ok, "gövde application/json olmalı")

	sema, ok := json_["schema"].(map[string]any)
	require.True(t, ok, "gövdenin şeması olmalı")

	return sema
}

// alanlar şemanın "properties" anahtarlarını döner.
func alanlar(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	return anahtarlar(ozellikler)
}

// zorunlular şemanın "required" listesini döner.
func zorunlular(t *testing.T, bilesenler, sema map[string]any) []string {
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

// anahtarlar bir haritanın anahtarlarını döner.
func anahtarlar[T any](m map[string]T) []string {
	adlar := make([]string, 0, len(m))
	for ad := range m {
		adlar = append(adlar, ad)
	}

	return adlar
}

// jsonAnahtarlari değeri encoding/json ile kodlayıp anahtarlarını döner.
//
// Karşılaştırmanın diğer ucu budur: şema, tel üzerinde GERÇEKTEN ne olduğunu
// anlatmalıdır ve bunu bilen tek şey encoding/json'un kendisidir.
func jsonAnahtarlari(t *testing.T, v any) []string {
	t.Helper()

	ham, err := json.Marshal(v)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	return anahtarlar(cozulmus)
}

// sifirDegeri verilen örneğin tipinin sıfır değerini döner.
//
// Sıfır değerde JSON'a yazılan anahtarlar tam olarak "her zaman yazılanlar"dır,
// yani şemanın "required" kümesi. Örneği elle ikinci kez yazmak yerine tipten
// türetilir: iki örnek arasında bir alan unutulduğunda test yanlış nedenle
// düşerdi.
func sifirDegeri(v any) any {
	return reflect.New(reflect.TypeOf(v)).Elem().Interface()
}

// ucBeklentisi anlatılan tek bir ucun sözleşmesidir.
type ucBeklentisi struct {
	metod string
	yol   string
	// durum başarılı yanıtın GERÇEK status kodudur; handler'ın yazdığı kodla
	// aynı olmalıdır (bkz. api.go).
	durum string
	// istek istek gövdesinin TÜM alanlarını taşıyan örnektir; nil ise uç gövde
	// almaz.
	istek any
	// yanit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir; nil ise
	// yanıtın gövdesi yoktur (204).
	yanit any
	// liste yanıtın LİSTE zarfıyla döndüğünü bildirir. Tekil ile listeyi
	// ayırmak şart: zarfın şekli farklıdır ve istemci üreteci ikisinden
	// farklı dönüş tipleri üretir.
	liste bool
	// sorgu handler'ın GERÇEKTEN okuduğu sorgu parametreleridir.
	sorgu []string
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// uclar anlatılan uçların beklentileridir.
//
// Örnekler DOLUDUR: omitempty taşıyan her alan sıfırdan farklı bir değer alır,
// çünkü karşılaştırma "şemanın properties kümesi = kodlanan anahtar kümesi"
// biçimindedir ve boş bir örnek omitempty alanları hiç yazmazdı.
func uclar() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodPost, yol: pathStockLocations, durum: "201",
			istek: createStockLocationRequest{}, yanit: doluLokasyon(),
		},
		{
			metod: http.MethodGet, yol: pathStockLocations, durum: "200",
			yanit: doluLokasyon(), liste: true,
			sorgu: []string{"limit", "offset"},
		},
		{
			metod: http.MethodGet, yol: pathStockLocation, durum: "200",
			yanit: doluLokasyon(),
		},
		{
			metod: http.MethodPost, yol: pathItems, durum: "201",
			istek: createItemRequest{}, yanit: doluKalem(),
		},
		{
			metod: http.MethodGet, yol: pathItems, durum: "200",
			yanit: doluKalem(), liste: true,
			sorgu: []string{"limit", "offset", "sku", "requires_shipping"},
		},
		{
			metod: http.MethodGet, yol: pathItem, durum: "200",
			yanit: doluKalem(),
		},
		{
			metod: http.MethodDelete, yol: pathItem, durum: "204",
		},
		{
			metod: http.MethodGet, yol: pathItemLevels, durum: "200",
			yanit: inventoryLevelDTO{}, liste: true,
		},
		{
			// 200'dür, 201 DEĞİL: seviye satırı kalem ile lokasyonun
			// kesişiminde zaten vardır, uç yeni bir kaynak yaratmaz.
			metod: http.MethodPost, yol: pathItemLevels, durum: "200",
			istek: setLevelRequest{}, yanit: inventoryLevelDTO{},
		},
		{
			metod: http.MethodPost, yol: pathItemLevelAdjust, durum: "200",
			istek: adjustLevelRequest{}, yanit: inventoryLevelDTO{},
		},
	}
}

// doluLokasyon omitempty alanları da yazılan bir lokasyon kaydı üretir.
func doluLokasyon() stockLocationDTO {
	return stockLocationDTO{
		Address1:    "A",
		Address2:    "B",
		City:        "C",
		Province:    "D",
		PostalCode:  "E",
		CountryCode: "TR",
	}
}

// doluKalem omitempty alanları da yazılan bir stok kalemi üretir.
func doluKalem() inventoryItemDTO {
	return inventoryItemDTO{Title: "A", Description: "B"}
}

// TestUclarGovdeleriniAnlatir her ucun ne ALDIĞINI ve ne DÖNDÜĞÜNÜ söylediğini
// doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve
// şöyle başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de
// her şeyi 'any' olan, dönüş tipi 'void' olan bir metot üretir — yani o
// istemciyle stok YAZILAMAZ.
//
// Alan kümeleri DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, DTO'ya alan eklendiği gün eksik
// kalır ve test bunu görmezdi.
func TestUclarGovdeleriniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range uclar() {
		t.Run(uc.anahtar(), func(t *testing.T) {
			t.Parallel()

			op := islem(t, yollar, uc.metod, uc.yol)
			assert.NotEmpty(t, op["summary"], "özetsiz bir işlem istemcide adsız bir metot olur")

			istekTanimi, govdeVar := op["requestBody"].(map[string]any)
			require.Equal(t, uc.istek != nil, govdeVar,
				"gövde alan uçta requestBody olmalı, almayanda olmamalı")

			if uc.istek != nil {
				assert.Equal(t, true, istekTanimi["required"],
					"yazma ucunun gövdesi zorunlu olmalı")

				sema := govdeSemasi(t, istekTanimi)
				assert.ElementsMatch(t, jsonAnahtarlari(t, uc.istek),
					alanlar(t, bilesenler, sema),
					"istek gövdesinin alanları DTO ile örtüşmeli")
			}

			yanitlar, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			tanim, ok := yanitlar[uc.durum].(map[string]any)
			require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli: %s", uc.durum)

			if uc.yanit == nil {
				assert.NotContains(t, tanim, "content",
					"204'ün gövdesi yoktur; şema gövde vaat etmemeli")

				return
			}

			kayitSemasiniDogrula(t, bilesenler, govdeSemasi(t, tanim), uc)
		})
	}
}

// kayitSemasiniDogrula zarfı ve içindeki kaydı beklentiyle karşılaştırır.
func kayitSemasiniDogrula(t *testing.T, bilesenler, zarf map[string]any, uc ucBeklentisi) {
	t.Helper()

	beklenenZarf := []string{"data"}
	if uc.liste {
		beklenenZarf = []string{"data", "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, beklenenZarf, alanlar(t, bilesenler, zarf),
		"zarfın biçimi plan Bölüm 8'de sabittir")

	kayit := zarfKaydi(t, bilesenler, zarf, uc.liste)
	assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
		"yanıt kaydının alanları DTO ile örtüşmeli")
	assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
		zorunlular(t, bilesenler, kayit),
		"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
}

// zarfKaydi zarfın "data" alanındaki KAYIT şemasını döner.
//
// Liste zarfında data bir dizidir ve anlatılan asıl şey ÖĞE şemasıdır; diziye
// bakıp alan saymak, dolu bir kaydı boş sanmak olurdu.
func zarfKaydi(t *testing.T, bilesenler, zarf map[string]any, liste bool) map[string]any {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, zarf)["properties"].(map[string]any)
	require.True(t, ok)

	veri, ok := ozellikler["data"].(map[string]any)
	require.True(t, ok)

	if !liste {
		return veri
	}

	oge, ok := veri["items"].(map[string]any)
	require.True(t, ok, "liste zarfının öğe şeması olmalı")

	return oge
}

// TestUclarinTumuAnlatildi anlatılmamış bir uç kalmadığını doğrular.
//
// Yeni bir uç eklenip anlatılmadığında bu test düşer. Uyarı olmasaydı arıza
// SESSİZ olurdu: uç belgede yolu ve güvenliğiyle görünür, yalnızca gövdesi
// olmaz — yani şema "var ama ne aldığı bilinmiyor" der ve kimse fark etmez.
func TestUclarinTumuAnlatildi(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	var bulunan []string

	for yol, islemler := range yollar {
		islemHaritasi, ok := islemler.(map[string]any)
		require.True(t, ok, "yol girdisi metot haritası olmalı")

		for metod, ham := range islemHaritasi {
			op, ok := ham.(map[string]any)
			require.True(t, ok)

			assert.NotEmpty(t, op["summary"], "%s %s anlatılmalı", metod, yol)
			bulunan = append(bulunan, strings.ToUpper(metod)+" "+yol)
		}
	}

	beklenen := make([]string, 0, len(uclar()))
	for _, uc := range uclar() {
		beklenen = append(beklenen, uc.anahtar())
	}

	assert.ElementsMatch(t, beklenen, bulunan,
		"tabloda olmayan bir uç sınanmamış demektir")
}

// TestUclarYalnizcaOkunanParametreleriAnlatir sorgu parametrelerinin
// handler'ın GERÇEKTEN okuduklarıyla aynı olduğunu doğrular.
//
// Okunmayan bir parametreyi şemaya koymak, istemciye ÇALIŞMAYAN bir özellik
// vaat etmektir: üreteç metoda argüman koyar, çağıran doldurur, sunucu sessizce
// yok sayar. Ters yönü de aynı derecede önemlidir — kalem listesinin okuduğu
// "sku" süzgeci anlatılmazsa istemci onu HİÇ gönderemez.
func TestUclarYalnizcaOkunanParametreleriAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, uc := range uclar() {
		op := islem(t, yollar, uc.metod, uc.yol)
		assert.ElementsMatch(t, uc.sorgu, parametreAdlari(t, op, "query"),
			"%s sorgu parametreleri handler'ın okuduklarıyla aynı olmalı", uc.anahtar())
	}
}

// TestKalemSuzgeciMantiksalTipiAnlatir requires_shipping'in şemada mantıksal
// göründüğünü doğrular.
//
// Dize olarak anlatılsaydı istemci üreteci serbest metin gönderilebilen bir
// argüman üretirdi; oysa [Handler.listItems] onu strconv.ParseBool ile okur ve
// çözemediği değeri 422 ile reddeder.
func TestKalemSuzgeciMantiksalTipiAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)
	op := islem(t, yollar, http.MethodGet, pathItems)

	params, ok := op["parameters"].([]any)
	require.True(t, ok)

	var bulundu bool

	for _, ham := range params {
		p, ok := ham.(map[string]any)
		require.True(t, ok)

		if p["name"] != "requires_shipping" {
			continue
		}

		bulundu = true

		sema, ok := p["schema"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, tipMantiksal, sema[semaTip])
	}

	require.True(t, bulundu, "requires_shipping süzgeci anlatılmalı")
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
