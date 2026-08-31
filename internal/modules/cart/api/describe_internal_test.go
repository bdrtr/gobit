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

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([createCartRequest],
// [cartDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
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

// ucBeklentisi anlatılan tek bir vitrin ucunun sözleşmesidir.
type ucBeklentisi struct {
	metod string
	yol   string
	// durum başarılı yanıtın GERÇEK status kodudur; handler'ın yazdığı kodla
	// aynı olmalıdır (bkz. store.go).
	durum string
	// istek istek gövdesinin TÜM alanlarını taşıyan örnektir; nil ise uç gövde
	// almaz.
	istek any
	// yanit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir; nil ise
	// yanıtın gövdesi yoktur (204).
	yanit any
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// vitrinUclari anlatılan vitrin uçlarının beklentileridir.
//
// Örnekler DOLUDUR: omitempty taşıyan her alan sıfırdan farklı bir değer alır,
// çünkü karşılaştırma "şemanın properties kümesi = kodlanan anahtar kümesi"
// biçimindedir ve boş bir örnek omitempty alanları hiç yazmazdı.
func vitrinUclari() []ucBeklentisi {
	an := time.Now().UTC()
	adres := addressDTO{}

	return []ucBeklentisi{
		{
			metod: http.MethodPost, yol: "/store/v1/carts", durum: "201",
			istek: createCartRequest{}, yanit: doluCart(an),
		},
		{
			metod: http.MethodGet, yol: "/store/v1/carts/{id}", durum: "200",
			yanit: cartDetailDTO{
				cartDTO:         doluCart(an),
				ShippingAddress: &adres,
				BillingAddress:  &adres,
			},
		},
		{
			metod: http.MethodPost, yol: "/store/v1/carts/{id}", durum: "200",
			istek: updateCartRequest{}, yanit: doluCart(an),
		},
		{
			metod: http.MethodDelete, yol: "/store/v1/carts/{id}", durum: "204",
		},
		{
			metod: http.MethodPost, yol: "/store/v1/carts/{id}/line-items", durum: "201",
			istek: addLineItemRequest{}, yanit: doluSatir(),
		},
		{
			metod: http.MethodPatch, yol: "/store/v1/carts/{id}/line-items/{line_item_id}",
			durum: "200",
			istek: updateLineItemRequest{}, yanit: doluSatir(),
		},
		{
			metod: http.MethodDelete, yol: "/store/v1/carts/{id}/line-items/{line_item_id}",
			durum: "204",
		},
		{
			metod: http.MethodPut, yol: "/store/v1/carts/{id}/shipping-address", durum: "200",
			istek: addressRequest{}, yanit: doluAdres(),
		},
		{
			metod: http.MethodPut, yol: "/store/v1/carts/{id}/billing-address", durum: "200",
			istek: addressRequest{}, yanit: doluAdres(),
		},
		{
			metod: http.MethodPost, yol: "/store/v1/carts/{id}/shipping-methods", durum: "201",
			istek: addShippingMethodRequest{}, yanit: doluYontem(),
		},
		{
			metod: http.MethodDelete,
			yol:   "/store/v1/carts/{id}/shipping-methods/{shipping_method_id}",
			durum: "204",
		},
	}
}

// doluCart omitempty alanları da yazılan bir sepet kaydı üretir.
func doluCart(an time.Time) cartDTO {
	return cartDTO{
		CustomerID:  "cus_1",
		Email:       "a@b.c",
		Metadata:    map[string]any{"k": "v"},
		CompletedAt: &an,
	}
}

// doluSatir omitempty alanları da yazılan bir satır kaydı üretir.
func doluSatir() lineItemDTO {
	return lineItemDTO{Metadata: map[string]any{"k": "v"}}
}

// doluAdres omitempty alanları da yazılan bir adres kaydı üretir.
func doluAdres() addressDTO {
	return addressDTO{
		SourceAddressID: "addr_1",
		FirstName:       "A",
		LastName:        "B",
		Company:         "C",
		Address1:        "D",
		Address2:        "E",
		City:            "F",
		Province:        "G",
		PostalCode:      "H",
		CountryCode:     "TR",
		Phone:           "I",
		Metadata:        map[string]any{"k": "v"},
	}
}

// doluYontem omitempty alanları da yazılan bir kargo yöntemi üretir.
func doluYontem() shippingMethodDTO {
	return shippingMethodDTO{ShippingOptionID: "so_1", Data: map[string]any{"k": "v"}}
}

// TestVitrinUclariGovdeleriniAnlatir her vitrin ucunun ne ALDIĞINI ve ne
// DÖNDÜĞÜNÜ söylediğini doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve
// şöyle başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de
// her şeyi 'any' olan, dönüş tipi 'void' olan bir metot üretir.
//
// Alan kümeleri DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, DTO'ya alan eklendiği gün eksik
// kalır ve test bunu görmezdi.
func TestVitrinUclariGovdeleriniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range vitrinUclari() {
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

			yanitlar, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			tanim, ok := yanitlar[uc.durum].(map[string]any)
			require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli: %s", uc.durum)

			if uc.yanit == nil {
				assert.NotContains(t, tanim, "content",
					"204'ün gövdesi yoktur; şema gövde vaat etmemeli")

				return
			}

			zarf := govdeSemasi(t, tanim)
			assert.ElementsMatch(t, []string{"data"}, alanlar(t, bilesenler, zarf),
				"tekil yanıtlar {\"data\": …} zarfıyla döner")

			kayit := zarfKaydi(t, bilesenler, zarf)
			assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
				"yanıt kaydının alanları DTO ile örtüşmeli")
			assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
				zorunlular(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
}

// zarfKaydi tekil zarfın "data" alanının şemasını döner.
func zarfKaydi(t *testing.T, bilesenler, zarf map[string]any) map[string]any {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, zarf)["properties"].(map[string]any)
	require.True(t, ok)

	kayit, ok := ozellikler["data"].(map[string]any)
	require.True(t, ok)

	return kayit
}

// TestVitrinUclarininTumuAnlatildi anlatılmamış bir vitrin ucu kalmadığını
// doğrular.
//
// Yeni bir uç eklenip anlatılmadığında bu test düşer. Uyarı olmasaydı arıza
// SESSİZ olurdu: uç belgede yolu ve güvenliğiyle görünür, yalnızca gövdesi
// olmaz — yani şema "var ama ne aldığı bilinmiyor" der ve kimse fark etmez.
func TestVitrinUclarininTumuAnlatildi(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

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

	beklenen := make([]string, 0, len(vitrinUclari()))
	for _, uc := range vitrinUclari() {
		beklenen = append(beklenen, uc.anahtar())
	}

	assert.ElementsMatch(t, beklenen, bulunan,
		"tabloda olmayan bir vitrin ucu sınanmamış demektir")
}

// TestVitrinUclariSorguParametresiVaatEtmez şemanın okunmayan bir parametre
// duyurmadığını doğrular.
//
// Vitrin sepeti handler'larının hiçbiri sorgu dizesini okumaz (bkz. store.go).
// Şemaya yine de bir parametre yazmak, istemci üretecinin metoda argüman
// koyması ve çağıranın onu doldurup sunucunun sessizce yok sayması demekti.
func TestVitrinUclariSorguParametresiVaatEtmez(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, uc := range vitrinUclari() {
		op := islem(t, yollar, uc.metod, uc.yol)

		params, _ := op["parameters"].([]any)
		for _, ham := range params {
			p, ok := ham.(map[string]any)
			require.True(t, ok)

			assert.Equal(t, "path", p["in"],
				"%s yalnızca yol parametresi taşımalı; %q sorgu parametresi okunmuyor",
				uc.anahtar(), p["name"])
		}
	}
}
