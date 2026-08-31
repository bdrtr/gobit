//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// Bu dosya /openapi.json ucunun ÇALIŞAN sunucuda ne sunduğunu denetler.
//
// # Neden birim testleri yetmiyor
//
// Çekirdeğin şema üreteci de, modüllerin anlatımı da kendi paketlerinde
// sınanır; ikisi de yeşilken belgenin GÖVDESİZ sunulması hâlâ mümkündür,
// çünkü anlatım kancasını işleten yer kurulumun kendisidir. Kanca
// bağlanmazsa hiçbir birim testi kırılmaz: modüller anlatımını yapar,
// üretici çalışır, sunucu yine "bu uç var, kimlik ister, şöyle başarısız
// olabilir" diyen boş bir şema yayımlar. Buradaki testler tam olarak o
// boşluğu kapatır — belge, isteklerin gerçekten geçtiği router'dan ve
// modüllerin gerçekten kurulmuş hâlinden üretilir.
//
// # Hangi küme denetleniyor
//
// [TestSemaAnlatilanUclariGovdeleriyleAnlatir] YALNIZCA ANLATILMIŞ uçları
// denetler ve kümeyi elle yazmaz, [anlatilanUclar] ile modüllerden okur.
// Ayrım bilinçlidir: bugün yalnızca /store/v1 yüzeyi anlatılmıştır ve
// anlatılmamış bir uç GEÇERLİ bir modeldir — belgede yolu, metodu ve
// güvenliğiyle görünür, yalnızca gövdesi olmaz. "Her uçta gövde olmalı"
// demek, bugün kırmızı duran ve kimsenin düzeltemeyeceği bir test üretirdi;
// anlatım genişledikçe denetlenen küme kendiliğinden büyür.
//
// Kök anahtarlar, $ref bütünlüğü, giriş ucunun güvenliği ve ham kimlik
// sızıntısı ise BELGENİN TAMAMINDA denetlenir; onlar anlatımdan bağımsızdır.

// refOneki bileşen şemalarına yapılan atıfların yol önekidir.
const refOneki = "#/components/schemas/"

// semaRefAnahtari JSON Schema'nın atıf anahtarıdır.
const semaRefAnahtari = "$ref"

// semaYolu OpenAPI belgesini sunan uçtur.
//
// Uç bu sabitle BAĞLANIR (bkz. zeminiKur); testin kendi kopyasını tutması,
// yol değiştiğinde 404 alıp "şema üretilemedi" diye rapor etmesi demekti.
const semaYolu = "/openapi.json"

// govdeliMetotlar istek GÖVDESİ taşıması beklenen yazma metotlarıdır.
//
// DELETE bilinçli olarak DIŞARIDADIR: o da bir yazma metodudur ama kaynağını
// yolundan seçer, gövde okumaz. Listeye alınsaydı test, sunucunun hiç
// okumadığı bir gövdenin belgelenmesini zorunlu kılardı — yani şemanın
// yalan söylemesini isterdi.
var govdeliMetotlar = map[string]struct{}{
	http.MethodPost:  {},
	http.MethodPut:   {},
	http.MethodPatch: {},
}

// kayitKimligiRe önekli bir kayıt kimliğini yakalar (örn. "cart_01H…").
//
// Desen kimlik ÜRETİCİSİNİN biçimidir: küçük harfli önek + Crockford Base32
// alfabesiyle 26 karakter (bkz. modüllerin models/ids.go dosyaları). Tek tek
// önek listelemek yerine biçmin kendisi aranır; yeni bir modülün yeni bir
// öneki listeye eklenmeyi bekleyemez.
var kayitKimligiRe = regexp.MustCompile(`[a-z][a-z0-9]*_[0-9A-HJKMNP-TV-Z]{26}`)

