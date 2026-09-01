package graph_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/introspection"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// tumUrunAlanlari şemanın ÜRÜN ağacındaki her alanını isteyen seçim kümesidir.
//
// Maliyet tavanının kalibrasyonu buna dayanır: bir istemcinin meşru olarak
// isteyebileceği en pahalı şey, alanların tamamıdır (kod üreteçleri "hepsini
// seç" belgeleri üretir). Şemaya alan eklendiğinde buraya da eklenmelidir;
// eklenmezse kalibrasyon testi geçmeye devam eder ama artık gerçek en ağır
// belgeyi ölçmüyor olur.
const tumUrunAlanlari = `
  id handle title subtitle description thumbnail isGiftcard discountable
  weight length height width material originCountry collectionId metadata
  createdAt updatedAt
  variants {
    id productId title sku barcode ean upc manageInventory allowBackorder
    weight rank metadata createdAt updatedAt priceSet inventoryItem
    optionValues { id optionId value rank optionTitle }
  }
  options { id productId title rank values { id optionId value rank optionTitle } }
  images { id productId url rank metadata }
  tags { id value }
  categories { id name handle description parentId isActive isInternal rank }
`

// enDerinVeriSorgusu şemanın izin verdiği en derin VERİ yoludur (5 seviye).
const enDerinVeriSorgusu = `{ products { items { variants { optionValues { optionTitle } } } } }`

// takmaAdliYigma aynı kök sorguyu n kez takma adla tekrarlayan belgeyi üretir.
//
// GraphQL'in REST'te karşılığı olmayan çarpanı budur: aşağıdaki belge TEK bir
// HTTP isteğidir, yani hız sınırlayıcı için BİR sayaçtır, sunucu için ise n
// katalog sorgusudur.
func takmaAdliYigma(n int) string {
	var belge strings.Builder

	belge.WriteString("{")

	for i := range n {
		belge.WriteString(" a" + strconv.Itoa(i) + ": products { count }")
	}

	belge.WriteString(" }")

	return belge.String()
}

// takmaAdliAlan aynı alanı n kez takma adla seçen seçim listesini üretir.
//
// Takma ad, GraphQL'in aynı alanı bir seçim kümesinde birden fazla kez
// istemeye izin veren tek aracıdır; ölçülen saldırıların üçü de bunu
// kullanıyordu (489 description, 302 __schema, 448 __type).
func takmaAdliAlan(n int, alan string) string {
	return takmaAdliAlanOnekli("a", n, alan)
}

// takmaAdliAlanOnekli takma adları verilen önekle üretir.
//
// Önek gerekiyor çünkü GraphQL doğrulaması AYNI takma adın FARKLI alanlara
// verilmesini reddeder (OverlappingFieldsCanBeMerged): iki ayrı fragment'ta
// "a0" kullanan bir belge, sınıra hiç ulaşmadan doğrulamada ölürdü ve test
// ölçmek istediği şeyi değil, başka bir kuralı ölçerdi.
func takmaAdliAlanOnekli(onek string, n int, alan string) string {
	var secimler strings.Builder

	for i := range n {
		secimler.WriteString(" " + onek + strconv.Itoa(i) + ": " + alan)
	}

	return secimler.String()
}

// tekrarliAciklama bulgunun ölçtüğü belgeyi üretir: bir sayfa dolusu ürünün
// AÇIKLAMASINI n kez isteyen sorgu.
//
// Ölçülen maliyeti 489 tekrar ve 100'lük sayfa için tam 50.000'dir — yani
// tavana OTURUR, aşmaz ve geçerdi. Yanıtı ise ölçüldüğünde 204,9 MiB'dı:
// karmaşıklık modelinin alan sayısını fiyatlayıp baytı hiç sormadığı yer tam
// olarak burasıdır.
func tekrarliAciklama(tekrar, sayfa int) string {
	return `{ products(limit: ` + strconv.Itoa(sayfa) + `) { items {` +
		takmaAdliAlan(tekrar, "description") + `} } }`
}

// TestDerinlikSiniriAsilanBelgeyiReddeder sınırı aşan sorgunun hiç
// çalıştırılmadığını doğrular.
//
// Sınır TEST İÇİN düşürülür (3) çünkü şema bugün DÖNGÜSEL DEĞİLDİR: en derin
// meşru yol 5 seviyedir ve varsayılan sınırı geçerli bir belgeyle aşmak
// mümkün değildir. Sınırın var olma sebebi de zaten bugünün şeması değil,
// bir alanın geri referans verdiği (variant → product → variants → …) gündür;
// o gün geldiğinde bu testin ölçtüğü mekanizma çoktan yerinde olacak.
func TestDerinlikSiniriAsilanBelgeyiReddeder(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc,
		enDerinVeriSorgusu, graph.Options{MaxDepth: 3})

	require.NotEmpty(t, yanit.Errors)
	assert.Contains(t, yanit.Errors[0].Message, "depth")
	assert.Equal(t, "DEPTH_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listeOlculeri, "sınırı aşan belge servise HİÇ ulaşmamalı")
}

