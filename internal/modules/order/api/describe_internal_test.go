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

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([createReturnRequest],
// [returnDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
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
	// aynı olmalıdır (bkz. admin.go).
	durum string
	// istek istek gövdesinin TÜM alanlarını taşıyan örnektir; nil ise uç gövde
	// almaz.
	istek any
	// yanit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir.
	yanit any
	// liste yanıtın LİSTE zarfıyla döndüğünü bildirir; tekil zarftan farkı
	// sayfalama alanlarıdır ve ikisini karıştırmak istemci üretecinde yanlış
	// dönüş tipi demektir.
	liste bool
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// anlatilanUclar anlatılan uçların beklentileridir.
//
// Örnekler DOLUDUR: omitempty taşıyan her alan sıfırdan farklı bir değer alır,
// çünkü karşılaştırma "şemanın properties kümesi = kodlanan anahtar kümesi"
// biçimindedir ve boş bir örnek omitempty alanları hiç yazmazdı.
//
// [orderDetailDTO] döndüren beş uç burada YOKTUR ve olmaması bilinçlidir:
// gerekçe [Describe] belgesindedir ("LineItem" bileşen adı cart ile çakışıyor).
func anlatilanUclar() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodGet, yol: "/admin/v1/orders", durum: "200",
			yanit: doluSiparis(), liste: true,
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/orders/{id}/returns", durum: "200",
			yanit: doluIade(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/orders/{id}/returns", durum: "201",
			istek: createReturnRequest{}, yanit: doluIade(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/orders/{id}/returns/{returnId}",
			durum: "200", yanit: doluIade(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/orders/{id}/exchanges", durum: "200",
			yanit: doluDegisim(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/orders/{id}/exchanges", durum: "201",
			istek: createExchangeRequest{}, yanit: doluDegisim(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/orders/{id}/exchanges/{exchangeId}",
			durum: "200", yanit: doluDegisim(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/orders/{id}/claims", durum: "200",
			yanit: doluHasar(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/orders/{id}/claims", durum: "201",
			istek: createClaimRequest{}, yanit: doluHasar(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/orders/{id}/claims/{claimId}",
			durum: "200", yanit: doluHasar(),
		},
	}
}

// anlatilmayanUclar çakışma yüzünden gövdesi anlatılmayan uçlardır.
var anlatilmayanUclar = []string{
	http.MethodGet + " /store/v1/orders/{id}",
	http.MethodGet + " /admin/v1/orders/{id}",
	http.MethodPost + " /admin/v1/orders/{id}/cancel",
	http.MethodPost + " /admin/v1/orders/{id}/complete",
	http.MethodPost + " /admin/v1/orders/{id}/archive",
}

// doluSiparis omitempty alanları da yazılan bir sipariş kaydı üretir.
func doluSiparis() orderDTO {
	an := time.Now().UTC()

	return orderDTO{
		CustomerID:   "cus_1",
		Email:        "a@b.c",
		CartID:       "cart_1",
		Metadata:     map[string]any{"k": "v"},
		CompletedAt:  &an,
		CanceledAt:   &an,
		CancelReason: "müşteri vazgeçti",
	}
}

// doluIade omitempty alanları da yazılan bir iade kaydı üretir.
func doluIade() returnDTO {
	an := time.Now().UTC()

	return returnDTO{
		Reason:     "hasarlı",
		Note:       "kargo teslim etti",
		Metadata:   map[string]any{"k": "v"},
		ReceivedAt: &an,
		CanceledAt: &an,
	}
}

// doluDegisim omitempty alanları da yazılan bir değişim kaydı üretir.
func doluDegisim() exchangeDTO {
	an := time.Now().UTC()

	return exchangeDTO{
		Note:        "beden değişimi",
		Metadata:    map[string]any{"k": "v"},
		CompletedAt: &an,
		CanceledAt:  &an,
	}
}

// doluHasar omitempty alanları da yazılan bir hasar kaydı üretir.
func doluHasar() claimDTO {
	an := time.Now().UTC()

	return claimDTO{
		Reason:      "eksik ürün",
		Note:        "kutu açıktı",
		Metadata:    map[string]any{"k": "v"},
		CompletedAt: &an,
		CanceledAt:  &an,
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
			assert.NotEmpty(t, op["summary"], "her anlatılan uç bir özet taşımalı")

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

			kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), uc.liste)
			assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
				"yanıt kaydının alanları DTO ile örtüşmeli")
			assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
				zorunlular(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
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

	ozellikler, ok := semaCoz(t, bilesenler, zarf)["properties"].(map[string]any)
	require.True(t, ok)

	kayit, ok := ozellikler[zarfVeriAlani].(map[string]any)
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

// TestSatirTasiyanUclarBelgedeGovdesiz anlatılmayan beş ucun belgeden
// DÜŞMEDİĞİNİ, yalnızca gövdesiz kaldığını doğrular.
//
// Eksikliğin kendisi sınanır çünkü eksiklik BİLİNÇLİDİR: [orderDetailDTO]
// satırlarını [lineItemDTO] ile taşır ve o tipin isteyeceği "LineItem" bileşen
// adı cart modülünde zaten kayıtlıdır; anlatmak belgenin TAMAMINI üretilemez
// kılardı (bkz. [Describe]). Uçlar yine de yolu, metodu ve güvenliğiyle
// görünür — istemci onların var olduğunu bilir, yalnızca şeklini bilmez.
//
// Çakışma bir gün tiplerden biri yeniden adlandırılarak çözülürse bu test
// düşer; o zaman yapılacak şey uçları [anlatilanUclar] tablosuna taşıyıp bu
// testi KALDIRMAKTIR.
func TestSatirTasiyanUclarBelgedeGovdesiz(t *testing.T) {
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
