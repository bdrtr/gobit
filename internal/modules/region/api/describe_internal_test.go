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

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([createRegionRequest],
// [regionDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
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
	// liste yanıtın liste zarfıyla döndüğünü bildirir.
	liste bool
	// sorgular handler'ın GERÇEKTEN okuduğu sorgu parametreleridir.
	sorgular []string
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// sayfalama her sayfalanan ucun okuduğu parametrelerdir.
var sayfalama = []string{"limit", "offset"}

// anlatilanUclar anlatılan uçların beklentileridir.
//
// Hiçbir DTO omitempty taşımaz, dolayısıyla sıfır değerli örnekler tüm alanları
// yazar; yine de örnek TİPTEN türetilir, elle yazılmış alan listesinden değil.
func anlatilanUclar() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodPost, yol: pathAdminRegions, durum: "201",
			istek: createRegionRequest{}, yanit: regionDTO{},
		},
		{
			metod: http.MethodGet, yol: pathAdminRegions, durum: "200",
			yanit: regionDTO{}, liste: true, sorgular: sayfalama,
		},
		{
			metod: http.MethodGet, yol: pathAdminRegion, durum: "200",
			yanit: regionDTO{},
		},
		{
			metod: http.MethodPut, yol: pathAdminRegion, durum: "200",
			istek: updateRegionRequest{}, yanit: regionDTO{},
		},
		{
			metod: http.MethodDelete, yol: pathAdminRegion, durum: "204",
		},
		{
			metod: http.MethodPost, yol: pathAdminRegionCountries, durum: "201",
			istek: addCountryRequest{}, yanit: countryDTO{},
		},
		{
			metod: http.MethodGet, yol: pathAdminRegionCountries, durum: "200",
			yanit: countryDTO{}, liste: true, sorgular: sayfalama,
		},
		{
			metod: http.MethodDelete, yol: pathAdminRegionCountry, durum: "204",
		},
		{
			metod: http.MethodGet, yol: pathAdminCountries, durum: "200",
			yanit: countryDTO{}, liste: true,
			sorgular: []string{"limit", "offset", "region_id"},
		},
		{
			metod: http.MethodGet, yol: pathAdminCurrencies, durum: "200",
			yanit: currencyDTO{}, liste: true, sorgular: sayfalama,
		},
		{
			metod: http.MethodGet, yol: pathAdminCurrency, durum: "200",
			yanit: currencyDTO{},
		},
		{
			metod: http.MethodGet, yol: pathStoreRegions, durum: "200",
			yanit: storeRegionDTO{}, liste: true, sorgular: sayfalama,
		},
		{
			metod: http.MethodGet, yol: pathStoreRegion, durum: "200",
			yanit: storeRegionDTO{},
		},
	}
}

// TestUclarGovdeleriniAnlatir her ucun ne ALDIĞINI ve ne DÖNDÜĞÜNÜ söylediğini
// doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve şöyle
// başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de her
// şeyi 'any' olan, dönüş tipi 'void' olan bir metot üretir.
//
// Alan kümeleri DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, DTO'ya alan eklendiği gün eksik
// kalır ve test bunu görmezdi.
func TestUclarGovdeleriniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range anlatilanUclar() {
		t.Run(uc.anahtar(), func(t *testing.T) {
			t.Parallel()

			op := islem(t, yollar, uc.metod, uc.yol)

			istekTanimi, govdeVar := op["requestBody"].(map[string]any)
			require.Equal(t, uc.istek != nil, govdeVar,
				"gövde alan uçta requestBody olmalı, almayanda olmamalı")

			if uc.istek != nil {
				sema := govdeSemasi(t, istekTanimi)
				assert.ElementsMatch(t, jsonAnahtarlari(t, uc.istek),
					alanlar(t, bilesenler, sema),
					"istek gövdesinin alanları DTO ile örtüşmeli")
			}

			tanim := basariliYanit(t, op, uc.durum)

			if uc.yanit == nil {
				assert.NotContains(t, tanim, "content",
					"204'ün gövdesi yoktur; şema gövde vaat etmemeli")

				return
			}

			kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), uc.liste)
			assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
				"yanıt kaydının alanları DTO ile örtüşmeli")
			assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
				zorunlular(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
}