// TestDerinlikSiniriSinirdakiBelgeyiGecirir tam sınıra oturan belgenin
// geçtiğini doğrular.
//
// "Aşınca reddet" testi tek başına eksiktir: her belgeyi reddeden bir sınır da
// o testi geçerdi. Sayımın nerede BİTTİĞİ ancak sınırdaki belge geçince
// belli olur.
func TestDerinlikSiniriSinirdakiBelgeyiGecirir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc,
		enDerinVeriSorgusu, graph.Options{MaxDepth: 5})

	require.Empty(t, yanit.Errors)
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestVarsayilanDerinlikMesruSorguyuGecirir bugünkü şemanın en derin yolunun
// varsayılan sınırın altında kaldığını doğrular.
//
// Varsayılan bir gün düşürülür ya da şema derinleşirse, arıza burada görünür:
// aksi hâlde vitrinin en derin sorgusu ÜRETİMDE reddedilmeye başlar ve kimse
// sınırın ne zaman daraldığını hatırlamaz.
func TestVarsayilanDerinlikMesruSorguyuGecirir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, enDerinVeriSorgusu)

	require.Empty(t, yanit.Errors)
	assert.Less(t, 5, graph.DefaultMaxDepth, "şemanın en derin yolu varsayılanın altında kalmalı")
}

// TestDerinlikSiniriFragmentlaAtlatilamaz aynı ağacın fragment'lara
// bölünerek sınırdan kaçamadığını doğrular.
//
// Kaçış yolu gerçektir: derinlik sayımı fragment tanımlarının içine
// bakmasaydı, istemci her seviyeyi ayrı bir fragment yapıp sınırı 1'e
// düşürürdü. Aşağıdaki belge [enDerinVeriSorgusu] ile AYNI ağacı ister,
// yalnızca yazımı farklıdır.
func TestDerinlikSiniriFragmentlaAtlatilamaz(t *testing.T) {
	t.Parallel()

	belge := `
	  { products { ...listeAlanlari } }
	  fragment listeAlanlari on ProductList { items { ...urunAlanlari } }
	  fragment urunAlanlari on Product { variants { optionValues { optionTitle } } }
	`

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, belge, graph.Options{MaxDepth: 3})

	require.NotEmpty(t, yanit.Errors)
	assert.Contains(t, yanit.Errors[0].Message, "depth")
	assert.Empty(t, svc.listeOlculeri)
}

// TestFragmentKendiBasinaSeviyeEklemez fragment'a bölünmüş belgenin sınırı
// AYNI ağacın düz yazımından daha erken tüketmediğini doğrular.
//
// Bir öncekinin tersidir ve o testin fazla katı bir sayımla da geçebileceğini
// kapatır: fragment spread'in kendisi bir seviye SAYILSAYDI, sorgusunu
// okunabilirlik için fragment'lara bölen istemci — bölmemesi için hiçbir
// sebep yokken — sınıra takılırdı.
func TestFragmentKendiBasinaSeviyeEklemez(t *testing.T) {
	t.Parallel()

	belge := `
	  { products { ...listeAlanlari } }
	  fragment listeAlanlari on ProductList { items { ...urunAlanlari } }
	  fragment urunAlanlari on Product { variants { optionValues { optionTitle } } }
	`

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, belge, graph.Options{MaxDepth: 5})

	require.Empty(t, yanit.Errors, "aynı ağaç düz yazımda 5 seviyedir")
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestKarmasiklikListeUzunluguylaCarpilir maliyetin istenen KAYIT SAYISIYLA
// çarpıldığını doğrular.
//
// Testin sınadığı şey bir sınır değil, MALİYET MODELİDİR: aşağıdaki iki belge
// alan alana aynıdır, yalnızca limit'leri farklıdır. Liste alanına sabit
// maliyet verilseydi ikisi de aynı fiyata görünür — yani tam olarak pahalı
// olan sorgu ucuz sayılır ve sınır, asıl durdurması gereken şeyi geçirirdi.
func TestKarmasiklikListeUzunluguylaCarpilir(t *testing.T) {
	t.Parallel()

	const tavan = 5000

	ucuz := `{ products(limit: 1) { items {` + tumUrunAlanlari + `} } }`
	pahali := `{ products(limit: 100) { items {` + tumUrunAlanlari + `} } }`

	ucuzSvc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), ucuzSvc, ucuz,
		graph.Options{MaxComplexity: tavan})

	require.Empty(t, yanit.Errors, "tek kayıt isteyen belge geçmeli")
	assert.Len(t, ucuzSvc.listeOlculeri, 1)

	pahaliSvc := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), pahaliSvc, pahali,
		graph.Options{MaxComplexity: tavan})

	require.NotEmpty(t, yanit.Errors, "yüz kat fazla kayıt isteyen AYNI belge geçmemeli")
	assert.Contains(t, yanit.Errors[0].Message, "complexity")
	assert.Empty(t, pahaliSvc.listeOlculeri, "sınırı aşan belge servise HİÇ ulaşmamalı")
}

