// Package payment ödeme modülüdür (plan Bölüm 6, Faz 6).
//
// Sorumluluğu tek cümleyle: bir sepet ya da sipariş için PARANIN hangi
// aşamada olduğunu bilmek — bloke mi, çekildi mi, iade mi edildi. Modül
// PaymentCollection, PaymentSession, Payment ve Refund verisinin TEK yazma
// yetkilisidir (Prensip 2.3).
//
// # The provider abstraction
//
// The side that talks to a payment institution is not the module but a
// PROVIDER that satisfies core/provider's PaymentProvider contract. The module
// keeps providers by id in a registry ([service.ProviderRegistry]) and resolves
// them BY NAME during a flow.
//
// The providers in the box are the ones [Module.Register] registers: the gift
// card in every installation; the manual provider
// (internal/modules/payment/manual), which authorizes whatever the caller
// names, only where [Options.ManualProvider] asks for it, which the composition
// root does everywhere but production (ADR 0283); and the two tenders that
// spend a customer's own balance (storecredit, loyaltypoints) only where the
// customer claim is proven ([Options.PersonBoundTenders]). The plugin system
// adds its own provider to the registry in the container without touching the
// core or this module — plugins/paymentpaytr does exactly that.
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
// (Prensip 2.2) ve varlığı burada doğrulanmaz; bağ bir link ile kurulur.
// ~~Bu yüzden bu modül HİÇBİR link tanımı bildirmez: bağın sahibi ödeme değil,
// ödemeye ihtiyaç duyan taraftır.~~
//
// **2026-09-07: tanımı BU modül bildirir.** Bağ hâlâ foreign key ile değil
// link ile kurulur, ama bir tanım YALNIZCA BİR KEZ bildirilebilir ve bildiren
// taraf, bağın taşıdığı kaydı YAZAN taraftır — ödeme tahsilatı. Bu yüzden
// "order_payment" burada bildirilir (bkz. [service.LinkOrderPayment]),
// sipariş modülü ise hiçbir tanım bildirmez. Tanımı bildirmek siparişi bilmek
// değildir: tanım yalnızca iki varlığın adını taşır, bu modül hâlâ hiçbir
// siparişi çözmez ve hiçbir reference'ı doğrulamaz.
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
	"github.com/bdrtr/gobit/core/openapi"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/payment/api"
	"github.com/bdrtr/gobit/internal/modules/payment/giftcard"
	"github.com/bdrtr/gobit/internal/modules/payment/loyaltypoints"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
	"github.com/bdrtr/gobit/internal/modules/payment/repository"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/modules/payment/storecredit"
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

// eventBusServiceName olay otobüsünün konteynerdeki adı.
const eventBusServiceName = "core.eventbus"

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

