// Package notification bildirim modülüdür (plan Bölüm 5.6).
//
// Sorumluluğu tek cümleyle: bir olay müşteriye bildirilmeliyse bunu SEÇİLİ
// sağlayıcıya yaptırmak ve denemeyi kalıcı bir günlüğe yazmak. Modül
// notification_deliveries verisinin TEK yazma yetkilisidir (Prensip 2.3).
//
// # Sağlayıcı soyutlaması
//
// E-posta/SMS servisiyle konuşan taraf modül değil, internal/core/provider'daki
// NotificationProvider sözleşmesini karşılayan bir SAĞLAYICIDIR. Modül
// sağlayıcıları kimlikleriyle bir kayıtta tutar ([service.ProviderRegistry]) ve
// gönderim anında ADLA çözer. Kutudan çıkan tek sağlayıcı, hiçbir yere
// göndermeyen ve bunu adıyla söyleyen "log" sağlayıcısıdır
// (internal/modules/notification/logonly); eklenti sistemi, çekirdeğe ve bu
// modüle dokunmadan container'daki kayda kendi sağlayıcısını ekleyebilir
// (coreplugin.Host.RegisterNotificationProvider).
//
// Hangi sağlayıcının kullanılacağını NOTIFICATION_PROVIDER seçer. Adın
// GERÇEKTEN kayıtlı olup olmadığı burada doğrulanamaz — eklenti sağlayıcıları
// modüller ayağa kalktıktan SONRA kaydedilir — ve denetim bu yüzden kompozisyon
// kökündedir (cmd/server): bilinmeyen bir ad açılışı DURDURUR.
//
// # "order.placed" abonesi
//
// Modülün tek yazma tetikleyicisi bir olaydır. Abonelik Register sırasında
// kurulur ve işleyici [service.Service.OrderPlaced]'dir. E-posta OLAYDAN
// GELMEZ: olay yükü kişisel veri taşımaz ve adres, "order.interop" yüzeyinden
// siparişin kendisinden okunur (bkz. service/orders.go).
//
// # Neyi bilmez
//
// Modül hiçbir modülü import etmez ve siparişin ne olduğunu bilmez;
// notification_deliveries.reference serbest bir metindir, foreign key
// DEĞİLDİR (Prensip 2.2). Şablonun METNİNİ de bilmez: bildirimin nasıl
// göründüğüne sağlayıcı karar verir.
//
// # Dışarıya açtığı yüzeyler
//
//   - "notification.service" — modül içi zengin yüzey (domain tipleriyle).
//   - "notification.providers" — sağlayıcı kaydı; eklentiler buraya sağlayıcı
//     ekler.
//   - GET /admin/v1/notifications — teslim günlüğünün okuma ucu.
//
// Modüller arası bir "interop" yüzeyi ve Query sağlayıcısı BİLİNÇLİ OLARAK
// YOKTUR: teslim günlüğünü okuyan başka bir modül yoktur ve günlük,
// birleştirilebilir bir varlık değil bir defterdir. İkisini şimdiden açmak,
// tüketicisi olmayan iki sözleşme üretirdi — sözleşmeye giren alan bir daha
// çıkarılamaz.
package notification

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/notification/api"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	"github.com/bdrtr/gobit/internal/modules/notification/repository"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "notification"

// ServiceName modül servisinin container'daki adıdır.
const ServiceName = ModuleName + ".service"

// ProvidersName sağlayıcı kaydının container'daki adıdır.
//
// Eklenti sistemi kendi NotificationProvider'ını bu kaydı çözüp ekler; modülün
// kodunu değiştirmesi gerekmez. Değer coreplugin.NotificationProvidersName ile
// AYNI olmalıdır ve uyum internal/arch testiyle sabitlenmiştir.
const ProvidersName = ModuleName + ".providers"

// DefaultProviderID sağlayıcı seçilmediğinde kullanılan kimliktir.
//
// Değer logonly paketinden gelir: config'in varsayılanı ("log") ile sağlayıcının
// kimliği ayrışırsa kurulum, hiçbir sağlayıcı bulamayan bir bildirim yoluyla
// açılırdı.
const DefaultProviderID = logonly.ID

// Container'da çözülen çekirdek servislerin adları.
const (
	svcDB       = "core.db"
	svcEventBus = "core.eventbus"
)