// TestKarmasiklikTavaniSayfaTavaniniAsanLimitiKirpar sayfa tavanının üstündeki
// bir limit'in maliyet tahminini şişirmediğini doğrular.
//
// limit=100000 yazan istemci yüz bin kayıt ALAMAZ; servis sayfayı
// service.MaxLimit'e çeker. Maliyet tahmini bunu bilmeseydi, meşru bir
// istemcinin abartılı yazılmış limit'i sorguyu reddettirirdi — hem de sunucu
// o kadar işi hiç yapmayacakken.
func TestKarmasiklikTavaniSayfaTavaniniAsanLimitiKirpar(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc,
		`{ products(limit: 100000) { items { id handle } } }`,
		graph.Options{MaxComplexity: 1500})

	require.Empty(t, yanit.Errors)
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestKarmasiklikTakmaAdliYigmayiReddeder tek istekte yüzlerce kök sorgunun
// VARSAYILAN ayarlarla reddedildiğini doğrular.
//
// Hız sınırlayıcı bu belgeyi BİR istek sayar, sunucu ise dört yüz katalog
// sorgusu yapardı: kotayı ödemeden yük bindirmenin yolu tam olarak budur ve
// REST'te karşılığı yoktur (orada dört yüz yük, dört yüz kota demektir).
//
// Belge bugün ÖNCE alan tekrarı kapısına takılır (400 kez Query.products) ve
// bu doğrudur: iki kapı da aynı belgeyi reddeder, ucuz olanı öndedir. Ama
// karmaşıklık tavanının kendi kalibrasyonu yine de ölçülmelidir — tekrar
// kapısı bir gün gevşetilirse tavanın hâlâ tuttuğunu kimse bilmezdi. İkinci
// iddia bu yüzden tekrar kapısını YOLDAN ÇEKER ve tavanı yalnız bırakır.
func TestKarmasiklikTakmaAdliYigmayiReddeder(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, takmaAdliYigma(400))

	require.NotEmpty(t, yanit.Errors)
	assert.Empty(t, svc.listeOlculeri, "sınırı aşan belge hiç çalıştırılmamalı")

	yalnizTavan := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), yalnizTavan, takmaAdliYigma(400),
		graph.Options{MaxFieldRepetition: 500})

	require.NotEmpty(t, yanit.Errors)
	assert.Contains(t, yanit.Errors[0].Message, "complexity",
		"tekrar kapısı yoldan çekilince karmaşıklık tavanı yakalamalı")
	assert.Empty(t, yalnizTavan.listeOlculeri)
}

// TestVarsayilanTavanMesruBelgeyiGecirir kalibrasyonun İKİ yanını da
// sabitler.
//
// Bir sınırın doğru yerde olduğu ancak iki iddiayla bilinir: en ağır MEŞRU
// belge geçmeli (yoksa sertleştirme, vitrinin kendi istemcisini kırar) ve
// bunun katı olan belge geçmemeli (yoksa sınır süstür). Varsayılan tavan
// değiştirilirse bu test, hangi tarafın feda edildiğini söyler.
func TestVarsayilanTavanMesruBelgeyiGecirir(t *testing.T) {
	t.Parallel()

	mesru := `{ products { count offset limit items {` + tumUrunAlanlari + `} } }`

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, mesru)

	require.Empty(t, yanit.Errors, "varsayılan sayfada tüm alanları isteyen belge geçmeli")
	assert.Len(t, svc.listeOlculeri, 1)

	asiri := `{ products(limit: 100) { count offset limit items {` + tumUrunAlanlari + `} } }`

	asiriSvc := &sahteVitrin{}
	yanit, _ = sorgula(t, kimlikli([]string{"sc_1"}), asiriSvc, asiri)

	require.NotEmpty(t, yanit.Errors, "aynı ağacı yüz kayıt için isteyen belge geçmemeli")
	assert.Empty(t, asiriSvc.listeOlculeri)
}

// TestGecersizSinirVarsayilanaDuser sıfır ve negatif ayarın "sınırsız"
// ANLAMINA GELMEDİĞİNİ doğrular.
//
// Sertleştirmenin sessizce kaybolabileceği tek yol budur: ayarı doldurmayı
// unutan (ya da yanlış dolduran) bir kurulum, hiçbir hata görmeden korumasız
// bir uç açardı. Sıfır "her belgeyi reddet" de olamaz — o da ucu bir başka
// biçimde kapatırdı; bu yüzden test hem meşru belgenin geçtiğini hem aşırı
// belgenin reddedildiğini iddia eder.
func TestGecersizSinirVarsayilanaDuser(t *testing.T) {
	t.Parallel()

	bozuk := graph.Options{
		MaxDepth:              -1,
		MaxComplexity:         -1,
		MaxFieldRepetition:    -1,
		MaxIntrospectionRoots: -1,
		MaxIntrospectionDepth: -1,
		MaxResponseBytes:      -1,
	}

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, enDerinVeriSorgusu, bozuk)

	require.Empty(t, yanit.Errors, "geçersiz ayar meşru sorguyu kırmamalı")
	assert.Len(t, svc.listeOlculeri, 1)

	yigmaSvc := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), yigmaSvc, takmaAdliYigma(400), bozuk)

	require.NotEmpty(t, yanit.Errors, "geçersiz ayar sınırı KALDIRMAMALI")
	assert.Empty(t, yigmaSvc.listeOlculeri)
}

// devasaBelge sınırı kesin olarak aşan, GEÇERLİ bir GraphQL belgesi döner.
//
// Sorgunun kendisi kusursuzdur; reddedilme sebebi şekli değil BOYUTUDUR.
func devasaBelge() string {
	return `{ product(handle: "` + strings.Repeat("x", 128<<10) + `") { id } }`
}

