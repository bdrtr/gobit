//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/module"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// Bu dosya arama EKLENTİSİNİN gerçek sistemde çalıştığını uçtan uca kanıtlar:
//
//	Ürün yazıldığında katalog olay yayımlar, eklenti olayı alıp KENDİ
//	tablosuna indeksler ve GET /store/v1/search ürünü döner — üstelik
//	yalnızca ürünün satış kanalını taşıyan anahtara.
//
// Zincirin dört halkası da gerçektir: gerçek product modülü, gerçek olay veri
// yolu, eklentinin gerçek PostgreSQL indeksi ve üretimdeki koruma yığınından
// geçen gerçek HTTP ucu. Eklentinin kendi paketindeki testlerde katalog her
// zaman SAHTEDİR ve sahte olmak zorundadır (eklenti hiçbir modülü import
// edemez, ADR 0001); dolayısıyla "eklenti product'ın gerçekten yayımladığı
// olayı alıyor ve product'ın gerçekten döndürdüğü kaydı yazıyor" iddiasının
// kanıtlanabileceği TEK yer burasıdır.
//
// # Eklenti neden ZEMİNE kuruldu, testin içine değil
//
// Üretimde eklentiler modül bootstrap'ından ÖNCE kurulur ve eklentinin
// getirdiği modül çekirdek modüllerle AYNI kayda eklenir
// (bkz. cmd/server/main.go: Install -> Bootstrap -> Start -> MountRoutes).
// Kurulumu teste taşımak, o modülü İKİNCİ bir [module.Registry] ile ayağa
// kaldırmayı gerektirirdi — üretimde var olmayan bir kablolama. Test o zaman
// eklentinin üretimde nasıl kurulduğunu değil, yalnızca kendi kurduğu şeyin
// çalıştığını kanıtlardı.
//
// İkinci sebep zamanlamadır: abonelikler ilk üründen ÖNCE kurulmuş olmalıdır.
// Bellek içi veri yolu geçmiş tutmaz ve EN FAZLA BİR KEZ teslim eder
// (bkz. eventbus paket belgesi), yani koşunun ortasında kurulan bir abone
// kendisinden önce yayımlanmış olayları hiç görmez ve "olay indekse ulaştı"
// iddiası testlerin SIRASINA bağlı hâle gelirdi.
//
// # Zemine eklemek mevcut testleri kırmıyor
//
// Üç ayrı sebeple: (1) hiçbir test arama sonucu saymaz, sabit bir modül ya da
// route kümesi iddia etmez; (2) koruma yığını YOL ÖNEKİ ile kurulduğu için yeni
// uçlar otomatik olarak korumaya girer, mevcut uçların davranışı değişmez;
// (3) abone hatası yayımcıya ULAŞMAZ — Publish handler'ları beklemez ve bellek
// içi backend her handler'ı kendi goroutine'inde çalıştırır, dolayısıyla
// eklentinin bir arızası ürün yazma yolunu düşüremez.
//
// Aynı şey ödeme eklentisi için DOĞRU DEĞİLDİR ve o yüzden o eklenti zemine
// kurulmaz: sertlestirme_test.go'daki senaryo, sağlayıcının kayıttan ÖNCE
// bulunmadığını ön koşul olarak iddia eder (bkz.
// [TestEklentiCekirdegeDokunmadanSaglayiciEkler]); zemine kurulmuş bir stripe
// eklentisi o testi kırardı. Karar eklenti başına verilmiştir.
//
// # Kendi ürünlerini kendi kurar
//
// Her senaryo KENDİ ürününü ve katalogda başka hiçbir yerde geçmeyen kendi
// arama sözcüğünü kurar (bkz. [aramaSozcugu]). Zemin tek bir indeks paylaşır ve
// koşu ilerledikçe başka senaryoların ürünleri de indekslenir; sabit bir sözcük
// ya da "indekste kaç kayıt var" biçiminde bir iddia, testleri birbirinin
// sırasına bağlardı.

// aramaYoklamaAraligi indeks tazelenirken iki yoklama arasında beklenen süredir.
//
// Bekleme süresinin tavanı [olayBeklemeSuresi]'dir ve o sabit burada
// TEKRARLANMAZ: beklenen şey aynı şeydir — bellek içi veri yolunun bir olayı
// aboneye taşıması.
const aramaYoklamaAraligi = 20 * time.Millisecond

