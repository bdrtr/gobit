// Package product katalog modülüdür: ürün, varyant, seçenek, kategori,
// koleksiyon, etiket ve görsel bu modülün verisidir.
//
// # Modülün çekirdekle sözleşmesi
//
// [Module] çekirdeğin module.Module arayüzünü karşılar. Register sırasında üç
// şey yapılır:
//
//  1. Servis container'a "product.service" adıyla kaydedilir.
//  2. Query sağlayıcıları "product.query" ve "variant.query" adlarıyla
//     kaydedilir (ADR 0004).
//  3. Fiyat, stok ve satış kanalı link tanımları bildirilir (ADR 0005).
//
// # Başka modüller
//
// pricing, inventory ve auth paketleri İMPORT EDİLMEZ (Prensip 2.4, ADR 0001;
// kural .golangci.yml içindeki depguard ile CI'da zorlanır). Fiyat ve stok
// verisi yalnızca link adları ve Query katmanı üzerinden görünür; satış kanalı
// ise yalnızca bir link adı ve isteğin kimliğinden gelen kimlik dizgeleri
// olarak görünür (bkz. service.LinkProductSalesChannel).
package product

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
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Name modülün adıdır: container adlarının, migration sürüm defterinin ve log
// alanlarının öneki budur.
const Name = "product"

// Container'da çözülen çekirdek servislerin adları.
const (
	svcDB    = "core.db"
	svcLink  = "core.link"
	svcQuery = "core.query"
)

// ServiceName modülün servisinin container'daki adıdır.
//
// Başka modüller (ADR 0001 gereği bu paketi import ETMEDEN) katalog servisine
// bu adla ulaşır.
const ServiceName = Name + ".service"

// Hata kodları.
const (
	codeSetupFailed = "product_module_setup_failed"
	codeLinkDefine  = "product_link_define_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module product modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// New kayıt edilmeye hazır bir modül üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New() *Module {
	return &Module{}
}

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return Name }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi ve Query sağlayıcılarını container'a kaydeder, link
// tanımlarını bildirir.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi).
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", Name, svcDB)
	}
	links, err := container.Resolve[link.LinkService](c, svcLink)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü link servisini çözemedi (%q)", Name, svcLink)
	}
	graph, err := container.Resolve[query.Query](c, svcQuery)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü query katmanını çözemedi (%q)", Name, svcQuery)
	}

	repo := repository.New(pool.Pool())
	svc, err := service.New(service.Options{
		Repo:  repo,
		Links: links,
		Query: graph,
		// Uygulama açılışta slog.SetDefault ile yapılandırılmış logger'ı kurar;
		// modül ayrı bir logger kaydı aramaz.
		Logger: slog.Default().With("modul", Name),
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", Name)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// Sağlayıcı adları "<entity>.query" biçimindedir; Query onları bu adla
	// arar ve Entity() ile adın örtüştüğünü doğrular (ADR 0004).
	if err := c.Provide(service.EntityProduct+query.ProviderSuffix, service.NewProductProvider(repo)); err != nil {
		return err
	}
	if err := c.Provide(service.EntityVariant+query.ProviderSuffix, service.NewVariantProvider(repo)); err != nil {
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
	return nil
}

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
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
// dosyaların varlığını zaten derleme zamanında doğrulamıştır.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("product: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