// TestBuyukSorguGovdesiReddedilir devasa bir belgenin ayrıştırılmadan
// reddedildiğini doğrular.
//
// Bu kapı ötekilerin YAPAMADIĞI işi yapar: derinlik ve karmaşıklık ancak belge
// ayrıştırıldıktan SONRA ölçülebilir, yani onlara ulaşana kadar sunucu metni
// zaten okumuş ve ayrıştırmış olurdu.
//
// Yanıt, GraphQL zarfı değil ÇEKİRDEĞİN hata zarfıdır: belge çalıştırıcıya
// hiç ulaşmadı (gerekçe için bkz. graph.NewHandler'ın sardığı govdeSiniri).
func TestBuyukSorguGovdesiReddedilir(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	rec := istekYap(t, kimlikli([]string{"sc_1"}), svc, devasaBelge(), graph.Options{})

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var zarf corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &zarf))
	assert.Equal(t, "product_graphql_body_too_large", zarf.Error.Code)

	assert.Empty(t, svc.tekilSecici, "sınırı aşan gövde servise HİÇ ulaşmamalı")
}

// TestBoyutunuGizleyenGovdeDeReddedilir boyutunu BİLDİRMEYEN bir istemcinin
// sınırı atlayamadığını doğrular.
//
// Content-Length istemcinin İDDİASIDIR; parçalı (chunked) gövdede hiç yoktur.
// İlk kapı yalnızca o iddiaya bakar, bu yüzden tek başına bir sınır değil
// yalnızca dürüst istemciye verilen düzgün bir hatadır. Asıl sınırı okuyucu
// uygular ve sınadığımız şey odur.
//
// İddia gqlgen'in cümlesine DEĞİL bizim kodumuza bakar ve bu bilinçlidir:
// istemci burada da sebebi ve SAYIYI öğrenir, yalnızca zarfı farklıdır
// (bkz. govdeSiniri). Metne bakan bir iddia, kütüphanenin cümlesini bu depoda
// ikinci kez tanımlardı.
func TestBoyutunuGizleyenGovdeDeReddedilir(t *testing.T) {
	t.Parallel()

	govde, err := json.Marshal(map[string]any{"query": devasaBelge()})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, graph.Path, strings.NewReader(string(govde)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(kimlikli([]string{"sc_1"}))
	// İstemcinin boyutu saklaması bu satırla taklit edilir.
	req.ContentLength = -1

	svc := &sahteVitrin{}
	rec := httptest.NewRecorder()
	graph.NewHandler(svc, graph.Options{}).ServeHTTP(rec, req)

	assert.Empty(t, svc.tekilSecici, "gövde okunurken kesilmeli, sorgu çalışmamalı")

	var yanit graphqlYaniti
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &yanit), "gövde: %s", rec.Body.String())

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "REQUEST_BODY_TOO_LARGE", yanit.Errors[0].Extensions["code"])
	assert.Contains(t, yanit.Errors[0].Message, strconv.Itoa(64<<10),
		"istemci sınırı sayıyla öğrenmeli")
}

// TestIcGozlemVarsayilanOlarakAcik şemanın istemci araçlarına görünür
// olduğunu doğrular.
//
// Karar ve gerekçesi [graph.Options] alanındadır: şema bu deponun içinde duran
// bir dosyadır, kapatmak saldırgandan bir şey saklamaz ama kod üreteçlerini
// körleştirir. Kararın testi yoksa bir gün "sıkılaştırma" diye kapatılır ve
// kimse neyi kaybettiğini bilmez.
func TestIcGozlemVarsayilanOlarakAcik(t *testing.T) {
	t.Parallel()

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), &sahteVitrin{},
		`{ __schema { queryType { name } } }`)

	require.Empty(t, yanit.Errors)
	assert.NotNil(t, yanit.Data["__schema"])
}

// TestIcGozlemKapatilabilir anahtarın GERÇEKTEN kapattığını doğrular.
//
// Anahtarın varlığı tek başına bir şey ifade etmez; kapatmanın yolu gqlgen'de
// bir eklentiyi HİÇ EKLEMEMEKTİR ve bu, unutulması kolay bir ayrıntıdır.
// Veri sorgusunun kapalıyken de çalıştığı ayrıca iddia edilir: iç gözlemi
// kapatırken yüzeyin tamamını kapatmak da mümkündü.
func TestIcGozlemKapatilabilir(t *testing.T) {
	t.Parallel()

	kapali := graph.Options{IntrospectionDisabled: true}

	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), &sahteVitrin{},
		`{ __schema { queryType { name } } }`, kapali)

	require.NotEmpty(t, yanit.Errors, "iç gözlem kapalıyken şema okunamamalı")

	svc := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, `{ products { count } }`, kapali)

	require.Empty(t, yanit.Errors, "iç gözlem kapalıyken veri sorgusu çalışmalı")
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestIcGozlemSorgusuVarsayilanlarlaGecer istemci araçlarının gönderdiği
// STANDART iç gözlem sorgusunun varsayılan ayarlarla çalıştığını doğrular.
//
// Sorgu ölçüldü: 13 seviye derindir (ofType zinciri, tip sarmalayıcılarını
// açmak için) ve kardeş kapsamında en çok tekrarlanan alanı 1 kez seçilir.
// Yani iç gözlemin AYRI tavanı ([graph.DefaultMaxIntrospectionDepth]) bu
// sorgu için vardır; tek bir tavan kullanılsaydı VERİ yüzeyinin sınırını
// 13'ün üstüne çıkarmak zorunda kalırdık ve gevşeme asıl korumak istediğimiz
// yerde olurdu.
//
// İç gözlem ağacının bir sınırı daha vardır ve bizim ayarımızdan bağımsızdır:
// gqlparser'ın MaxIntrospectionDepth kuralı iç içe __Type listelerini üç
// seviyede keser.
func TestIcGozlemSorgusuVarsayilanlarlaGecer(t *testing.T) {
	t.Parallel()

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, introspection.Query)

	require.Empty(t, yanit.Errors)
	assert.NotNil(t, yanit.Data["__schema"])
}

