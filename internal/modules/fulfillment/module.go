// Package fulfillment kargo/teslimat modülüdür (plan Bölüm 6, Faz 7).
//
// Sorumluluğu tek cümleyle: bir siparişin FİZİKSEL olarak nereye kadar
// geldiğini bilmek — hangi kargo seçeneği kaç para, gönderi açıldı mı, yola
// çıktı mı, teslim edildi mi. Modül ShippingProfile, ShippingOption,
// ShippingOptionRule Fulfillment ve FulfillmentItem verisinin TEK yazma
// yetkilisidir (Prensip 2.3).
//
// # Sağlayıcı soyutlaması
//
// Kargo firmasıyla konuşan taraf modül değil, internal/core/provider'daki
// FulfillmentProvider sözleşmesini karşılayan bir SAĞLAYICIDIR. Modül
// sağlayıcıları kimlikleriyle bir kayıtta tutar ([service.ProviderRegistry]) ve
// akış sırasında ADLA çözer. Kutudan çıkan tek sağlayıcı manuel/test
// sağlayıcısıdır (internal/modules/fulfillment/manual); Faz 9'daki plugin
// sistemi çekirdeğe ve bu modüle dokunmadan container'daki kayda kendi
// sağlayıcısını ekleyebilir.
//
// # Saga telafisi
//
// Bir sipariş akışı kargo adımını [service.Service.CancelFulfillment] ile geri
// alır ve o metot İDEMPOTENTTİR: iki kez çağrılırsa ikinci çağrı hata vermez.
// Telafinin tekrar çalıştırılabilir olması bir tercih değil, saga'nın çalışma
// şartıdır (plan Bölüm 5.5). Tek istisna TESLİM EDİLMİŞ gönderidir: teslim
// geri alınamayan fiziksel bir olgudur ve iptal errors.Conflict döner —
// payment modülünde tahsil edilmiş bir oturumun iptal edilemeyip iade
// edilmesiyle aynı kural.
//
// # Neyi bilmez
//
// Modül hiçbir modülü import etmez ve bir gönderinin HANGİ siparişe ait
// olduğunu bilmez. reference serbest bir metindir, foreign key DEĞİLDİR
// (Prensip 2.2) ve varlığı burada doğrulanmaz; aynı şey kargo seçeneğinin
// region_id'si ve gönderi kaleminin line_item_id'si için de geçerlidir. Bağ,
// siparişin bildireceği link ile kurulur. Bu yüzden bu modül HİÇBİR link
// tanımı bildirmez: bağın sahibi kargo değil, kargoya ihtiyaç duyan taraftır.
//
// Aynı sebeple modül, sepetin ürünlerinin hangi kargo profillerine bağlı
// olduğunu da bilmez; profil kimlikleri uygunluk sorgusuna DIŞARIDAN verilir.
//
// # Dışarıya açtığı yüzeyler
//
//   - "fulfillment.service" — modül içi zengin yüzey (domain tipleriyle).
//   - "fulfillment.interop" — modüller arası İLKEL yüzey (ADR 0001/0006);
//     sipariş akışları kargo adımlarını buradan yürütür.
//   - "fulfillment.providers" — sağlayıcı kaydı; eklentiler buraya sağlayıcı
//     ekler.
//   - "shipping_option.query" — Query katmanına açılan okuma sağlayıcısı
//     (ADR 0004).
//   - /admin/v1/shipping-profiles, /admin/v1/shipping-options,
//     /admin/v1/fulfillments … — yönetim API'si.
//   - /store/v1/shipping-options — sepet için uygun seçenekler. Sepet olguları
//     (ara toplam, adet, ağırlık) bu uçta İSTEMCİNİN İDDİASIDIR ve
//     doğrulanamaz; bu yüzden o olgulara bağlı kuralı olan seçenekler bu uçtan
//     HİÇ dönmez ve müşteriye sepet akışı üzerinden (interop) gösterilir
//     (gerekçe: [service.Service.ListShippingOptionsFor]).
package fulfillment

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
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/api"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/manual"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "fulfillment"

// ServiceName modül servisinin container'daki adıdır.
//
// Başka modüller ve workflow'lar (ADR 0001/0006 gereği bu paketi import
// ETMEDEN) servise bu adla ulaşır ve KENDİ paketlerinde tanımladıkları dar bir
// arayüzle kullanır.
const ServiceName = ModuleName + ".service"

// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
//
// Servisin kendisinden AYRI kaydedilir: servis fulfillment'ın zengin
// tipleriyle konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Sepet ve
// sipariş akışları onu kendi dar arayüzleriyle çözer.
const InteropName = ModuleName + ".interop"

// ProvidersName sağlayıcı kaydının container'daki adıdır.
//
// Faz 9'daki plugin sistemi kendi FulfillmentProvider'ını bu kaydı çözüp
// ekler; modülün kodunu değiştirmesi gerekmez.
const ProvidersName = ModuleName + ".providers"

// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
const dbServiceName = "core.db"

// Hata kodları.
const (
	codeSetupFailed      = "fulfillment_module_setup_failed"
	codeProviderRegister = "fulfillment_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module fulfillment modülünün çekirdeğe sunduğu uygulamadır.
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
// kırılmaz, yalnızca kargo uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir fulfillment modülü üretir.
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

	log.DebugContext(ctx, "fulfillment modülü kaydedildi",
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
		slog.Default().Warn("fulfillment modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.handler.Routes(r)
}

// Describe modülün uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// DTO'larından türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi.
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
		panic("fulfillment: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
