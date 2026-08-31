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
)

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([createProfileRequest],
// [fulfillmentDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri
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
	// aynı olmalıdır (bkz. handlers.go).
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
// Örnekler DOLUDUR: omitempty taşıyan her alan sıfırdan farklı bir değer alır,
// çünkü karşılaştırma "şemanın properties kümesi = kodlanan anahtar kümesi"
// biçimindedir ve boş bir örnek omitempty alanları hiç yazmazdı.
func anlatilanUclar() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodGet, yol: pathAdminProviders, durum: "200",
			// Kayıt bir DTO değil, düz dizedir (bkz. [Handler.listProviders]).
			yanit: "", liste: true,
		},

		{
			metod: http.MethodPost, yol: pathAdminProfiles, durum: "201",
			istek: createProfileRequest{}, yanit: doluProfil(),
		},
		{
			metod: http.MethodGet, yol: pathAdminProfiles, durum: "200",
			yanit: doluProfil(), liste: true,
		},
		{
			metod: http.MethodGet, yol: pathAdminProfile, durum: "200",
			yanit: doluProfil(),
		},
		{
			metod: http.MethodPatch, yol: pathAdminProfile, durum: "200",
			istek: updateProfileRequest{}, yanit: doluProfil(),
		},
		{metod: http.MethodDelete, yol: pathAdminProfile, durum: "204"},

		{metod: http.MethodDelete, yol: pathAdminOption, durum: "204"},
		{
			metod: http.MethodPost, yol: pathAdminOptionRules, durum: "201",
			istek: createRuleRequest{}, yanit: ruleDTO{},
		},
		{
			metod: http.MethodGet, yol: pathAdminOptionRules, durum: "200",
			yanit: ruleDTO{}, liste: true,
		},
		{metod: http.MethodDelete, yol: pathAdminOptionRule, durum: "204"},

		{
			metod: http.MethodGet, yol: pathAdminEligible, durum: "200",
			yanit: quotedOptionDTO{}, liste: true,
		},
		{
			metod: http.MethodGet, yol: pathStoreOptions, durum: "200",
			yanit: storeOptionDTO{}, liste: true,
		},

		{
			metod: http.MethodPost, yol: pathAdminFulfillments, durum: "201",
			istek: createFulfillmentRequest{}, yanit: doluGonderi(),
		},
		{
			metod: http.MethodGet, yol: pathAdminFulfillments, durum: "200",
			yanit: doluGonderi(), liste: true,
		},
		{
			metod: http.MethodGet, yol: pathAdminFulfillment, durum: "200",
			yanit: doluGonderi(),
		},
		{
			metod: http.MethodPost, yol: pathAdminCancel, durum: "200",
			yanit: doluGonderi(),
		},
		{
			metod: http.MethodPost, yol: pathAdminShip, durum: "200",
			istek: shipRequest{}, yanit: doluGonderi(),
		},
		{
			metod: http.MethodPost, yol: pathAdminDeliver, durum: "200",
			yanit: doluGonderi(),
		},
	}
}

// anlatilmayanSecenekUclari [optionDTO] taşıdığı için anlatılmayan uçlardır.
//
// Gerekçe [Describe] godoc'undadır: bileşen adı çakışması belge üretimini
// tümüyle çökertirdi.
func anlatilmayanSecenekUclari() []ucBeklentisi {
	return []ucBeklentisi{
		{metod: http.MethodPost, yol: pathAdminOptions},
		{metod: http.MethodGet, yol: pathAdminOptions},
		{metod: http.MethodGet, yol: pathAdminOption},
		{metod: http.MethodPatch, yol: pathAdminOption},
	}
}

// doluProfil omitempty alanları da yazılan bir profil kaydı üretir.
func doluProfil() profileDTO {
	return profileDTO{Metadata: map[string]any{"k": "v"}}
}

// doluGonderi omitempty alanları da yazılan bir gönderi kaydı üretir.
func doluGonderi() fulfillmentDTO {
	an := time.Now().UTC()

	return fulfillmentDTO{
		TrackingNumber: "TN1",
		TrackingURL:    "https://ornek/TN1",
		ShippedAt:      &an,
		DeliveredAt:    &an,
		CanceledAt:     &an,
		Data:           json.RawMessage(`{"k":"v"}`),
		Metadata:       map[string]any{"k": "v"},
		Items:          []fulfillmentItemDTO{{}},
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

			kayitSemasiniDogrula(t, bilesenler, govdeSemasi(t, tanim), uc)
		})
	}
}