// TestIcGozlemDerinligiVeriTavanindanBagimsiz iki derinlik tavanının gerçekten
// AYRI çalıştığını doğrular.
//
// İddia çift taraflıdır ve tek taraflısı yanıltıcı olurdu: veri tavanı 3'e
// indirilmişken 13 seviyelik iç gözlem sorgusu GEÇMELİ (ayrı tavan işini
// yapıyor), ama 4 seviyelik bir VERİ sorgusu aynı ayarla REDDEDİLMELİ
// (ayrılık, veri kapısını gevşetmiyor).
func TestIcGozlemDerinligiVeriTavanindanBagimsiz(t *testing.T) {
	t.Parallel()

	dar := graph.Options{MaxDepth: 3}

	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, introspection.Query, dar)
	require.Empty(t, yanit.Errors, "iç gözlemin kendi tavanı veri tavanından bağımsız olmalı")

	svc := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, enDerinVeriSorgusu, dar)

	require.NotEmpty(t, yanit.Errors, "veri tavanı iç gözlem yüzünden gevşememeli")
	assert.Equal(t, "DEPTH_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listeOlculeri)
}

// TestIcGozlemDerinligiKendiTavaniylaKesilir iç gözlem ağacının artık ÖLÇÜLDÜĞÜNÜ
// doğrular.
//
// Eskiden derinlik sayımı __schema/__type köklerini atlıyordu, gqlgen'in
// karmaşıklık yürüyüşü de __Schema tipli alanı atlar: yani iç gözlemin ölçülen
// derinliği 0, karmaşıklığı 0'dı ve operatörün elinde onu daraltacak hiçbir
// ayar yoktu. Bu test o boşluğun kapandığını iddia eder.
func TestIcGozlemDerinligiKendiTavaniylaKesilir(t *testing.T) {
	t.Parallel()

	sig := graph.Options{MaxIntrospectionDepth: 2}

	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), &sahteVitrin{},
		`{ __schema { queryType { name } } }`, sig)

	require.NotEmpty(t, yanit.Errors, "üç seviyelik iç gözlem, iki seviyelik tavanı aşmalı")
	assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Contains(t, yanit.Errors[0].Message, "introspection")

	// Ayarın veri yüzeyini kapatmadığı ayrıca iddia edilir: iç gözlemi daraltmak
	// isterken tüm ucu daraltmak da mümkündü.
	svc := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, `{ products { count } }`, sig)

	require.Empty(t, yanit.Errors)
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestIcGozlemKokYigmasiReddedilir tek belgede yüzlerce __schema kökünün
// reddedildiğini doğrular.
//
// Ölçülen sel buydu: 302 takma adlı __schema kökü, 45.796 baytlık istekten
// 5,00 MiB yanıt üretiyordu ve iki eski kapı da onu göremiyordu — belge
// Options{MaxDepth: 1, MaxComplexity: 1} ile bile 200 dönüyor, aynı ayarla
// "products { count }" ise reddediliyordu. Kökleri sığdır (dört seviye), yani
// derinlik kapısı bu belgeyi hiçbir ayarla yakalayamazdı; sayılması gereken
// şey ağacın ne kadar indiği değil, KAÇ KEZ istendiğidir.
func TestIcGozlemKokYigmasiReddedilir(t *testing.T) {
	t.Parallel()

	belge := "{" + takmaAdliAlan(302, "__schema { queryType { name } }") + "}"

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, belge)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Contains(t, yanit.Errors[0].Message, "introspection roots")
	assert.Nil(t, yanit.Data["a0"], "reddedilen belge hiç çalıştırılmamalı")
}

// TestIcGozlemKokSiniriSinirdakiBelgeyiGecirir varsayılanın araçları
// kırmadığını doğrular.
//
// "Aşınca reddet" testi tek başına eksiktir: her iç gözlem sorgusunu reddeden
// bir sınır da onu geçerdi. Şema tarayıcıları aynı belgede bir __schema ile
// bir __type gönderebilir; varsayılanın altına inmek o araçları kırardı.
func TestIcGozlemKokSiniriSinirdakiBelgeyiGecirir(t *testing.T) {
	t.Parallel()

	belge := `{ __schema { queryType { name } } __type(name: "Product") { name } }`

	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, belge)

	require.Empty(t, yanit.Errors)
	assert.NotNil(t, yanit.Data["__schema"])
	assert.Equal(t, 2, graph.DefaultMaxIntrospectionRoots)
}

