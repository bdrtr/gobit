package openapi_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// zenginVaryant gölgeleyen alanın taşıdığı ZENGİN tiptir.
type zenginVaryant struct {
	ID    string `json:"id"`
	Fiyat int64  `json:"fiyat"`
}

// temelKayit gömülecek olan taban tiptir.
//
// Dışa KAPALIDIR ve bu bilinçlidir: encoding/json dışa kapalı bir tipin
// gömülü hâlini yine tarar ve içindeki dışa açık alanları yazar. Şema da
// yazmalıdır; yazmazsa istemci ürünün kimliğini hiç göremez.
type temelKayit struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	Variants []string  `json:"variants"`
	Skipped  string    `json:"-"`
	kapali   string    // dışa kapalı alan; şemaya GİRMEMELİ
	Created  time.Time `json:"created_at"`
}

// golgeleyenKayit gömülü tipin bir alanını GÖLGELER.
//
// Şekil, internal/modules/product/service.StoreProduct'ın ta kendisidir:
// gömülü kaydın Variants alanı, zenginleştirilmiş bir dilimle gölgelenir.
// Gerçek tiple kurulan bağ internal/arch'taki testtedir; çekirdek testleri
// modülleri import EDEMEZ (Prensip 2.4), bu yüzden şekil burada tekrarlanır.
type golgeleyenKayit struct {
	temelKayit
	Variants []zenginVaryant `json:"variants"`
}

// solTaraf ve sagTaraf aynı adı AYNI derinlikte isteyen iki gömülü tiptir.
type solTaraf struct {
	Ortak string
}

// sagTaraf solTaraf ile aynı alanı taşır; ikisi de etiketsizdir.
type sagTaraf struct {
	Ortak string
}

// belirsizKayit iki gömülü tipten aynı adı miras alır.
//
// Aday alanların derinliği de etiketliliği de eşittir; encoding/json böyle
// bir alanı HİÇ yazmaz (kazanan yoktur) ve şema da yazmamalıdır.
type belirsizKayit struct {
	solTaraf
	sagTaraf
	Tekil string `json:"tekil"`
}

// etiketliTaraf ile etiketsizTaraf AYNI JSON adını AYNI derinlikte ister ama
// yalnızca biri etiketlidir.
//
// Etiketin Go alan adından farklı olması bilinçlidir: çakışma alanın JSON
// ADI üzerinden doğar, Go adı üzerinden değil.
type etiketliTaraf struct {
	Etiketli string `json:"Ortak"`
}

// etiketsizTaraf etiketliTaraf ile aynı JSON adını etiketsiz taşır.
type etiketsizTaraf struct {
	Ortak string
}

// kismenBelirsizKayit eşit derinlikte tek bir ETİKETLİ aday taşır.
//
// encoding/json'da etiketli aday belirsizliği çözer ve alan yazılır.
type kismenBelirsizKayit struct {
	etiketliTaraf
	etiketsizTaraf
}

// tumTipler yansıma katmanının tanıması gereken tip ailesini toplar.
type tumTipler struct {
	Metin    string          `json:"metin"`
	Uzun     int64           `json:"uzun"`
	Kisa     int32           `json:"kisa"`
	Ondalik  float64         `json:"ondalik"`
	Mantik   bool            `json:"mantik"`
	Dilim    []string        `json:"dilim"`
	Harita   map[string]any  `json:"harita"`
	Zaman    time.Time       `json:"zaman"`
	Ham      json.RawMessage `json:"ham"`
	Isaretci *string         `json:"isaretci"`
	ZamanIsa *time.Time      `json:"zaman_isaretci"`
	Secmeli  *int64          `json:"secmeli,omitempty"`
	Baytlar  []byte          `json:"baytlar"`
	IcIce    zenginVaryant   `json:"ic_ice"`
	Skipped  string          `json:"-"`
	kapali   string          // dışa kapalı alan; şemaya GİRMEMELİ
}

// dugum kendine referans veren bir tiptir; şema üreticisi burada sonsuz
// döngüye girmemelidir.
type dugum struct {
	ID  string  `json:"id"`
	Alt []dugum `json:"alt"`
	Ust *dugum  `json:"ust"`
}

