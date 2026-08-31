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

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([loginRequest], [userDTO]
// …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa açmak olurdu;
// belgeyi sınamak uğruna modülün yüzeyini genişletmek, sınanan şeyin kendisini
// bozardı. Paketin ÖTEKİ testleri (yetki, çıkış) dışa açık yüzeyi sınadığı
// için api_test paketindedir; ikisi yan yana durabilir.

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

	bilesenSemalari, ok := cozulmus["components"].(map[string]any)
	require.True(t, ok)

	bilesenler, ok = bilesenSemalari["schemas"].(map[string]any)
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

	ref, refli := sema[semaRef].(string)
	if !refli {
		return sema
	}

	hedef, ok := bilesenler[strings.TrimPrefix(ref, refOneki)].(map[string]any)
	require.True(t, ok, "%q bileşeni kayıtlı olmalı", ref)

	return hedef
}

// govdeSemasi bir yanıt ya da istek gövdesi tanımından JSON şemasını çıkarır.
func govdeSemasi(t *testing.T, tanim map[string]any) map[string]any {
	t.Helper()

	sema := altHarita(tanim, govdeIcerik, govdeTur, govdeSema)
	require.NotNil(t, sema, "gövde tanımı application/json şeması taşımalı: %#v", tanim)

	return sema
}

// alanlar şemanın "properties" anahtarlarını döner.
func alanlar(t *testing.T, bilesenler, sema map[string]any) []string {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, sema)[semaOzellikler].(map[string]any)
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

// ucBeklentisi anlatılan tek bir yönetim ucunun sözleşmesidir.
type ucBeklentisi struct {
	metod string
	yol   string
	// durum başarılı yanıtın GERÇEK status kodudur; handler'ın yazdığı kodla
	// aynı olmalıdır (bkz. admin.go).
	durum string
	// istek istek gövdesinin TÜM alanlarını taşıyan örnektir; nil ise uç gövde
	// OKUMAZ.
	istek any
	// yanit başarılı yanıttaki KAYDIN tüm alanlarını taşıyan örnektir; nil ise
	// yanıtın gövdesi yoktur (204).
	yanit any
	// liste yanıtın liste zarfıyla döndüğünü bildirir.
	liste bool
	// sorgu handler'ın GERÇEKTEN okuduğu sorgu parametreleridir.
	sorgu []string
}

// anahtar işlemin "METOD yol" kimliğini döner.
func (u ucBeklentisi) anahtar() string { return u.metod + " " + u.yol }

// sayfaSorgusu sayfalanan liste uçlarının ortak parametreleridir.
var sayfaSorgusu = []string{"limit", "offset"}