// belgeyiAnlat OpenAPI belgesini kurar ve kendini anlatabilen modüllere işletir.
//
// cmd/server'daki aynı adlı fonksiyonun ikizidir ve tekrar bilinçlidir:
// [openapi.Describer] OPSİYONEL bir arayüzdür, tip iddiası kompozisyon
// kökünde yapılır ve çekirdek modülleri tanımaz (Prensip 2.4). e2e'nin
// kompozisyon kökü TestMain'dir; kancayı burada da işletmek üretimdeki
// kurulumun AYNISINI kurmanın tek yoludur. Modül listesi de zaten aynı
// gerekçeyle tekrarlanıyor (bkz. TestMain'deki kayit.Add çağrıları).
func belgeyiAnlat(baslik, surum string, moduller []module.Module) *openapi.Doc {
	doc := openapi.New(baslik, surum)

	for _, mod := range moduller {
		anlatici, anlatabilir := mod.(openapi.Describer)
		if !anlatabilir {
			continue
		}

		anlatici.Describe(doc)
	}

	return doc
}

// anlatilanUclar modüllerin anlattığı "METOD /yol" kayıtlarının tamamını döner.
//
// # Neden boş bir router'a karşı üretiliyor
//
// [openapi.Doc] anlatım haritasını dışa açmaz; kümeyi okumanın tek yolu
// [openapi.Doc.UnmatchedDescriptions] fonksiyonudur ve o, hiçbir route ile
// eşleşmeyenleri döner. BOŞ bir router'a karşı üretildiğinde hiçbir açıklama
// eşleşmez, dolayısıyla dönen liste anlatılanların TAMAMIDIR.
//
// Alternatif — kümeyi bu dosyaya elle yazmak — testi kör ederdi: elle
// yazılmış liste, yeni anlatılan ucu sessizce kapsam dışı bırakır ve gövdesi
// eksik kalan uç yeşil testin arkasında saklanırdı.
func anlatilanUclar(t *testing.T) []string {
	t.Helper()

	sonda := belgeyiAnlat("sonda", "e2e", testModuller)

	_, err := sonda.Build(chi.NewRouter())
	require.NoError(t, err, "sonda belgesi üretilebilmeli")

	uclar := sonda.UnmatchedDescriptions()
	require.NotEmpty(t, uclar,
		"ön koşul: en az bir uç anlatılmış olmalı; hiçbiri yoksa bu dosyadaki gövde iddiaları BOŞA denetlenir")

	return uclar
}

// semaBelgesi /openapi.json ucunu çağırır; ham gövdeyi ve çözülmüş belgeyi döner.
//
// Ham gövde de dönülür ve gerekçesi tekniktir: "alan hiç yazılmamış" ile
// "null yazılmış" ayrımı ve belgenin İÇİNDE ham kayıt kimliği aranması ancak
// metin üzerinde yapılabilir.
func semaBelgesi(t *testing.T) ([]byte, map[string]any) {
	t.Helper()

	istek := httptest.NewRequest(http.MethodGet, semaYolu, http.NoBody)
	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	require.Equal(t, http.StatusOK, kayit.Code,
		"şema ucu 200 dönmeli; gövde: %s", kayit.Body.String())

	ham := kayit.Body.Bytes()

	var belge map[string]any
	require.NoError(t, json.Unmarshal(ham, &belge),
		"şema çözülemedi; gövde: %s", string(ham))

	return ham, belge
}

// altNesne bir haritadan nesne alanı okur; yoksa testi durdurur.
func altNesne(t *testing.T, kaynak map[string]any, alan, nerede string) map[string]any {
	t.Helper()

	deger, bulundu := kaynak[alan]
	require.True(t, bulundu, "%s içinde %q alanı olmalı; olanlar: %v", nerede, alan, anahtarlar(kaynak))

	nesne, uygun := deger.(map[string]any)
	require.True(t, uygun, "%s içindeki %q bir nesne olmalı, %T bulundu", nerede, alan, deger)

	return nesne
}

// islemBul belgedeki bir yol+metod işlemini döner.
func islemBul(t *testing.T, belge map[string]any, metot, desen string) map[string]any {
	t.Helper()

	yollar := altNesne(t, belge, "paths", "belge")
	yol := altNesne(t, yollar, desen, "paths")

	return altNesne(t, yol, strings.ToLower(metot), desen)
}