// kayitSemasiniDogrula zarfı ve içindeki kaydı DTO'ya karşı doğrular.
func kayitSemasiniDogrula(t *testing.T, bilesenler, zarf map[string]any, uc ucBeklentisi) {
	t.Helper()

	kayit := zarfKaydi(t, bilesenler, zarf, uc.liste)

	// Sağlayıcı listesinin kaydı bir struct DEĞİL, düz dizedir; alan
	// karşılaştırması orada anlamsızdır ve şemanın söyleyeceği tek doğru şey
	// tipin kendisidir.
	if reflect.TypeOf(uc.yanit).Kind() != reflect.Struct {
		assert.Equal(t, "string", kayit["type"],
			"dize listesinin öğe tipi şemada da dize olmalı")

		return
	}

	assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
		"yanıt kaydının alanları DTO ile örtüşmeli")
	assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
		zorunlular(t, bilesenler, kayit),
		"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
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

// TestSevkGovdesiIstegeBagli sevk bildiriminin gövdesinin ZORUNLU
// işaretlenmediğini doğrular.
//
// Handler boş gövdeyi kabul eder (bkz. [decodeOptionalBody]): bazı taşıyıcılar
// takip numarasını sonradan verir. Zorunlu göstermek, istemci üretecinin
// gövdesiz çağrıyı hiç üretmemesi demekti.
func TestSevkGovdesiIstegeBagli(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)
	op := islem(t, yollar, http.MethodPost, pathAdminShip)

	govde, ok := op["requestBody"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, govde["required"])
}

// TestSecenekUclariBilinclieAnlatilmadi çakışan uçların GÖVDESİZ kaldığını
// doğrular.
//
// Test "eksik" bir durumu sabitler ve bu bilinçlidir: [optionDTO] "Option"
// bileşen adını ister, aynı adı product modülünün models.Option'ı da ister ve
// iki tipin aynı adı istemesi belge üretimini TÜMÜYLE çökertir. Uçlar bu
// yüzden anlatılmadı. Biri bir gün gövde eklerse /openapi.json 500 dönmeye
// başlar ve bu test, o değişikliğin sebebini burada yazılı hâlde bulur.
func TestSecenekUclariBilinclieAnlatilmadi(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, uc := range anlatilmayanSecenekUclari() {
		op := islem(t, yollar, uc.metod, uc.yol)

		assert.NotContains(t, op, "summary", "%s anlatılmamış olmalı", uc.anahtar())
		assert.NotContains(t, op, "requestBody", "%s gövdesiz kalmalı", uc.anahtar())

		yanitlar, ok := op["responses"].(map[string]any)
		require.True(t, ok)

		for kod := range yanitlar {
			assert.NotEqual(t, "2", kod[:1],
				"%s başarı yanıtı vaat etmemeli: %s", uc.anahtar(), kod)
		}
	}
}

// TestAnlatilanUclarinTumuTabloda anlatılan her ucun tabloda karşılığı
// olduğunu doğrular.
//
// Yeni bir uç eklenip anlatılmadığında ya da anlatılıp tabloya yazılmadığında
// bu test düşer. Uyarı olmasaydı arıza SESSİZ olurdu: uç belgede yolu ve
// güvenliğiyle görünür, yalnızca gövdesi olmaz — yani şema "var ama ne aldığı
// bilinmiyor" der ve kimse fark etmez.
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

			if op["summary"] == nil {
				continue
			}

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

// TestUygunlukParametreleriGuvenKararlariniAcmaz şemanın güven kararlarını
// sorgu parametresi olarak duyurmadığını doğrular.
//
// "include_admin_only" ve "trusted_facts" handler'da SABİTLENİR; şemaya
// yazmak, vitrinden gelen bir istemciye tek bir parametreyle yönetime özel
// seçenekleri açabileceğini ima etmek olurdu.
func TestUygunlukParametreleriGuvenKararlariniAcmaz(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	beklenen := []string{
		"region_id", "currency_code", "country_code", "shipping_profile_id",
		"subtotal", "item_count", "total_weight", "is_return",
	}

	for _, yol := range []string{pathStoreOptions, pathAdminEligible} {
		adlar := parametreAdlari(t, islem(t, yollar, http.MethodGet, yol), "query")

		assert.ElementsMatch(t, beklenen, adlar,
			"%s parametreleri parseEligibilityQuery'nin okuduklarıyla aynı olmalı", yol)
		assert.NotContains(t, adlar, "include_admin_only")
		assert.NotContains(t, adlar, "trusted_facts")
	}
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