// Module is the payment module as the core sees it.
type Module struct {
	// opts are the installation's settings; the zero value picks the safe side.
	opts      Options
	svc       *service.Service
	providers *service.ProviderRegistry
	handler   *api.Handler
	// personal answers what the module keeps about a person (ADR 0277); nil
	// until Register.
	personal *service.PersonalData
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
func New(opts ...Options) *Module {
	m := &Module{}
	if len(opts) > 0 {
		m.opts = opts[0]
	}

	return m
}

// Options are the module's settings, given by the installation.
//
// The zero value picks the SAFE side: neither the manual provider nor the
// person-bound tenders are registered. An embedder who builds the module by
// hand without having heard of either setting has opened no tender that places
// a paid order without payment, and none that spends a customer's money or
// points.
type Options struct {
	// ManualProvider is whether the manual provider is registered.
	//
	// It authorizes and captures whatever the caller names, so where a shopper
	// can choose it an order is placed paid with nothing paid (ADR 0283). It is
	// what an installation without a provider account takes an order end to end
	// with, and the composition root registers it everywhere but production.
	ManualProvider bool

	// OfflineMethods are the names of the offline payment methods — a bank
	// transfer, cash on delivery — each registered as a provider of its own
	// whose money arrives after the order is placed (ADR 0284). None by
	// default: a method places orders that owe their total, which a shop
	// offers only by naming it.
	OfflineMethods []string

	// OfflineWaitDays is how many days each named offline method waits for its
	// money before the offline expiry job cancels its order (ADR 0289). A
	// method left out never expires, which is every method by default; a named
	// one has to be in OfflineMethods.
	OfflineWaitDays map[string]int

	// PersonBoundTenders bir KİŞİNİN bakiyesini harcayan iki sağlayıcının —
	// mağaza kredisi (ADR 0152) ve sadakat puanı (ADR 0165) — kaydedilip
	// kaydedilmeyeceğidir.
	//
	// # Neden kapatılabilir bir şey, ve neden TEK ayar
	//
	// Çünkü harcanan bakiye BİR KİŞİNİN ve o kişinin kimliği sepetin müşteri
	// alanından geliyor. ADR 0125'ten beri müşteri adlandıran bir sepet gövdesi
	// KANITLANMAK zorunda — ama bir kurulum eski davranışa
	// (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM) dönebiliyor ve orada iddia
	// sorgulanmıyor. O kurulumda bu tender'lar, bir müşterinin adını yazan
	// herkesin onun bakiyesini harcaması demek olurdu.
	//
	// Bu yüzden birleşim YAPILANDIRILAMIYOR: kurulum kök, iddiaya güvenen bir
	// kurulumda ikisini de HİÇ KAYDETMİYOR. Gerekçe krediye ya da puana değil
	// KİŞİYE ait olduğu için ayar tektir; iki ayar, aynı güvenlik kararının iki
	// kopyası olurdu. Ayarı burada tutmak, modülün yapılandırmayı okumasını
	// gerektirmeden (İlke 2.4) o kararı tek bir yerde bırakıyor.
	PersonBoundTenders bool

	// LoyaltyEarnBasisPoints tahsil edilen paranın her minor unit'inin kaç puan
	// kazandırdığıdır, on binde olarak (ADR 0164).
	//
	// SIFIR kazanmayı KAPATIR ve varsayılan odur: defter var, içine hiçbir şey
	// yazılmaz, okuma sıfır döner. Güvenli taraf o, çünkü kimsenin istemediği
	// bir puan programı mağazanın vermediği bir söz demek.
	//
	// Tavan minor unit başına bir puandır ve aşan bir değer kuruluşta REDDEDİLİR
	// (bkz. [service.MaxLoyaltyEarnBasisPoints]). Ayarı burada tutmak, modülün
	// yapılandırmayı okumasını gerektirmeden (İlke 2.4) oranı tek bir yerde
	// bırakıyor.
	LoyaltyEarnBasisPoints int64

	// GiftCardValidityDays is how many days a gift card pays for when nobody
	// names its moment; zero, the default, is never (ADR 0214). It is held here
	// for the loyalty rate's reason: the module does not read configuration.
	GiftCardValidityDays int
}

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
// The manual provider ([manual.Provider]) is registered here when
// [Options.ManualProvider] asks for it. It uses the same repository but writes
// to a SEPARATE table; the service's [service.Store] has none of that table's
// methods, so the module cannot reach the provider's ledger at the type level.
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

	// Resolved through narrow interfaces: the service publishes, the
	// registration below subscribes to one order event (ADR 0288), and nothing
	// here closes the bus (see service.EventPublisher).
	//
	// REQUIRED, and this is the order module's stance: a lost money event has
	// no compensation. An installation without events is one where the order
	// learns neither what was captured nor what was refunded.
	bus, err := container.Resolve[service.EventPublisher](c, eventBusServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus (%q)", ModuleName, eventBusServiceName)
	}
	subscriber, err := container.Resolve[service.EventSubscriber](c, eventBusServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not resolve the event bus as a subscriber (%q)", ModuleName, eventBusServiceName)
	}

	log := slog.Default().With("modul", ModuleName)
	repo := repository.New(pool.Pool())

	providers := service.NewProviderRegistry()
	if m.opts.ManualProvider {
		if err := providers.Register(manual.New(repo, log)); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the manual provider", ModuleName)
		}
	}
	// Mağaza kredisi ve sadakat puanı da birer ödeme yöntemi ve kutudan çıkıyor
	// (ADR 0152, ADR 0165): eklenti gerektirmiyorlar, çünkü harcadıkları bakiye
	// bu modülün kendi defterlerinde. Kredisi ya da puanı olmayan bir kurulumda
	// hiçbir şey değişmiyor — sağlayıcı kayıtlı ama bakiyesi sıfır olan kimse
	// onunla ödeyemiyor.
	//
	// KAYDEDİLMEDİKLERİ hâl ise bir güvenlik kararı ve gerekçesi
	// [Options.PersonBoundTenders] üzerinde: müşteri iddiasına kanıtsız güvenen
	// bir kurulumda bu sağlayıcılar başkasının bakiyesini harcatırdı, o yüzden
	// birleşim yapılandırılamıyor.
	// A gift card is registered in every installation (ADR 0208). It is not
	// person-bound: its owner is whoever presents the code, so the claim the two
	// tenders above depend on plays no part in it.
	if err := providers.Register(giftcard.New(repo, log)); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"the %s module could not register the gift card provider", ModuleName)
	}
	if m.opts.PersonBoundTenders {
		if err := providers.Register(storecredit.New(repo, log)); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"%s modülü mağaza kredisi sağlayıcısını kaydedemedi", ModuleName)
		}
		if err := providers.Register(loyaltypoints.New(repo, log)); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"%s modülü sadakat puanı sağlayıcısını kaydedemedi", ModuleName)
		}
	}
	// The offline methods come after the providers in the box, so a method
	// named like one of them is the registration that fails, naming the method
	// (ADR 0284).
	for _, method := range m.opts.OfflineMethods {
		provider, err := offline.New(method)
		if err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the offline method %q", ModuleName, method)
		}
		if err := providers.Register(provider); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
				"the %s module could not register the offline method %q", ModuleName, method)
		}
	}

	svc, err := service.New(service.Options{
		Store:                  repo,
		Providers:              providers,
		Events:                 bus,
		Logger:                 log,
		LoyaltyEarnBasisPoints: m.opts.LoyaltyEarnBasisPoints,
		GiftCardValidityDays:   m.opts.GiftCardValidityDays,
		Links:                  links,
		OfflineWaitDays:        m.opts.OfflineWaitDays,
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
	// The panel's surface (ADR 0287).
	if err := c.Provide(AdminName, &ReceivingSurface{svc: svc}); err != nil {
		return err
	}
	// A canceled order's authorized sessions are closed here (ADR 0288). A
	// failure STOPS THE STARTUP, for the order module's reason: a module that
	// hears no cancel leaves every unpaid order's promise open and looks wired.
	if err := subscriber.Subscribe(service.TopicOrderCanceled, svc.HandleOrderCanceled); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"the %s module could not subscribe to the %q event", ModuleName, service.TopicOrderCanceled)
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
	m.personal = service.NewPersonalData(repo, log)
	m.handler = api.New(svc).WithIdentity(&identityBinding{c: c, log: log})

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