// jsonSemasi bir istek/yanıt tanımından application/json şemasını çıkarır.
func jsonSemasi(t *testing.T, tanim map[string]any, nerede string) map[string]any {
	t.Helper()

	icerik := altNesne(t, tanim, "content", nerede)
	tur := altNesne(t, icerik, "application/json", nerede+".content")

	return altNesne(t, tur, "schema", nerede+".content.application/json")
}

// cozulmus $ref taşıyan bir şemayı bileşen tanımına kadar izler.
//
// Zincir izlenmeden alan sayılamaz: türetici adlandırılmış her struct için
// ref üretir ([openapi.Doc.SchemaOf]) ve ref'e bakıp "özelliği yok" demek,
// dolu bir şemayı boş sanmak olurdu — yani testin yakalaması gereken arızayı
// testin kendisi üretirdi.
func cozulmus(t *testing.T, belge, sema map[string]any) map[string]any {
	t.Helper()

	semalar := semaBilesenleri(t, belge)

	// Adım sınırı bir güvenlik ağıdır: kendine dönen bir ref zinciri testi
	// sonsuza kadar döndürmek yerine burada durdurmalıdır.
	for adim := 0; adim < 32; adim++ {
		ham, refli := sema[semaRefAnahtari]
		if !refli {
			return sema
		}

		yol, dize := ham.(string)
		require.True(t, dize, "$ref bir dize olmalı, %T bulundu", ham)

		ad := strings.TrimPrefix(yol, refOneki)
		require.NotEqual(t, yol, ad, "$ref yalnızca %s ile başlayabilir: %s", refOneki, yol)

		sema = altNesne(t, semalar, ad, "components/schemas")
	}

	t.Fatalf("$ref zinciri 32 adımda çözülmedi")

	return nil
}

// semaBilesenleri components/schemas haritasını döner.
func semaBilesenleri(t *testing.T, belge map[string]any) map[string]any {
	t.Helper()

	return altNesne(t, altNesne(t, belge, "components", "belge"), "schemas", "components")
}

// kayitSemasi zarf şemasından KAYIT şemasını çıkarır.
//
// Zarf hem tekil ({"data": {…}}) hem liste ({"data": […]}) olabilir; ikisinde
// de anlatılan asıl şey data'nın İÇİDİR. Zarfa bakıp "iki alanı var" demek,
// gövdesiz bir şemayı dolu saymak olurdu.
func kayitSemasi(t *testing.T, belge, zarf map[string]any) map[string]any {
	t.Helper()

	ozellikler := altNesne(t, cozulmus(t, belge, zarf), "properties", "zarf")
	veri := cozulmus(t, belge, altNesne(t, ozellikler, "data", "zarf.properties"))

	oge, dizi := veri["items"]
	if !dizi {
		return veri
	}

	ogeSemasi, uygun := oge.(map[string]any)
	require.True(t, uygun, "data.items bir şema olmalı, %T bulundu", oge)

	return cozulmus(t, belge, ogeSemasi)
}

// kayitAlanlari bir işlemin yanıt kaydındaki alan adlarını ve zorunlularını döner.
func kayitAlanlari(t *testing.T, belge map[string]any, metot, desen, kod string) (alanlar, zorunlular []string) {
	t.Helper()

	islem := islemBul(t, belge, metot, desen)
	yanitlar := altNesne(t, islem, "responses", metot+" "+desen)
	tanim := altNesne(t, yanitlar, kod, metot+" "+desen+" yanıtları")

	kayit := kayitSemasi(t, belge, jsonSemasi(t, tanim, metot+" "+desen+" "+kod))

	return anahtarlar(altNesne(t, kayit, "properties", "kayıt şeması")), dizeDilimi(t, kayit["required"])
}

