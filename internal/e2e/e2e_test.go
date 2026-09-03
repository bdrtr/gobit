//go:build integration

// Package e2e planın Faz 5, Faz 6 ve Faz 7 DoD'lerini GERÇEK modüllerle uçtan
// uca doğrular.
//
// Faz 5 DoD'si tek cümleyle: "Sepet oluştur -> ürün ekle -> adet güncelle ->
// ara toplam / indirim / vergi / genel toplam DOĞRU hesaplanıyor; MİSAFİR ve
// KAYITLI MÜŞTERİ senaryoları test edilmiş."
//
// Faz 6 DoD'si tek cümleyle: "Uçtan uca sepet -> sipariş akışı test provider
// ile çalışıyor; ödeme adımı başarısızken STOK REZERVASYONU VE SİPARİŞ GERİ
// ALINIYOR (saga testi); order.placed eventi yayınlanıyor."
//
// Faz 7 DoD'si tek cümleyle: "Siparişe fulfillment oluşturulabiliyor; sepete
// indirim uygulanıp toplam DOĞRU güncelleniyor; vergi region'a göre
// hesaplanıyor."
//
// # Faz 7 devralması Faz 5/6 tutarlarını neden değiştirmiyor
//
// Faz 7'de internal/workflows/cart'taki iki geçici çözüm devralındı: indirim
// artık promotion, vergi ise tax modülünden gelir. Buna rağmen Faz 5 ve Faz 6
// senaryolarının elle yazılmış tutarları AYNI kaldı ve bu bir tesadüf değil,
// fikstürün gereğidir.
//
// Vergi tarafında: [vergiFiksturleriniKur] tax modülünde [vergiliUlke] için
// %20'lik bir VARSAYILAN oran kurar, yani yeni yetkili eskisiyle aynı cevabı
// verir. Oran kurulmasaydı tax "bu ülkenin vergi bölgesi yok" derdi, vergi
// sıfıra düşerdi ve Faz 5/6'nın her tutarı kayardı — eski testler böylece
// devralmanın doğru kablolandığının da denetimi olur.
//
// İndirim tarafında aynı korumayı promosyonun HEDEF KURALI sağlar: Faz 7
// senaryosunun otomatik promosyonu yalnızca kendi varyantlarına iner
// (bkz. indirim_test.go), bu yüzden diğer senaryoların indirimi sıfır kalır.
// Kural konmasaydı tek bir otomatik promosyon, testlerin çalışma sırasına göre
// başka senaryoların toplamlarını da düşürürdü.
//
// # Neden internal/workflows altında değil
//
// ADR 0006, internal/workflows altındaki HİÇBİR paketin internal/modules'ü
// import etmesine izin vermez ve internal/arch'taki
// TestWorkflowlarModulleriImportEtmez bunu dosya sisteminde denetler — test
// dosyaları dahil. Bu paketin işi ise tam tersidir: gerçek modülleri kurmak,
// gerçek migration'ları uygulamak ve akışları o zeminin üstünde koşturmak.
// İkisi aynı ağaçta yaşayamaz, bu yüzden paket internal/e2e altındadır ve
// ADR 0006'nın kapsamı dışındadır.
//
// # Kurulum
//
// Testler tek bir PostgreSQL konteyneri paylaşır (testcontainers) ve kurulum
// cmd/server/main.go'daki sırayı ÖRNEK ALIR: çekirdek servisler container'a
// adla kaydedilir (core.db, core.link, core.query, core.eventbus,
// core.workflow), çekirdek migration'ları uygulanır, modüller
// [module.Registry] ile ayağa kaldırılır ve akışlar container'dan ADLA çözülen
// yüzeylerle kurulur. Kurulumun gerçek olması testin bütün değeridir: sahte bir
// bağımlılıkla geçen bir hesap, üretimde aynı hesabı yapacağını kanıtlamaz.
//
// Zemin ayrıca ARAMA EKLENTİSİNİ de üretimdeki sırayla kurar (Install ->
// Bootstrap -> Start): eklentinin getirdiği modül çekirdek modüllerle aynı
// yaşam döngüsünden geçer ve abonelikleri ilk üründen önce bağlanır. Kararın
// tamamı ve neden ödeme eklentisinin zemine KURULMADIĞI arama_test.go
// dosyasının başındadır.
//
// Saga motoru BELLEK İÇİ değil, üretimdeki gibi pgstore üzerinde koşar
// (core.workflow.store). Fark testin gördüğü şeyi değiştirir: idempotency
// anahtarı ve yürütme durumu gerçekten veritabanına yazılır, dolayısıyla "aynı
// sepet iki kez tamamlanamaz" iddiası süreç içi bir haritanın değil, kalıcı
// bir kaydın davranışını sınar.
//
// # Beklenen tutarlar neden elle yazılıyor
//
// Her senaryodaki ara toplam, vergi ve genel toplam testin İÇİNDE elle
// hesaplanmış SABİTLERDİR. Üretim kodunun formülünü testte tekrar etmek
// (örneğin vergiyi yine "taban × oran / 10000" ile hesaplamak) aynı hatayı iki
// yerde birden yapmak olurdu ve test kör kalırdı.
//
// # Para
//
// Tüm tutarlar TAM SAYI minor unit'tir (plan Bölüm 8); testte de float yoktur.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/core/workflow"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	authmod "github.com/bdrtr/gobit/internal/modules/auth"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	b2bmod "github.com/bdrtr/gobit/internal/modules/b2b"
	b2bsvc "github.com/bdrtr/gobit/internal/modules/b2b/service"
	cartmod "github.com/bdrtr/gobit/internal/modules/cart"
	cartapi "github.com/bdrtr/gobit/internal/modules/cart/api"
	cartsvc "github.com/bdrtr/gobit/internal/modules/cart/service"
	customermod "github.com/bdrtr/gobit/internal/modules/customer"
	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	filemod "github.com/bdrtr/gobit/internal/modules/file"
	fulfillmentmod "github.com/bdrtr/gobit/internal/modules/fulfillment"
	fulfillmentsvc "github.com/bdrtr/gobit/internal/modules/fulfillment/service"
	inventorymod "github.com/bdrtr/gobit/internal/modules/inventory"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	notificationmod "github.com/bdrtr/gobit/internal/modules/notification"
	ordermod "github.com/bdrtr/gobit/internal/modules/order"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	paymentmod "github.com/bdrtr/gobit/internal/modules/payment"
	paymentsvc "github.com/bdrtr/gobit/internal/modules/payment/service"
	pricingmod "github.com/bdrtr/gobit/internal/modules/pricing"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmod "github.com/bdrtr/gobit/internal/modules/product"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
	promotionmod "github.com/bdrtr/gobit/internal/modules/promotion"
	promotionsvc "github.com/bdrtr/gobit/internal/modules/promotion/service"
	regionmod "github.com/bdrtr/gobit/internal/modules/region"
	regionsvc "github.com/bdrtr/gobit/internal/modules/region/service"
	taxmod "github.com/bdrtr/gobit/internal/modules/tax"
	taxsvc "github.com/bdrtr/gobit/internal/modules/tax/service"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// postgresImage testlerin paylaştığı veritabanı imajıdır; modül entegrasyon