// yonetimUclari anlatılan yönetim uçlarının beklentileridir.
//
// Örnekler DOLUDUR: omitempty taşıyan her alan sıfırdan farklı bir değer alır,
// çünkü karşılaştırma "şemanın properties kümesi = kodlanan anahtar kümesi"
// biçimindedir ve boş bir örnek omitempty alanları hiç yazmazdı.
func yonetimUclari() []ucBeklentisi {
	return []ucBeklentisi{
		{
			metod: http.MethodPost, yol: LoginPath, durum: "200",
			istek: loginRequest{}, yanit: loginResponse{},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/auth/me", durum: "200",
			yanit: doluKimlik(),
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/auth/logout", durum: "200",
			yanit: logoutResponse{},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/users", durum: "201",
			istek: createUserRequest{}, yanit: doluKullanici(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/users", durum: "200",
			yanit: doluKullanici(), liste: true,
			sorgu: append(sayfaSorgusu, "email", "scope"),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/users/{id}", durum: "200",
			yanit: doluKullanici(),
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/users/{id}", durum: "200",
			istek: updateUserRequest{}, yanit: doluKullanici(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/users/{id}", durum: "204",
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/users/{id}/password", durum: "204",
			istek: setPasswordRequest{},
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/api-keys", durum: "201",
			istek: createAPIKeyRequest{},
			yanit: createAPIKeyResponse{APIKey: doluAnahtar(), Key: "sk_düz"},
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/api-keys", durum: "200",
			yanit: doluAnahtar(), liste: true,
			sorgu: append(sayfaSorgusu, "type", "revoked"),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/api-keys/{id}", durum: "200",
			yanit: doluAnahtar(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/api-keys/{id}", durum: "204",
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/api-keys/{id}/revoke", durum: "200",
			yanit: doluAnahtar(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/api-keys/{id}/sales-channels",
			durum: "200", yanit: doluKanal(), liste: true,
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/api-keys/{id}/sales-channels",
			durum: "200", istek: linkChannelRequest{}, yanit: doluKanal(), liste: true,
		},
		{
			metod: http.MethodDelete,
			yol:   "/admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}",
			durum: "204",
		},
		{
			metod: http.MethodPost, yol: "/admin/v1/sales-channels", durum: "201",
			istek: salesChannelRequest{}, yanit: doluKanal(),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/sales-channels", durum: "200",
			yanit: doluKanal(), liste: true,
			sorgu: append(sayfaSorgusu, "name", "is_disabled"),
		},
		{
			metod: http.MethodGet, yol: "/admin/v1/sales-channels/{id}", durum: "200",
			yanit: doluKanal(),
		},
		{
			metod: http.MethodPut, yol: "/admin/v1/sales-channels/{id}", durum: "200",
			istek: updateSalesChannelRequest{}, yanit: doluKanal(),
		},
		{
			metod: http.MethodDelete, yol: "/admin/v1/sales-channels/{id}", durum: "204",
		},
	}
}

// doluKimlik omitempty alanı da yazılan bir kimlik kaydı üretir.
func doluKimlik() principalResponse {
	return principalResponse{SalesChannelIDs: []string{"sc_1"}}
}

// doluKullanici omitempty alanı da yazılan bir kullanıcı kaydı üretir.
func doluKullanici() userDTO {
	return userDTO{Metadata: map[string]any{"k": "v"}}
}

// doluAnahtar omitempty alanları da yazılan bir anahtar kaydı üretir.
func doluAnahtar() apiKeyDTO {
	an := time.Now().UTC()

	return apiKeyDTO{
		LastUsedAt: &an,
		RevokedAt:  &an,
		RevokedBy:  "usr_1",
	}
}

// doluKanal omitempty alanı da yazılan bir satış kanalı kaydı üretir.
func doluKanal() salesChannelDTO {
	return salesChannelDTO{Metadata: map[string]any{"k": "v"}}
}

// TestYonetimUclariGovdeleriniAnlatir her yönetim ucunun ne ALDIĞINI ve ne
// DÖNDÜĞÜNÜ söylediğini doğrular.
//
// Bulgunun tam karşılığı budur: gövdesiz bir şema istemciye "bu uç var ve
// şöyle başarısız olabilir" der, ne göndereceğini söylemez; istemci üreteci de
// gövdesi olmayan, dönüş tipi 'void' olan bir metot üretir — POST
// /admin/v1/users için bu, o istemciyle kullanıcı OLUŞTURULAMAMASI demektir.
//
// Alan kümeleri DTO'nun encoding/json çıktısıyla karşılaştırılır, elle yazılmış
// bir listeyle değil: elle yazılmış liste, DTO'ya alan eklendiği gün eksik
// kalır ve test bunu görmezdi.
func TestYonetimUclariGovdeleriniAnlatir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range yonetimUclari() {
		t.Run(uc.anahtar(), func(t *testing.T) {
			t.Parallel()

			op := islem(t, yollar, uc.metod, uc.yol)
			assert.NotEmpty(t, op["summary"], "özetsiz bir işlem istemcide adsız bir metot olur")

			istekTanimi, govdeVar := op["requestBody"].(map[string]any)
			require.Equal(t, uc.istek != nil, govdeVar,
				"gövde OKUYAN uçta requestBody olmalı, okumayanda olmamalı")

			if uc.istek != nil {
				assert.Equal(t, true, istekTanimi["required"], "yazma ucunun gövdesi zorunludur")
				assert.ElementsMatch(t, jsonAnahtarlari(t, uc.istek),
					alanlar(t, bilesenler, govdeSemasi(t, istekTanimi)),
					"istek gövdesinin alanları DTO ile örtüşmeli")
			}

			yanitlar, ok := op["responses"].(map[string]any)
			require.True(t, ok)

			tanim, ok := yanitlar[uc.durum].(map[string]any)
			require.True(t, ok, "handler'ın GERÇEKTEN yazdığı kod belgelenmeli: %s", uc.durum)
			assert.NotEmpty(t, tanim["description"], "yanıt açıklama taşımalı")

			if uc.yanit == nil {
				assert.NotContains(t, tanim, govdeIcerik,
					"204'ün gövdesi yoktur; şema gövde vaat etmemeli")

				return
			}

			zarf := govdeSemasi(t, tanim)
			if uc.liste {
				assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"},
					alanlar(t, bilesenler, zarf), "liste zarfı plan Bölüm 8'deki biçimdir")
			} else {
				assert.ElementsMatch(t, []string{"data"}, alanlar(t, bilesenler, zarf),
					"tekil yanıtlar {\"data\": …} zarfıyla döner")
			}

			kayit := zarfKaydi(t, bilesenler, zarf, uc.liste)
			assert.ElementsMatch(t, jsonAnahtarlari(t, uc.yanit), alanlar(t, bilesenler, kayit),
				"yanıt kaydının alanları DTO ile örtüşmeli")
			assert.ElementsMatch(t, jsonAnahtarlari(t, sifirDegeri(uc.yanit)),
				zorunlular(t, bilesenler, kayit),
				"required, encoding/json'un HER ZAMAN yazdığı anahtarlarla aynı olmalı")
		})
	}
}

// zarfKaydi zarfın taşıdığı KAYIT şemasını döner.
//
// Liste zarfında kayıt "data"nın İÇİNDEDİR; zarfa bakıp durmak, gövdesi
// bilinmeyen bir listeyi anlatılmış saymak olurdu.
func zarfKaydi(t *testing.T, bilesenler, zarf map[string]any, liste bool) map[string]any {
	t.Helper()

	ozellikler, ok := semaCoz(t, bilesenler, zarf)[semaOzellikler].(map[string]any)
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

// anlatilmayanUclar bilerek anlatılmayan uçlardır.
//
// BOŞTUR ve öyle kalmalıdır. Bir zamanlar POST /admin/v1/sales-channels
// buradaydı: gövdesi "SalesChannelRequest" bileşenini isterdi ve AYNI adı
// product modülünün bir tipi de istiyordu; iki farklı tip aynı adı istediğinde
// belgenin TAMAMI üretilemez hâle gelir. Çakışma, product tarafındaki tipi
// gerçekte ne olduğuna göre adlandırarak çözüldü (linkSalesChannelRequest).
//
// Liste yine de duruyor çünkü bir GÜVENLİK AĞIDIR: ileride bir uç bilerek
// anlatılmadan bırakılırsa gerekçesi burada yazılı olmak zorunda kalır.
// Yazılmamış bir eksiklik, bilinmeyen bir eksikliktir.
var anlatilmayanUclar = []string{}

// TestYonetimUclarininTumuAnlatildi anlatılmamış bir yönetim ucu kalmadığını
// doğrular.
//
// Yeni bir uç eklenip anlatılmadığında bu test düşer. Uyarı olmasaydı arıza
// SESSİZ olurdu: uç belgede yolu ve güvenliğiyle görünür, yalnızca gövdesi
// olmaz — yani şema "var ama ne aldığı bilinmiyor" der ve kimse fark etmez.
func TestYonetimUclarininTumuAnlatildi(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	var anlatilan, anlatilmayan []string

	for yol, islemler := range yollar {
		islemHaritasi, ok := islemler.(map[string]any)
		require.True(t, ok, "yol girdisi metot haritası olmalı")

		for metod, ham := range islemHaritasi {
			op, ok := ham.(map[string]any)
			require.True(t, ok)

			anahtar := strings.ToUpper(metod) + " " + yol

			if op["summary"] == nil {
				// Anlatılmamış uç GEÇERLİ bir modeldir ama gövdesi de
				// OLMAMALIDIR: özetsiz ama gövdeli bir işlem, yarım kalmış bir
				// anlatım demek olurdu.
				assert.NotContains(t, op, "requestBody", "%s anlatılmadı", anahtar)

				anlatilmayan = append(anlatilmayan, anahtar)

				continue
			}

			anlatilan = append(anlatilan, anahtar)
		}
	}

	beklenen := make([]string, 0, len(yonetimUclari()))
	for _, uc := range yonetimUclari() {
		beklenen = append(beklenen, uc.anahtar())
	}

	assert.ElementsMatch(t, beklenen, anlatilan,
		"tabloda olmayan bir yönetim ucu sınanmamış demektir")
	assert.ElementsMatch(t, anlatilmayanUclar, anlatilmayan,
		"anlatılmayan uç kümesi YAZILI olmalı; sessiz bir eksik, eksik olduğu bilinmeyen eksiktir")
}

// TestYonetimUclariYalnizcaOkunanSorgulariAnlatir şemanın okunmayan bir
// parametre duyurmadığını doğrular.
//
// Okunmayan bir parametreyi şemaya koymak, istemciye ÇALIŞMAYAN bir özellik
// vaat etmektir: üreteç metoda argüman koyar, çağıran doldurur, sunucu
// sessizce yok sayar.
func TestYonetimUclariYalnizcaOkunanSorgulariAnlatir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	for _, uc := range yonetimUclari() {
		op := islem(t, yollar, uc.metod, uc.yol)
		assert.ElementsMatch(t, uc.sorgu, parametreAdlari(t, op, "query"),
			"%s parametreleri handler'ın okuduklarıyla aynı olmalı", uc.anahtar())
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

// TestParolaAlanlariMaskeliBildirilir parolanın istek şemasında parola OLARAK
// göründüğünü doğrular.
//
// İşaretsiz bir parola, e-posta ile aynı görünen sıradan bir dizedir: istemci
// üreteci onu düz metin alanı yapar, şema görüntüleyici ekrana açık yazar ve
// örnek istek üreten araç değeri kaydeder. Test ayrıca [parolaliGovde]'nin
// bileşene yazmasını kilitler — çekirdek bir gün derin kopya dönerse işaret
// sessizce kaybolmaz, burada düşer.
func TestParolaAlanlariMaskeliBildirilir(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	parolaliUclar := []struct{ metod, yol string }{
		{http.MethodPost, LoginPath},
		{http.MethodPost, "/admin/v1/users"},
		{http.MethodPost, "/admin/v1/users/{id}/password"},
	}

	for _, uc := range parolaliUclar {
		op := islem(t, yollar, uc.metod, uc.yol)

		govde, ok := op["requestBody"].(map[string]any)
		require.True(t, ok, "%s %s gövde almalı", uc.metod, uc.yol)

		ozellikler, ok := semaCoz(t, bilesenler,
			govdeSemasi(t, govde))[semaOzellikler].(map[string]any)
		require.True(t, ok)

		parola, ok := ozellikler[alanParola].(map[string]any)
		require.True(t, ok, "%s %s parola alanı taşımalı", uc.metod, uc.yol)

		assert.Equal(t, tipDize, parola[semaTip], "parola tel üzerinde dizedir")
		assert.Equal(t, bicimParola, parola[semaBicim],
			"parola alanı format: %q ile işaretlenmeli", bicimParola)
	}
}

// TestYanitlarParolaTasimaz hiçbir başarılı yanıtta parola alanı
// BULUNMADIĞINI doğrular.
//
// İddia yanıt DTO'larının bugünkü hâline değil, ŞEMANIN TAMAMINA bakar:
// yanıttan ulaşılabilen her bileşen taranır. Yanıt gövdesi bir gün istek
// tipini yeniden kullansa (ya da bir DTO'ya parola alanı eklense) sızıntı
// belgede görünür hâle gelirdi — ve belgede görünen bir alan, istemci
// üretecinin okumaya çalışacağı bir alandır.
func TestYanitlarParolaTasimaz(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)

	for _, uc := range yonetimUclari() {
		if uc.yanit == nil {
			continue
		}

		op := islem(t, yollar, uc.metod, uc.yol)

		yanitlar, ok := op["responses"].(map[string]any)
		require.True(t, ok)

		tanim, ok := yanitlar[uc.durum].(map[string]any)
		require.True(t, ok)

		ulasilan := ulasilanAlanlar(t, bilesenler, govdeSemasi(t, tanim), map[string]struct{}{})
		assert.NotContains(t, ulasilan, alanParola,
			"%s yanıtı parola taşımamalı", uc.anahtar())
	}
}

// ulasilanAlanlar şemadan ulaşılabilen TÜM özellik adlarını toplar.
//
// gorulen, aynı bileşene ikinci kez inmeyi engeller; şemalar kendine referans
// verebilir ve döngü testi sonsuza kadar döndürürdü.
func ulasilanAlanlar(t *testing.T, bilesenler, sema map[string]any,
	gorulen map[string]struct{},
) []string {
	t.Helper()

	if ref, refli := sema[semaRef].(string); refli {
		if _, tekrar := gorulen[ref]; tekrar {
			return nil
		}

		gorulen[ref] = struct{}{}
	}

	cozulmusSema := semaCoz(t, bilesenler, sema)

	var adlar []string

	if ozellikler, ok := cozulmusSema[semaOzellikler].(map[string]any); ok {
		for ad, ham := range ozellikler {
			adlar = append(adlar, ad)

			if alt, nesne := ham.(map[string]any); nesne {
				adlar = append(adlar, ulasilanAlanlar(t, bilesenler, alt, gorulen)...)
			}
		}
	}

	if oge, ok := cozulmusSema["items"].(map[string]any); ok {
		adlar = append(adlar, ulasilanAlanlar(t, bilesenler, oge, gorulen)...)
	}

	return adlar
}

// TestDuzAnahtarinBirKezDondugunuSemaSoyler düz anahtarın ömrünün şemada
// yazılı olduğunu doğrular.
//
// Şema alanın VARLIĞINI anlatır ama tek seferlik olduğunu anlatamaz: "key"
// sıradan bir dizedir ve istemci onu her çağrıda okuyabileceğini sanır.
// Bilginin tek yeri açıklamadır; olmasaydı istemci geliştiricisi anahtarı
// saklamaz ve değeri kaybederdi.
func TestDuzAnahtarinBirKezDondugunuSemaSoyler(t *testing.T) {
	t.Parallel()

	yollar, bilesenler := belge(t)
	op := islem(t, yollar, http.MethodPost, "/admin/v1/api-keys")

	ozet, _ := op["summary"].(string)
	assert.Contains(t, ozet, "BİR KEZ", "özet anahtarın bir kez döndüğünü söylemeli")

	aciklama, _ := op["description"].(string)
	assert.Contains(t, aciklama, "bir daha hiçbir uçtan okunamaz",
		"açıklama düz anahtarın geri okunamayacağını söylemeli")

	yanitlar, ok := op["responses"].(map[string]any)
	require.True(t, ok)

	tanim, ok := yanitlar["201"].(map[string]any)
	require.True(t, ok)

	yanitAciklamasi, _ := tanim["description"].(string)
	assert.Contains(t, yanitAciklamasi, "bir daha dönmez",
		"yanıt açıklaması düz metnin tek seferlik olduğunu söylemeli")

	// Düz metin YALNIZCA oluşturma yanıtındadır; okuma uçlarının kaydında
	// maskelenmiş gösterim vardır.
	kayit := zarfKaydi(t, bilesenler, govdeSemasi(t, tanim), false)
	assert.ElementsMatch(t, []string{"api_key", "key"}, alanlar(t, bilesenler, kayit))

	okuma := islem(t, yollar, http.MethodGet, "/admin/v1/api-keys/{id}")

	okumaYanitlari, ok := okuma["responses"].(map[string]any)
	require.True(t, ok)

	okumaTanimi, ok := okumaYanitlari["200"].(map[string]any)
	require.True(t, ok)

	okumaAlanlari := alanlar(t, bilesenler,
		zarfKaydi(t, bilesenler, govdeSemasi(t, okumaTanimi), false))
	assert.NotContains(t, okumaAlanlari, "key", "okuma ucu düz anahtar vaat etmemeli")
	assert.Contains(t, okumaAlanlari, "redacted")
}

// TestGirisUcuSemadaKorumasizKalir jetonu veren ucun jeton istemediğini
// doğrular.
//
// Ayrım incedir ve bedeli büyüktür: alan HİÇ YAZILMASAYDI işlem
// "belirtilmemiş" sayılır ve kök seviyedeki varsayılan güvenliği MİRAS
// ALIRDI; istemci üreteci de giriş için jeton isteyen, yani hiç çağrılamayan
// bir metot üretirdi. Kararı çekirdek verir ([openapi.Doc] giriş yolunu
// tanır); buradaki iddia, anlatımın onu EZMEDİĞİDİR.
func TestGirisUcuSemadaKorumasizKalir(t *testing.T) {
	t.Parallel()

	yollar, _ := belge(t)

	giris := islem(t, yollar, http.MethodPost, LoginPath)

	guvenlik, yazilmis := giris["security"]
	require.True(t, yazilmis,
		"giriş ucunda security alanı YAZILMIŞ olmalı; yazılmamış bir alan kök varsayılanını miras alır")
	require.NotNil(t, guvenlik, "security null olamaz; null 'belirtilmemiş' ile aynı kapıya çıkar")

	dizi, ok := guvenlik.([]any)
	require.True(t, ok, "security bir dizi olmalı, %T bulundu", guvenlik)
	assert.Empty(t, dizi, "boş dizi 'bu uç açıkça korumasız' demektir")

	// Boş dizinin bir anlamı olması için dolusunun da görülmesi gerekir.
	korumali := islem(t, yollar, http.MethodGet, "/admin/v1/users")
	assert.Equal(t, []any{map[string]any{"bearerAuth": []any{}}}, korumali["security"],
		"öteki yönetim uçları oturum jetonu istemeli")
}
