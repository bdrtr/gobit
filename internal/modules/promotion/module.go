// Package promotion promosyon modülüdür (plan Bölüm 6, Faz 7).
//
// Sorumluluğu tek cümleyle: bir sepetin hangi indirimleri hak ettiğini
// hesaplamak ve kuponların kaç kez kullanıldığını saymak. Modül Campaign,
// Promotion, ApplicationMethod, PromotionRule ve kullanım defteri verisinin TEK
// yazma yetkilisidir (Prensip 2.3).
//
// # Devraldığı iş
//
// Faz 5'te sepet toplamının indirim alanı DAİMA SIFIRDI ve
// internal/workflows/cart bunu "Faz 7'de promotion devralacak" notuyla
// bırakmıştı. Devralma bu modülün [service.Service.ComputeDiscounts]
// metoduyla olur; sepet akışı onu "promotion.interop" adıyla çözer ve
// ComputeDiscountsJSON üzerinden çağırır.
//
// Vergi tabanı Faz 5'te BUGÜNDEN indirim sonrası tanımlanmıştı
// (internal/workflows/cart paket yorumu, "Vergi sözleşmesi"), yani indirim
// devreye girdiğinde vergi kendiliğinden doğru tabana oturur.
//
// # Hesap ile kullanım AYRIDIR
//
// [service.Service.ComputeDiscounts] YAN ETKİSİZDİR: sepet her değiştiğinde
// çağrılır ve hiçbir sayacı tüketmez. Kuponu fiilen harcayan
// [service.Service.RedeemPromotion]'dır ve o idempotenttir; telafisi
// [service.Service.ReleasePromotion] da öyledir (plan Bölüm 5.5).
//
// # Neyi bilmez
//
// promotion hiçbir modülü import etmez. Bir kullanımın hangi siparişe ait
// olduğu serbest bir "reference" metnidir, foreign key DEĞİLDİR (Prensip 2.2)
// ve varlığı burada doğrulanmaz; bağ, siparişin bildireceği link ile kurulur.
// Bu yüzden bu modül HİÇBİR link tanımı bildirmez: bağın sahibi promosyon
// değil, promosyona ihtiyaç duyan taraftır.
//
// # Dışarıya açtığı yüzeyler
//
//   - "promotion.service" — modül içi zengin yüzey (domain tipleriyle).
//   - "promotion.interop" — modüller arası İLKEL yüzey (ADR 0001/0006); sepet
//     akışı ve sipariş saga'sı indirimi buradan hesaplatır.
//   - "promotion.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004);
//     YALNIZCA aktif promosyonları ve dar bir alan kümesini döner.
//   - /admin/v1/promotions, /admin/v1/campaigns … — yönetim API'si.
//   - /store/v1/promotions/{code} — kupon doğrulama; taslak/pasif promosyonu ve
//     kural koşullarını SIZDIRMAZ.
package promotion

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
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
	"github.com/bdrtr/gobit/internal/modules/promotion/repository"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration sürüm tablosunun öneki de
	// budur.
	ModuleName = "promotion"
	// ServiceName servisin container'daki adıdır.
	//
	// Başka modüller ve workflow'lar (ADR 0001/0006 gereği bu paketi import
	// ETMEDEN) servise bu adla ulaşır ve KENDİ paketlerinde tanımladıkları dar
	// bir arayüzle kullanır.
	ServiceName = ModuleName + ".service"
	// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
	//
	// Servisin kendisinden AYRI kaydedilir: servis promotion'ın zengin
	// tipleriyle konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle.
	InteropName = ModuleName + ".interop"
	// ProviderName Query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

// codeSetupFailed modül kurulumunun başarısız olduğunu bildirir.
const codeSetupFailed = "promotion_module_setup_failed"

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Module promotion modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc     *service.Service
	handler *api.API
	log     *slog.Logger
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca promosyon uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir promotion modülü üretir; servis [Module.Register]
// içinde kurulur. log nil ise loglar atılır.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
//
// Kök dizin "migrations" alt klasörüne indirilir; golang-migrate dosyaları
// kaynağın KÖKÜNDE arar ve embed.FS onları klasör adıyla birlikte taşırdı.
func (m *Module) Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		// embed yolu derleme zamanında sabittir; buraya düşmek, migrations
		// klasörünün gömülmediği anlamına gelir ve sessiz geçilemez.
		panic("promotion: migration kaynağı açılamadı: " + err.Error())
	}
	return sub
}

// Register servisi, modüller arası yüzeyi ve Query sağlayıcısını container'a
// kaydeder.
//
// promotion hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir — modül sırasına bağımlılık yaratan tek şey başka bir MODÜLÜN
// servisini çözmek olurdu ve bu yapılmaz.
//
// Link tanımı bildirilmez: bir siparişin hangi promosyonu kullandığı bağının
// sahibi sipariş tarafıdır (bkz. paket yorumu).
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, dbServiceName)
	}

	svc := service.New(repository.New(pool.Pool()), service.Options{Logger: m.log})

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
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

	m.log.InfoContext(ctx, "promotion modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("interop", InteropName),
		slog.String("saglayici", ProviderName),
	)
	return nil
}

// Routes modülün admin ve store route'larını router'a bağlar.
//
// Register'dan SONRA çağrılır (bkz. module.Registry.Bootstrap); handler bu
// yüzden kurulmuş olur. Yine de nil kontrolü vardır: Register hata verip
// Bootstrap yarıda kesilirse Routes hiç çağrılmaz, ama modül elle kullanılırsa
// panik yerine sessiz bir no-op daha güvenlidir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
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
// [Module.Routes]'un tersine handler kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün
// belgesini de sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service kurulmuş servisi döner; Register çağrılmadıysa nil.
//
// Modülü doğrudan kullanan testler ve gömen uygulamalar içindir; normal akışta
// servis container'dan [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }
