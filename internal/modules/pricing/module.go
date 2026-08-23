// Package pricing fiyatlandırma modülüdür (plan Bölüm 6, Faz 4).
//
// Sorumluluğu tek cümleyle: bir varyantın fiyatlarının kabını (PriceSet) tutmak
// ve verilen bağlamda geçerli fiyatı seçmek. Modül PriceSet, Price, PriceList
// ve PriceRule verisinin TEK yazma yetkilisidir (Prensip 2.3).
//
// # Neyi bilmez
//
// pricing hiçbir modülü import etmez ve varyantların varlığından haberdar
// değildir. product ile bağ, product'ın bildirdiği "product_variant_price_set"
// linkiyle kurulur; link tablosu çekirdektedir ve pricing onu hiç görmez
// (Prensip 2.2: cross-module FK yoktur).
//
// # Dışarıya açtığı yüzeyler
//
//   - "pricing.service" — modüller arası çağrılar için servis (bkz.
//     internal/modules/pricing/service, "Modüller arası yüzey").
//   - "price_set.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//     Kayıtlar FİYATLARIYLA döner ki product'ın store listelemesi tek çağrıda
//     fiyatı görebilsin.
//   - /admin/v1/price-sets, /admin/v1/price-lists … — yönetim API'si.
//   - /store/v1/price-sets/{id} — tek okuma uç noktası.
//
// # Link'i bildiren tarafa not
//
// Query, bir genişletmenin hedef sağlayıcısını link tanımının UCUNDAKİ MODÜL
// ADINDAN bulur (bkz. internal/core/query targetSide: hedef ad + ".query"
// aranır). Bu yüzden linki bildiren modül, pricing ucunu ENTITY ADIYLA
// yazmalıdır:
//
//	link.LinkDefinition{
//	    Name:        "product_variant_price_set",
//	    From:        link.LinkSide{Module: "product_variant", Field: "variant_id"},
//	    To:          link.LinkSide{Module: "price_set", Field: "price_set_id"},
//	    Cardinality: link.OneToOne,
//	}
//
// Uç "pricing" olarak yazılırsa Query "pricing.query" adını arar ve
// errors.NotFound döner; sağlayıcı "price_set.query" adıyla kayıtlıdır.
package pricing

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
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// Container'daki adlar.
const (
	// Name modülün benzersiz adıdır; migration versiyon tablosunun öneki de budur.
	Name = "pricing"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = Name + ".service"
	// ProviderName query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// DBName çekirdek veritabanı havuzunun container'daki adıdır.
	DBName = "core.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Module pricing modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// New kurulmamış bir pricing modülü üretir; servis [Module.Register] içinde
// kurulur. log nil ise loglar atılır.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return Name }

// Register servisi ve query sağlayıcısını container'a kaydeder.
//
// pricing hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir — modül sırasına bağımlılık yaratan tek şey başka bir MODÜLÜN
// servisini çözmek olurdu ve bu yapılmaz.
//
// Link tanımı bildirilmez: "product_variant_price_set" linkinin sahibi
// product'tır ve pricing o linki tanımaz.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, DBName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "pricing_db_unavailable",
			"pricing modülü %q servisini çözemedi", DBName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{Logger: m.log})
	m.api = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "pricing modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("saglayici", ProviderName),
	)
	return nil
}

// Migrations modülün migration dosyalarını döner.
//
// Kök dizin "migrations" alt klasörüne indirilir; golang-migrate dosyaları
// kaynağın KÖKÜNDE arar ve embed.FS onları klasör adıyla birlikte taşırdı.
func (m *Module) Migrations() fs.FS {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		// embed yolu derleme zamanında sabittir; buraya düşmek, migrations
		// klasörünün gömülmediği anlamına gelir ve sessiz geçilemez.
		panic("pricing: migration kaynağı açılamadı: " + err.Error())
	}
	return sub
}

// Routes modülün admin ve store route'larını router'a bağlar.
//
// Register'dan SONRA çağrılır (bkz. module.Registry.Bootstrap); api bu yüzden
// kurulmuş olur. Yine de nil kontrolü vardır: Register hata verip Bootstrap
// yarıda kesilirse Routes hiç çağrılmaz, ama modül elle kullanılırsa panik
// yerine sessiz bir no-op daha güvenlidir.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		return
	}
	m.api.Routes(r)
}

// Service kurulmuş servisi döner; Register çağrılmadıysa nil.
//
// Modülü doğrudan kullanan testler ve gömen uygulamalar içindir; normal akışta
// servis container'dan [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }
