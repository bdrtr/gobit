// Package region bölge ve para birimi modülüdür (plan Bölüm 6, Faz 5).
//
// Sorumluluğu tek cümleyle: bir satışın hangi para biriminde ve hangi vergi
// bölgesinde yapıldığını tanımlamak. Modül Region, Currency ve Country
// verisinin TEK yazma yetkilisidir (Prensip 2.3).
//
// # Sepet akışının temeli
//
// Sepet para birimini ve vergi bölgesini buradan alır: müşterinin ülkesinden
// bölge bulunur ([service.Service.RegionIDForCountry]), bölgenin para birimi
// sepete yazılır ([service.Service.RegionCurrency]) ve vergi satırı bölgenin
// YEDEK oranıyla hesaplanır ([service.Service.RegionTax]). Bu üç metot ilkel
// tiplerle yazılmıştır ki tüketici modül region'ı import etmeden kendi dar
// arayüzünü tanımlayabilsin (ADR 0001).
//
// # Referans veri
//
// Currency ve Country referans veridir ve migration ile tohumlanır
// (000002_region_seed): 41 para birimi ve ISO 3166-1'in 249 ülkesi. Her
// kurulumun bunları elle girmesi beklenemez; eksik girilen tek bir ülke,
// o ülkedeki müşteri için sepet açılamaması demektir.
//
// # Neyi bilmez
//
// region hiçbir modülü import etmez ve sepetlerin, siparişlerin varlığından
// haberdar değildir. Sepet ve sipariş bölgeyi KENDİ SÜTUNLARINDA taşır; bunun
// bir link ile aynalanması denendi ve okuyucusu çıkmadığı için kaldırıldı
// (bkz. CHANGELOG, "cart_region"). Bugün region'a işaret eden bir link
// YOKTUR — ihtiyaç doğarsa bildiren taraf aşağıdaki nota bakmalıdır.
//
// # Dışarıya açtığı yüzeyler
//
//   - "region.service" — modüller arası çağrılar için servis (bkz.
//     internal/modules/region/service, "Modüller arası yüzey").
//   - "region.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//     Kayıtlar para birimi ve ülkeleriyle döner.
//   - /admin/v1/regions, /admin/v1/currencies, /admin/v1/countries — yönetim API'si.
//   - /store/v1/regions — vitrinin para birimi/bölge seçimi.
//
// # Link'i bildiren tarafa not
//
// Query, bir genişletmenin hedef sağlayıcısını link tanımının UCUNDAKİ MODÜL
// ADINDAN bulur (bkz. core/query targetSide: hedef ad + ".query"
// aranır). region için entity adı ile modül adı AYNIDIR ("region"), yani linki
// bildiren modül ucu doğal biçimde yazabilir. Aşağıdaki tanım VARSAYIMSALDIR;
// böyle bir link bugün yoktur ve eklenmesi ancak GEZEN bir okuyucusu varsa
// doğrudur (bkz. internal/arch TestTheLinkDefinitionsAreTraversed):
//
//	link.LinkDefinition{
//	    Name:        "siparis_region",
//	    From:        link.LinkSide{Module: "siparis", Field: "siparis_id"},
//	    To:          link.LinkSide{Module: "region", Field: "region_id"},
//	    Cardinality: link.OneToOne,
//	}
package region

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/region/api"
	"github.com/bdrtr/gobit/internal/modules/region/repository"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration versiyon tablosunun
	// öneki de budur.
	ModuleName = "region"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = ModuleName + ".service"
	// ProviderName query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot migration dosyalarının kök dizinidir.
//
// golang-migrate kaynağı köke bakar (iofs.New(src, ".")), embed.FS ise
// dosyaları "migrations/" altında tutar; alt ağaç bu yüzden bir kez burada
// açılır.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module region modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc *service.Service
	api *api.API
	log *slog.Logger
}

var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca bölgenin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kurulmamış bir region modülü üretir; servis [Module.Register] içinde
// kurulur. log nil ise loglar atılır.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return ModuleName }

// Register servisi ve query sağlayıcısını container'a kaydeder.
//
// region hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir — modül sırasına bağımlılık yaratan tek şey başka bir MODÜLÜN
// servisini çözmek olurdu ve bu yapılmaz.
//
// Link tanımı bildirilmez ve region'a işaret eden bir link de yoktur: bölge
// kimliğini taşıyan taraflar onu kendi sütunlarında tutar.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "region_db_unavailable",
			"region modülü %q servisini çözemedi", dbServiceName)
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

	m.log.InfoContext(ctx, "region modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("saglayici", ProviderName),
	)
	return nil
}

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Routes modülün admin ve store route'larını router'a bağlar.
//
// Register'dan SONRA çağrılır (bkz. module.Registry.Bootstrap); api bu yüzden
// kurulmuş olur. Yine de nil kontrolü vardır: Register hata verip Bootstrap
// yarıda kesilirse Routes hiç çağrılmaz, ama modül elle kullanılırsa panik
// yerine sessiz bir no-op daha güvenlidir.
func (m *Module) Routes(r chi.Router) {
	if m.api == nil {
		m.log.Warn("region modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
		return
	}
	m.api.Routes(r)
}

// Describe modülün uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// DTO'larından türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi.
//
// [Module.Routes]'un tersine api kontrolü YOKTUR ve gerekmez: şema tiplerden
// gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün belgesini de
// sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service kurulmuş servisi döner; Register çağrılmadıysa nil.
//
// Modülü doğrudan kullanan testler ve gömen uygulamalar içindir; normal akışta
// servis container'dan [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub alt dosya sistemini açar; açılamazsa panikler.
//
// //go:embed dizinin varlığını derleme zamanında garanti ettiği için hata yolu
// erişilemezdir. Yine de sessizce nil dönmek, modülün migration'sız (yani
// tablosuz) ayağa kalkması demek olurdu; kurulum hatası açıkça patlamalıdır.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("region: migration dizini açılamadı: " + err.Error())
	}
	return sub
}