// aramaIndeksTablosu eklentinin indeks tablosunun adıdır.
//
// Ad elle yazılmıştır çünkü eklenti onu dışa AÇMAZ: yayımladığı sözleşme
// uçlar, modül adı ve şemadır (bkz. searchpg paket belgesi). Tablo adının
// değişmesi bir şema göçü demektir ve o durumda bu testin düşmesi doğrudur —
// sessizce başka bir tabloyu okumasındansa.
const aramaIndeksTablosu = "searchpg_product"

// Zemine kurulan eklentilerin kaydı ve host'u.
//
// İkisi de TestMain akışında doldurulur ([setUpPlugins]) ve iki fazda
// kullanılır: kayıtlar modüllerden önce bildirilir, abonelikler ve route'lar
// modüller ayağa kalktıktan sonra uygulanır.
var (
	eklentiKayit *coreplugin.Registry
	eklentiHost  *coreplugin.Host
)

// setUpPlugins eklentileri MODÜLLERDEN ÖNCE kurar.
//
// Modül kaydı ve veri yolu dışarıdan verilir çünkü ikisi de eklentinin
// çalışacağı GERÇEK zemindir: eklentinin getirdiği modül çekirdek modüllerle
// aynı Bootstrap'tan geçmeli, abonelikleri de gerçek katalog olaylarını
// dinlemelidir. Ayrı bir kayıt ya da ayrı bir veri yolu vermek, eklentiyi
// kendi kabarcığında test etmek olurdu.
//
// Ayar haritası nil'dir: arama eklentisi yapılandırma İSTEMEZ (bkz. searchpg
// paket belgesi). Ayar isteyen bir eklentinin eksik ayarla durduğu ayrıca
// sınanır (bkz. [TestSetupStopsWhenAPluginSettingIsMissing]).
func setUpPlugins(ctx context.Context, moduller *module.Registry, veriYolu eventbus.EventBus) error {
	eklentiKayit = coreplugin.NewRegistry(nil)
	eklentiKayit.Add(searchpg.New())

	eklentiHost = coreplugin.NewHost(ctr, moduller, veriYolu, nil, nil)

	return eklentiKayit.Install(ctx, eklentiHost)
}

// startPlugins abonelikleri uygular ve eklenti route'larını bağlar.
//
// MODÜLLER AYAĞA KALKTIKTAN SONRA çağrılır; sıra üretimdekiyle aynıdır.
// [coreplugin.Registry.MountRoutes] bu kurulumda hiçbir şey bağlamaz — arama
// eklentisinin uçları eklenti kancasından değil, getirdiği MODÜLÜN
// Routes'undan gelir. Yine de çağrılır: atlanması, uçları eklenti kancasıyla
// bağlayan bir sonraki eklentinin sessizce route'suz kalması demek olurdu.
func startPlugins(ctx context.Context) error {
	if err := eklentiKayit.Start(ctx, eklentiHost); err != nil {
		return err
	}

	return eklentiKayit.MountRoutes(testRouter, eklentiHost)
}

// aramaSozcugu katalogda başka hiçbir yerde geçmeyen bir arama sözcüğü üretir.
//
// Sözcük paylaşılan fikstür sayacından üretilir: aynı zemini kullanan her
// senaryo kendi sözcüğünü alır ve bir testin ürünü başka bir testin sorgusuna
// asla düşmez.
//
// Rakamla biten sözcük 'simple' sözlüğünde TEK bir lexeme'e çözülür ve arama
// önek eşleşmesi yapmaz: "aramaimi1" sorgusu "aramaimi10" yazan ürünü BULMAZ.
// Sayaç bu yüzden sözcükleri ayırmaya yeter.
func aramaSozcugu() string {
	return fmt.Sprintf("aramaimi%d", fixtureCounter.Add(1))
}

// aramaUrunu verilen sözcüğü BAŞLIĞINDA taşıyan yayında bir ürün oluşturur ve
// kimliğiyle handle'ını döner.
//
// Ürün SERVİSTEN kurulur, HTTP'den değil: zincirin ilk halkası product
// servisinin olay yayımıdır ve testin başlangıç noktası tam olarak orasıdır.
//
// Durum published'dır. Taslak ürün kataloğun vitrin okumasından zaten dönmez,
// yani taslak bir fikstürle ölçülen şey indeks değil yayın süzgeci olurdu.
func aramaUrunu(ctx context.Context, t *testing.T, sozcuk string) (urunID, handle string) {
	t.Helper()

	sira := fixtureCounter.Add(1)
	urun, err := productSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-arama-%d", sira),
		Title:  aramaBasligi(sozcuk),
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err, "arama fikstür ürünü oluşturulamadı")

	return urun.ID, urun.Handle
}

