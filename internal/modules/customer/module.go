// Package customer müşteri modülüdür (plan Bölüm 6, Faz 5).
//
// Sorumluluğu tek cümleyle: kimin alışveriş yaptığını bilmek — misafir de olsa,
// kayıtlı da olsa. Modül Customer, CustomerGroup ve CustomerAddress verisinin
// TEK yazma yetkilisidir (Prensip 2.3).
//
// # Misafir mi, hesap mı
//
// Modülün merkezî kararı e-posta benzersizliğinin yalnızca KAYITLI hesaplarda
// uygulanmasıdır; misafir kayıtları aynı e-postayı paylaşabilir. Kural
// veritabanındaki kısmi benzersiz indekstedir ve gerekçesi
// internal/modules/customer/models, Customer godoc'unda yazılıdır.
//
// # Neyi bilmez
//
// customer hiçbir modülü import etmez ve sepetlerin, siparişlerin varlığından
// haberdar değildir. cart ↔ customer ve order ↔ customer bağlarını, ilişkinin
// sahibi olan o modüller Module Links ile kurar; customer o linkleri hiç görmez
// (Prensip 2.2: cross-module FK yoktur). Ülke kodları da doğrulanır ama
// LİSTELENMEZ: ülke listesinin sahibi region modülüdür.
//
// # Dışarıya açtığı yüzeyler
//
//   - "customer.service" — modüller arası çağrılar için servis (bkz.
//     internal/modules/customer/service, interop.go).
//   - "customer.query" — Query katmanına açılan okuma sağlayıcısı (ADR 0004).
//     Kayıtlar GRUP KİMLİKLERİYLE döner ki fiyat hesabının kural bağlamı tek
//     çağrıda kurulabilsin.
//   - /admin/v1/customers, /admin/v1/customer-groups … — yönetim API'si.
//   - /store/v1/customers … — vitrin API'si (Faz 8'e kadar KORUMASIZ; bkz.
//     internal/modules/customer/api paket belgesi).
//
// # Link'i bildiren tarafa not
//
// Query, bir genişletmenin hedef sağlayıcısını link tanımının UCUNDAKİ MODÜL
// ADINDAN bulur (hedef ad + ".query" aranır). customer ucu ENTITY ADIYLA
// yazılmalıdır:
//
//	link.LinkDefinition{
//	    Name:        "b2b_employee_customer",
//	    From:        link.LinkSide{Module: "b2b", Field: "employee_id"},
//	    To:          link.LinkSide{Module: "customer", Field: "customer_id"},
//	    Cardinality: link.OneToOne,
//	}
//
// Örnek GERÇEKTİR: b2b modülü bu bağı bildirir ve çalışanın müşteri kaydını
// ONUN ÜZERİNDEN okur. Okunmayan bir bağ bildirmek yerine sütun kullanmak
// gerekir; bunun neden böyle olduğu için bkz. internal/arch
// TestTheLinkDefinitionsAreTraversed.
//
// Burada entity adı ile modül adı aynıdır ("customer"), ama bu bir rastlantıdır
// ve sağlayıcı adı [ProviderName] sabitinden okunmalıdır.
package customer

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
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration versiyon tablosunun öneki
	// de budur.
	ModuleName = "customer"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = ModuleName + ".service"
	// ProviderName query sağlayıcısının container'daki adıdır (ADR 0004).
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

// codeSetupFailed modül kurulumunun başarısız olduğunu bildirir.
const codeSetupFailed = "customer_module_setup_failed"

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// golang-migrate kaynağı KÖKTEN okur ve embed.FS dosyaları klasör adıyla
// birlikte taşırdı.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Module customer modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
}

var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca müşterinin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kurulmamış bir customer modülü üretir; servis [Module.Register] içinde
// kurulur. log nil ise loglar atılır.
func New(log *slog.Logger) *Module {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi ve query sağlayıcısını container'a kaydeder.
//
// customer hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir — modül sırasına bağımlılık yaratan tek şey başka bir MODÜLÜN
// servisini çözmek olurdu ve bu yapılmaz.
//
// Link tanımı BİLDİRİLMEZ: customer, kendisine işaret eden bağların ucudur,
// sahibi değil. Bugünkü tek sahip b2b modülüdür ("b2b_employee_customer").
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü %q servisini çözemedi", ModuleName, dbServiceName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{Logger: m.log})
	m.handler = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "customer modülü kaydedildi",
		slog.String("servis", ServiceName),
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

// mustSub gömülü dosya sisteminin alt ağacını açar.
//
// Yol derleme zamanında sabittir; buraya düşmek migrations klasörünün
// gömülmediği anlamına gelir ve sessiz geçilemez — migration'sız açılan bir
// modül, tabloları olmadan çalışmaya başlardı.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("customer: migration kaynağı açılamadı: " + err.Error())
	}
	return sub
}
