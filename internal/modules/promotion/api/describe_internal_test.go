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

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([campaignRequest],
// [promotionDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
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
	// aynı olmalıdır (bkz. admin.go ve store.go).
	durum string
	// istek istek gövdesinin TÜM alanlarını taşıyan örnektir; nil ise uç gövde
	// almaz.
	istek any
	// yanit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir; nil ise
	// yanıtın gövdesi yoktur (204).
	yanit any
	// liste yanıtın liste zarfıyla mı döndüğünü bildirir.
	liste bool
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// anlatilanUclar anlatılan uçların beklentileridir.
//
// Örnekler SIFIR değerdir ve bu güvenlidir: bu paketin DTO'larının hiçbiri
// omitempty taşımaz, yani sıfır değer de tüm alanları yazar. Bir gün omitempty
// eklenirse karşılaştırma alan eksiğiyle düşer ve örneğin doldurulması gerektiği
// tam o noktada görünür.
func anlatilanUclar() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodPost, yol: "/admin/v1/campaigns", durum: "201",
			istek: campaignRequest{}, yanit: campaignDTO{},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/campaigns", durum: "200",
			yanit: campaignDTO{}, liste: true,
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/campaigns/{id}", durum: "200",
			yanit: campaignDTO{},
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/campaigns/{id}", durum: "200",
			istek: campaignRequest{}, yanit: campaignDTO{},
		},
		{metod: http.MethodDelete, yol: "/admin/v1/campaigns/{id}", durum: "204"},

		{
			metod: http.MethodPost, yol: "/admin/v1/promotions", durum: "201",
			istek: promotionRequest{}, yanit: promotionDTO{},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/promotions", durum: "200",
			yanit: promotionDTO{}, liste: true,
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/promotions/{id}", durum: "200",
			yanit: promotionDTO{},
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/promotions/{id}", durum: "200",
			istek: promotionRequest{}, yanit: promotionDTO{},
		},
		{metod: http.MethodDelete, yol: "/admin/v1/promotions/{id}", durum: "204"},

		{
			metod: http.MethodPut, yol: "/admin/v1/promotions/{id}/application-method",
			durum: "200",
			istek: applicationMethodRequest{}, yanit: applicationMethodDTO{},
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/promotions/{id}/application-method",
			durum: "204",
		},

		{
			metod: http.MethodGet, yol: "/admin/v1/promotions/{id}/rules", durum: "200",
			yanit: promotionRuleDTO{}, liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/promotions/{id}/rules", durum: "201",
			istek: promotionRuleRequest{}, yanit: promotionRuleDTO{},
		},
		{metod: http.MethodDelete, yol: "/admin/v1/promotion-rules/{id}", durum: "204"},

		{
			metod: http.MethodGet, yol: "/admin/v1/promotions/{id}/redemptions", durum: "200",
			yanit: redemptionDTO{}, liste: true,
		},
		{
			// 200: kullanım İDEMPOTENTTİR ve ikinci istek yeni kayıt YARATMAZ.
			metod: http.MethodPost, yol: "/admin/v1/promotions/{id}/redeem", durum: "200",
			istek: redeemRequest{}, yanit: redemptionDTO{},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/promotions/{id}/release", durum: "200",
			istek: releaseRequest{}, yanit: releaseResultDTO{},
		},

		{
			metod: http.MethodPost, yol: "/admin/v1/promotions/compute", durum: "200",
			istek: computeRequest{}, yanit: computeResultDTO{},
		},

		{
			metod: http.MethodGet, yol: "/store/v1/promotions/{code}", durum: "200",
			yanit: storeCouponDTO{},
		},
	}
}

// TestAnlatilanUclarGovdeleriniAnlatir her ucun ne ALDIĞINI ve ne DÖNDÜĞÜNÜ
// söylediğini doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve
// şöyle başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de
// her şeyi 'any' olan, dönüş tipi 'void' olan bir metot üretir.
//
// Alan kümeleri DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, DTO'ya alan eklendiği gün eksik
// kalır ve test bunu görmezdi.
func TestAnlatilanUclarGovdeleriniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range anlatilanUclar() {
		t.Run(uc.anahtar(), func(t *testing.T) {
			t.Parallel()

			op := islem(t, yollar, uc.metod, uc.yol)
			assert.NotEmpty(t, op["summary"], "uç tek satırla anlatılmalı")

			istekTanimi, govdeVar := op["requestBody"].(map[string]any)
			require.Equal(t, uc.istek != nil, govdeVar,
				"gövde alan uçta requestBody olmalı, almayanda olmamalı")

			if uc.istek != nil {
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

			kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), uc.liste)

			assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit),
				alanlar(t, bilesenler, kayit),
				"yanıt kaydının alanları DTO ile örtüşmeli")
			assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
				zorunlular(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
}