// testleriyle AYNI sürüm kullanılır ki şema davranışı iki yerde ayrışmasın.
const postgresImage = "postgres:16-alpine"

// Çekirdek servislerin container'daki adları.
//
// Adlar cmd/server/main.go'dakilerin AYNISIDIR ve tekrarlanmaları bilinçlidir:
// sepet akışları bağımlılıklarını derleme zamanında değil, tam olarak bu
// dizelerle çözer (ADR 0006). Buradaki bir yazım hatası üretimdeki bir yazım
// hatasıyla aynı sonucu verir ve test onu görmelidir.
const (
	svcDB       = "core.db"
	svcLink     = "core.link"
	svcQuery    = "core.query"
	svcEventBus = "core.eventbus"
	// svcWorkflow saga yürütücüsüdür; sipariş tamamlama akışı onu bu adla
	// çözer (checkoutwf.ServiceWorkflow).
	svcWorkflow = "core.workflow"
	// svcWorkflowStore yürütme durumunun KALICI deposudur.
	svcWorkflowStore = "core.workflow.store"
	// svcAuthInterop kimlik doğrulayıcının container'daki adıdır; çekirdek
	// onu bu ADLA çözer ve auth modülünü import etmez (ADR 0001).
	svcAuthInterop = "auth.interop"
)

// Faz 8 kimlik fikstürünün sabitleri.
//
// Sır 32 karakterden UZUNDUR: auth modülü kısa sırrı reddetmez ama uyarı
// loglar ve testin çıktısı iddialarla kalmalıdır.
const (
	// testJWTSecret uçtan uca testlerin imza sırrıdır.
	testJWTSecret = "e2e-test-imza-sirri-32-bayttan-uzun-olmali"
	// yoneticiEposta fikstür yöneticisinin e-postasıdır.
	yoneticiEposta = "yonetici@gobit.test"
	// yoneticiParola fikstür yöneticisinin parolasıdır.
	yoneticiParola = "cok-gizli-parola-42"
	// testKanalAdi publishable anahtarın bağlandığı satış kanalıdır.
	testKanalAdi = "e2e-vitrin"
	// testHizSiniri paylaşılan router'ın dakikalık istek sınırıdır.
	//
	// Üretim varsayılanından (600) bilinçli olarak YÜKSEKTİR: yığının şekli
	// üretimdekiyle aynı kalsın ama sınır, senaryoların ortasında tetiklenip
	// alakasız testleri düşürmesin. Sınırın KENDİ davranışı kendi router'ında
	// sınanır (bkz. sertlestirme_test.go).
	testHizSiniri = 1_000_000
)

// Vergisi otomatik uygulanan bölgenin fikstür sabitleri.
//
// Ülke ve para birimi kodları region modülünün TOHUM verisinden gelir
// (000002_region_seed); test yalnızca bölgeyi kurar ve ülkeyi ona bağlar.
const (
	// vergiliUlke vergili bölgeye bağlanan ülkedir (ISO 3166-1 alpha-2).
	vergiliUlke = "TR"
	// vergiliParaBirimi vergili bölgenin para birimidir (ISO 4217).
	vergiliParaBirimi = "TRY"
	// vergiOraniBps vergili bölgenin baz puan oranıdır: 2000 = %20.
	vergiOraniBps int32 = 2000
)

// Vergisi otomatik uygulanMAYAN bölgenin fikstür sabitleri.
//
// Oran bilinçli olarak SIFIR DEĞİLDİR: bölge sıfır oran taşıdığı için değil,
// otomatik vergiyi kapattığı için verginin sıfır çıkması gerekir. Sıfır oranla
// kurulsaydı test iki durumu ayırt edemezdi.
const (
	// vergisizUlke vergisiz bölgeye bağlanan ülkedir.
	vergisizUlke = "DE"
	// vergisizParaBirimi vergisiz bölgenin para birimidir.
	vergisizParaBirimi = "EUR"
	// vergisizOranBps vergisiz bölgenin taşıdığı ama UYGULANMAMASI gereken
	// orandır: 1900 = %19.
	vergisizOranBps int32 = 1900
)

// Vergisi TAX modülünden gelen ikinci bölgenin fikstür sabitleri (Faz 7).
//
// Bölge, [vergiliUlke] bölgesinden YALNIZCA vergi oranıyla ayrılır: para
// birimi bilinçli olarak aynıdır ([vergiliParaBirimi]). Farklı olsaydı aynı
// ürünün fiyatı da değişir ve iki bölgenin vergisi arasındaki farkın orandan
// mı fiyattan mı geldiği ayırt edilemezdi.
const (
	// ikinciVergiUlke ikinci vergi bölgesine bağlanan ülkedir.
	ikinciVergiUlke = "FR"
	// ikinciVergiOraniBps ülkenin TAX modülündeki oranıdır: 1000 = %10.
	ikinciVergiOraniBps int32 = 1000
	// ikinciBolgeRegionOraniBps bölgenin KENDİ (Faz 5) oranıdır: 5000 = %50 ve
	// bölgede otomatik vergi AÇIKTIR.
	//
	// Değer tax'ınkinden bilinçli olarak FARKLIDIR: hesap hâlâ region'ın
	// oranını kullanıyor olsaydı vergi beş katına çıkar ve devralmanın
	// yapılmadığı tek bir sayıda görünürdü.
	ikinciBolgeRegionOraniBps int32 = 5000
)

