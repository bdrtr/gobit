// Package inventory stok modülüdür: stok kalemleri, lokasyonlar, seviyeler ve
// rezervasyonlar (plan Bölüm 6, Faz 4).
//
// Modül kendi tablolarına sahiptir ve başka HİÇBİR modülü import etmez
// (Prensip 2.1/2.4, ADR 0001). Dışarıya üç yüzey açar:
//
//   - Servis: container'da [ServiceName] adıyla. Faz 6'daki complete_cart
//     saga'sı stok adımını ve telafisini buradan çağırır.
//   - Query sağlayıcısı: container'da "inventory_item.query" adıyla (ADR 0004).
//     Kayıtlar toplam satılabilir adetle birlikte döner; product'ın mağaza
//     listelemesi ürünü ve stoğunu tek çağrıda görür.
//   - Admin API: /admin/v1/stock-locations ve /admin/v1/inventory-items.
//
// Link tanımı BİLDİRMEZ: varyant ile stok kalemi arasındaki
// "product_variant_inventory" bağını, ilişkinin sahibi olan product modülü
// bildirir.
package inventory

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
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
	"github.com/bdrtr/gobit/internal/modules/inventory/repository"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// Container adları.
const (
	// ModuleName modülün adıdır; migration versiyon tablosunun da önekidir.
	ModuleName = "inventory"
	// ServiceName modül servisinin container'daki adıdır. Başka modüller
	// servisi bu adla çözer ve KENDİ paketlerinde tanımladıkları dar bir
	// arayüzle kullanır (ADR 0001).
	ServiceName = ModuleName + ".service"
	// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.EntityName + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot migration dosyalarının kök dizinidir.
//
// golang-migrate kaynağı köke bakar (iofs.New(src, ".")), embed.FS ise dosyaları
// "migrations/" altında tutar; alt ağaç bu yüzden bir kez burada açılır.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module inventory modülünün çekirdek sözleşmesini uygular.
type Module struct {
	svc *service.Service
}

// Modülün çekirdek sözleşmesini karşıladığı derleme zamanında doğrulanır.
var _ module.Module = (*Module)(nil)

// New kaydedilmeye hazır bir inventory modülü üretir.
func New() *Module {
	return &Module{}
}

// Name modülün adını döner.
func (m *Module) Name() string {
	return ModuleName
}

// Register servisi ve Query sağlayıcısını container'a kaydeder.
//
// Yalnızca ÇEKİRDEK servisi (core.db) çözülür; başka bir modülün servisi burada
// çözülmez, çünkü bu aşamada henüz kayıtlı olmayabilir (bkz. module.Module
// sözleşmesi). core.db, modüller ayağa kalkmadan önce main.go'da hazır değer
// olarak kaydedildiği için burada çözülmesi güvenlidir ve eksikliği modülün
// hiç çalışamayacağı bir kurulum hatasıdır — sessizce ertelenmez.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), "inventory_db_unavailable",
			"%s modülü %q servisini çözemedi", ModuleName, dbServiceName)
	}

	svc := service.New(repository.New(pool.Pool()), slog.Default())

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(svc)); err != nil {
		return err
	}

	m.svc = svc
	slog.Default().DebugContext(ctx, "inventory modülü kaydedildi",
		"servis", ServiceName, "saglayici", ProviderName)
	return nil
}

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS {
	return migrationsRoot
}

// Routes modülün admin route'larını router'a bağlar.
//
// Register çağrılmadan route bağlanmaz: servissiz bir handler her isteği
// panikle karşılardı; sessiz kalmak (route yok -> 404) daha güvenlidir ve
// Bootstrap zaten Register'ı Routes'tan önce çalıştırır.
func (m *Module) Routes(r chi.Router) {
	if m.svc == nil {
		slog.Default().Warn("inventory modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	api.NewHandler(m.svc).Routes(r)
}

// mustSub alt dosya sistemini açar; açılamazsa panikler.
//
// //go:embed dizinin varlığını derleme zamanında garanti ettiği için hata yolu
// erişilemezdir. Yine de sessizce nil dönmek, modülün migration'sız (yani
// tablosuz) ayağa kalkması demek olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("inventory: migration dizini açılamadı: " + err.Error())
	}
	return sub
}
