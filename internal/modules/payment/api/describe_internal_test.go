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

// zarfVeriAlani yanıt zarfının kayıt taşıyan alanının adıdır (plan Bölüm 8).
//
// Sabit olarak tutulmasının sebebi tekrarın kendisi değil, yazım hatasının
// SESSİZ olmasıdır: "dta" yazılmış bir anahtar derlenir ve test yanlış nedenle
// düşerdi.
const zarfVeriAlani = "data"

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([createSessionRequest],
// [sessionDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
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

// ozellikler şemanın "properties" haritasını döner.
func ozellikler(t *testing.T, bilesenler, sema map[string]any) map[string]any {
	t.Helper()

	m, ok := semaCoz(t, bilesenler, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	return m
}

// alanlar şemanın "properties" anahtarlarını döner.
func alanlar(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	return anahtarlar(ozellikler(t, bilesenler, sema))
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
	// govdeIstegeBagli gövdenin hiç gönderilmeyebileceğini bildirir.
	govdeIstegeBagli bool
	// yanit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir; nil ise
	// yanıtın gövdesi yoktur (204) ya da kaydı ilkel bir değerdir.
	yanit any
	// liste yanıtın LİSTE zarfıyla döndüğünü bildirir.
	liste bool
	// ilkelOge liste öğesi ilkel bir değerse JSON Schema tip adıdır.
	ilkelOge string
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// govdesiz ucun yanıt gövdesi olup olmadığını bildirir.
func (u ucBeklentisi) govdesiz() bool { return u.yanit == nil && u.ilkelOge == "" }

// anlatilanUclar anlatılan uçların beklentileridir.
//
// Örnekler DOLUDUR: omitempty taşıyan her alan sıfırdan farklı bir değer alır,
// çünkü karşılaştırma "şemanın properties kümesi = kodlanan anahtar kümesi"
// biçimindedir ve boş bir örnek omitempty alanları hiç yazmazdı.
//
// Ödeme KOLEKSİYONU gövdesi taşıyan dört uç burada YOKTUR ve olmaması
// bilinçlidir: gerekçe [Describe] belgesindedir ("Collection" bileşen adı
// product ile çakışıyor).
func anlatilanUclar() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodGet, yol: pathAdminProviders, durum: "200",
			liste: true, ilkelOge: "string",
		},
		{
			metod: http.MethodGet, yol: pathStoreProviders, durum: "200",
			liste: true, ilkelOge: "string",
		},
		{
			metod: http.MethodGet, yol: pathAdminCollectionSess, durum: "200",
			yanit: doluOturum(), liste: true,
		},
		{
			metod: http.MethodPost, yol: pathAdminCollectionSess, durum: "201",
			istek: createSessionRequest{}, yanit: doluOturum(),
		},
		{
			metod: http.MethodGet, yol: pathAdminSession, durum: "200",
			yanit: doluOturum(),
		},
		{
			metod: http.MethodPost, yol: pathAdminSessionAuthorize, durum: "200",
			yanit: doluOturum(),
		},
		{
			metod: http.MethodPost, yol: pathAdminSessionCapture, durum: "201",
			istek: amountRequest{}, govdeIstegeBagli: true, yanit: doluTahsilat(),
		},
		{
			metod: http.MethodPost, yol: pathAdminSessionCancel, durum: "204",
		},
		{
			metod: http.MethodGet, yol: pathAdminCollectionPays, durum: "200",
			yanit: doluTahsilat(), liste: true,
		},
		{
			metod: http.MethodGet, yol: pathAdminPayment, durum: "200",
			yanit: doluTahsilat(),
		},
		{
			metod: http.MethodGet, yol: pathAdminPaymentRefund, durum: "200",
			yanit: doluIade(), liste: true,
		},
		{
			metod: http.MethodPost, yol: pathAdminPaymentRefund, durum: "201",
			istek: refundRequest{}, yanit: doluIade(),
		},
		{
			metod: http.MethodPost, yol: pathStoreCollectSess, durum: "201",
			istek: createStoreSessionRequest{}, yanit: doluOturum(),
		},
		{
			metod: http.MethodPost, yol: pathStoreSessionCancel, durum: "204",
		},
	}
}

// anlatilmayanUclar çakışma yüzünden gövdesi anlatılmayan uçlardır.
var anlatilmayanUclar = []string{
	http.MethodPost + " " + pathAdminCollections,
	http.MethodGet + " " + pathAdminCollections,
	http.MethodGet + " " + pathAdminCollection,
	http.MethodGet + " " + pathStoreCollection,
}

// doluOturum omitempty alanları da yazılan bir ödeme oturumu üretir.
func doluOturum() sessionDTO {
	return sessionDTO{
		Data:          json.RawMessage(`{"k":"v"}`),
		DeclineReason: "insufficient_funds",
	}
}

// doluTahsilat bir tahsilat kaydı üretir.
//
// [paymentDTO]'nun omitempty alanı yoktur; zaman alanı yine de doldurulur ki
// örnek gerçek bir yanıta benzesin.
func doluTahsilat() paymentDTO {
	return paymentDTO{CapturedAt: time.Now().UTC()}
}