// CakisanKayit dahili test dosyasındaki openapi.CakisanKayit ile AYNI adı
// taşır; ikisi ayrı paketlerdedir.
type CakisanKayit struct {
	BaskaAlan int `json:"baska_alan"`
}

// Error çekirdeğin ortak hata bileşeniyle aynı adı taşır.
type Error struct {
	Kod string `json:"kod"`
}

// doluTumTipler her alanı DOLU bir örnek döner.
//
// Alanların dolu olması şart: omitempty taşıyan bir alan boşken JSON'a hiç
// yazılmaz ve anahtar kümesi karşılaştırması onu hiç görmezdi.
func doluTumTipler() tumTipler {
	metin := "değer"
	sayi := int64(7)
	an := time.Unix(0, 0).UTC()

	return tumTipler{
		Metin:    "a",
		Uzun:     1,
		Kisa:     2,
		Ondalik:  3.5,
		Mantik:   true,
		Dilim:    []string{"x"},
		Harita:   map[string]any{"k": "v"},
		Zaman:    time.Unix(0, 0).UTC(),
		Ham:      json.RawMessage(`{"serbest":true}`),
		Isaretci: &metin,
		ZamanIsa: &an,
		Secmeli:  &sayi,
		Baytlar:  []byte{1, 2, 3},
		IcIce:    zenginVaryant{ID: "v1", Fiyat: 100},
		Skipped:  "yazılmamalı",
		kapali:   "yazılmamalı",
	}
}

// belge şema üretimi için boş bir belge döner.
func belge() *openapi.Doc {
	return openapi.New("test", "v1")
}

// coz "$ref" atıflarını belgedeki bileşene çözer.
//
// [openapi.Doc.SchemaOf] adlandırılmış struct'lar için atıf döner; testin
// baktığı şey ise atfın HEDEFİDİR.
func coz(t *testing.T, d *openapi.Doc, sema map[string]any) map[string]any {
	t.Helper()

	ref, refli := sema["$ref"].(string)
	if !refli {
		return sema
	}

	ad := strings.TrimPrefix(ref, "#/components/schemas/")
	hedef, var_ := d.Schemas()[ad]
	require.True(t, var_, "%q bileşeni kayıtlı olmalı", ad)

	m, ok := hedef.(map[string]any)
	require.True(t, ok, "%q bileşeni nesne olmalı", ad)

	return m
}

// alanlar şemanın "properties" anahtar kümesini döner.
func alanlar(t *testing.T, d *openapi.Doc, sema map[string]any) []string {
	t.Helper()

	ozellikler, ok := coz(t, d, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı: %#v", sema)

	adlar := make([]string, 0, len(ozellikler))
	for ad := range ozellikler {
		adlar = append(adlar, ad)
	}

	return adlar
}

// ozellik şemanın tek bir alanının şemasını döner.
func ozellik(t *testing.T, d *openapi.Doc, sema map[string]any, ad string) map[string]any {
	t.Helper()

	ozellikler, ok := coz(t, d, sema)["properties"].(map[string]any)
	require.True(t, ok, "şemada properties olmalı")
	require.Contains(t, ozellikler, ad)

	m, ok := ozellikler[ad].(map[string]any)
	require.True(t, ok, "%q alanının şeması nesne olmalı", ad)

	return m
}

// zorunlular şemanın "required" listesini döner.
func zorunlular(t *testing.T, d *openapi.Doc, sema map[string]any) []string {
	t.Helper()

	ham, var_ := coz(t, d, sema)["required"]
	if !var_ {
		return nil
	}

	liste, ok := ham.([]string)
	require.True(t, ok, "required bir dize dilimi olmalı")

	return liste
}

// jsonAnahtarlari değeri encoding/json ile kodlayıp anahtarlarını döner.
func jsonAnahtarlari(t *testing.T, v any) []string {
	t.Helper()

	ham, err := json.Marshal(v)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	adlar := make([]string, 0, len(cozulmus))
	for ad := range cozulmus {
		adlar = append(adlar, ad)
	}

	return adlar
}

// TestSemaAlanlariJSONIleBIREBIRAyni yansıma katmanının EN GÜÇLÜ sınavıdır.
//
// Bir örnek değer encoding/json ile kodlanır ve JSON'daki anahtar kümesi,
// üretilen şemanın "properties" anahtar kümesiyle karşılaştırılır. Tek bir
// iddia; etiketle ad değiştirme, "-" ile atlama, dışa kapalı alan ve
// GÖLGELENME hatalarının hepsi buraya düşer.
//
// Örnekler DOLU verilir: omitempty taşıyan bir alan boşken JSON'a hiç
// yazılmaz ve boş bir örnek, şemada fazladan duran bir alanı gizlerdi.
func TestSemaAlanlariJSONIleBIREBIRAyni(t *testing.T) {
	t.Parallel()

	ornekler := map[string]any{
		"gölgeleyen kayıt": golgeleyenKayit{
			temelKayit: temelKayit{
				ID:       "p1",
				Title:    "Tişört",
				Variants: []string{"gölgelenen"},
				Skipped:  "yazılmamalı",
				kapali:   "yazılmamalı",
				Created:  time.Unix(0, 0).UTC(),
			},
			Variants: []zenginVaryant{{ID: "v1", Fiyat: 100}},
		},
		"belirsiz gömülü alan":             belirsizKayit{Tekil: "t"},
		"etiketli aday belirsizliği çözer": kismenBelirsizKayit{},
		"tüm tipler":                       doluTumTipler(),
		"kendine referans":                 dugum{ID: "kök", Alt: []dugum{{ID: "yaprak"}}},
	}

	for ad, ornek := range ornekler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			d := belge()

			assert.ElementsMatch(t, jsonAnahtarlari(t, ornek), alanlar(t, d, d.SchemaOf(ornek)),
				"şemanın alanları encoding/json'un yazdığı anahtarlarla AYNI olmalı")
		})
	}
}