// Vergi bölgesi TAX modülünde YAPILANDIRILMAMIŞ ülkenin fikstür sabitleri
// (Faz 7).
//
// Bölge otomatik vergiyi AÇIK tutar ve sıfır olmayan bir oran taşır; bu
// bilinçlidir. Hesap "tax cevap veremedi, region'a geri döneyim" deseydi vergi
// bu oranla çıkardı. Sıfır çıkması, tax'ın yetkili cevabının (bu ülkenin vergi
// bölgesi yok) OLDUĞU GİBİ kabul edildiğini kanıtlar.
const (
	// yapilandirilmamisUlke vergi bölgesi kurulmayan ülkedir.
	yapilandirilmamisUlke = "IT"
	// cokUlkeliOranBps çok ülkeli bölgenin REGION oranıdır: %30.
	// Diğer fikstür oranlarından FARKLI seçildi ki tutarın hangi kaynaktan
	// geldiği tek başına ele versin.
	cokUlkeliOranBps int32 = 3000
	// yapilandirilmamisRegionOraniBps bölgenin taşıdığı ama uygulanMAMASI
	// gereken orandır: 1800 = %18.
	yapilandirilmamisRegionOraniBps int32 = 1800
)

// Testlerin paylaştığı zemin. TestMain doldurur, testler yalnızca okur.
var (
	// testPool tüm modüllerin paylaştığı bağlantı havuzudur.
	testPool *db.Pool
	// testDSN migration çağrılarının kullandığı bağlantı adresidir.
	testDSN string
	// kap modüllerin ve akışların çözüldüğü DI kabıdır.
	kap *container.Container
	// baglar çekirdeğin Module Links servisidir; kaba ve Query motoruna
	// verilir, çünkü genişletmeler bağları onun üzerinden gezer.
	baglar link.LinkService
	// testAuthn koruma middleware'ine bağlanan kimlik doğrulayıcıdır.
	//
	// Router, modüller ayağa kalkmadan ÖNCE kurulmak zorundadır (chi,
	// route'lardan sonra r.Use çağrılmasını reddeder), kimlik doğrulayıcı ise
	// auth modülü Register olduğunda doğar. Üretimde de aynı boşluk vardır ve
	// aynı tiple kapatılır (bkz. cmd/server/main.go).
	testAuthn = &corehttp.DeferredAuthenticator{}
	// testRouter modüllerin route'larını taşıyan router'dır.
	//
	// Faz 5 ve Faz 6 senaryoları akışları doğrudan çağırır ve router'a hiç
	// dokunmaz; Faz 7'nin "mağaza yüzeyi" senaryosu ise tam olarak HTTP ucunun
	// davranışını sınar (bkz. kargo_test.go). admin_only bir seçeneğin
	// vitrinde görünmemesi bir SERVİS kararı değil, o ucun sabitlediği bir
	// güven kararıdır ve yalnızca uçtan geçilerek kanıtlanabilir.
	testRouter chi.Router
	// testModuller router'a bağlanan modüllerin TAM listesidir (eklentilerin
	// getirdikleri dâhil). Şema testleri "hangi uçlar anlatıldı" sorusunu
	// yalnızca bu listeden yanıtlayabilir; elle tutulan ikinci bir liste,
	// anlatımı yeni eklenen modülü sessizce kapsam dışı bırakırdı.
	testModuller []module.Module
	// testBelge /openapi.json ucunun sunduğu belgenin TA KENDİSİDİR.
	//
	// Testin ayrı bir kopya kurmaması bilinçlidir: kopya, üretilen şemayı
	// değil testin kendi kurduğu şemayı doğrular ve ikisi ayrıştığında yeşil
	// kalırdı. Değişkenin ayrıca tutulmasının sebebi
	// [openapi.Doc.UnmatchedDescriptions]: hiçbir route ile eşleşmeyen
	// açıklamalar JSON gövdesinde GÖRÜNMEZ, yalnızca belgeden okunur.
	testBelge *openapi.Doc
)

// Faz 8 fikstürünün ürettiği kimlikler; testler yalnızca okur.
var (
	// authSvc auth modülünün servisidir; fikstür kullanıcıyı ve anahtarları
	// bununla kurar.
	authSvc *authsvc.Service
	// yoneticiID fikstür yöneticisinin kimliğidir.
	yoneticiID string
	// gizliAnahtar yönetim yüzeyinde kullanılabilen DÜZ gizli anahtardır.
	gizliAnahtar string
	// publishableAnahtar mağaza yüzeyinin DÜZ publishable anahtarıdır.
	publishableAnahtar string
	// testKanalID publishable anahtarın bağlı olduğu satış kanalıdır.
	testKanalID string
)

// Modül servisleri; hepsi container'dan ADLA çözülür, elle kurulmaz.
var (
	urunSvc    *productsvc.Service
	fiyatSvc   *pricingsvc.Service
	bolgeSvc   *regionsvc.Service
	musteriSvc *customersvc.Service
	sepetSvc   *cartsvc.Service
	stokSvc    *inventorysvc.Service
	siparisSvc *ordersvc.Service
	odemeSvc   *paymentsvc.Service
	// Faz 7 modülleri.
	kargoSvc     *fulfillmentsvc.Service
	promosyonSvc *promotionsvc.Service
	vergiSvc     *taxsvc.Service
	// Bölüm 10 modülü.
	b2bSvc *b2bsvc.Service
)

// kargoYuzeyi fulfillment modülünün modüller arası yüzeyidir
// ("fulfillment.interop", ADR 0006).
//
// Arayüz BURADA yeniden tanımlanır, modülün somut tipi kullanılmaz. Sebep,
// yüzeyin bugün hiçbir tüketicisi olmamasıdır: sipariş saga'sı kargo adımını
// henüz yürütmez, yani modülün interop.go'sunda "tüketici tarafındaki
// karşılığı" diye yazılmış imzaları hiçbir paket derleme zamanında
// sabitlemiyor. Dar arayüzü burada tanımlamak o boşluğu doldurur: imza kayarsa
// container çözümü DÜŞER ve kayma testte görünür.
type kargoYuzeyi interface {
	// ListOptionsJSON bir sepet bağlamı için uygun seçenekleri fiyatlarıyla
	// döner.
	ListOptionsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
	// CreateFulfillment bir sipariş için gönderi açar ve KİMLİĞİNİ döner.
	CreateFulfillment(ctx context.Context, reference, optionID, idempotencyKey string) (string, error)
	// CancelFulfillment gönderiyi iptal eder; saga telafisi budur.
	CancelFulfillment(ctx context.Context, fulfillmentID string) error
	// FulfillmentStatus gönderinin güncel durumunu döner.
	FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error)
}