// aramaBasligi arama sözcüğünü ürün başlığına gömer.
//
// Sözcük BAŞLIKTA durur çünkü eklenti başlığa en yüksek ağırlığı verir; sabit
// ön ek ise fikstürün bir hata mesajında tanınmasını sağlar.
func aramaBasligi(sozcuk string) string { return "E2E Arama " + sozcuk }

// aramaCagir verilen publishable anahtarla arama ucunu çağırır.
//
// Adres eklentinin dışa açık sabitinden ([searchpg.SearchPath]) kurulur, elle
// yazılmaz: uç adresi eklentinin YAYIMLADIĞI sözleşmedir ve testin ondan
// sapması, adres değiştiğinde testin eski adresi doğrulamaya devam etmesi
// demek olurdu.
func aramaCagir(t *testing.T, anahtar string, sorgu url.Values) *httptest.ResponseRecorder {
	t.Helper()

	return magazaIstegi(t, searchpg.SearchPath+"?"+sorgu.Encode(), anahtar)
}

// aramaZarfi arama yanıtını çözer ve ucun 200 döndüğünü doğrular.
//
// Yanıt [vitrinZarfi] ile çözülür, yani mağaza ürün listesiyle AYNI tiple. Bu
// bir kolaylık değil, kasıtlı bir okumadır: eklenti gösterilecek kayıtları
// kataloğun vitrin yüzeyinden alır ve yeniden biçimlendirmez, dolayısıyla iki
// ucun gövdesi aynı şekildedir.
func aramaZarfi(t *testing.T, anahtar string, sorgu url.Values) vitrinZarfi {
	t.Helper()

	kayit := aramaCagir(t, anahtar, sorgu)
	require.Equal(t, http.StatusOK, kayit.Code,
		"arama ucu 200 dönmeli; gövde: %s", kayit.Body.String())

	var zarf vitrinZarfi
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"arama yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return zarf
}

// aramaSonucu tek bir sözcük için arama yapar.
func aramaSonucu(t *testing.T, anahtar, sozcuk string) vitrinZarfi {
	t.Helper()

	return aramaZarfi(t, anahtar, url.Values{"q": {sozcuk}})
}

// yoklayarakBekle koşul sağlanana kadar SENKRON olarak yoklar.
//
// [require.Eventually] bilinçli olarak kullanılmadı: koşulu ayrı bir
// goroutine'de çalıştırır ve süre dolduğunda o goroutine hâlâ koşuyor olabilir.
// Son gözlemi hata mesajına taşımak için koşul dışında tutulan bir değişken, o
// durumda gerçek bir veri yarışı olurdu ve testler -race ile koşuyor. Senkron
// döngüde böyle bir yarış yoktur; çağıran, en son gördüğü değeri güvenle iddia
// edebilir.
func yoklayarakBekle(kosul func() bool) bool {
	bitis := time.Now().Add(olayBeklemeSuresi)

	for {
		if kosul() {
			return true
		}
		if time.Now().After(bitis) {
			return false
		}

		time.Sleep(aramaYoklamaAraligi)
	}
}

// aramaBekle sorgunun beklenen kimlik kümesini döndürmesini bekler.
//
// Bekleme ZORUNLUDUR: [eventbus.EventBus].Publish handler'ları beklemez, yani
// ürün servisi döndüğünde indeks yazması henüz başlamamış bile olabilir.
//
// Beklenen küme boş verilebilir; o durumda çağrı hemen döner ve "hiç bulunmadı"
// gözlemi bir bekleme sonucu değildir. Bu ayrım önemlidir: bir şeyin
// OLMADIĞINI beklemekle kanıtlayamayız, o yüzden boş kümeyi iddia eden her
// senaryo önce dolu bir kümeyi beklemiş olmalıdır.
func aramaBekle(t *testing.T, anahtar, sozcuk string, beklenen ...string) {
	t.Helper()

	var son []string
	// slices.Equal SIRA duyarlıdır; senaryolar tek kimlikli kümeler iddia
	// ettiği için bu yeterlidir ve alaka sırasını da korur.
	yoklayarakBekle(func() bool {
		son = aramaSonucu(t, anahtar, sozcuk).kimlikler()

		return slices.Equal(son, beklenen)
	})

	require.ElementsMatch(t, beklenen, son,
		"%q sorgusu beklenen kimlikleri döndürmeli; dönmediyse olay -> indeks -> arama "+
			"zincirinin bir halkası kopmuştur", sozcuk)
}