// doluIade omitempty alanları da yazılan bir iade kaydı üretir.
func doluIade() refundDTO {
	return refundDTO{Reason: "müşteri iadesi"}
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
			assert.NotEmpty(t, op["summary"], "her anlatılan uç bir özet taşımalı")

			istekGovdesiniDenetle(t, bilesenler, op, uc)

			yanitlar, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			tanim, ok := yanitlar[uc.durum].(map[string]any)
			require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli: %s", uc.durum)

			if uc.govdesiz() {
				assert.NotContains(t, tanim, "content",
					"204'ün gövdesi yoktur; şema gövde vaat etmemeli")

				return
			}

			kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), uc.liste)

			if uc.ilkelOge != "" {
				assert.Equal(t, uc.ilkelOge, kayit["type"],
					"liste öğesi ilkel tipte olmalı; nesne demek istemcide yanlış sınıf üretir")

				return
			}

			assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
				"yanıt kaydının alanları DTO ile örtüşmeli")
			assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
				zorunlular(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
}

// istekGovdesiniDenetle ucun istek gövdesi sözleşmesini doğrular.
//
// Gövdenin ZORUNLULUĞU da sınanır: tahsilat ucunda gövde göndermemek geçerlidir
// ve "tamamı" demektir. Şema zorunlu deseydi istemci üreteci çağıranı, yalnızca
// şema öyle dediği için boş bir nesne kurmaya zorlardı.
func istekGovdesiniDenetle(t *testing.T, bilesenler, op map[string]any, uc ucBeklentisi) {
	t.Helper()

	tanim, govdeVar := op["requestBody"].(map[string]any)
	require.Equal(t, uc.istek != nil, govdeVar,
		"gövde alan uçta requestBody olmalı, almayanda olmamalı")

	if uc.istek == nil {
		return
	}

	assert.Equal(t, !uc.govdeIstegeBagli, tanim["required"],
		"gövdenin zorunluluğu handler'ın davranışıyla aynı olmalı")

	sema := govdeSemasi(t, tanim)
	assert.ElementsMatch(t, jsonAnahtarlari(t, uc.istek), alanlar(t, bilesenler, sema),
		"istek gövdesinin alanları DTO ile örtüşmeli")
}

// zarfKaydi yanıt zarfının taşıdığı KAYIT şemasını döner.
//
// Zarfın kendisi de sınanır: tekil ile liste zarfını karıştırmak istemci
// üretecinde yanlış dönüş tipi demektir — sayfalama alanlarını bekleyen bir
// çağıran tek kayıt alır ya da tersi.
func zarfKaydi(t *testing.T, bilesenler, zarf map[string]any, liste bool) map[string]any {
	t.Helper()

	beklenen := []string{zarfVeriAlani}
	if liste {
		beklenen = []string{zarfVeriAlani, "count", "offset", "limit"}
	}

	assert.ElementsMatch(t, beklenen, alanlar(t, bilesenler, zarf), "yanıt zarfı")

	kayit, ok := ozellikler(t, bilesenler, zarf)[zarfVeriAlani].(map[string]any)
	require.True(t, ok)

	if !liste {
		return kayit
	}

	assert.Equal(t, "array", kayit["type"], "liste zarfının data alanı dizi olmalı")

	oge, ok := kayit["items"].(map[string]any)
	require.True(t, ok, "dizinin öğe şeması olmalı")

	return oge
}

// TestAnlatilanUclarinTumuTabloda anlatılan uç kümesinin tabloyla AYNI
// olduğunu doğrular.
//
// İki yönü de kapsar. Yeni bir uç eklenip anlatılmadığında test düşer: uyarı
// olmasaydı arıza SESSİZ olurdu — uç belgede yolu ve güvenliğiyle görünür,
// yalnızca gövdesi olmaz. Tabloya girmemiş bir uç anlatıldığında da düşer;
// anlatılmış ama sınanmamış bir uç, doğru sanılan bir sözleşmedir.
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

// TestKoleksiyonUclariBelgedeGovdesiz anlatılmayan dört ucun belgeden
// DÜŞMEDİĞİNİ, yalnızca gövdesiz kaldığını doğrular.
//
// Eksiklik BİLİNÇLİDİR: [collectionDTO] ile [createCollectionRequest]'in
// isteyeceği "Collection" ve "CreateCollectionRequest" bileşen adları product
// modülünde zaten kayıtlıdır; anlatmak belgenin TAMAMINI üretilemez kılardı
// (bkz. [Describe]). Uçlar yine de yolu, metodu ve güvenliğiyle görünür —
// istemci onların var olduğunu bilir, yalnızca şeklini bilmez.
//
// Çakışma bir gün tiplerden biri yeniden adlandırılarak çözülürse bu test
// düşer; o zaman yapılacak şey uçları [anlatilanUclar] tablosuna taşıyıp bu
// testi KALDIRMAKTIR.
func TestKoleksiyonUclariBelgedeGovdesiz(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, kayit := range anlatilmayanUclar {
		metod, yol, _ := strings.Cut(kayit, " ")

		op := islem(t, yollar, metod, yol)
		assert.Nil(t, op["summary"], "%s hâlâ anlatılmamış olmalı", kayit)
		assert.Nil(t, op["requestBody"], "%s gövdesiz kalmalı", kayit)

		yanitlar, ok := op["responses"].(map[string]any)
		require.True(t, ok)

		for kod := range yanitlar {
			assert.NotEqual(t, "2", kod[:1],
				"%s için başarılı yanıt anlatılmamış olmalı", kayit)
		}
	}
}