// vergiYuzeyi vergi modülünün modüller arası yüzeyidir ("tax.interop",
// ADR 0006).
//
// İki metot da yazılıdır, oysa sepet hesabı yalnızca [vergiYuzeyi.CalculateTaxJSON]
// kullanır (bkz. cartwf paketindeki Taxes arayüzü). RateForCountry'nin burada
// olması bilinçlidir: region modülünün geçici RegionTax metodunun birebir
// karşılığı odur ve devralmanın "eski yüzeyin yerine geçen yeni yüzey"
// tarafını hiçbir üretim paketi sabitlemiyor.
type vergiYuzeyi interface {
	// CalculateTaxJSON verilen ülke ve kalemler için vergiyi hesaplar.
	CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
	// RateForCountry bir ülkenin VARSAYILAN oranını baz puan olarak döner;
	// ikinci dönüş değeri yapılandırmanın var olup olmadığıdır.
	RateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error)
}

// Faz 7 yüzeyleri; ikisi de container'dan ADLA çözülür.
var (
	kargoInterop kargoYuzeyi
	vergiInterop vergiYuzeyi
)

// akislar sepet akışlarının ÜRETİM kablolamasıyla kurulmuş örneğidir
// (cartwf.FromContainer). Testte hiçbir köprü ya da sahte yoktur.
var akislar *cartwf.Workflows

// siparisAkislari sipariş tamamlama akışının ÜRETİM kablolamasıyla kurulmuş
// örneğidir (checkoutwf.FromContainer).
//
// Ayrı bir değişken olması bilinçlidir: iki akış kümesi aynı container üzerinde
// ama BİRBİRİNDEN habersiz kurulur ve checkout, sepet hesabını kendi içinde
// yeniden kurar (bkz. checkoutwf.FromContainer). Testin ikisini de aynı
// container'dan alması, üretimde de aynı kabın kullanıldığını doğrular.
var siparisAkislari *checkoutwf.Workflows

// Fikstür bölgelerinin kimlikleri.
var (
	vergiliBolgeID  string
	vergisizBolgeID string
	// ikinciVergiBolgeID vergisi tax modülünden gelen ikinci bölgedir.
	ikinciVergiBolgeID string
	// yapilandirilmamisBolgeID ülkesinin tax modülünde vergi bölgesi
	// BULUNMAYAN bölgedir.
	yapilandirilmamisBolgeID string
	// cokUlkeliBolgeID iki ülke taşıyan bölgedir ve verginin REGION'dan
	// hesaplandığı yolu tetikler: sepet hesabı vergi ülkesini bölgeden okur,
	// bölge birden çok ülke taşıdığında hangisinin sorulacağı bilinemez ve
	// tax'a HİÇ sorulmaz. Üretimde son derece sıradan bir yapılandırmadır
	// (çok ülkeli "Avrupa" bölgesi) ve geri düşüş yolunun tek e2e kanıtıdır.
	cokUlkeliBolgeID string
	// cokUlkeliUlkeler bölgenin taşıdığı iki ülkedir.
	cokUlkeliUlkeler = []string{"ES", "PT"}
)

// stokLokasyonID senaryoların PAYLAŞTIĞI stok lokasyonudur.
//
// Lokasyon TestMain'de bir kez kurulur ve tüm testler onu paylaşır; her test
// KENDİ stok kalemini oluşturduğu için seviyeler yine testler arasında
// ayrışmaz.
//
// Depoyu paylaşan senaryolar lokasyonu akışa BİLDİRİR
// (checkoutwf.CompleteCartInput.LocationID): alan artık opsiyoneldir ve boş
// bırakıldığında depo satır başına seçilir, ama bildirildiğinde eski davranış
// birebir korunur ve bu testlerin sınadığı şey depo seçimi değildir. Çok
// depolu yolun kendi kanıtı ayrıdır ve depolarını da kendisi kurar
// (bkz. coklu_depo_test.go).
var stokLokasyonID string

// olayDefteri yayımlanmış "order.placed" olaylarının test tarafındaki kaydıdır.
var olayDefteri = &siparisOlayDefteri{}

// dosyaKoku yüklenen dosyaların yazıldığı kök dizindir (FILE_ROOT karşılığı).
//
// GEÇİCİ bir dizindir ve koşu bitince silinir. Üretimde geçici dizin
// YASAKTIR — yeniden başlatmada sessiz veri kaybı olurdu ve file modülü bu
// yüzden oraya asla düşmez (bkz. file/local paketi). Testte doğru olan tam
// tersidir: koşu bittiğinde diskte hiçbir şey kalmamalıdır, çünkü buradaki
// dosyalar test verisidir ve kalıcı olmaları yalnızca bir sonraki koşuyu
// kirletirdi.
//
// Dizin TestMain'de bir kez kurulur ve tüm testler onu paylaşır; paylaşmak
// güvenlidir çünkü depo anahtarını sağlayıcı üretir ve iki yükleme aynı adı
// asla almaz.
var dosyaKoku string

// TestMain tek bir Postgres konteyneri kaldırır, modülleri ayağa kaldırır ve
// tüm testleri o zeminin üstünde koşturur.
func TestMain(m *testing.M) {
	os.Exit(postgresIleCalistir(m))
}