// TestGolgelenenAlanZenginTipiTasir gölgelenmenin yalnızca "alan tek kez
// görünüyor" düzeyinde değil, TİP düzeyinde de doğru olduğunu doğrular.
//
// Anahtar kümesi karşılaştırması burada yetmez: gölgelenen alan da gölgeleyen
// de "variants" adını taşır, yani yanlış olanı seçmek anahtar kümesini
// bozmaz. Yanlış seçim, istemcinin varyantları dize dizisi sanması demektir.
func TestGolgelenenAlanZenginTipiTasir(t *testing.T) {
	t.Parallel()

	d := belge()
	sema := d.SchemaOf(golgeleyenKayit{})

	varyantlar := ozellik(t, d, sema, "variants")
	assert.Equal(t, "array", varyantlar["type"])

	oge, ok := varyantlar["items"].(map[string]any)
	require.True(t, ok)

	assert.ElementsMatch(t, []string{"id", "fiyat"}, alanlar(t, d, oge),
		"gölgeleyen alanın öğe tipi zengin varyant olmalı, gömülüdeki dize değil")
}

// TestBelirsizGomuluAlanSemayaGirmez eşit derinlikte ve eşit etiketlilikte
// çakışan alanın DÜŞTÜĞÜNÜ doğrular.
func TestBelirsizGomuluAlanSemayaGirmez(t *testing.T) {
	t.Parallel()

	d := belge()

	assert.ElementsMatch(t, []string{"tekil"}, alanlar(t, d, d.SchemaOf(belirsizKayit{})),
		"encoding/json belirsiz alanı yazmaz; şema da yazmamalı")

	assert.ElementsMatch(t, []string{"Ortak"}, alanlar(t, d, d.SchemaOf(kismenBelirsizKayit{})),
		"eşit derinlikte tek etiketli aday belirsizliği çözer")
}

// TestDisaKapaliVeAtlananAlanSemayaGirmez iki ayrı gizleme yolunun da
// şemadan düştüğünü doğrular.
func TestDisaKapaliVeAtlananAlanSemayaGirmez(t *testing.T) {
	t.Parallel()

	d := belge()
	adlar := alanlar(t, d, d.SchemaOf(doluTumTipler()))

	assert.NotContains(t, adlar, "Skipped", `json:"-" alanı şemada olmamalı`)
	assert.NotContains(t, adlar, "kapali", "dışa kapalı alan şemada olmamalı")
}