// anahtarlar bir haritanın anahtarlarını sıralı döner.
func anahtarlar[T any](m map[string]T) []string {
	adlar := make([]string, 0, len(m))
	for ad := range m {
		adlar = append(adlar, ad)
	}

	sort.Strings(adlar)

	return adlar
}

// dizeDilimi JSON'dan gelen bir dize dizisini Go dilimine çevirir.
//
// Alan hiç yoksa boş dilim döner: "required yazılmamış" ile "required boş"
// JSON Schema'da aynı anlama gelir.
func dizeDilimi(t *testing.T, deger any) []string {
	t.Helper()

	if deger == nil {
		return nil
	}

	ham, uygun := deger.([]any)
	require.True(t, uygun, "dize dizisi bekleniyordu, %T bulundu", deger)

	dizeler := make([]string, 0, len(ham))

	for _, oge := range ham {
		dize, dizeMi := oge.(string)
		require.True(t, dizeMi, "dizi öğesi dize olmalı, %T bulundu", oge)

		dizeler = append(dizeler, dize)
	}

	return dizeler
}

// altKumeOlmali alt kümesindeki her öğenin ust içinde bulunmasını doğrular.
func altKumeOlmali(t *testing.T, alt, ust []string, mesaj string) {
	t.Helper()

	for _, oge := range alt {
		assert.Contains(t, ust, oge, "%s (eksik: %q; küme: %v)", mesaj, oge, ust)
	}
}

// refleriTopla belgede geçen bütün $ref değerlerini toplar.
func refleriTopla(dugum any, toplam *[]string) {
	switch deger := dugum.(type) {
	case map[string]any:
		for alan, alt := range deger {
			if alan == semaRefAnahtari {
				if yol, dize := alt.(string); dize {
					*toplam = append(*toplam, yol)
				}

				continue
			}

			refleriTopla(alt, toplam)
		}
	case []any:
		for _, oge := range deger {
			refleriTopla(oge, toplam)
		}
	}
}

// sepetOlustur vitrin ucundan GERÇEK bir sepet açar; kimliğini ve gövdesini döner.
func sepetOlustur(t *testing.T) (string, []byte) {
	t.Helper()

	istekGovdesi, err := json.Marshal(map[string]string{
		"region_id":     vergiliBolgeID,
		"currency_code": vergiliParaBirimi,
	})
	require.NoError(t, err, "sepet gövdesi kodlanamadı")

	istek := httptest.NewRequest(http.MethodPost, "/store/v1/carts", bytes.NewReader(istekGovdesi))
	istek.Header.Set("Content-Type", "application/json")
	istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	require.Equal(t, http.StatusCreated, kayit.Code,
		"sepet oluşturulmalı; gövde: %s", kayit.Body.String())

	govde := kayit.Body.Bytes()

	var zarf struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(govde, &zarf), "sepet yanıtı çözülemedi; gövde: %s", string(govde))
	require.NotEmpty(t, zarf.Data.ID, "sepet kimliği dönmeli")

	return zarf.Data.ID, govde
}

// yanitAlanlari bir yanıtın data zarfındaki alan adlarını döner.
func yanitAlanlari(t *testing.T, govde []byte) []string {
	t.Helper()

	var zarf struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(govde, &zarf), "yanıt çözülemedi; gövde: %s", string(govde))
	require.NotEmpty(t, zarf.Data, "yanıt data zarfı taşımalı; gövde: %s", string(govde))

	return anahtarlar(zarf.Data)
}

// TestSemaAnlatilanUclariGovdeleriyleAnlatir anlatılan her ucun ne alıp ne
// döndürdüğünü söylediğini doğrular.
//
// Denetlenen küme ANLATILMIŞ uçlardır ([anlatilanUclar]); anlatılmamış uçlar
// için gövde iddiası KURULMAZ (gerekçesi dosyanın başındadır).
//
// İddia üç katmanlıdır ve üçü de bir istemci üretecinin ihtiyacıdır:
//
//   - en az bir 2xx yanıt: yoksa üreteç dönüş tipi "void" olan bir metot yazar;
//   - 2xx gövdesi ALAN taşır: zarfın içine bakılır, çünkü {data: {}} de bir
//     nesnedir ve zarfa bakan bir test boş şemayı dolu sanardı;
//   - yazma uçlarında requestBody: yoksa üreteç her şeyi "any" alan bir metot
//     yazar ve çağıran alan adlarını tahmin etmeye kalkar.
//
// 204 tek istisnadır ve gövdesi OLMAMALIDIR: boş bir content "bir şey
// dönüyor ama şekli bilinmiyor" demek olurdu, oysa kastedilen "hiçbir şey
// dönmüyor"dur.
func TestSemaAnlatilanUclariGovdeleriyleAnlatir(t *testing.T) {
	_, belge := semaBelgesi(t)

	anlatilanlar := anlatilanUclar(t)
	t.Logf("denetlenen küme — %d anlatılmış uç: %s", len(anlatilanlar), strings.Join(anlatilanlar, ", "))

	for _, uc := range anlatilanlar {
		metot, desen, ayrildi := strings.Cut(uc, " ")
		require.True(t, ayrildi, "anlatım anahtarı çözülemedi: %q", uc)

		t.Run(uc, func(t *testing.T) {
			islem := islemBul(t, belge, metot, desen)

			assert.NotEmpty(t, islem["summary"],
				"anlatılan uç özet taşımalı; özetsiz bir işlem istemcide adsız bir metot olur")

			yanitlar := altNesne(t, islem, "responses", uc)

			var basarililar []string

			for _, kod := range anahtarlar(yanitlar) {
				if strings.HasPrefix(kod, "2") {
					basarililar = append(basarililar, kod)
				}
			}

			require.NotEmpty(t, basarililar,
				"anlatılan uç en az bir 2xx yanıtı taşımalı; yalnızca hata yanıtları var: %v", anahtarlar(yanitlar))

			for _, kod := range basarililar {
				tanim := altNesne(t, yanitlar, kod, uc+" yanıtları")
				assert.NotEmpty(t, tanim["description"], "%s yanıtı açıklama taşımalı", kod)

				if kod == "204" {
					assert.NotContains(t, tanim, "content",
						"204 GÖVDESİZDİR; boş bir content 'şekli bilinmiyor' demek olurdu")

					continue
				}

				kayit := kayitSemasi(t, belge, jsonSemasi(t, tanim, uc+" "+kod))
				assert.NotEmpty(t, altNesne(t, kayit, "properties", uc+" "+kod+" kaydı"),
					"%s yanıt kaydı alan taşımalı", kod)
			}

			if _, yazma := govdeliMetotlar[metot]; !yazma {
				assert.NotContains(t, islem, "requestBody",
					"%s gövde okumaz; şemaya gövde yazmak okunmayan bir alan vaat etmek olurdu", metot)

				return
			}

			govde := altNesne(t, islem, "requestBody", uc)
			assert.Equal(t, true, govde["required"], "yazma ucunun gövdesi zorunlu olmalı")

			istek := cozulmus(t, belge, jsonSemasi(t, govde, uc+" requestBody"))
			assert.NotEmpty(t, altNesne(t, istek, "properties", uc+" istek gövdesi"),
				"istek gövdesi alan taşımalı; alansız bir gövde istemciyi tahmine bırakır")
		})
	}
}

// TestSemaAnlatimlariGercekRoutelarlaEslesir hiçbir açıklamanın boşa
// düşmediğini doğrular.
//
// Eşleşmeyen bir açıklama SESSİZDİR: belgede hiç görünmez, uç gövdesiz kalır
// ve şema yine geçerli bir JSON olarak sunulur. Kanıt yalnızca burada
// üretilebilir — kümede modüllerin yanı sıra eklentilerin getirdiği route'lar
// da vardır ve tam ağaç ancak çalışan sunucuda kuruludur.
func TestSemaAnlatimlariGercekRoutelarlaEslesir(t *testing.T) {
	// Belge her istekte yeniden üretilir; eşleşme kaydı da o üretimde dolar.
	// Bu yüzden okuma ÖNCE bir istek yapılarak tetiklenir.
	semaBelgesi(t)

	assert.Empty(t, testBelge.UnmatchedDescriptions(),
		"anlatılan her uç router ağacında bulunmalı; eşleşmeyen kayıt, yolu değişmiş ya da silinmiş bir route demektir")
}