// TestAlanTekrariYanitBoyutuYigmasiniReddeder bulgunun ÖLÇÜLEN belgesini
// tekrarlar: karmaşıklık tavanının altında kalan ama yanıtı yüz megabaytlara
// çıkaran sorgu.
//
// Belge şudur ve kusursuzdur:
//
//	products(limit: 100) { items { a0: description … a488: description } }
//
// Ölçülen maliyeti tam 50.000'dir: 50.000'lik tavana OTURUR, aşmaz ve
// dolayısıyla geçerdi. Ölçüldü: 8.729 baytlık istek 204,9 MiB yanıt
// üretiyordu (24.620 kat) ve hız sınırlayıcı bunu BİR istek sayıyordu.
// REST'te karşılığı yoktur — orada aynı alan 489 kez istenemez, aynı verinin
// yanıtı ~450 KiB'dır.
func TestAlanTekrariYanitBoyutuYigmasiniReddeder(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, tekrarliAciklama(489, 100))

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Contains(t, yanit.Errors[0].Message, "Product.description")
	assert.Empty(t, svc.listeOlculeri, "sınırı aşan belge servise HİÇ ulaşmamalı")
}

// TestAlanTekrariniKarmasiklikYakalamiyor bir öncekinin neden GEREKTİĞİNİ
// ölçer.
//
// Yeni kapı yoldan çekildiğinde AYNI belge geçer: yani onu durduran şey
// karmaşıklık tavanı değildi ve olamazdı — model alan SAYISINI fiyatlar,
// BAYT'ı değil. Bu iddia olmasaydı, tekrar kapısının gereksiz olduğu (zaten
// karmaşıklığın yakaladığı) bir gün rahatça iddia edilebilirdi.
func TestAlanTekrariniKarmasiklikYakalamiyor(t *testing.T) {
	t.Parallel()

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, tekrarliAciklama(489, 100),
		graph.Options{MaxFieldRepetition: 500})

	require.Empty(t, yanit.Errors, "karmaşıklık tavanı bu belgeyi ASLA görmüyordu")
	assert.Len(t, svc.listeOlculeri, 1)
}

// TestAlanTekrariMesruTakmaAdlariGecirir sınırın gerçek istemciyi kırmadığını
// doğrular.
//
// Takma ad meşru bir araçtır: bir ana sayfa aynı kök sorguyu birkaç vitrin
// şeridi için (öne çıkanlar, yeniler, indirimdekiler) tekrarlar. Sınır o
// belgeyi reddetseydi sertleştirme, korumak istediği vitrini kırardı.
func TestAlanTekrariMesruTakmaAdlariGecirir(t *testing.T) {
	t.Parallel()

	belge := `{
	  oneCikanlar: products(limit: 4) { items { id title } }
	  yeniler: products(limit: 4, q: "yeni") { items { id title } }
	  indirimdekiler: products(limit: 4, q: "indirim") { items { id title } }
	}`

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, belge)

	require.Empty(t, yanit.Errors)
	assert.Len(t, svc.listeOlculeri, 3)
}

// TestAlanTekrariFragmentlaAtlatilamaz yığmanın fragment'lara bölünerek
// sınırdan kaçamadığını doğrular.
//
// Kaçış yolu gerçektir ve derinlikteki ile aynıdır: sayım fragment
// tanımlarının içine bakmasaydı, istemci tekrarlarını fragment'lara dağıtıp
// sayacı sıfırlardı. Aşağıdaki belge tek bir seçim kümesine 30 tekrar
// yerleştirir, yalnızca yazımı ikiye bölünmüştür.
func TestAlanTekrariFragmentlaAtlatilamaz(t *testing.T) {
	t.Parallel()

	belge := `
	  { products { items { ...ilk ...ikinci } } }
	  fragment ilk on Product {` + takmaAdliAlanOnekli("a", 15, "description") + `}
	  fragment ikinci on Product {` + takmaAdliAlanOnekli("b", 15, "description") + `}
	`

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, belge)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listeOlculeri)
}

// TestAlanTekrariKardesKapsamlidir sayımın belge geneli DEĞİL kardeş kapsamlı
// olduğunu doğrular.
//
// Ayrım bir ayrıntı değil, kapının kullanılabilir olmasının koşuludur: belge
// geneli sayılsaydı standart iç gözlem sorgusu reddedilirdi — TypeRef
// fragment'ı __Type.ofType'ı ayrı zincirlerde onlarca kez taşır ve hiçbiri bir
// yığma değildir. Saldırının şekli KARDEŞ yığmadır ve ölçülen belge de öyleydi.
func TestAlanTekrariKardesKapsamlidir(t *testing.T) {
	t.Parallel()

	dar := graph.Options{MaxFieldRepetition: 2}

	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, introspection.Query, dar)

	require.Empty(t, yanit.Errors,
		"iç içe zincirler kardeş değildir; sayım onları yığma sanmamalı")

	svc := &sahteVitrin{}
	yanit, _ = sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc,
		`{ products { items { a: title b: title c: title } } }`, dar)

	require.NotEmpty(t, yanit.Errors, "aynı kümedeki üç tekrar sayılmalı")
	assert.Empty(t, svc.listeOlculeri)
}

// TestAlanTekrariFarkliTipleriAyriSayar sayacın "aynı alan" değil "AYNI NESNE
// ALTINDA aynı alan" saydığını doğrular.
//
// Şemadaki dört ayrı tip "id" alanı taşır; hepsi tek bir sayaca düşseydi
// sıradan bir vitrin sorgusu — ürün, varyant, görsel ve kategori kimliklerini
// birlikte isteyen — sınıra takılırdı.
func TestAlanTekrariFarkliTipleriAyriSayar(t *testing.T) {
	t.Parallel()

	belge := `{ products { items { id variants { id } images { id } categories { id } } } }`

	svc := &sahteVitrin{}
	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), svc, belge,
		graph.Options{MaxFieldRepetition: 1})

	require.Empty(t, yanit.Errors, "farklı tiplerin aynı adlı alanları ayrı sayılmalı")
	assert.Len(t, svc.listeOlculeri, 1)
}