// TestZorunluAlanlarOmitemptyDisindakilerdir "required" kümesinin
// encoding/json'un HER ZAMAN yazdığı anahtarlar olduğunu doğrular.
func TestZorunluAlanlarOmitemptyDisindakilerdir(t *testing.T) {
	t.Parallel()

	d := belge()
	sema := d.SchemaOf(tumTipler{})

	zorunlu := zorunlular(t, d, sema)
	assert.Contains(t, zorunlu, "metin")
	assert.NotContains(t, zorunlu, "secmeli", "omitempty taşıyan alan zorunlu değildir")

	// Sıfır değerde omitempty alanları JSON'a hiç yazılmaz; geriye kalan
	// anahtar kümesi tam olarak "her zaman yazılanlar"dır.
	assert.ElementsMatch(t, jsonAnahtarlari(t, tumTipler{}), zorunlu,
		"required, sıfır değerin JSON anahtarlarıyla aynı olmalı")
}

// TestTemelTipEslemeleri Go tiplerinin JSON Schema karşılıklarını doğrular.
func TestTemelTipEslemeleri(t *testing.T) {
	t.Parallel()

	d := belge()
	sema := d.SchemaOf(doluTumTipler())

	beklenen := map[string]string{
		"metin":   "string",
		"uzun":    "integer",
		"kisa":    "integer",
		"ondalik": "number",
		"mantik":  "boolean",
		"dilim":   "array",
		"harita":  "object",
		"baytlar": "string", // encoding/json bayt dilimini base64 DİZE yazar
	}

	for ad, tip := range beklenen {
		assert.Equal(t, tip, ozellik(t, d, sema, ad)["type"], "%q alanının tipi", ad)
	}

	assert.Equal(t, "int64", ozellik(t, d, sema, "uzun")["format"])
	assert.Equal(t, "int32", ozellik(t, d, sema, "kisa")["format"])
	assert.Equal(t, map[string]any{"type": "string"},
		ozellik(t, d, sema, "dilim")["items"], "dilim öğesinin tipi taşınmalı")
}

// TestZamanTarihSaatDizesidir time.Time'ın nesne değil dize olduğunu doğrular.
//
// time.Time'ın alanları dışa kapalıdır; yansıma onu naif okusaydı şemada BOŞ
// bir nesne çıkardı ve istemci tarih alanına nesne göndermeyi denerdi.
func TestZamanTarihSaatDizesidir(t *testing.T) {
	t.Parallel()

	d := belge()

	assert.Equal(t, map[string]any{"type": "string", "format": "date-time"},
		ozellik(t, d, d.SchemaOf(doluTumTipler()), "zaman"))
}

// TestZamanIsaretcisiHemTarihHemNull *time.Time'ın hem biçimini hem null'ı
// taşıdığını doğrular.
//
// Tuzak gerçektir: time.Time'ın MarshalJSON'ı DEĞER alıcılıdır, dolayısıyla
// *time.Time de onu taşır. "Kendi kodlayıcısı var, şeklini bilmiyorum" diyen
// naif bir denetim, deleted_at gibi HER modelde bulunan alanları serbest
// şemaya düşürür ve istemci tarih alanını hiç tanımaz.
func TestZamanIsaretcisiHemTarihHemNull(t *testing.T) {
	t.Parallel()

	d := belge()

	assert.Equal(t,
		map[string]any{"type": []any{"string", "null"}, "format": "date-time"},
		ozellik(t, d, d.SchemaOf(doluTumTipler()), "zaman_isaretci"))
}

// TestHamJSONSerbestSema json.RawMessage'ın şeklinin BİLİNMEDİĞİNİ doğrular.
func TestHamJSONSerbestSema(t *testing.T) {
	t.Parallel()

	d := belge()

	assert.Empty(t, ozellik(t, d, d.SchemaOf(doluTumTipler()), "ham"),
		"ham JSON'un şekli tanımı gereği bilinmez; serbest şema olmalı")
}

// TestIsaretciNullable işaretçi alanların null kabul ettiğini doğrular.
func TestIsaretciNullable(t *testing.T) {
	t.Parallel()

	d := belge()
	sema := d.SchemaOf(doluTumTipler())

	assert.Equal(t, []any{"string", "null"}, ozellik(t, d, sema, "isaretci")["type"])
	assert.Equal(t, "array", ozellik(t, d, sema, "dilim")["type"],
		"dilimin nil'liği yazarın seçimi değil Go'nun sıfır değeridir; null'lanmaz")
}