// ayrilmisAdlar çekirdeğin kendi ortak bileşenlerinin adlarıdır.
//
// Yalnızca "türetilmiş şema var mı" sorusunu yanıtlamak için tutulur;
// çekirdek bu adları dışa açmaz ve açmasına da gerek yoktur.
var ayrilmisAdlar = []string{"Error", "List"}

// TestSemaKokAnahtarlariVeOrtakBilesenleriTasir belgenin iskeletini doğrular.
//
// İskelet eksikse şema "sözdizimsel olarak geçerli ama okunamaz" olur: sürüm
// bilgisi olmayan bir belgeyi hiçbir üreteç ayrıştıramaz, components eksikse
// her $ref kırılır.
func TestSemaKokAnahtarlariVeOrtakBilesenleriTasir(t *testing.T) {
	_, belge := semaBelgesi(t)

	assert.Equal(t, openapi.Version, belge["openapi"], "belge OpenAPI sürümünü bildirmeli")

	bilgi := altNesne(t, belge, "info", "belge")
	assert.NotEmpty(t, bilgi["title"], "info.title dolu olmalı")
	assert.NotEmpty(t, bilgi["version"], "info.version dolu olmalı")

	assert.NotEmpty(t, altNesne(t, belge, "paths", "belge"), "paths dolu olmalı")

	semalar := semaBilesenleri(t, belge)

	// Ortak hata zarfı: her uç 401/422/429/500 için buna atıf yapar.
	hata := altNesne(t, semalar, "Error", "components/schemas")
	icHata := altNesne(t, altNesne(t, hata, "properties", "Error"), "error", "Error.properties")
	assert.ElementsMatch(t, []string{"code", "message"}, dizeDilimi(t, icHata["required"]),
		"hata gövdesinde kod ve mesaj HER ZAMAN vardır")

	hataAlanlari := altNesne(t, icHata, "properties", "Error.error")
	altKumeOlmali(t, []string{"code", "message", "request_id", "details"}, anahtarlar(hataAlanlari),
		"hata zarfının alanları eksiksiz anlatılmalı")

	// Tipsiz liste zarfı: kayıt şeması bilinmeyen uçlar için sayfalama biçimi.
	liste := altNesne(t, semalar, "List", "components/schemas")
	assert.ElementsMatch(t, []string{"data", "count", "offset", "limit"}, dizeDilimi(t, liste["required"]),
		"liste zarfının biçimi sabittir")

	guvenlik := altNesne(t, altNesne(t, belge, "components", "belge"), "securitySchemes", "components")
	altKumeOlmali(t, []string{"bearerAuth", "publishableKey"}, anahtarlar(guvenlik),
		"iki yüzeyin güvenlik şeması da tanımlı olmalı")

	// Türetilmiş şemalar ortakların YANINDA durmalı: yalnızca Error ve List
	// varsa gövde anlatımı hiç işlememiş demektir ve bu dosyadaki $ref
	// testi de boşa denetlenirdi.
	assert.Greater(t, len(semalar), len(ayrilmisAdlar),
		"gövdelerden türetilmiş bileşen şemaları da olmalı; olanlar: %v", anahtarlar(semalar))
}