// postgresIleCalistir konteyneri kaldırıp kurulumu yapar ve çıkış kodunu döner.
//
// os.Exit defer'ları atladığı için ayrı bir fonksiyondadır: konteyner ve havuz
// ancak burada güvenle kapatılabilir.
func postgresIleCalistir(m *testing.M) int {
	// Modüller açılışta slog.Default() kullanır; testin çıktısı hesap
	// iddialarıyla kalsın diye loglar atılır.
	slog.SetDefault(slog.New(slog.DiscardHandler))

	ctx := context.Background()

	// Yükleme kökü konteynerden ÖNCE kurulur ve çıkışta silinir; os.Exit
	// defer'ları atladığı için temizlik ancak bu fonksiyonda güvenlidir
	// (konteyner ve havuzla aynı gerekçe).
	var kokErr error
	if dosyaKoku, kokErr = os.MkdirTemp("", "gobit-e2e-yuklemeler-"); kokErr != nil {
		fmt.Fprintf(os.Stderr, "yükleme kök dizini oluşturulamadı: %v\n", kokErr)

		return 1
	}
	defer func() {
		if rmErr := os.RemoveAll(dosyaKoku); rmErr != nil {
			fmt.Fprintf(os.Stderr, "yükleme kök dizini silinemedi: %v\n", rmErr)
		}
	}()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_e2e"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := zeminiKur(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "zemin kurulamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// zeminiKur container'ı, modülleri, akışları ve bölge fikstürlerini hazırlar.
//
// Sıra cmd/server/main.go ile AYNIDIR ve aynı olması şarttır: modüller
// Register sırasında core.db, core.link ve core.query'yi çözer, dolayısıyla o
// üçü Bootstrap'tan ÖNCE kayıtlı olmalıdır. Sıra değişirse üretimde de
// patlayacak bir kurulum burada patlar — istenen budur.
func zeminiKur(ctx context.Context) error {
	kap = container.New(nil)

	if err := kap.Provide(svcDB, testPool); err != nil {
		return err
	}

	// Çekirdek migration'ları modül migration'larından ÖNCE uygulanır; sepet
	// akışları workflow motorunu kullanmasa da kurulum sırası üretimdekiyle
	// aynı kalmalıdır.
	if err := db.Migrate(ctx, testDSN, pgstore.Migrations(), pgstore.MigrationOwner); err != nil {
		return err
	}

	baglar = link.New(testPool, nil)
	if err := kap.Provide(svcLink, baglar); err != nil {
		return err
	}
	if err := kap.Provide(svcQuery, query.New(baglar, kap, nil)); err != nil {
		return err
	}

	// Saga motoru KALICI depo üzerine kurulur (main.go'daki gibi). Bellek içi
	// motor (workflow.NewInMemory) idempotency korumasını süreç sınırında
	// bırakır; Faz 6'nın "aynı sepet iki kez tamamlanamaz" iddiası tam olarak o
	// korumanın veritabanındaki hâlini sınamak zorundadır.
	kaliciDepo := pgstore.New(testPool, nil)
	if err := kap.Provide(svcWorkflowStore, kaliciDepo); err != nil {
		return err
	}
	if err := kap.Provide(svcWorkflow, workflow.New(kaliciDepo, nil)); err != nil {
		return err
	}

	// Veri yolu ayrı bir değişkende tutulur: "order.placed" abonesi modüller
	// ayağa kalkmadan ÖNCE bağlanmalıdır, aksi hâlde bootstrap sırasında
	// yayımlanan bir olay kaçırılırdı.
	veriYolu := eventbus.NewInMemory(nil)
	if err := kap.Provide(svcEventBus, veriYolu); err != nil {
		return err
	}
	if err := olayDefteri.abone(veriYolu); err != nil {
		return err
	}

	kayit := module.NewRegistry(nil, func(ctx context.Context, src fs.FS, owner string) error {
		return db.Migrate(ctx, testDSN, src, owner)
	})
	// Modül kümesi ve sırası cmd/server/main.go'dakinin aynısıdır. Kurulumun
	// tamamı sınanmalıdır: bir modülü test için ayıklamak, üretimde ancak
	// açılışta görülecek bir çakışmayı testten gizlerdi.
	kayit.Add(productmod.New(productmod.Options{}))
	kayit.Add(pricingmod.New(nil))
	kayit.Add(inventorymod.New())
	kayit.Add(regionmod.New(nil))
	kayit.Add(customermod.New(nil))
	kayit.Add(cartmod.New())
	kayit.Add(paymentmod.New())
	kayit.Add(ordermod.New())
	// Faz 7: kargo, promosyon, vergi. Üçü de main.go'daki SIRAYLA eklenir.
	kayit.Add(fulfillmentmod.New())
	kayit.Add(promotionmod.New(nil))
	kayit.Add(taxmod.New(nil))
	// Bildirim. "order.placed" abonesinin GERÇEKTEN kurulduğu ve siparişin
	// iletişim bilgisini gerçek order modülünden okuyabildiği ancak burada
	// sınanabilir — modülün kendi entegrasyon testinde sipariş yüzeyi
	// TAKLİTTİR ve iki tarafın şeması derleyiciyle denetlenemez (Prensip 2.4).
	//
	// Sağlayıcı olarak kutudan çıkan "log" DEĞİL, bir CASUS seçilir
	// (bkz. bildirim_test.go). Tek sebebi vardır: "bildirimin alıcısı
	// siparişin e-postasıdır" iddiası adresi GÖREN bir yer gerektirir ve adres
	// bilinçli olarak hiçbir yerde saklanmaz — teslim günlüğünde sütunu
	// yoktur, "log" sağlayıcısı da onu loglamaz. Geriye tek yer kalır:
	// sağlayıcının kendisi. Casus, gerçek bir eklenti sağlayıcısının durduğu
	// yerde durur; zincirin geri kalanı üretim kodudur ve kutudan çıkan
	// sağlayıcı da kayıtta kalır.
	kayit.Add(notificationmod.New(notificationmod.Options{ProviderID: bildirimCasusuID}))
	// Dosya. Yüklemenin ürettiği adresin GERÇEKTEN bir ürün görseli olarak
	// kullanılabildiği ancak burada sınanabilir: zincirin iki ucu iki ayrı
	// modüldedir (file yükler, product saklar) ve ikisi birbirini import
	// etmez, yani hiçbir birim testi ikisini aynı anda göremez
	// (bkz. dosya_test.go).
	//
	// Sınır ve izin listesi ÜRETİM VARSAYILANLARIDIR (config sabitleri);
	// testin kendi değerlerini uydurması, e2e'nin bir gün üretimin
	// kabul etmediği bir dosyayı kabul ettiğini "kanıtlamasına" yol açardı.
	// Kök dizin ise zorunlu olarak ayrışır (bkz. [dosyaKoku]).
	kayit.Add(filemod.New(filemod.Options{
		Root:           dosyaKoku,
		MaxUploadBytes: config.DefaultFileMaxUploadBytes,
		AllowedTypes:   strings.Split(config.DefaultFileAllowedTypes, ","),
	}))
	// Faz 8: kimlik.
	kayit.Add(authmod.New(authmod.Options{
		JWTSecret: testJWTSecret,
		JWTTTL:    time.Hour,
		JWTIssuer: "gobit-e2e",
		// Bcrypt maliyeti test için DÜŞÜRÜLÜR: varsayılan maliyet her giriş
		// çağrısına ~100ms ekler ve kimlik senaryoları onlarca giriş yapar.
		// Maliyet parametresinin KENDİSİ burada sınanmaz; parola doğrulamanın
		// davranışı sınanır.
		BcryptCost: bcrypt.MinCost,
	}))
	// Bölüm 10: B2B. ÜRETİMDEKİ gibi kaydedilir, çünkü sınanan şey modülün
	// kendi uçları değil, kaydedilmiş olmasının order modülünün davranışını
	// DEĞİŞTİRMESİDİR: order harcama kuralını "b2b.interop" adıyla container'dan
	// çözer ve kayıt yoksa her müşteriyi sınırsız sayar.
	kayit.Add(b2bmod.New(nil))

	// Router ÜRETİMDEKİ gibi kurulur: koruma yığını (hız sınırı -> kimlik ->
	// idempotency) çekirdekteki tek tanımdan gelir, testin kendi kopyası
	// yoktur. Kopya olsaydı üretimdeki sıra değiştiğinde test hâlâ eski
	// sırayı doğrular ve yeşil kalırdı.
	testRouter = corehttp.NewRouter(corehttp.RouterOptions{
		Version: "e2e",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator:    testAuthn,
			AdminExempt:      []string{authapi.LoginPath},
			Limiter:          corehttp.NewMemoryLimiter(testHizSiniri, time.Minute),
			LimitKey:         corehttp.ClientIPKey,
			IdempotencyStore: corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
			// Muafiyet listesi ÜRETİMDEKİYLE aynı olmalı, yoksa bu dosya
			// uçtan uca bir kurulumu değil, kendi kurduğu başka bir
			// yapılandırmayı sınar. Fark tam olarak burada ısırdı: sepet
			// yaratma üretimde halkadan çıkarıldığında buradaki testler
			// eskisi gibi geçmeye devam etti ve artık var olmayan bir
			// davranışı belgelemeye başladı.
			IdempotencyExempt: []string{graph.Path, cartapi.StoreCartsPath},
		}),
	})

	// Eklentiler modüllerden ÖNCE kurulur (main.go ile aynı sıra): eklentinin
	// GETİRDİĞİ modül de Register/migration/route döngüsünden geçmelidir.
	// Gerekçesi ve zemine kurulmalarının neden mevcut testleri kırmadığı
	// arama_test.go dosyasının başındadır.
	if err := eklentileriKur(ctx, kayit, veriYolu); err != nil {
		return fmt.Errorf("eklentiler kurulamadı: %w", err)
	}

	if err := kayit.Bootstrap(ctx, kap, testRouter); err != nil {
		return err
	}

	// Kimlik doğrulayıcı ancak Bootstrap'tan sonra container'dadır.
	dogrulayici, err := container.Resolve[corehttp.Authenticator](kap, svcAuthInterop)
	if err != nil {
		return fmt.Errorf("kimlik doğrulayıcı çözülemedi: %w", err)
	}
	testAuthn.Bind(dogrulayici)

	// Eklentilerin abonelikleri ve route'ları modüller ayağa kalktıktan SONRA
	// uygulanır; sağlayıcı kayıtları ile abonelikler ancak o anda çözülebilir.
	if err := eklentileriBaslat(ctx); err != nil {
		return fmt.Errorf("eklentiler başlatılamadı: %w", err)
	}

	// Bildirim casusu eklenti sağlayıcılarıyla AYNI aşamada, yani modüller
	// ayağa kalktıktan SONRA kaydedilir: "notification.providers" container'a
	// modülün Register'ında konur, daha erken denemek hiçbir şeyin gerçekten
	// eksik olmadığı bir hatayla zemini düşürürdü.
	if err := bildirimCasusunuKur(); err != nil {
		return fmt.Errorf("bildirim casusu kurulamadı: %w", err)
	}

	// OpenAPI ucu da üretimdeki gibi kurulur (Faz 9): yol, metod ve güvenlik
	// router ağacından okunur, GÖVDE şemaları ise modüllerin anlatımından
	// gelir. Anlatım kancasının burada da işletilmesi zorunludur —
	// openapi.New tek başına bağlansaydı e2e şeması gövdesiz kalır ve
	// "sunucunun sunduğu şema dolu" iddiası üretimde hiç var olmayan bir
	// kurulumu sınamış olurdu.
	//
	// Modül listesi registry'den OKUNUR (main.go ile aynı gerekçe):
	// eklentilerin getirdiği modüller yalnızca orada görünür.
	testModuller = kayit.Modules()
	testBelge = belgeyiAnlat("gobit API", "e2e", testModuller)
	testRouter.Get(semaYolu, testBelge.Handler(testRouter))

	if err := modulServisleriniCoz(); err != nil {
		return err
	}

	var kurulumErr error
	if akislar, kurulumErr = sepetAkislariniKur(); kurulumErr != nil {
		return fmt.Errorf("sepet akışları kurulamadı: %w", kurulumErr)
	}
	if siparisAkislari, kurulumErr = siparisAkislariniKur(); kurulumErr != nil {
		return fmt.Errorf("sipariş tamamlama akışı kurulamadı: %w", kurulumErr)
	}

	if err := bolgeFiksturleriniKur(ctx); err != nil {
		return err
	}
	if err := vergiFiksturleriniKur(ctx); err != nil {
		return err
	}
	if err := kimlikFiksturunuKur(ctx); err != nil {
		return err
	}
	return stokLokasyonuKur(ctx)
}