// Hata kodları.
const (
	codeSetupFailed      = "notification_module_setup_failed"
	codeProviderRegister = "notification_module_provider_register_failed"
	codeSubscribeFailed  = "notification_module_subscribe_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Options modülün kurulum ayarlarıdır.
type Options struct {
	// ProviderID gönderimde kullanılacak sağlayıcının kimliğidir
	// (NOTIFICATION_PROVIDER). Boş verilirse [DefaultProviderID] uygulanır.
	ProviderID string
	// Logger nil verilirse slog.Default kullanılır.
	Logger *slog.Logger
}

// Module notification modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	opts      Options
	svc       *service.Service
	providers *service.ProviderRegistry
	handler   *api.Handler
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca bu modülün ucu belgeden sessizce düşerdi.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir notification modülü üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New(opts Options) *Module {
	if opts.ProviderID == "" {
		opts.ProviderID = DefaultProviderID
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &Module{opts: opts}
}

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi ve sağlayıcı kaydını container'a kaydeder, "order.placed"
// aboneliğini kurar.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi). core.db ve
// core.eventbus modüller ayağa kalkmadan önce main.go'da hazır değer olarak
// kaydedildiği için burada çözülmeleri güvenlidir ve eksiklikleri modülün hiç
// çalışamayacağı bir kurulum hatasıdır — sessizce ertelenmez.
//
// SİPARİŞ YÜZEYİ burada çözülmez: "order.interop" bu anda container'da
// olmayabilir ve çözmeye çalışmak, hiçbir şeyin gerçekten eksik olmadığı bir
// hatayla açılışı düşürürdü. Çözüm ilk olaya ertelenir (bkz.
// [service.NewOrderContacts]).
//
// Varsayılan sağlayıcı ([logonly.Provider]) da burada kaydedilir; seçili
// sağlayıcı o değilse bile kayıtlı kalır, çünkü kayıt bir listedir, seçim
// değil.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, svcDB)
	}
	// Dar arayüzle çözülür: modül yalnızca ABONE OLUR, yayımlamaz ve veri
	// yolunu kapatmaz (bkz. service.EventSubscriber).
	bus, err := container.Resolve[service.EventSubscriber](c, svcEventBus)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü olay veri yolunu çözemedi (%q)", ModuleName, svcEventBus)
	}

	log := m.opts.Logger.With("modul", ModuleName)

	providers := service.NewProviderRegistry()
	if err := providers.Register(logonly.New(log)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"%s modülü varsayılan sağlayıcıyı kaydedemedi", ModuleName)
	}

	svc, err := service.New(service.Options{
		Store:      repository.New(pool.Pool()),
		Providers:  providers,
		ProviderID: m.opts.ProviderID,
		Contacts:   service.NewOrderContacts(c),
		Logger:     log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}

	// Abonelik Register'da kurulur (module.Module sözleşmesinin öngördüğü yer).
	// Kurulamaması AÇILIŞI DURDURUR: hiçbir olay almayan bir bildirim modülü,
	// sessizce hiç e-posta göndermez ve bu ancak müşteriler onay beklerken
	// fark edilir.
	if err := bus.Subscribe(service.EventOrderPlaced, svc.OrderPlaced); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSubscribeFailed,
			"%s modülü %q olayına abone olamadı", ModuleName, service.EventOrderPlaced)
	}

	m.svc = svc
	m.providers = providers
	m.handler = api.New(svc)

	log.DebugContext(ctx, "notification modülü kaydedildi",
		"servis", ServiceName,
		"saglayicilar", providers.IDs(),
		"secili_saglayici", m.opts.ProviderID,
		"olay", service.EventOrderPlaced,
	)
	return nil
}

// Routes modülün admin ucunu router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("notification modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.handler.Routes(r)
}

// Describe modülün yönetim ucunu OpenAPI belgesine işler.
//
// [Module.Routes]'un tersine Register kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service modülün servisini döner; Register çağrılmadıysa nil'dir.
//
// Testler ve gömülü kullanım içindir; normal akışta servis container'dan
// [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// Providers modülün sağlayıcı kaydını döner; Register çağrılmadıysa nil'dir.
//
// Gömen uygulama kendi sağlayıcısını buraya ekleyebilir; normal akışta kayıt
// container'dan [ProvidersName] adıyla çözülür.
func (m *Module) Providers() *service.ProviderRegistry { return m.providers }

// mustSub alt dizini açar; açılamazsa panikler.
//
// Panik burada güvenlidir: dizin adı derleme zamanında sabittir ve go:embed
// dosyaların varlığını zaten derleme zamanında doğrulamıştır. Yine de sessizce
// nil dönmek, modülün migration'sız (yani tablosuz) ayağa kalkması demek
// olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("notification: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