// kalibrasyonBelgeleri sertleştirme tablosunun ÖLÇÜLEN belgeleridir.
//
// Tablo README'de ve [graph.DefaultMaxComplexity] godoc'unda tekrarlanır;
// burası onun tek KAYNAĞIDIR. Belgelerin metni buraya yazılmazsa tablodaki
// sayılar bir süre sonra kimsenin doğrulayamadığı folklora dönüşür — nitekim
// eski tablonun ürün sayfası satırı kök sorgu maliyetini hiç saymıyordu
// (1,4 bin yazıyordu, ölçüldüğünde 2.368 çıktı).
var kalibrasyonBelgeleri = map[string]struct {
	belge       string
	karmasiklik int
}{
	"ürün sayfası (PDP, her şey dâhil)": {
		belge:       `{ product(handle: "tisort") {` + tumUrunAlanlari + `} }`,
		karmasiklik: 2368,
	},
	"kategori listesi (24 ürün, kart alanları + fiyat)": {
		belge: `{ products(limit: 24) { count items { id handle title thumbnail ` +
			`variants { id title sku priceSet inventoryItem } } } }`,
		karmasiklik: 2344,
	},
	"varsayılan sayfada TÜM alanlar (20 ürün × tüm ağaç)": {
		belge:       `{ products { count offset limit items {` + tumUrunAlanlari + `} } }`,
		karmasiklik: 28440,
	},
	"limit=100 ile TÜM alanlar": {
		belge:       `{ products(limit: 100) { count offset limit items {` + tumUrunAlanlari + `} } }`,
		karmasiklik: 138200,
	},
	"400 takma adlı products { count }": {
		belge:       takmaAdliYigma(400),
		karmasiklik: 408000,
	},
	"489 takma adlı description (limit=100)": {
		belge:       tekrarliAciklama(489, 100),
		karmasiklik: 50000,
	},
	"1500 takma adlı description (varsayılan sayfa)": {
		belge:       `{ products { items {` + takmaAdliAlan(1500, "description") + `} } }`,
		karmasiklik: 31020,
	},
}

// olculenKarmasiklik belgenin karmaşıklığını gqlgen'in KENDİ hesabından okur.
//
// Sayı ikinci bir hesapla üretilmez; tavan 1'e çekilir ve gqlgen reddederken
// bulduğu değeri mesaja yazar. İkinci bir hesap yazmak, tablonun modeli değil
// testin modelini ölçmesi olurdu.
//
// Diğer kapılar YOLDAN ÇEKİLİR: ölçülen belgelerin bir kısmı (400 takma ad,
// 489 tekrar) bugün önce alan tekrarı kapısına takılır ve o hâlde karmaşıklık
// hiç raporlanmazdı.
func olculenKarmasiklik(t *testing.T, belge string) int {
	t.Helper()

	yanit, _ := sorgulaOpts(t, kimlikli([]string{"sc_1"}), &sahteVitrin{}, belge, graph.Options{
		MaxComplexity:      1,
		MaxDepth:           1 << 20,
		MaxFieldRepetition: 1 << 20,
	})

	require.NotEmpty(t, yanit.Errors, "tavan 1 iken her belge reddedilmeli")

	eslesme := karmasiklikDeseni.FindStringSubmatch(yanit.Errors[0].Message)
	require.Len(t, eslesme, 2, "gqlgen karmaşıklığı mesajda bildirmeli: %s", yanit.Errors[0].Message)

	olculen, err := strconv.Atoi(eslesme[1])
	require.NoError(t, err)

	return olculen
}

// karmasiklikDeseni gqlgen'in karmaşıklık hatasından sayıyı çeker.
var karmasiklikDeseni = regexp.MustCompile(`complexity (\d+)`)

// TestKarmasiklikKalibrasyonu tablodaki her sayının HÂLÂ doğru olduğunu
// doğrular.
//
// Kalibrasyon bir kez ölçülüp belgeye yazılan bir sayı olduğunda, maliyet
// modelinin her değişikliği tabloyu sessizce yanlışlar: kimse 28,4 binin ne
// zaman 40 bin olduğunu fark etmez ve "tavan en ağır meşru belgeye iki kat
// pay bırakıyor" cümlesi bir gün gerçek olmaktan çıkar. Bu test o cümleyi
// ölçüme bağlar.
func TestKarmasiklikKalibrasyonu(t *testing.T) {
	t.Parallel()

	for ad, durum := range kalibrasyonBelgeleri {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, durum.karmasiklik, olculenKarmasiklik(t, durum.belge))
		})
	}
}

