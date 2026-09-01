// Package order sipariş modülüdür (plan Bölüm 6, Faz 6).
//
// Sorumluluğu tek cümleyle: bir siparişin NE olduğunu kalıcı olarak bilmek —
// hangi numarayla, hangi bölgede, kimin adına, hangi satırlarla ve hangi
// tutarla verildiği. Modül Order, OrderLineItem, OrderSummary, Return, Exchange
// ve Claim verisinin TEK yazma yetkilisidir (Prensip 2.3).
//
// # Neyi bilmez
//
// Sepeti bilmez. [service.Service.CreateOrder]'a verilen girdi sepetin ANLIK
// GÖRÜNTÜSÜDÜR: satırlar ve toplamlar hesaplanmış hâlde gelir. Görüntüyü kuran
// akış complete_cart WORKFLOW'udur (plan Bölüm 2.5, ADR 0006); bu modül cart'ı
// çağırmaz ve import etmez.
//
// Ödemeyi de bilmez: tahsil edilen ve iade edilen tutar OrderSummary üzerinden,
// ödeme sonucunu bilen akış tarafından yazılır.
//
// Harcama LİMİTİNİ de bilmez: limit b2b modülünün verisidir ve bu modüle
// [SpendingPolicyName] adıyla çözülen dar bir yüzeyden gelir. Bildiği şey
// HARCAMANIN kendisidir — verilmiş siparişlerin toplamı — ve limiti o toplama
// uygulayan taraf bu yüzden burasıdır; kural, siparişin yazıldığı işlemin
// içinde uygulandığında yarışa kapanır (bkz. [service.SpendingPolicy]).
// Bağımlılık OPSİYONELDİR: b2b kurulu değilse hiçbir limit uygulanmaz.
//
// Modül başka HİÇBİR modülü import etmez (Prensip 2.1/2.4, ADR 0001; kural
// .golangci.yml içindeki depguard ve internal/arch testleriyle zorlanır).
// region_id, customer_id, cart_id ve variant_id başka modüllerin kimlikleridir;
// serbest metin olarak saklanır ve foreign key verilmez (Prensip 2.2).
//
// # Dışarıya açtığı yüzeyler
//
//   - "order.service" — modül içi kullanım ve zengin tiplerle okuma.
//   - "order.interop" — saga'nın ve "order.placed" abonelerinin kullandığı
//     İLKEL yüzey (ADR 0006). complete_cart siparişi buradan açar ve telafide
//     buradan iptal eder; bildirim tarafı, olayda BULUNMAYAN e-postayı
//     [service.Interop.OrderContactJSON] ile buradan okur.
//   - "order.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//   - /admin/v1/orders … — yönetim API'si (okuma + durum geçişleri).
//   - /store/v1/orders/{id} — müşteri API'si (YALNIZCA okuma).
//
// # Yayımladığı olaylar
//
// "order.placed" — sipariş oluşturulduğunda (plan Faz 6 DoD). Yükü ve yayım
// politikası için bkz. [service.EventOrderPlaced] ve service/events.go.
//
// # Bildirdiği linkler
//
// Yoktur. Siparişin bölgesi ve müşterisi KENDİ SÜTUNLARINDA durur ve her okuma
// o sütunlardan yapılır (bkz. queries/orders.sql); aynı ilişkiyi bir de link
// tablosunda tutmak satır yazardı, bakım maliyeti doğururdu ve hiçbir okumaya
// hizmet etmezdi (bkz. CHANGELOG, "order_customer/order_region kaldırıldı").
// "order_payment" ve "order_fulfillment" bağlarının sahibi de bu modül
// değildir: bir link tanımı yalnızca BİR kez bildirilebilir (ADR 0005) ve
// tanımı, bağın taşıdığı kaydı yazan taraf — payment, fulfillment — bildirir.
package order

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/order/api"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "order"

// ServiceName modül servisinin container'daki adıdır.
//
// Başka modüller ve workflow'lar (ADR 0001/0006 gereği bu paketi import
// ETMEDEN) sipariş servisine bu adla ulaşır ve KENDİ paketlerinde tanımladıkları
// dar bir arayüzle kullanır.
const ServiceName = ModuleName + ".service"

// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
//
// Servisin kendisinden AYRI kaydedilir: servis order'ın zengin tipleriyle
// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. complete_cart saga'sı
// onu kendi tanımladığı dar arayüzle çözer.
const InteropName = ModuleName + ".interop"

// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// Container'da çözülen çekirdek servislerin adları.
const (
	svcDB       = "core.db"
	svcEventBus = "core.eventbus"
)

// SpendingPolicyName harcama limiti kuralını yayımlayan servisin container'daki
// adıdır (ADR 0001).
//
// Ad b2b modülünündür ve burada DİZE olarak tekrarlanır; modüller birbirini
// import edemez (Prensip 2.4) ve tekrarın bedeli izolasyonun kabul edilen
// bedelidir. Yazım hatası sessiz kalmaz: ad çözülemezse modül b2b'nin hiç
// kurulmadığı sonucuna varır ve limit uygulanmaz — bu yüzden ad değişirse
// [spendingPolicy] belgesindeki "kurulu değil" dalı yanlış tetiklenir. Adın tek
// doğruluk kaynağı b2b modülünün InteropName sabitidir.
const SpendingPolicyName = "b2b.interop"

// codeSetupFailed modülün kablolanamadığını bildiren hata kodudur.
const codeSetupFailed = "order_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module order modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca siparişin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir order modülü üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New() *Module {
	return &Module{}
}

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi, interop yüzeyini ve Query sağlayıcısını container'a
// kaydeder.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi). core.db ve core.eventbus
// modüller ayağa kalkmadan önce main.go'da hazır değer olarak kaydedildiği için
// burada çözülmeleri güvenlidir ve eksiklikleri modülün hiç çalışamayacağı bir
// kurulum hatasıdır — sessizce ertelenmez.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, svcDB)
	}
	// Dar arayüzle çözülür: modül yalnızca YAYIMLAR, abone olmaz ve veri yolunu
	// kapatmaz (bkz. service.EventPublisher).
	bus, err := container.Resolve[service.EventPublisher](c, svcEventBus)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü olay veri yolunu çözemedi (%q)", ModuleName, svcEventBus)
	}

	// Uygulama açılışta slog.SetDefault ile yapılandırılmış logger'ı kurar;
	// modül ayrı bir logger kaydı aramaz.
	log := slog.Default().With("modul", ModuleName)

	svc, err := service.New(service.Options{
		Repo:   repository.New(pool.Pool()),
		Events: bus,
		// Harcama kuralının sağlayıcısı BAŞKA bir modüldür ve bu aşamada henüz
		// kayıtlı olmayabilir; çözüm ilk kullanıma bırakılır
		// (bkz. [spendingPolicy] ve module.Module belgesi).
		Spending: &spendingPolicy{c: c, log: log},
		Logger:   log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// Modüller arası yüzey AYRI bir adla kaydedilir: servisin kendisi order'ın
	// zengin tipleriyle konuşur, bu yüzey ise yalnızca ilkel tiplerle (ADR 0006).
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	// Sağlayıcı adı "<entity>.query" biçimindedir; Query onu bu adla arar ve
	// Entity() ile adın örtüştüğünü doğrular (ADR 0004).
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	m.handler = api.New(svc)
	slog.Default().DebugContext(ctx, "order modülü kaydedildi",
		"servis", ServiceName, "interop", InteropName, "saglayici", ProviderName)
	return nil
}

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("order modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.handler.Routes(r)
}

// Describe modülün yönetim uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// DTO'larından türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi. Hangi uçların anlatılmadığı ve NEDEN anlatılmadığı da
// orada yazılıdır.
//
// [Module.Routes]'un tersine Register kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün
// belgesini de sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service modülün servisini döner; Register çağrılmadıysa nil'dir.
//
// Testler ve gömülü kullanım içindir; normal akışta servis container'dan
// [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub alt dizini açar; açılamazsa panikler.
//
// Panik burada güvenlidir: dizin adı derleme zamanında sabittir ve go:embed
// dosyaların varlığını zaten derleme zamanında doğrulamıştır. Yine de sessizce
// nil dönmek, modülün migration'sız (yani tablosuz) ayağa kalkması demek
// olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("order: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}

// spendingPolicy harcama kuralı sağlayıcısını İLK KULLANIMDA çözen
// sarmalayıcıdır.
//
// # Neden tembel
//
// [module.Module] sözleşmesi Register sırasında BAŞKA modüllerin servislerinin
// henüz kayıtlı olmayabileceğini söyler ve çözümü ilk kullanıma bırakmayı
// şart koşar. Kayıt sırası da bu yüzden önemsizdir: b2b modülü order'dan sonra
// eklenmiş olsa bile ilk sipariş açıldığında çoktan kayıtlıdır.
//
// # Neden OPSİYONEL
//
// [SpendingPolicyName] hiç kayıtlı değilse b2b modülü kurulu değildir; o
// kurulumda "harcama limiti" diye bir kavram yoktur ve doğru cevap kuralsız
// bir kuraldır. Bu, [emptySpendingRule] gövdesiyle verilir — servise nil
// vermek yerine sabit bir cevap dönmek, "politika yok" durumunu servisin
// dallanması gereken bir hâl olmaktan çıkarır.
//
// # Ama SESSİZCE devre dışı KALMAZ
//
// Ad kayıtlı AMA beklenen yüzeyi karşılamıyorsa hata döner ve sipariş açılmaz.
// Bu ayrım önemlidir: "b2b kurulu değil" bir kurulum kararıdır, "b2b kurulu ama
// yüzeyi tanınmıyor" ise bir kablolama hatasıdır ve onu sessizce limitsiz
// alışverişe çevirmek, kuralın en çok gerektiği kurulumda kapanması demek
// olurdu. Karar BİR KEZ verilir ve saklanır; her siparişte yeniden çözmek aynı
// hatayı sonsuza kadar tekrar üretmekten başka bir şey yapmazdı.
type spendingPolicy struct {
	c    *container.Container
	log  *slog.Logger
	once sync.Once
	svc  service.SpendingPolicy
	err  error
}

// emptySpendingRule "bu müşterinin harcama kuralı yok" cevabının gövdesidir.
//
// Şema service.SpendingPolicy belgesinde tanımlıdır; "limited" alanı false
// olduğunda diğer alanlar okunmaz.
var emptySpendingRule = json.RawMessage(`{"limited":false}`)

// Sarmalayıcının servisin beklediği yüzeyi karşıladığı derleme zamanında
// sabitlenir.
var _ service.SpendingPolicy = (*spendingPolicy)(nil)

// SpendingLimitJSON müşterinin harcama kuralını döner.
func (p *spendingPolicy) SpendingLimitJSON(ctx context.Context, customerID string) (json.RawMessage, error) {
	p.once.Do(func() { p.resolve(ctx) })
	if p.err != nil {
		return nil, p.err
	}
	if p.svc == nil {
		return emptySpendingRule, nil
	}
	return p.svc.SpendingLimitJSON(ctx, customerID)
}

// resolve sağlayıcıyı container'dan çözer; sonucu bir kez saklar.
func (p *spendingPolicy) resolve(ctx context.Context) {
	svc, err := container.Resolve[service.SpendingPolicy](p.c, SpendingPolicyName)
	switch {
	case err == nil:
		p.svc = svc
		p.log.InfoContext(ctx, "harcama limiti kuralı bağlandı",
			"saglayici", SpendingPolicyName)
	case errors.IsNotFound(err):
		// Kurulumda b2b modülü yok: limit kavramı da yok.
		p.log.DebugContext(ctx, "harcama limiti sağlayıcısı kayıtlı değil, limit uygulanmayacak",
			"saglayici", SpendingPolicyName)
	default:
		p.err = errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü harcama kuralı sağlayıcısını çözemedi (%q)", ModuleName, SpendingPolicyName)
	}
}