// TestIsaretciBilesenAnyOfIleNullable bir bileşene işaretçinin nasıl
// null'landığını doğrular.
//
// "$ref"in yanına type yazmak JSON Schema 2020-12'de $ref ile BİRLİKTE
// değerlendirilir ve null hiçbir şeye uymaz; doğru biçim anyOf'tur.
func TestIsaretciBilesenAnyOfIleNullable(t *testing.T) {
	t.Parallel()

	d := belge()
	ust := ozellik(t, d, d.SchemaOf(dugum{}), "ust")

	secenekler, ok := ust["anyOf"].([]any)
	require.True(t, ok, "bileşene işaretçi anyOf ile null'lanmalı: %#v", ust)
	assert.Contains(t, secenekler, map[string]any{"type": "null"})
}

// TestOzyinelemeSonsuzDonguYapmaz kendine referans veren bir tipin şemasının
// ÜRETİLEBİLDİĞİNİ doğrular.
//
// Test, döngü hâlinde tamamlanmaz (zaman aşımına düşer); iddialar döngünün
// $ref ile kırıldığını gösterir.
func TestOzyinelemeSonsuzDonguYapmaz(t *testing.T) {
	t.Parallel()

	d := belge()
	sema := d.SchemaOf(dugum{})

	assert.Equal(t, "#/components/schemas/Dugum", sema["$ref"],
		"adlandırılmış struct bileşene kaydedilip atıfla anlatılmalı")

	alt := ozellik(t, d, sema, "alt")
	assert.Equal(t, map[string]any{"$ref": "#/components/schemas/Dugum"}, alt["items"],
		"özyineleme derinlik sınırıyla değil atıfla kırılmalı")
}

// TestTuretilenSemalarBelgeyeYazilir bileşenlerin gerçekten belgeye girdiğini
// doğrular.
//
// Yalnızca [openapi.Doc.SchemaOf] çıktısına bakmak yetmezdi: atıf hedefi
// belgede yoksa şema SÖZDİZİMSEL olarak geçerli ama çözülemez olurdu.
func TestTuretilenSemalarBelgeyeYazilir(t *testing.T) {
	t.Parallel()

	d := belge()
	d.Describe("GET", "/store/v1/products", openapi.Operation{
		Responses: map[string]any{"200": openapi.Response("Ürünler", d.List(golgeleyenKayit{}))},
	})

	sema := semaUret(t, d, routerKur(t))
	semalar := harita(t, harita(t, sema["components"], "components")["schemas"], "schemas")

	assert.Contains(t, semalar, "GolgeleyenKayit")
	assert.Contains(t, semalar, "ZenginVaryant")
	assert.Contains(t, semalar, "Error", "çekirdeğin ortak hata bileşeni durmalı")
}

// TestTekilZarfTiptenUretilir tekil yanıt zarfının şeklini doğrular.
func TestTekilZarfTiptenUretilir(t *testing.T) {
	t.Parallel()

	d := belge()
	zarf := d.Item(zenginVaryant{})

	assert.Equal(t, "object", zarf["type"])
	assert.Equal(t, []string{"data"}, zarf["required"])
	assert.ElementsMatch(t, []string{"id", "fiyat"}, alanlar(t, d, ozellik(t, d, zarf, "data")))
}

// TestListeZarfiTiptenUretilir liste zarfının sayfalama alanlarını ve öğe
// tipini doğrular.
func TestListeZarfiTiptenUretilir(t *testing.T) {
	t.Parallel()

	d := belge()
	zarf := d.List(zenginVaryant{})

	assert.ElementsMatch(t,
		[]string{"data", "count", "offset", "limit"}, zorunlular(t, d, zarf))
	assert.Equal(t, "integer", ozellik(t, d, zarf, "count")["type"])

	veri := ozellik(t, d, zarf, "data")
	assert.Equal(t, "array", veri["type"])

	oge, ok := veri["items"].(map[string]any)
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"id", "fiyat"}, alanlar(t, d, oge))
}

// TestListeZarfiDilimiTekrarSarmaz List'e dilim verilse de kayıt verilse de
// aynı zarfın çıktığını doğrular.
//
// Tekrar sarsaydı belgede "dizi dizisi" oluşurdu ve bunu kimse şemayı satır
// satır okumadan fark etmezdi.
func TestListeZarfiDilimiTekrarSarmaz(t *testing.T) {
	t.Parallel()

	d := belge()

	assert.Equal(t, d.List(zenginVaryant{}), d.List([]zenginVaryant{}))
}