// TestSemadakiHerRefCozulur atıfların hepsinin bir tanıma vardığını doğrular.
//
// Çözülmeyen bir ref, istemci üretecini ÇALIŞIRKEN patlatır ve belgeye gözle
// bakarak fark edilmez: JSON geçerlidir, uç görünür, yalnızca bir alanın tipi
// var olmayan bir bileşene işaret eder. Ad çakışmasının şemayı boşaltması da
// (iki modülün aynı adlı DTO'su) buradan görünür.
func TestSemadakiHerRefCozulur(t *testing.T) {
	_, belge := semaBelgesi(t)

	semalar := semaBilesenleri(t, belge)

	var refler []string

	refleriTopla(belge, &refler)
	require.NotEmpty(t, refler,
		"ön koşul: belgede en az bir $ref olmalı; hiç yoksa bu test hiçbir şey denetlemez")

	gorulen := map[string]struct{}{}

	for _, yol := range refler {
		if _, tekrar := gorulen[yol]; tekrar {
			continue
		}

		gorulen[yol] = struct{}{}

		ad := strings.TrimPrefix(yol, refOneki)
		require.NotEqual(t, yol, ad,
			"belgedeki her atıf bileşen şemalarına olmalı; %q başka bir yeri gösteriyor", yol)

		assert.Contains(t, semalar, ad, "çözülmeyen atıf: %s", yol)
	}

	t.Logf("çözülen atıf: %d benzersiz, %d bileşen şeması", len(gorulen), len(semalar))
}

// TestGirisUcuSemadaAcikcaKorumasizdir jetonu veren ucun jeton istemediğini
// doğrular.
//
// Ayrım incedir ve bedeli büyüktür: alan HİÇ YAZILMASAYDI işlem
// "belirtilmemiş" sayılır ve kök seviyedeki varsayılan güvenliği MİRAS
// ALIRDI; istemci üreteci de giriş için jeton isteyen, yani hiç
// çağrılamayan bir metot üretirdi. Boş dizi ise "bu uç açıkça korumasız"
// demektir ve varsayılanı EZER.
//
// Boş dizinin bir anlamı olması için dolusunun da görülmesi gerekir; test bu
// yüzden iki yüzeyden birer ucu karşılaştırır.
func TestGirisUcuSemadaAcikcaKorumasizdir(t *testing.T) {
	_, belge := semaBelgesi(t)

	giris := islemBul(t, belge, http.MethodPost, authapi.LoginPath)

	deger, yazilmis := giris["security"]
	require.True(t, yazilmis,
		"giriş ucunda security alanı YAZILMIŞ olmalı; yazılmamış bir alan kök varsayılanını miras alır")
	require.NotNil(t, deger, "security null olamaz; null 'belirtilmemiş' ile aynı kapıya çıkar")

	dizi, uygun := deger.([]any)
	require.True(t, uygun, "security bir dizi olmalı, %T bulundu", deger)
	assert.Empty(t, dizi, "boş dizi 'bu uç açıkça korumasız' demektir")

	// Giriş 401 ÜRETMEZ demek değildir: hatalı e-posta/parola yine 401'dir ve
	// istemci o dalı ele almalıdır.
	yanitlar := altNesne(t, giris, "responses", "giriş ucu")
	assert.Contains(t, yanitlar, "401", "korumasız uç da kimlik bilgisi hatasını bildirmeli")

	yonetim := islemBul(t, belge, http.MethodGet, "/admin/v1/users")
	assert.Equal(t, []any{map[string]any{"bearerAuth": []any{}}}, yonetim["security"],
		"yönetim ucu oturum jetonu istemeli")

	vitrin := islemBul(t, belge, http.MethodGet, "/store/v1/products")
	assert.Equal(t, []any{map[string]any{"publishableKey": []any{}}}, vitrin["security"],
		"vitrin ucu publishable anahtar istemeli")
}