// indekstekiSatir ürünün eklentinin KENDİ tablosunda satırı olup olmadığını
// söyler.
//
// # Neden doğrudan SQL
//
// Eklentinin dışa açık tek okuma yolu arama ucudur ve o uç kanal süzgecinden
// GEÇMİŞ sonucu döner. "Arama boş döndü" gözlemi bu yüzden iki farklı dünyayla
// uyumludur: satır silinmiştir ya da satır durmaktadır ama katalog kaydı
// gizlemektedir. İki iddianın ayrımı tam olarak budur — silmede satır GİTMELİ,
// kanal süzmesinde satır KALMALIDIR — ve ayrımı HTTP'den görmenin yolu yoktur.
//
// Okunan tablo eklentinin kendisine aittir; başka bir modülün şemasına
// dokunulmaz.
func indekstekiSatir(ctx context.Context, t *testing.T, urunID string) bool {
	t.Helper()

	var mevcut bool
	err := testPool.Pool().QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM "+aramaIndeksTablosu+" WHERE product_id = $1)",
		urunID).Scan(&mevcut)
	require.NoError(t, err, "arama indeksi okunamadı")

	return mevcut
}

// tabloVar veritabanında verilen adda bir tablo olup olmadığını söyler.
func tabloVar(ctx context.Context, t *testing.T, ad string) bool {
	t.Helper()

	var mevcut bool
	require.NoError(t,
		testPool.Pool().QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", ad).Scan(&mevcut),
		"%q tablosu sorgulanamadı", ad)

	return mevcut
}

// TestAramaEklentisiKendiModulunuGetirdi eklentinin BİRİNCİ uzatma noktasını
// gerçek zeminde doğrular.
//
// Eklenti bir sağlayıcı kaydetmekle kalmaz, KENDİ modülünü getirir ve o modül
// çekirdek modüllerle aynı yaşam döngüsünden geçer. İki tablonun varlığı bunun
// kanıtıdır: veri tablosu migration'ın uygulandığını, AYRI sürüm defteri ise
// migration'ın eklentinin kendi sahipliğiyle koştuğunu — yani şemasının hiçbir
// modülün defteriyle karışmadığını — gösterir.
//
// Ortak bir "schema_migrations" tablosu ASLA oluşmamalıdır; oluşsaydı
// eklentinin şeması modüllerinkiyle aynı deftere yazıyor, dolayısıyla eklenti
// kaldırıldığında geriye ne bıraktığı bilinemez olurdu.
func TestAramaEklentisiKendiModulunuGetirdi(t *testing.T) {
	ctx := t.Context()

	assert.True(t, tabloVar(ctx, t, aramaIndeksTablosu),
		"%q tablosu oluşmalı; oluşmadıysa eklentinin modülü migration adımından geçmemiş demektir",
		aramaIndeksTablosu)

	defter := searchpg.ModuleName + "_schema_migrations"
	assert.True(t, tabloVar(ctx, t, defter),
		"%q sürüm defteri oluşmalı; eklentinin şeması kendi defterine yazmalıdır", defter)

	assert.False(t, tabloVar(ctx, t, "schema_migrations"),
		"ortak schema_migrations tablosu ASLA oluşmamalı")
}