// TestIstekGovdesiTiptenUretilir requestBody tanımının şeklini doğrular.
func TestIstekGovdesiTiptenUretilir(t *testing.T) {
	t.Parallel()

	d := belge()
	govde := d.RequestBody(zenginVaryant{})

	assert.Equal(t, true, govde["required"])

	icerik := harita(t, govde["content"], "content")
	tip := harita(t, icerik["application/json"], "application/json")

	assert.Equal(t, map[string]any{"$ref": "#/components/schemas/ZenginVaryant"}, tip["schema"])
}

// TestAyniAdiIsteyenIkiTipBelgeyiDurdurur ad çakışmasının SESSİZ kalmadığını
// doğrular.
//
// Sessizce ezmek en kötü sonuçtu: iki uçtan birinin gövdesi yanlış anlatılır
// ve bu, ancak istemci yanlış alan gönderdiğinde anlaşılırdı.
func TestAyniAdiIsteyenIkiTipBelgeyiDurdurur(t *testing.T) {
	t.Parallel()

	d := belge()
	d.SchemaOf(openapi.CakisanKayit{})
	d.SchemaOf(CakisanKayit{})

	_, err := d.Build(routerKur(t))
	require.Error(t, err, "iki farklı tip aynı bileşen adını alamaz")
	assert.Contains(t, err.Error(), "CakisanKayit")
}

// TestCekirdekBilesenAdiKorunur ortak "Error" bileşeninin bir modül tipiyle
// ezilemediğini doğrular.
func TestCekirdekBilesenAdiKorunur(t *testing.T) {
	t.Parallel()

	d := belge()
	d.SchemaOf(Error{})

	_, err := d.Build(routerKur(t))
	require.Error(t, err, "çekirdeğin ortak bileşen adı türetilen şemayla ezilemez")
	assert.Contains(t, err.Error(), "Error")
}

// TestAnlatilmisUcunGovdesiSemayaYazilir uçtan uca akışı doğrular: modülün
// anlattığı gövde, üretilen belgede gerçekten görünmelidir.
//
// Bu testin varlık sebebi bir bulgudur: şema sözdizimsel olarak geçerliydi
// ama ANLAMSAL olarak boştu — hiçbir uçta requestBody ya da 2xx yanıt yoktu
// ve bir istemci üreteci ondan her şeyi 'any' olan metotlar üretirdi.
func TestAnlatilmisUcunGovdesiSemayaYazilir(t *testing.T) {
	t.Parallel()

	d := belge()
	d.Describe("GET", "/store/v1/products", openapi.Operation{
		Summary:     "Vitrin ürünlerini listeler",
		RequestBody: d.RequestBody(zenginVaryant{}),
		Responses: map[string]any{
			"200": openapi.Response("Ürün listesi", d.List(golgeleyenKayit{})),
		},
	})

	islem := islemAl(t, semaUret(t, d, routerKur(t)), "/store/v1/products", "get")

	require.Contains(t, islem, "requestBody")
	require.Contains(t, yanitlarAl(t, islem), "200")
}

// TestAnlatilmamisUcSemadaKalir Describer uygulamayan bir modülün ucunun
// belgeden DÜŞMEDİĞİNİ doğrular.
//
// [openapi.Describer] opsiyoneldir; anlatılmamış bir uç yolu, metodu ve
// güvenliğiyle görünmeye devam etmeli, yalnızca gövdesi olmamalıdır.
func TestAnlatilmamisUcSemadaKalir(t *testing.T) {
	t.Parallel()

	islem := islemAl(t, semaUret(t, belge(), routerKur(t)), "/store/v1/products", "get")

	assert.NotContains(t, islem, "requestBody")
	assert.Contains(t, yanitlarAl(t, islem), "401", "ortak hata yanıtları yine durmalı")
}

// TestSemaOfNilDegerSerbest tipi olmayan bir değerin şemasının hiçbir şey
// İDDİA ETMEDİĞİNİ doğrular.
func TestSemaOfNilDegerSerbest(t *testing.T) {
	t.Parallel()

	assert.Empty(t, belge().SchemaOf(nil))
}
