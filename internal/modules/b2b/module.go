// Package b2b şirket adına alışverişi mümkün kılan modüldür.
//
// Sorumluluğu tek cümleyle: alıcının KİMİN ADINA ve NE KADARA kadar alışveriş
// yapabileceğini bilmek. Modül Company ve CompanyEmployee verisinin TEK yazma
// yetkilisidir (Prensip 2.3).
//
// # Neden bu modül vitrin akışının varsayımını kırıyor
//
// B2C akışında alıcı bir bireydir ve kendi parasını harcar; harcama yetkisi
// diye bir kavram yoktur. B2B'de alıcı, HARCAMA YETKİSİ SINIRLI bir çalışandır:
// kimliği yine bir müşteri kaydıdır (customer modülü), ama ne kadar
// harcayabileceğini bağlı olduğu şirket belirler. Bu modül o iki bilgiyi tutar;
// kimliğin kendisini TUTMAZ.
//
// # Neyi bilmez
//
// Harcamanın kendisini bilmez: sepet, sipariş ve tutarlar başka modüllerin
// verisidir. Bu yüzden limiti UYGULAYAN taraf da bu modül DEĞİLDİR — modül
// yalnızca kuralı (limit, şirketin para birimi ve geçerli pencerenin
// başlangıcı) yayımlar; kuralı harcamaya uygulayan order modülüdür, çünkü
// harcama onun verisidir ve kural ancak siparişin yazıldığı işlemde
// uygulandığında yarışa kapalı olur (bkz. service/interop.go). Pencerenin
// tanımı için bkz. internal/modules/b2b/models, SpendingResetPeriod.
//
// Modül hiçbir modülü import etmez (Prensip 2.1/2.4, ADR 0001). Çalışanın
// müşteri kaydına bağı Module Links ile kurulur ve şemada karşılığı olan bir
// sütun yoktur (Prensip 2.2).
//
// # Dışarıya açtığı yüzeyler
//
//   - "b2b.service" — modülün zengin tipleriyle konuşan servis; bugün yalnızca
//     modülün kendi HTTP yüzeyi tüketir.
//   - "b2b.interop" — harcama KURALINI yayımlayan ilkel yüzey (ADR 0001).
//     order modülü onu bu adla çözer ve kuralı siparişin yazıldığı işlemde
//     uygular.
//   - /admin/v1/b2b/companies, /admin/v1/b2b/employees — yönetim API'si.
//   - /store/v1/b2b/customers/{customer_id}/… — vitrin API'si.
//
// # Neden Query sağlayıcısı YOK
//
// Modül "b2b_employee.query" gibi bir sağlayıcı KAYDETMEZ. Sağlayıcı, Query
// katmanının genişletmelerinde kök ya da hedef olabilmek içindir; b2b'yi
// genişletmenin ya da b2b üzerinden genişletmenin bugün bir tüketicisi yoktur.
// Kaydedilseydi, çağıranı olmayan bir yetenek daha eklenmiş olurdu — bu depoda
// tekrar eden hata sınıfı tam olarak budur. İhtiyaç doğduğunda eklenmesi
// birkaç satırdır; kaldırılması ise yayımlanmış bir sözleşmeyi geri almaktır.
//
// # Bildirdiği link
//
// "b2b_employee_customer" tanımını BU modül bildirir (ADR 0005); bağın sahibi
// çalışan kaydıdır. Kardinalite seçiminin gerekçesi için bkz.
// internal/modules/b2b/service, Definitions.
package b2b

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
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/repository"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration versiyon tablosunun öneki
	// de budur.
	ModuleName = "b2b"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName modüller arası İLKEL yüzeyin container'daki adıdır.
	//
	// Servisin kendisinden AYRI kaydedilir: servis b2b'nin zengin tipleriyle
	// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Harcama limitini
	// uygulayan order modülü onu bu adla ve kendi tanımladığı dar arayüzle
	// çözer (bkz. service/interop.go).
	InteropName = ModuleName + ".interop"
)

// Container'da çözülen çekirdek servislerin adları.
const (
	svcDB   = "core.db"
	svcLink = "core.link"
)

// Hata kodları.
const (
	codeSetupFailed = "b2b_module_setup_failed"
	codeLinkDefine  = "b2b_link_define_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı KÖKTEN okur ve embed.FS dosyaları klasör adıyla birlikte
// taşırdı.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Module b2b modülünün [module.Module] uygulamasıdır.
type Module struct {
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca modülün uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kurulmamış bir b2b modülü üretir; servis [Module.Register] içinde
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

// Register servisi container'a kaydeder ve link tanımını bildirir.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi). core.db ve core.link
// modüller ayağa kalkmadan önce kompozisyon kökünde hazır değer olarak
// kaydedildiği için burada çözülmeleri güvenlidir ve eksiklikleri modülün hiç
// çalışamayacağı bir kurulum hatasıdır — sessizce ertelenmez.
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
		Repo:   repository.New(pool.Pool()),
		Links:  links,
		Logger: m.log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	// Modüller arası yüzey AYRI bir adla kaydedilir: servis b2b'nin zengin
	// tipleriyle konuşur, bu yüzey yalnızca ilkel tiplerle (ADR 0001).
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
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
	m.log.InfoContext(ctx, "b2b modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("interop", InteropName),
		slog.String("link", service.LinkEmployeeCustomer),
	)
	return nil
}

// Routes modülün admin ve store route'larını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın ilk
// istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		m.log.Warn("b2b modülü Register edilmeden Routes çağrıldı, route bağlanmadı")
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
		panic("b2b: migration kaynağı açılamadı: " + err.Error())
	}
	return sub
}