// TestKalibrasyonunIkiYaniAyrilir tablonun "geçer/geçmez" sütununu VARSAYILAN
// ayarlarla sınar.
//
// Karmaşıklık sayısı tek başına bir karar değildir; tablonun asıl iddiası
// hangi belgenin geçtiğidir. En ağır meşru belge geçmeli (yoksa sertleştirme
// vitrinin kendi istemcisini kırar), aşırı olanlar geçmemeli (yoksa sınır
// süstür).
func TestKalibrasyonunIkiYaniAyrilir(t *testing.T) {
	t.Parallel()

	gecer := []string{
		"ürün sayfası (PDP, her şey dâhil)",
		"kategori listesi (24 ürün, kart alanları + fiyat)",
		"varsayılan sayfada TÜM alanlar (20 ürün × tüm ağaç)",
	}

	katalog := olcumKatalogu(1)

	for _, ad := range gecer {
		// Fikstür GEREKLİDİR: şemanın zorunlu alanları boş bir üründe null
		// döner ve test, sınırı değil eksik veriyi ölçmeye başlardı.
		svc := &sahteVitrin{liste: katalog, tekil: katalog.Items[0]}
		yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, kalibrasyonBelgeleri[ad].belge)

		assert.Empty(t, yanit.Errors, "%s geçmeli", ad)
	}

	gecmez := []string{
		"limit=100 ile TÜM alanlar",
		"400 takma adlı products { count }",
		"489 takma adlı description (limit=100)",
		"1500 takma adlı description (varsayılan sayfa)",
	}

	for _, ad := range gecmez {
		svc := &sahteVitrin{}
		yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, kalibrasyonBelgeleri[ad].belge)

		require.NotEmpty(t, yanit.Errors, "%s geçmemeli", ad)
		assert.Empty(t, svc.listeOlculeri, "%s servise ulaşmamalı", ad)
		assert.Empty(t, svc.tekilSecici, "%s servise ulaşmamalı", ad)
	}
}

// katlananFragmentBelgesi her seviyede kendini iki kez açan fragment zinciri
// üretir.
//
// Belge GEÇERLİDİR ve döngü İÇERMEZ — doğrulamanın reddettiği tek şey odur.
// Yine de açılımı 2^seviye seçimdir: 26 seviye 1.127 bayt yazıp 67 milyon
// seçim açar.
func katlananFragmentBelgesi(seviye int) string {
	var belge strings.Builder

	belge.WriteString("{ products { items { ...f" + strconv.Itoa(seviye) + " } } }\n")
	belge.WriteString("fragment f0 on Product { id }\n")

	for i := 1; i <= seviye; i++ {
		alt := "...f" + strconv.Itoa(i-1)
		belge.WriteString("fragment f" + strconv.Itoa(i) + " on Product { " + alt + " " + alt + " }\n")
	}

	return belge.String()
}

// TestSecimButcesiKatlananFragmentiReddeder üssel açılan fragment zincirinin
// ucu kilitleyemediğini doğrular.
//
// Ölçüldü: bu belge 1.127 BAYTTIR ve düzeltmeden önce istek on saniyede
// bitmiyordu. Tuzağa düşen tek bir hesap değildi — derinlik sayımı, alan
// tekrarı sayımı ve gqlgen'in kendi karmaşıklık yürüyüşü, üçü de fragment
// tanımına belleksiz iniyordu. Bu yüzden düzeltme bir yürüyüşü değil, ağacın
// BÜYÜKLÜĞÜNÜ bağlar ve diğer bütün kapılardan önce koşar.
//
// Test bir zaman aşımı iddiası taşımaz; taşısaydı yavaş bir makinede
// güvenilmez olurdu. Yerine daha güçlü bir şey iddia eder: belge REDDEDİLİR ve
// servise ulaşmaz. Sınır kalkarsa test yavaşlamaz, ASILIR — ve go test kendi
// zaman aşımıyla bunu söyler.
func TestSecimButcesiKatlananFragmentiReddeder(t *testing.T) {
	t.Parallel()

	belge := katlananFragmentBelgesi(26)
	require.Less(t, len(belge), 2<<10, "belgenin küçüklüğü bulgunun ta kendisidir")

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, belge)

	require.NotEmpty(t, yanit.Errors)
	assert.Equal(t, "SELECTION_BUDGET_EXCEEDED", yanit.Errors[0].Extensions["code"])
	assert.Empty(t, svc.listeOlculeri, "reddedilen belge servise ulaşmamalı")
}

// TestSecimButcesiFragmentliMesruBelgeyiGecirir bütçenin fragment kullanan
// sıradan istemciyi kırmadığını doğrular.
//
// Fragment, sorgusunu okunabilir tutan istemcinin aracıdır ve aynı fragment'ı
// birkaç yerde açmak olağandır. Bütçe o belgeye dokunmamalı; dokunsaydı
// düzeltme, koruduğu vitrini kırardı.
func TestSecimButcesiFragmentliMesruBelgeyiGecirir(t *testing.T) {
	t.Parallel()

	belge := `
	  {
	    oneCikanlar: products(limit: 4) { items { ...kart } }
	    yeniler: products(limit: 4, q: "yeni") { items { ...kart } }
	    indirimdekiler: products(limit: 4, q: "indirim") { items { ...kart } }
	  }
	  fragment kart on Product {
	    id handle title thumbnail
	    variants { ...varyant }
	  }
	  fragment varyant on Variant { id title sku priceSet inventoryItem }
	`

	svc := &sahteVitrin{}
	yanit, _ := sorgula(t, kimlikli([]string{"sc_1"}), svc, belge)

	require.Empty(t, yanit.Errors)
	assert.Len(t, svc.listeOlculeri, 3)
}
