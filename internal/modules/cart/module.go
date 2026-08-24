// Package cart sepet modülüdür (plan Bölüm 6, Faz 5).
//
// Sorumluluğu tek cümleyle: bir sepetin NEYE sahip olduğunu bilmek — hangi
// bölgede, kimin adına, hangi satırlarla, hangi adresi ve kargo yöntemiyle.
// Modül Cart, LineItem, CartAddress ve ShippingMethod verisinin TEK yazma
// yetkilisidir (Prensip 2.3).
//
// # Neyi bilmez
//
// Sepetin NE KADAR TUTTUĞUNU hesaplamaz. Fiyat pricing'in, vergi region/tax'ın
// verisidir; ikisini bir araya getiren akış calculate_totals WORKFLOW'udur
// (plan Bölüm 2.5, ADR 0006). Bu modül hiçbir fiyat/vergi kaynağını çağırmaz;
// toplamları yalnızca [service.Service.SetTotals] ile alır, DOĞRULAR ve saklar.
//
// Modül başka HİÇBİR modülü import etmez (Prensip 2.1/2.4, ADR 0001; kural
// .golangci.yml içindeki depguard ve internal/arch testleriyle zorlanır).
// region_id, customer_id ve variant_id başka modüllerin kimlikleridir; serbest
// metin olarak saklanır ve foreign key verilmez (Prensip 2.2).
//
// # Dışarıya açtığı yüzeyler
//
//   - "cart.service" — modüller arası çağrılar ve workflow'lar için servis.
//     Faz 6'daki complete_cart saga'sı sepeti buradan kapatır.
//   - "cart.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//   - /store/v1/carts … — müşteri API'si (sepeti kuran ve değiştiren yüzey).
//   - /admin/v1/carts — yönetim API'si (YALNIZCA okuma).
//
// # Bildirdiği linkler
//
// "cart_customer" ve "cart_region" tanımlarını BU modül bildirir (ADR 0005);
// bağın sahibi sepettir. Kardinalite seçiminin gerekçesi için bkz.
// [service.Definitions].
package cart

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/cart/api"
	"github.com/bdrtr/gobit/internal/modules/cart/repository"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "cart"

// ServiceName modül servisinin container'daki adıdır.
//
// Başka modüller ve workflow'lar (ADR 0001/0006 gereği bu paketi import
// ETMEDEN) sepet servisine bu adla ulaşır ve KENDİ paketlerinde tanımladıkları
// dar bir arayüzle kullanır.
const ServiceName = ModuleName + ".service"

// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
//
// Servisin kendisinden AYRI kaydedilir: servis cart'ın zengin tipleriyle
// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Sepet akışları onu
// kendi tanımladıkları dar arayüzle çözer.
const InteropName = ModuleName + ".interop"

// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
const ProviderName = service.EntityName + query.ProviderSuffix

// Container'da çözülen çekirdek servislerin adları.
const (
	svcDB   = "core.db"
	svcLink = "core.link"
)

// Hata kodları.
const (
	codeSetupFailed = "cart_module_setup_failed"
	codeLinkDefine  = "cart_link_define_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module cart modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// New kaydedilmeye hazır bir cart modülü üretir.
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

// Register servisi ve Query sağlayıcısını container'a kaydeder, link
// tanımlarını bildirir.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi). core.db ve core.link
// modüller ayağa kalkmadan önce main.go'da hazır değer olarak kaydedildiği için
// burada çözülmeleri güvenlidir ve eksiklikleri modülün hiç çalışamayacağı bir
// kurulum hatasıdır — sessizce ertelenmez.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, svcDB)
	}
	links, err := container.Resolve[link.LinkService](c, svcLink)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü link servisini çözemedi (%q)", ModuleName, svcLink)
	}

	svc, err := service.New(service.Options{
		Repo:  repository.New(pool.Pool()),
		Links: links,
		// Uygulama açılışta slog.SetDefault ile yapılandırılmış logger'ı kurar;
		// modül ayrı bir logger kaydı aramaz.
		Logger: slog.Default().With("modul", ModuleName),
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// Sağlayıcı adı "<entity>.query" biçimindedir; Query onu bu adla arar ve
	// Entity() ile adın örtüştüğünü doğrular (ADR 0004).
	// Modüller arası yüzey AYRI bir adla kaydedilir: servisin kendisi cart'ın
	// zengin tipleriyle konuşur, bu yüzey ise yalnızca ilkel tiplerle (ADR 0006).
	// Sepet akışları onu kendi dar arayüzleriyle çözer.
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	// Link tanımları BURADA bildirilir: şema, tanımın kendisiyle aynı yerde
	// durur ve her açılışta idempotent olarak doğrulanır (ADR 0005).
	for _, def := range service.Definitions() {
		if err := links.Define(ctx, def); err != nil {
			return errors.Wrap(err, errors.KindOf(err), codeLinkDefine,
				"%q link tanımı bildirilemedi", def.Name)
		}
	}

	m.svc = svc
	m.handler = api.New(svc)
	slog.Default().DebugContext(ctx, "cart modülü kaydedildi",
		"servis", ServiceName, "saglayici", ProviderName)
	return nil
}

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("cart modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.handler.Routes(r)
}

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
		panic("cart: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