// TestSemadaHamKayitKimligiGecmez belgenin route DESENLERİNİ yayımladığını,
// canlı veriyi değil, doğrular.
//
// Test önce GERÇEK bir sepet açar: şema çalışan sunucudan üretildiğine göre,
// canlı veriyle karışsaydı yeni kaydın kimliği belgeye düşerdi. Bir kimliğin
// şemaya sızması iki şeyi birden bozar — belge kişiye özel veri yayımlar ve
// istemci üreteci tek bir kaydın yoluna sabitlenmiş bir metot yazar.
func TestSemadaHamKayitKimligiGecmez(t *testing.T) {
	sepetID, _ := sepetOlustur(t)

	ham, belge := semaBelgesi(t)
	metin := string(ham)

	assert.Contains(t, altNesne(t, belge, "paths", "belge"), "/store/v1/carts/{id}",
		"şema yol DESENİNİ yayımlamalı")
	assert.NotContains(t, metin, sepetID, "az önce açılan sepetin kimliği şemada olmamalı")

	// Fikstür kimlikleri ve API anahtarları da aynı kapsamdadır: ikincisi
	// yalnızca "kimlik" değil SIR'dır ve şema kamuya açık bir belgedir.
	for ad, deger := range map[string]string{
		"yönetici kimliği":     yoneticiID,
		"satış kanalı kimliği": testKanalID,
		"bölge kimliği":        vergiliBolgeID,
		"gizli anahtar":        gizliAnahtar,
		"publishable anahtar":  publishableAnahtar,
	} {
		require.NotEmpty(t, deger, "ön koşul: %s dolu olmalı", ad)
		assert.NotContains(t, metin, deger, "%s şemada olmamalı", ad)
	}

	assert.Empty(t, kayitKimligiRe.FindAllString(metin, -1),
		"şemada önekli kayıt kimliği biçiminde hiçbir dize olmamalı")
}

// TestSemaGercekYanitlarlaTutarlidir anlatılan şemanın sunucunun GERÇEKTEN
// yazdığı gövdeyle örtüştüğünü doğrular.
//
// Dolu bir şema yeterli değildir; DOĞRU da olmalıdır. Şema tiplerden
// türetilir, yanıt ise handler'dan geçer: ikisi arasında bir dönüştürücü
// (DTO'ya çeviren kod) durur ve orada kaybolan bir alan hiçbir birim testinde
// görünmez. İddia iki yönlüdür:
//
//   - sunucunun yazdığı her alan şemada VARDIR — yoksa belge eksik anlatıyor;
//   - şemanın "zorunlu" dediği her alan yanıtta VARDIR — yoksa belge var
//     olmayan bir garanti veriyor ve istemci o alanı okurken boş bulur.
//
// Ters yön (şemada olup yanıtta olmayan alan) hata SAYILMAZ: omitempty
// taşıyan bir alan sıfır değerinde yazılmaz ve şemada zorunlu da görünmez.
func TestSemaGercekYanitlarlaTutarlidir(t *testing.T) {
	_, belge := semaBelgesi(t)

	sepetID, olusturmaGovdesi := sepetOlustur(t)

	t.Run("POST /store/v1/carts", func(t *testing.T) {
		alanlar, zorunlular := kayitAlanlari(t, belge, http.MethodPost, "/store/v1/carts", "201")
		gercek := yanitAlanlari(t, olusturmaGovdesi)

		altKumeOlmali(t, gercek, alanlar, "sunucunun yazdığı her alan şemada olmalı")
		altKumeOlmali(t, zorunlular, gercek, "şemanın zorunlu dediği her alan yanıtta olmalı")
	})

	// Sepet ayrıntısı GÖMÜLÜ bir DTO'yu düzleştirir; alan kümesinin tel
	// üzerinde de aynı düzleşmiş hâlde çıktığı ancak burada görülür.
	t.Run("GET /store/v1/carts/{id}", func(t *testing.T) {
		kayit := magazaIstegi(t, "/store/v1/carts/"+sepetID, publishableAnahtar)
		require.Equal(t, http.StatusOK, kayit.Code, "sepet okunmalı; gövde: %s", kayit.Body.String())

		alanlar, zorunlular := kayitAlanlari(t, belge, http.MethodGet, "/store/v1/carts/{id}", "200")
		gercek := yanitAlanlari(t, kayit.Body.Bytes())

		altKumeOlmali(t, gercek, alanlar, "sunucunun yazdığı her alan şemada olmalı")
		altKumeOlmali(t, zorunlular, gercek, "şemanın zorunlu dediği her alan yanıtta olmalı")
	})
}