// kimlikFiksturunuKur Faz 8 senaryolarının paylaştığı kimlikleri üretir.
//
// Kimlikler HTTP'den değil SERVİSTEN kurulur ve bu bilinçlidir: yönetim
// uçlarının kendisi artık korumalıdır, yani ilk yöneticiyi HTTP'den yaratmanın
// yolu yoktur. Gerçek bir kurulumda da ilk yönetici bir tohum (seed) adımıyla
// doğar; test o adımı taklit eder.
//
// Üretilenler:
//   - parolası olan bir yönetim kullanıcısı (giriş senaryoları),
//   - tam yetkili bir GİZLİ anahtar (jetonsuz yönetim erişimi),
//   - bir satış kanalı ve ona bağlı bir PUBLISHABLE anahtar (mağaza yüzeyi).
func kimlikFiksturunuKur(ctx context.Context) error {
	yonetici, err := authSvc.CreateUser(ctx, authsvc.CreateUserInput{
		Email:     yoneticiEposta,
		FirstName: "E2E",
		LastName:  "Yönetici",
	}, yoneticiParola)
	if err != nil {
		return fmt.Errorf("yönetim kullanıcısı kurulamadı: %w", err)
	}
	yoneticiID = yonetici.ID

	_, gizliAnahtar, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeySecret,
		Title:     "e2e gizli anahtar",
		CreatedBy: yoneticiID,
	})
	if err != nil {
		return fmt.Errorf("gizli api anahtarı kurulamadı: %w", err)
	}

	kanal, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        testKanalAdi,
		Description: "uçtan uca test vitrini",
	})
	if err != nil {
		return fmt.Errorf("satış kanalı kurulamadı: %w", err)
	}
	testKanalID = kanal.ID

	_, publishableAnahtar, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:            models.APIKeyPublishable,
		Title:           "e2e publishable anahtar",
		CreatedBy:       yoneticiID,
		SalesChannelIDs: []string{testKanalID},
	})
	if err != nil {
		return fmt.Errorf("publishable api anahtarı kurulamadı: %w", err)
	}

	return nil
}