// TestUrunOlayiIndekseUlasirVeAramaBulur zincirin tamamını kanıtlar:
// product.created yayımlanır, eklenti onu indeksler ve arama ucu ürünü bulur.
//
// Ürün yalnızca SERVİSTEN yazılır; test indekse hiç dokunmaz. Aradaki her adım
// (olayın yayımlanması, abonenin çağrılması, kataloğun okunması, belgenin
// yazılması) gerçek bileşenlerdedir, dolayısıyla sonuç yalnızca zincirin
// tamamı çalışıyorsa gelir.
func TestUrunOlayiIndekseUlasirVeAramaBulur(t *testing.T) {
	ctx := t.Context()
	sozcuk := aramaSozcugu()

	urunID, handle := aramaUrunu(ctx, t, sozcuk)

	aramaBekle(t, publishableKey, sozcuk, urunID)

	zarf := aramaSonucu(t, publishableKey, sozcuk)
	require.Len(t, zarf.Data, 1, "arama tek kayıt döndürmeli; gövde: %+v", zarf)
	assert.Equal(t, handle, zarf.Data[0].Handle,
		"kayıt KATALOGTAN gelmeli: indekste kimlikten başka hiçbir şey saklanmaz, "+
			"dolayısıyla dolu bir handle ancak vitrin okumasından gelebilir")
	assert.Equal(t, 1, zarf.Count,
		"sayaç bu yanıttaki kayıt sayısını bildirmeli")
}

// TestUrunGuncelleninceIndeksTazelenir indeksin kaydın gerisinde kalmadığını
// doğrular.
//
// İki iddia birlikte anlamlıdır: ürün YENİ başlığıyla bulunmalı ve ESKİ
// başlığıyla bulunMAMALIdır. Yalnızca ilki sınansaydı, belgeyi tazelemek yerine
// üstüne EKLEYEN bir uygulama da geçerdi; o uygulamada bir ürün, hiç taşımadığı
// eski bir başlıkla aramada görünmeye devam ederdi.
//
// Eski sözcüğün boş dönmesi burada beklemeye ihtiyaç duymaz: iki sözcük AYNI
// satırda yaşar, yani yeni sözcük bulunduğu anda eski sözcük çoktan silinmiştir.
func TestUrunGuncelleninceIndeksTazelenir(t *testing.T) {
	ctx := t.Context()
	eskiSozcuk, yeniSozcuk := aramaSozcugu(), aramaSozcugu()

	urunID, _ := aramaUrunu(ctx, t, eskiSozcuk)
	aramaBekle(t, publishableKey, eskiSozcuk, urunID)

	yeniBaslik := aramaBasligi(yeniSozcuk)
	_, err := productSvc.UpdateProduct(ctx, urunID, productsvc.UpdateProductInput{Title: &yeniBaslik})
	require.NoError(t, err, "ürün başlığı güncellenemedi")

	aramaBekle(t, publishableKey, yeniSozcuk, urunID)
	aramaBekle(t, publishableKey, eskiSozcuk)
}

// TestUrunSilininceIndekstenDuser silme olayının indeks satırını gerçekten
// düşürdüğünü doğrular.
//
// Aramanın boşalması TEK BAŞINA hiçbir şey kanıtlamaz: silinen ürün kataloğun
// vitrin okumasından da dönmez, yani eklenti silme olayını hiç işlemeseydi de
// arama boş dönerdi — ve bayat satır indekste sonsuza dek kalırdı. Asıl iddia
// bu yüzden satırın kendisi üzerindedir.
func TestUrunSilininceIndekstenDuser(t *testing.T) {
	ctx := t.Context()
	sozcuk := aramaSozcugu()

	urunID, _ := aramaUrunu(ctx, t, sozcuk)
	aramaBekle(t, publishableKey, sozcuk, urunID)
	require.True(t, indekstekiSatir(ctx, t, urunID),
		"ön koşul: ürün aramada bulunduğuna göre indekste satırı olmalı")

	require.NoError(t, productSvc.DeleteProduct(ctx, urunID), "ürün silinemedi")

	assert.True(t, yoklayarakBekle(func() bool { return !indekstekiSatir(ctx, t, urunID) }),
		"silinen ürünün indeks satırı DÜŞMELİ; kalırsa indeks katalogla ayrışır ve "+
			"ayrışma yalnızca satır bir gün yeniden görünür olduğunda fark edilir")
	aramaBekle(t, publishableKey, sozcuk)
}