// zarfKaydi zarfın taşıdığı KAYIT şemasını döner.
//
// Tekil zarf kaydı doğrudan "data" altında tutar; liste zarfı onu bir dizinin
// öğesi yapar. Zarfın kendi alanları da denetlenir: biçim plan Bölüm 8'de
// sabittir ve bozulması istemcinin yanıtı hiç çözememesi demektir.
func zarfKaydi(t *testing.T, bilesenler, zarf map[string]any, liste bool) map[string]any {
	t.Helper()

	if liste {
		assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
			alanlar(t, bilesenler, zarf), "liste zarfı plan Bölüm 8'deki biçimdir")
	} else {
		assert.ElementsMatch(t, []string{"data"}, alanlar(t, bilesenler, zarf),
			"tekil yanıtlar {\"data\": …} zarfıyla döner")
	}

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

// TestMusteriUcuYonetimGovdesiniSizdirmaz vitrin kupon ucunun DAR gövdeyi
// anlattığını doğrular.
//
// İki gövdeyi tek bileşenle anlatmak, şemanın müşteriye promosyonun durumunu,
// kullanım sayacını ve kampanyasını vaat etmesi demek olurdu; hiçbiri o uçtan
// gitmez ve istemci üreteci hep boş kalan alanlar üretirdi.
func TestMusteriUcuYonetimGovdesiniSizdirmaz(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)
	op := islem(t, yollar, http.MethodGet, "/store/v1/promotions/{code}")

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar["200"].(map[string]any)
	require.True(t, ok)

	kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), false)
	adlar := alanlar(t, bilesenler, kayit)

	assert.ElementsMatch(t, jsonAnahtarlari(t, storeCouponDTO{}), adlar)

	for _, yasak := range []string{"status", "usage_count", "usage_limit", "campaign_id", "metadata"} {
		assert.NotContains(t, adlar, yasak,
			"%q müşteriye gitmez; şema onu vaat etmemeli", yasak)
	}
}

// TestSayfalanmayanListeSorguParametresiVaatEtmez kural listesinin okunmayan
// bir parametre duyurmadığını doğrular.
//
// GET /admin/v1/promotions/{id}/rules sorgu dizesini HİÇ okumaz ([writeItems]
// ile yazılır). Şemaya limit/offset yazmak, istemci üretecinin metoda argüman
// koyması ve çağıranın onu doldurup sunucunun sessizce yok sayması demekti.
func TestSayfalanmayanListeSorguParametresiVaatEtmez(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)
	op := islem(t, yollar, http.MethodGet, "/admin/v1/promotions/{id}/rules")

	assert.Empty(t, parametreAdlari(t, op, "query"),
		"kural listesi sayfalanmaz; sayfalama parametresi duyurulmamalı")
}

// TestPromosyonListesiOkunanParametreleriAnlatir sorgu parametrelerinin
// handler'ın GERÇEKTEN okuduklarıyla aynı olduğunu doğrular.
func TestPromosyonListesiOkunanParametreleriAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)
	op := islem(t, yollar, http.MethodGet, "/admin/v1/promotions")

	assert.ElementsMatch(t, []string{"limit", "offset", "status", "campaign_id"},
		parametreAdlari(t, op, "query"),
		"parametreler listPromotions'ın okuduklarıyla aynı olmalı")
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

// TestAnlatilanUclarinTumuTabloda anlatılmamış bir uç kalmadığını doğrular.
//
// Yeni bir uç eklenip anlatılmadığında bu test düşer. Uyarı olmasaydı arıza
// SESSİZ olurdu: uç belgede yolu ve güvenliğiyle görünür, yalnızca gövdesi
// olmaz — yani şema "var ama ne aldığı bilinmiyor" der ve kimse fark etmez.
func TestAnlatilanUclarinTumuTabloda(t *testing.T) {
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