// basariliYanit işlemin BEKLENEN status kodundaki yanıt tanımını döner.
func basariliYanit(t *testing.T, op map[string]any, durum string) map[string]any {
	t.Helper()

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar[durum].(map[string]any)
	require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli: %s", durum)

	return tanim
}

// zarfKaydi yanıt zarfının içindeki KAYIT şemasını döner.
//
// Tekil ve liste zarfları aynı "data" alanını taşır ama listede o alan bir
// DİZİDİR; kaydı doğrudan okumak, dizi şemasını kayıt sanmak olurdu ve alan
// karşılaştırması sessizce boş kümeyle geçerdi.
func zarfKaydi(t *testing.T, bilesenler, zarf map[string]any, liste bool) map[string]any {
	t.Helper()

	beklenenAlanlar := []string{"data"}
	if liste {
		beklenenAlanlar = []string{"data", "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, beklenenAlanlar, alanlar(t, bilesenler, zarf),
		"yanıt zarfının şekli plan Bölüm 8'deki zarfla aynı olmalı")

	ozellikler, ok := semaCoz(t, bilesenler, zarf)["properties"].(map[string]any)
	require.True(t, ok)

	kayit, ok := ozellikler["data"].(map[string]any)
	require.True(t, ok)

	if !liste {
		return kayit
	}

	oge, ok := kayit["items"].(map[string]any)
	require.True(t, ok, "liste zarfının data alanı dizi olmalı")

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

	beklenen := make([]string, 0, len(anlatilanUclar()))
	for _, uc := range anlatilanUclar() {
		beklenen = append(beklenen, uc.anahtar())
	}

	assert.ElementsMatch(t, beklenen, bulunan,
		"tabloda olmayan bir uç sınanmamış demektir")
}

// TestUclarOkunmayanParametreVaatEtmez şemanın okunmayan bir sorgu parametresi
// duyurmadığını doğrular.
//
// Okunan tek parametreler sayfalama ile /admin/v1/countries'in "region_id"
// süzgecidir (bkz. [pageParams], [optionalParam]). Başka bir uca parametre
// yazmak, istemci üretecinin metoda argüman koyması ve çağıranın onu doldurup
// sunucunun sessizce yok sayması demekti.
func TestUclarOkunmayanParametreVaatEtmez(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, uc := range anlatilanUclar() {
		op := islem(t, yollar, uc.metod, uc.yol)

		var sorgular []string

		params, _ := op["parameters"].([]any)
		for _, ham := range params {
			p, ok := ham.(map[string]any)
			require.True(t, ok)

			if p["in"] != "query" {
				continue
			}

			ad, ok := p["name"].(string)
			require.True(t, ok)

			sorgular = append(sorgular, ad)
		}

		assert.ElementsMatch(t, uc.sorgular, sorgular,
			"%s yalnızca handler'ın GERÇEKTEN okuduğu parametreleri duyurmalı",
			uc.anahtar())
	}
}

// TestReferansVeriUclariYazmaVaatEtmez para birimi ve ülke uçlarının OKUMA
// olduğunu ve bunu şemada SÖYLEDİĞİNİ doğrular.
//
// İki ayrı arıza yakalanır. Birincisi, referans veriye bir gün yazma ucu
// eklenmesi: ISO tablosunu HTTP üzerinden "düzeltilebilir" kılmak, yanlış
// ondalık basamakla girilen tek bir para biriminde o para birimindeki her
// tutarı yanlış ölçekte gösterirdi. İkincisi sessizliktir — yol listesine bakan
// bir istemci geliştiricisi GET'i görüp POST'un da olduğunu varsayar; ayrımın
// açıklamada yazılı olması o varsayımı keser.
func TestReferansVeriUclariYazmaVaatEtmez(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	referansYollar := []string{pathAdminCurrencies, pathAdminCurrency, pathAdminCountries}

	for _, yol := range referansYollar {
		islemHaritasi, ok := yollar[yol].(map[string]any)
		require.True(t, ok, "%s belgede olmalı", yol)

		assert.ElementsMatch(t, []string{"get"}, anahtarlar(islemHaritasi),
			"%s referans veridir; yalnızca GET taşımalı", yol)

		aciklama, _ := islem(t, yollar, http.MethodGet, yol)["description"].(string)
		assert.Contains(t, aciklama, "yalnızca OKUNUR",
			"%s açıklaması referans veri olduğunu söylemeli", yol)
	}
}