// TestAramaKanalSuzmesiniAtlamaz aramanın katalog süzmesinin BYPASS'ı
// olmadığını doğrular.
//
// Kurulum iki vitrindir ve ürün YALNIZCA ikinci kanala atanmıştır: aynı sözcük,
// iki farklı publishable anahtarla iki farklı sonuç vermelidir. Tek anahtarla
// "bulunamadı" gözlemi hiçbir şey kanıtlamazdı — ürün indekslenmemiş de
// olabilirdi; ikinci anahtarın AYNI ürünü BULMASI, gizlemenin sebebinin tam
// olarak kanal olduğunu söyleyen tek gözlemdir.
//
// İndeks satırının DURUYOR olması ayrıca sınanır ve iddianın çekirdeği odur:
// eklenti kanal kuralını kendi indeksinde tekrarlamaz, indeksten yalnızca
// kimlik üretir ve süzgeci katalog uygular. Satır dururken ürünün birinci
// vitrinde görünmemesi, süzgecin gerçekten okuma anında çalıştığını gösterir.
func TestAramaKanalSuzmesiniAtlamaz(t *testing.T) {
	zemin := channelCatalogFixture(t)
	ctx := t.Context()

	sozcuk := aramaSozcugu()
	urunID, _ := aramaUrunu(ctx, t, sozcuk)
	require.NoError(t, bindChannel(urunID, zemin.secondChannelID),
		"ürün ikinci satış kanalına bağlanmalı")

	// Önce ürünün KENDİ vitrininde bulunması beklenir; indeksin dolduğunun
	// kanıtı budur. Bu beklenmeden yapılan "birinci vitrinde yok" gözlemi,
	// indeksin henüz yazılmamış olmasıyla da açıklanabilirdi.
	aramaBekle(t, zemin.ikinciAnahtar, sozcuk, urunID)

	assert.True(t, indekstekiSatir(ctx, t, urunID),
		"ürün indekste OLMALI; süzgecin katalogta uygulandığı ancak satır dururken görülebilir")

	birinci := aramaSonucu(t, publishableKey, sozcuk)
	assert.Empty(t, birinci.kimlikler(),
		"başka bir kanala atanmış ürün bu vitrinin ARAMASINDA görünmemeli; görünüyorsa "+
			"arama, kanal süzmesinin bypass'ı olmuş demektir")
	assert.Zero(t, birinci.Count, "sayaç da süzülmüş kümeyi yansıtmalı")

	// Kanal SORGU DİZESİNDEN seçilemez: seçilebilseydi elindeki herhangi bir
	// publishable anahtarla gelen istemci, başka bir vitrinin kataloğunda arama
	// yapardı. Aynı koruma vitrin listesinde de sınanır
	// (bkz. [TestTheStorefrontDoesNotTakeTheChannelFromTheQueryString]); aramanın onu devralmadığı
	// varsayılamaz, çünkü kanalları isteğin kimliğinden okuyan kod AYRIDIR.
	kacamak := aramaZarfi(t, publishableKey, url.Values{
		"q":                {sozcuk},
		"sales_channel_id": {zemin.secondChannelID},
	})
	assert.Empty(t, kacamak.kimlikler(),
		"sorgu dizesindeki kanal kimliği YOK SAYILMALI")
}

// TestAramaUcuPublishableAnahtarsizReddedilir yeni ucun koruma yığınına
// otomatik olarak girdiğini doğrular.
//
// Koruma yığını tek tek uçlara değil YOL ÖNEKİNE takılıdır (bkz.
// corehttp.APIGuards), yani eklentinin açtığı bir /store/v1 ucu hiçbir şey
// yapmadan korumaya girer. Sınanan tam olarak budur: eklenti yazarının
// koruması unutması ihtimali mimari olarak ortadan kalkmış olmalıdır.
//
// İki isteğin İKİSİ de gereklidir. 401 tek başına ucun var olduğunu SÖYLEMEZ:
// koruma yönlendirmeden önce çalıştığı için /store/v1 altındaki OLMAYAN bir yol
// da 401 döner. Aynı adresin geçerli anahtarla 200 dönmesi, reddedilen şeyin
// gerçekten arama ucu olduğunu çivileyen ikinci gözlemdir.
func TestAramaUcuPublishableAnahtarsizReddedilir(t *testing.T) {
	sorgu := url.Values{"q": {aramaSozcugu()}}

	anahtarsiz := aramaCagir(t, "", sorgu)
	require.Equal(t, http.StatusUnauthorized, anahtarsiz.Code,
		"publishable anahtarsız arama reddedilmeli; gövde: %s", anahtarsiz.Body.String())

	gecerli := aramaCagir(t, publishableKey, sorgu)
	assert.Equal(t, http.StatusOK, gecerli.Code,
		"aynı adres geçerli anahtarla çalışmalı; çalışmıyorsa 401 ucun varlığından değil "+
			"YOKLUĞUNDAN geliyor demektir; gövde: %s", gecerli.Body.String())
}
