// Package payment ödeme modülüdür (plan Bölüm 6, Faz 6).
//
// Sorumluluğu tek cümleyle: bir sepet ya da sipariş için PARANIN hangi
// aşamada olduğunu bilmek — bloke mi, çekildi mi, iade mi edildi. Modül
// PaymentCollection, PaymentSession, Payment ve Refund verisinin TEK yazma
// yetkilisidir (Prensip 2.3).
//
// # Sağlayıcı soyutlaması
//
// Ödeme kuruluşuyla konuşan taraf modül değil, core/provider'daki
// PaymentProvider sözleşmesini karşılayan bir SAĞLAYICIDIR. Modül sağlayıcıları
// kimlikleriyle bir kayıtta tutar ([service.ProviderRegistry]) ve akış sırasında
// ADLA çözer. Kutudan çıkan tek sağlayıcı manuel/test sağlayıcısıdır
// (internal/modules/payment/manual); eklenti sistemi, çekirdeğe ve bu modüle
// dokunmadan container'daki kayda kendi sağlayıcısını ekler — plugins/paymentpaytr
// tam olarak bunu yapar.
//
// # Saga telafisi
//
// Faz 6'nın complete_cart saga'sı ödeme adımını [service.Service.CancelPayment]
// ile geri alır ve o metot İDEMPOTENTTİR: iki kez çağrılırsa ikinci çağrı hata
// vermez. Telafinin tekrar çalıştırılabilir olması bir tercih değil, saga'nın
// çalışma şartıdır (plan Bölüm 5.5).
//
// # Neyi bilmez
//
// Modül hiçbir modülü import etmez ve bir ödemenin HANGİ sepete ya da siparişe
// ait olduğunu bilmez. reference serbest bir metindir, foreign key DEĞİLDİR
// (Prensip 2.2) ve varlığı burada doğrulanmaz; bağ, siparişin bildireceği
// link ile kurulur. Bu yüzden bu modül HİÇBİR link tanımı bildirmez: bağın
// sahibi ödeme değil, ödemeye ihtiyaç duyan taraftır.
//
// # Dışarıya açtığı yüzeyler
//
//   - "payment.service" — modül içi zengin yüzey (domain tipleriyle).
//   - "payment.interop" — modüller arası İLKEL yüzey (ADR 0001/0006); Faz 6
//     saga'sı ödeme adımlarını buradan yürütür.
//   - "payment.providers" — sağlayıcı kaydı; eklentiler buraya sağlayıcı ekler.
//   - "payment_collection.query" — Query katmanına açılan okuma sağlayıcısı
//     (ADR 0004).
//   - /admin/v1/payment-collections … — yönetim API'si.
//   - /store/v1/payment-collections/{id} … — müşterinin ödeme akışı.
package payment

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "payment"

// ServiceName modül servisinin container'daki adıdır.
//
// Başka modüller ve workflow'lar (ADR 0001/0006 gereği bu paketi import
// ETMEDEN) servise bu adla ulaşır ve KENDİ paketlerinde tanımladıkları dar bir
// arayüzle kullanır.
const ServiceName = ModuleName + ".service"

// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
//
// Servisin kendisinden AYRI kaydedilir: servis payment'ın zengin tipleriyle
// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Sipariş tamamlama
// saga'sı onu kendi dar arayüzüyle çözer.
const InteropName = ModuleName + ".interop"

// ProvidersName sağlayıcı kaydının container'daki adıdır.
//
// Bir eklenti kendi PaymentProvider'ını bu kaydı çözüp ekler ve modülün kodunu
// değiştirmesi gerekmez; plugins/paymentpaytr bunun çalışan örneğidir.
const ProvidersName = ModuleName + ".providers"

// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
const dbServiceName = "core.db"

// linkServiceName Module Links servisinin container'daki adıdır.
const linkServiceName = "core.link"

// codeLinkDefine link tanımının açılışta bildirilemediğini raporlar.
const codeLinkDefine = "payment_module_link_define_failed"

// Hata kodları.
const (
	codeSetupFailed      = "payment_module_setup_failed"
	codeProviderRegister = "payment_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module payment modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
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
// kırılmaz, yalnızca ödemenin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir payment modülü üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New() *Module { return &Module{} }

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi, modüller arası yüzeyi, sağlayıcı kaydını ve Query
// sağlayıcısını container'a kaydeder.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi). core.db modüller
// ayağa kalkmadan önce main.go'da hazır değer olarak kaydedildiği için burada
// çözülmesi güvenlidir ve eksikliği modülün hiç çalışamayacağı bir kurulum
// hatasıdır — sessizce ertelenmez.
//
// Varsayılan sağlayıcı ([manual.Provider]) burada kaydedilir. Aynı depo
// örneğini kullanır ama AYRI bir tabloya yazar; servisin [service.Store]
// arayüzünde o tablonun metotları yoktur, yani modül sağlayıcının defterine
// tip düzeyinde erişemez.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, dbServiceName)
	}

	links, err := container.Resolve[link.LinkService](c, linkServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü link servisini çözemedi (%q)", ModuleName, linkServiceName)
	}

	// Link tanımları BURADA bildirilir: şema tanımın yanında durur ve her
	// açılışta idempotent olarak doğrulanır (ADR 0005). Bir tanım YALNIZCA BİR
	// KEZ bildirilebilir, o yüzden order_payment'ı sipariş modülü değil bu
	// modül bildiriyor — bağın taşıdığı kaydı yazan taraf burası (bkz.
	// [service.LinkOrderPayment]).
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeLinkDefine,
				"%q link tanımı bildirilemedi", def.Name)
		}
	}

	log := slog.Default().With("modul", ModuleName)
	repo := repository.New(pool.Pool())

	providers := service.NewProviderRegistry()
	if err := providers.Register(manual.New(repo, log)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"%s modülü varsayılan sağlayıcıyı kaydedemedi", ModuleName)
	}

	svc, err := service.New(service.Options{
		Store:     repo,
		Providers: providers,
		Logger:    log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}
	// Sağlayıcı adı "<entity>.query" biçimindedir; Query onu bu adla arar ve
	// Entity() ile adın örtüştüğünü doğrular (ADR 0004).
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	m.providers = providers
	m.handler = api.New(svc)

	log.DebugContext(ctx, "payment modülü kaydedildi",
		"servis", ServiceName,
		"interop", InteropName,
		"saglayicilar", providers.IDs(),
		"query", ProviderName,
	)
	return nil
}

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("payment modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.handler.Routes(r)
}

// Describe modülün store ve admin uçlarını OpenAPI belgesine işler.
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
		panic("payment: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
