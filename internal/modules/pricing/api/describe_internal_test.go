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

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([priceListRequest],
// [priceSetDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
// açmak olurdu; belgeyi sınamak uğruna modülün yüzeyini genişletmek, sınanan
// şeyin kendisini bozardı.

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
	New(nil).Routes(r)

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
	// aynı olmalıdır (bkz. admin.go, store.go).
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
			metod: http.MethodPost, yol: "/admin/v1/price-sets", durum: "201",
			istek: createPriceSetRequest{}, yanit: doluPriceSet(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/price-sets", durum: "200",
			yanit: doluPriceSet(), liste: true,
			sorgu: []string{"limit", "offset"},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/price-sets/{id}", durum: "200",
			yanit: doluPriceSet(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/price-sets/{id}", durum: "204",
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/price-sets/{id}/prices", durum: "200",
			yanit: priceDTO{}, liste: true,
		},
		{
			// 200'dür, 201 DEĞİL: uç kaynak yaratmaz, kümeyi yerine koyar ve
			// yazdığı kümeyi LİSTE zarfıyla döner (bkz. [API.setPrices]).
			metod: http.MethodPost, yol: "/admin/v1/price-sets/{id}/prices", durum: "200",
			istek: setPricesRequest{}, yanit: priceDTO{}, liste: true,
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/price-sets/{id}/calculate", durum: "200",
			yanit: calculatedPriceDTO{},
			sorgu: []string{paramCurrencyCode, paramQuantity, paramAt},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/price-lists", durum: "201",
			istek: priceListRequest{}, yanit: priceListDTO{},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/price-lists", durum: "200",
			yanit: priceListDTO{}, liste: true,
			sorgu: []string{"limit", "offset"},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/price-lists/{id}", durum: "200",
			yanit: priceListDTO{},
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/price-lists/{id}", durum: "200",
			istek: priceListRequest{}, yanit: priceListDTO{},
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/price-lists/{id}", durum: "204",
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/prices/{price_id}/rules", durum: "200",
			yanit: priceRuleDTO{}, liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/prices/{price_id}/rules", durum: "201",
			istek: ruleRequest{}, yanit: priceRuleDTO{},
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/price-rules/{id}", durum: "204",
		},
		{
			metod: http.MethodGet, yol: "/store/v1/price-sets/{id}", durum: "200",
			yanit: doluPriceSet(),
		},
	}
}

// doluPriceSet omitempty alanı da yazılan bir fiyat kabı üretir.
//
// "prices" tek omitempty alandır; boş bırakılsaydı test onun şemadan düştüğünü
// göremezdi.
func doluPriceSet() priceSetDTO {
	return priceSetDTO{Prices: []priceDTO{{}}}
}

// TestUclarGovdeleriniAnlatir her ucun ne ALDIĞINI ve ne DÖNDÜĞÜNÜ söylediğini
// doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve
// şöyle başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de
// her şeyi 'any' olan, dönüş tipi 'void' olan bir metot üretir — yani o
// istemciyle fiyat KURULAMAZ.
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
// yok sayar. Ters yönü de aynı derecede önemlidir — hesaplama ucunun okuduğu
// para birimi anlatılmazsa istemci onu HİÇ gönderemez.
func TestUclarYalnizcaOkunanParametreleriAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, uc := range uclar() {
		op := islem(t, yollar, uc.metod, uc.yol)
		assert.ElementsMatch(t, uc.sorgu, parametreAdlari(t, op, "query"),
			"%s sorgu parametreleri handler'ın okuduklarıyla aynı olmalı", uc.anahtar())
	}
}

// TestHesaplamaUcuKuralBaglaminiAciklamadaAnlatir "attr_" önekinin şemada
// parametre olarak DEĞİL, açıklamada anlatıldığını doğrular.
//
// OpenAPI parametreyi ADIYLA tanımlar, önekle değil: uydurma bir "attr_*"
// girdisi, istemci üretecinde tam olarak o adı taşıyan ve sunucunun
// reddedeceği bir argüman üretirdi (bkz. [calculateQuery]; tanınmayan
// parametre hatadır).
func TestHesaplamaUcuKuralBaglaminiAciklamadaAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)
	op := islem(t, yollar, http.MethodGet, "/admin/v1/price-sets/{id}/calculate")

	for _, ad := range parametreAdlari(t, op, "query") {
		assert.NotContains(t, ad, paramAttrPrefix,
			"önekli bağlam bir parametre adı olarak yazılamaz")
	}

	aciklama, ok := op["description"].(string)
	require.True(t, ok, "kural bağlamı açıklamada anlatılmalı")
	assert.Contains(t, aciklama, paramAttrPrefix)
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

// TestZamanParametresiBiciminiAnlatir hesaplama anının RFC 3339 olduğunun
// şemada YAZILI olduğunu doğrular.
//
// Düz "string" demek, istemci üretecinin alanı serbest metin yapması demekti;
// oysa [timeParam] başka her biçimi reddeder ve hata çalışma zamanında,
// istemci elinde çıkardı.
func TestZamanParametresiBiciminiAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)
	op := islem(t, yollar, http.MethodGet, "/admin/v1/price-sets/{id}/calculate")

	params, ok := op["parameters"].([]any)
	require.True(t, ok)

	var bulundu bool

	for _, ham := range params {
		p, ok := ham.(map[string]any)
		require.True(t, ok)

		if p["name"] != paramAt {
			continue
		}

		bulundu = true

		sema, ok := p["schema"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, tipDize, sema[semaTip])
		assert.Equal(t, bicimTarihSaat, sema[semaBicim])
	}

	require.True(t, bulundu, "%q parametresi anlatılmalı", paramAt)
}

// TestSemaZamanAlanlariniTarihOlarakAnlatir yanıt kaydındaki zaman
// damgalarının şemada tarih-saat olarak göründüğünü doğrular.
//
// Kanıtın somut karşılığı şudur: doğru biçimlendirilmiş bir alanı istemci
// üreteci Date tipiyle üretir, düz dizeyle değil — yani fiyat listesinin
// geçerlilik penceresi istemcide tarih olarak karşılaştırılabilir.
func TestSemaZamanAlanlariniTarihOlarakAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)
	op := islem(t, yollar, http.MethodGet, "/admin/v1/price-lists/{id}")

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar["200"].(map[string]any)
	require.True(t, ok)

	kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), false)

	ozellikler, ok := semaCoz(t, bilesenler, kayit)["properties"].(map[string]any)
	require.True(t, ok)

	olusturma, ok := ozellikler["created_at"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, bicimTarihSaat, olusturma[semaBicim])

	// Boş bırakılabilen pencere alanı hem tarih hem null olabilmelidir; tek
	// tip yazmak, listesi süresiz olan bir kaydı istemcide çözülemez yapardı.
	baslangic, ok := ozellikler["starts_at"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []any{tipDize, "null"}, baslangic[semaTip])
}