// modulServisleriniCoz fikstürlerin kullanacağı modül servislerini container'dan
// ADLA çözer.
//
// Servisler modül nesnelerinden (örn. cartmod.Module.Service()) DEĞİL,
// container'dan alınır: testin kullandığı servis, akışların kullandığı servisin
// TA KENDİSİ olmalıdır. İki ayrı örnek olsaydı test kendi yazdığını okur ve
// akışın gerçekten aynı sepete dokunduğunu kanıtlayamazdı.
func modulServisleriniCoz() error {
	var err error
	if urunSvc, err = container.Resolve[*productsvc.Service](kap, productmod.ServiceName); err != nil {
		return err
	}
	if fiyatSvc, err = container.Resolve[*pricingsvc.Service](kap, pricingmod.ServiceName); err != nil {
		return err
	}
	if bolgeSvc, err = container.Resolve[*regionsvc.Service](kap, regionmod.ServiceName); err != nil {
		return err
	}
	if musteriSvc, err = container.Resolve[*customersvc.Service](kap, customermod.ServiceName); err != nil {
		return err
	}
	if sepetSvc, err = container.Resolve[*cartsvc.Service](kap, cartmod.ServiceName); err != nil {
		return err
	}
	if stokSvc, err = container.Resolve[*inventorysvc.Service](kap, inventorymod.ServiceName); err != nil {
		return err
	}
	if siparisSvc, err = container.Resolve[*ordersvc.Service](kap, ordermod.ServiceName); err != nil {
		return err
	}
	if odemeSvc, err = container.Resolve[*paymentsvc.Service](kap, paymentmod.ServiceName); err != nil {
		return err
	}
	if kargoSvc, err = container.Resolve[*fulfillmentsvc.Service](kap, fulfillmentmod.ServiceName); err != nil {
		return err
	}
	if promosyonSvc, err = container.Resolve[*promotionsvc.Service](kap, promotionmod.ServiceName); err != nil {
		return err
	}
	if vergiSvc, err = container.Resolve[*taxsvc.Service](kap, taxmod.ServiceName); err != nil {
		return err
	}
	if authSvc, err = container.Resolve[*authsvc.Service](kap, authmod.ServiceName); err != nil {
		return err
	}
	if b2bSvc, err = container.Resolve[*b2bsvc.Service](kap, b2bmod.ServiceName); err != nil {
		return err
	}

	// Yüzeyler DAR ARAYÜZLE çözülür (bkz. [kargoYuzeyi], [vergiYuzeyi]); somut
	// tiple çözmek imza uyumunu hiç sınamazdı.
	if kargoInterop, err = container.Resolve[kargoYuzeyi](kap, fulfillmentmod.InteropName); err != nil {
		return err
	}
	vergiInterop, err = container.Resolve[vergiYuzeyi](kap, taxmod.InteropName)
	return err
}

// sepetAkislariniKur sepet akışlarını ÜRETİM kablolamasıyla kurar ve
// container'a KAYDEDER.
//
// [cartwf.FromContainer] altı yüzeyi de container'dan adla çözer; cart tarafı
// "cart.interop" adıyla kayıtlı ilkel yüzeydir (ADR 0006). Testte hiçbir köprü
// ya da sahte yoktur: burada bir uyumsuzluk çıkarsa üretimde de çıkar.
//
// # Kayıt neden ZORUNLU
//
// Akış yalnızca bu dosyanın değişkenine yazılsaydı, testler onu çağırabilir
// ama MAĞAZA UÇLARI çağıramazdı: cart modülünün vitrin satır uçları akışı
// container'dan [cartwf.InteropName] adıyla çözer ve bulamazsa KAPALI
// arızalanır. Kayıt cmd/server'daki registerWorkflows'un aynısıdır; olmasaydı
// e2e, üretimde çalışan bir kurulumu değil yalnızca akışın kendisini sınardı.
func sepetAkislariniKur() (*cartwf.Workflows, error) {
	akislar, err := cartwf.FromContainer(kap)
	if err != nil {
		return nil, err
	}
	if err := kap.Provide(cartwf.InteropName, cartwf.NewInterop(akislar)); err != nil {
		return nil, err
	}
	return akislar, nil
}

// siparisAkislariniKur sipariş tamamlama akışını ÜRETİM kablolamasıyla kurar ve
// container'a KAYDEDER.
//
// [checkoutwf.FromContainer] yedi yüzeyi container'dan adla çözer
// (cart.interop, inventory.interop, order.interop, payment.interop, core.link,
// core.query, core.workflow) ve sepet hesabını AYRICA aynı container üzerinde
// kendisi kurar. Testte hiçbir köprü ya da sahte yoktur: buradaki bir
// uyumsuzluk üretimde de açılışta patlar.
//
// Kaydın gerekçesi [sepetAkislariniKur] ile aynıdır: POST
// /store/v1/carts/{id}/complete ucu akışı [checkoutwf.InteropName] adıyla
// çözer.
func siparisAkislariniKur() (*checkoutwf.Workflows, error) {
	akis, err := checkoutwf.FromContainer(kap)
	if err != nil {
		return nil, err
	}
	if err := kap.Provide(checkoutwf.InteropName, checkoutwf.NewInterop(akis)); err != nil {
		return nil, err
	}
	return akis, nil
}

// stokLokasyonuKur senaryoların paylaştığı tek stok lokasyonunu hazırlar.
//
// Lokasyon test başına değil TestMain'de kurulur: paylaşılması güvenlidir çünkü
// stok SEVİYESİ (kalem, lokasyon) çiftine yazılır ve her test kendi kalemini
// oluşturur. Ülke kodu vergili bölgeninkiyle aynı seçilmiştir ki fikstür
// gerçekçi kalsın; akış lokasyonun ülkesini bugün kullanmaz.
func stokLokasyonuKur(ctx context.Context) error {
	lokasyon, err := stokSvc.CreateStockLocation(ctx, inventorysvc.CreateStockLocationInput{
		Name:        "E2E Ana Depo",
		CountryCode: vergiliUlke,
	})
	if err != nil {
		return err
	}
	stokLokasyonID = lokasyon.ID
	return nil
}