// TestTutarAlanlariMinorUnitTamSayidir para taşıyan her alanın tam sayı olarak
// anlatıldığını doğrular.
//
// Somut arıza şudur: şema tutarı "number" gösterseydi istemci geliştiricisi
// 100.50 gönderir, sunucu gövdeyi çözemez ya da — daha kötüsü — kayan nokta
// bir yerde yuvarlanır. Para hiçbir aşamada kayan noktaya uğramaz (plan Bölüm
// 8) ve "format: int64" olmadan JavaScript 2^53'ten sonra değeri SESSİZCE
// bozar.
//
// Alanlar ADLARINDAN bulunur, elle yazılmış bir listeyle değil: yeni bir tutar
// alanı eklendiği gün liste eksik kalır ve test bunu görmezdi.
func TestTutarAlanlariMinorUnitTamSayidir(t *testing.T) {
	t.Parallel()

	_, bilesenler := belge(t)

	sayilan := 0

	for ad, ham := range bilesenler {
		sema, ok := ham.(map[string]any)
		require.True(t, ok, "%q bileşeni nesne olmalı", ad)

		props, varsa := sema["properties"].(map[string]any)
		if !varsa {
			continue
		}

		for alan, alanSemasi := range props {
			if !tutarAlani(alan) {
				continue
			}

			m, ok := alanSemasi.(map[string]any)
			require.True(t, ok)

			assert.Equal(t, "integer", m["type"], "%s.%s tam sayı olmalı", ad, alan)
			assert.Equal(t, "int64", m["format"], "%s.%s int64 olmalı", ad, alan)

			sayilan++
		}
	}

	assert.Positive(t, sayilan, "en az bir tutar alanı anlatılmış olmalı")
}

// TestTutarTasiyanUclarBirimiYaziyor tutar taşıyan her ucun birimi AÇIKÇA
// söylediğini doğrular.
//
// "integer" tek başına birimi söylemez: 100,50 TL'yi gönderemeyeceğini gören
// istemci geliştiricisi 100 ya da 101 göndermeyi deneyebilir. Doğru cevap
// 10050'dir ve bunu söyleyen tek şey açıklamadır.
//
// Hangi uçların not taşıması gerektiği ŞEMADAN türetilir, tablodan değil:
// tutar alanı olan her uç nota muhtaçtır ve yeni bir uç eklendiğinde bu
// kendiliğinden geçerli olur.
func TestTutarTasiyanUclarBirimiYaziyor(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range anlatilanUclar() {
		t.Run(uc.anahtar(), func(t *testing.T) {
			t.Parallel()

			op := islem(t, yollar, uc.metod, uc.yol)
			if !ucTutarTasiyor(t, bilesenler, op, uc) {
				return
			}

			aciklama, _ := op["description"].(string)
			assert.Contains(t, aciklama, "MINOR UNIT",
				"tutar taşıyan uç birimini söylemeli")
			assert.Contains(t, aciklama, "kuruş/cent",
				"birim istemci geliştiricisinin bildiği sözcükle yazılmalı")
		})
	}
}

// ucTutarTasiyor ucun istek ya da yanıt kaydında tutar alanı olup olmadığını
// bildirir.
func ucTutarTasiyor(t *testing.T, bilesenler, op map[string]any, uc ucBeklentisi) bool {
	t.Helper()

	if tanim, varsa := op["requestBody"].(map[string]any); varsa {
		if semaTutarTasiyor(t, bilesenler, govdeSemasi(t, tanim)) {
			return true
		}
	}

	if uc.govdesiz() || uc.ilkelOge != "" {
		return false
	}

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar[uc.durum].(map[string]any)
	require.True(t, ok)

	return semaTutarTasiyor(t, bilesenler,
		zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), uc.liste))
}

// semaTutarTasiyor şemanın doğrudan bir tutar alanı olup olmadığını bildirir.
func semaTutarTasiyor(t *testing.T, bilesenler, sema map[string]any) bool {
	t.Helper()

	for alan := range ozellikler(t, bilesenler, sema) {
		if tutarAlani(alan) {
			return true
		}
	}

	return false
}

// tutarAlani alan adının para taşıyıp taşımadığını bildirir.
func tutarAlani(ad string) bool {
	return ad == "amount" || strings.HasSuffix(ad, "_amount")
}