// bolgeFiksturleriniKur senaryoların paylaştığı dört bölgeyi hazırlar.
//
// Bölgeler TestMain'de bir kez kurulur çünkü bir ülke aynı anda tek bir bölgeye
// bağlanabilir; test başına yeniden kurmak ikinci çağrıda çakışırdı.
//
// Her bölge TEK bir ülkeye bağlanır ve bu Faz 7'de bir gerekliliktir: sepet
// hesabı vergi ülkesini bölgeden okur ve bölge birden çok ülke taşıyorsa
// hangisinin sorulacağı bilinemediği için tax'a HİÇ sorulmaz (bkz. cartwf
// countryForRegion). Çok ülkeli bir fikstür, tax modülünün cevabını sınayan
// her senaryoyu sessizce region yoluna düşürürdü.
//
// Son iki bölge Faz 7 içindir ve ikisi de otomatik vergiyi AÇIK tutar: bu
// sayede "vergi region'dan mı tax'tan mı geliyor" sorusu, tutarın kendisiyle
// yanıtlanabilir.
func bolgeFiksturleriniKur(ctx context.Context) error {
	vergili, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Vergili Bölge",
		CurrencyCode:   vergiliParaBirimi,
		AutomaticTaxes: true,
		TaxRate:        vergiOraniBps,
	})
	if err != nil {
		return err
	}
	if _, err := bolgeSvc.AddCountryToRegion(ctx, vergili.ID, vergiliUlke); err != nil {
		return err
	}
	vergiliBolgeID = vergili.ID

	vergisiz, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Vergisiz Bölge",
		CurrencyCode:   vergisizParaBirimi,
		AutomaticTaxes: false,
		TaxRate:        vergisizOranBps,
	})
	if err != nil {
		return err
	}
	if _, err := bolgeSvc.AddCountryToRegion(ctx, vergisiz.ID, vergisizUlke); err != nil {
		return err
	}
	vergisizBolgeID = vergisiz.ID

	ikinci, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E İkinci Vergi Bölgesi",
		CurrencyCode:   vergiliParaBirimi,
		AutomaticTaxes: true,
		TaxRate:        ikinciBolgeRegionOraniBps,
	})
	if err != nil {
		return err
	}
	if _, err := bolgeSvc.AddCountryToRegion(ctx, ikinci.ID, ikinciVergiUlke); err != nil {
		return err
	}
	ikinciVergiBolgeID = ikinci.ID

	yapilandirilmamis, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Vergisi Yapılandırılmamış Bölge",
		CurrencyCode:   vergiliParaBirimi,
		AutomaticTaxes: true,
		TaxRate:        yapilandirilmamisRegionOraniBps,
	})
	if err != nil {
		return err
	}
	if _, err := bolgeSvc.AddCountryToRegion(ctx, yapilandirilmamis.ID, yapilandirilmamisUlke); err != nil {
		return err
	}
	yapilandirilmamisBolgeID = yapilandirilmamis.ID

	// Çok ülkeli bölge: region oranı %30, tax modülünde HİÇBİR ŞEY yok.
	// İki ülke taşıdığı için ülke çözülemez ve hesap region'a düşer.
	cokUlkeli, err := bolgeSvc.CreateRegion(ctx, regionsvc.CreateRegionInput{
		Name:           "E2E Çok Ülkeli Bölge",
		CurrencyCode:   vergisizParaBirimi,
		AutomaticTaxes: true,
		TaxRate:        cokUlkeliOranBps,
	})
	if err != nil {
		return fmt.Errorf("çok ülkeli bölge oluşturulamadı: %w", err)
	}
	for _, ulke := range cokUlkeliUlkeler {
		if _, err := bolgeSvc.AddCountryToRegion(ctx, cokUlkeli.ID, ulke); err != nil {
			return fmt.Errorf("%s ülkesi çok ülkeli bölgeye eklenemedi: %w", ulke, err)
		}
	}
	cokUlkeliBolgeID = cokUlkeli.ID

	return nil
}

// vergiFiksturleriniKur tax modülündeki vergi bölgelerini ve oranlarını
// hazırlar (Faz 7).
//
// # Neden ülke BAŞINA tek kök ve tek varsayılan oran
//
// tax modülü bir ülkeye ikinci kök bölge ya da bir bölgeye ikinci varsayılan
// oran yazılmasını reddeder; fikstür bu yüzden TestMain'de bir kez kurulur.
// Test başına kurulsaydı ikinci çağrı errors.Conflict alırdı.
//
// # Hangi ülkeye NE kuruluyor
//
//   - [vergiliUlke] -> %20. Faz 5 ve Faz 6 senaryolarının bütün tutarları bu
//     orana dayanır; devralmadan sonra AYNI sayıların çıkması için yeni
//     yetkilinin eskisiyle aynı cevabı vermesi gerekir.
//   - [ikinciVergiUlke] -> %10. İki ülkenin FARKLI vergi üretmesi buradan
//     görünür.
//   - [yapilandirilmamisUlke] -> HİÇBİR ŞEY. Bölgesi olmayan ülkenin ne
//     yaptığı ancak yapılandırma yokluğuyla sınanabilir.
//
// Bölgelerin sağlayıcısı boş bırakılır: kök bölgede boş sağlayıcı "yerel
// hesaplama" demektir ve dış bir vergi servisi bu testin konusu değildir.
func vergiFiksturleriniKur(ctx context.Context) error {
	vergiliKok, err := vergiSvc.CreateTaxRegion(ctx, taxsvc.CreateTaxRegionInput{
		CountryCode: vergiliUlke,
	})
	if err != nil {
		return err
	}
	if _, err := vergiSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: vergiliKok.ID,
		Name:        "E2E KDV",
		RateBps:     vergiOraniBps,
		IsDefault:   true,
	}); err != nil {
		return err
	}

	ikinciKok, err := vergiSvc.CreateTaxRegion(ctx, taxsvc.CreateTaxRegionInput{
		CountryCode: ikinciVergiUlke,
	})
	if err != nil {
		return err
	}
	if _, err := vergiSvc.CreateTaxRate(ctx, taxsvc.CreateTaxRateInput{
		TaxRegionID: ikinciKok.ID,
		Name:        "E2E TVA",
		RateBps:     ikinciVergiOraniBps,
		IsDefault:   true,
	}); err != nil {
		return err
	}

	return nil
}
