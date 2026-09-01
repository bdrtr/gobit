// Package product katalog modülüdür: ürün, varyant, seçenek, kategori,
// koleksiyon, etiket ve görsel bu modülün verisidir.
//
// # Modülün çekirdekle sözleşmesi
//
// [Module] çekirdeğin module.Module arayüzünü karşılar. Register sırasında
// dört şey yapılır:
//
//  1. Servis container'a "product.service" adıyla kaydedilir.
//  2. Modüller arası İLKEL okuma yüzeyi "product.interop" adıyla kaydedilir
//     (ADR 0006); eklentiler ve akışlar katalog kaydını buradan okur.
//  3. Query sağlayıcıları "product.query" ve "variant.query" adlarıyla
//     kaydedilir (ADR 0004).
//  4. Fiyat, stok ve satış kanalı link tanımları bildirilir (ADR 0005).
//
// # Yayımladığı olaylar
//
// "product.created", "product.updated" ve "product.deleted" — ürün yazıldığında,
// güncellendiğinde ve silindiğinde. Yükleri ve yayım politikası için bkz.
// [service.EventProductCreated] ve service/events.go.
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
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/api"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Name modülün adıdır: container adlarının, migration sürüm defterinin ve log
// alanlarının öneki budur.
const Name = "product"

// Container'da çözülen çekirdek servislerin adları.
const (
	svcDB       = "core.db"
	svcLink     = "core.link"
	svcQuery    = "core.query"
	svcEventBus = "core.eventbus"
)

// ServiceName modülün servisinin container'daki adıdır.
//
// Başka modüller (ADR 0001 gereği bu paketi import ETMEDEN) katalog servisine
// bu adla ulaşır.
const ServiceName = Name + ".service"

// InteropName modüller arası ilkel yüzeyin container'daki adıdır (ADR 0006).
//
// Servisin kendisinden AYRI kaydedilir: servis product'ın zengin tipleriyle
// konuşur, bu yüzey yalnızca ilkel ve stdlib tipleriyle. Eklentiler (plugins/**)
// hiçbir modülü import edemedikleri için katalogu ANCAK bu adla ve kendi
// tanımladıkları dar arayüzle okuyabilir.
const InteropName = Name + ".interop"

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

// Options product modülünün kurulum ayarlarıdır.
//
// Modül internal/core/config paketini TANIMAZ (Prensip 2.4) ve container'da
// da config kayıtlı değildir; ayarlar uygulamayı kuran taraftan parametre
// olarak gelir (auth ve file modüllerindeki kalıbın aynısı).
type Options struct {
	// GraphQL vitrinin GraphQL okuma ucunun sertleştirme sınırlarıdır.
	//
	// Tip modülün kendi tipi DEĞİL, doğrudan [graph.Options]'tır: aradan bir
	// kopya geçirmek, sınırların ikinci bir tanımı ve her yeni sınırda
	// güncellenmesi unutulacak bir eşleme kodu demekti.
	//
	// Sıfır değeri paket varsayılanlarını verir; "sınırsız" ANLAMINA GELMEZ.
	GraphQL graph.Options
}

// Module product modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	opts    Options
	svc     *service.Service
	handler *api.Handler
}

// Çekirdek sözleşmesinin karşılandığı derleme zamanında sabitlenir.
var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca vitrin uçları belgeden sessizce düşerdi. Bu satır o
// sessizliği kapatır.
var _ openapi.Describer = (*Module)(nil)

// New kayıt edilmeye hazır bir modül üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
//
// [Options] sıfır değeriyle çağrılabilir; gömülü kullanım ve testler bunu
// yapar ve GraphQL ucu paket varsayılanı sınırlarla açılır.
func New(opts Options) *Module {
	return &Module{opts: opts}
}

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return Name }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi, interop yüzeyini ve Query sağlayıcılarını container'a
// kaydeder, link tanımlarını bildirir.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi).
//
// # Olay veri yolu ZORUNLUDUR ve eksikse açılış durur
//
// core.eventbus da tıpkı core.db, core.link ve core.query gibi modüller ayağa
// kalkmadan önce main.go'da hazır değer olarak kaydedilir; eksikliği bir
// dağıtım biçimi değil, bir KURULUM HATASIDIR. Bu yüzden "olaylar sessizce
// atlansın" seçilmedi: o yol arızayı görünmez kılardı — katalog çalışmaya
// devam eder, hiçbir hata görünmez, yalnızca arama indeksi güncellenmez ve
// eksiklik ancak müşteriler yeni ürünleri bulamadığında, yani ÜRETİMDE fark
// edilirdi. Açılışta düşen bir kurulum ise ilk saniyede görülür.
//
// Sessiz atlama yine de mümkündür ama YALNIZCA gömülü kullanım ve testler için:
// [service.Options].Events nil verilebilir (bkz. service.Service.publishProductEvent).
// Bu yol Register'dan geçmez.
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
	sorgu, err := container.Resolve[query.Query](c, svcQuery)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü query katmanını çözemedi (%q)", Name, svcQuery)
	}
	// Dar arayüzle çözülür: modül yalnızca YAYIMLAR, abone olmaz ve veri
	// yolunu kapatmaz (bkz. service.EventPublisher).
	bus, err := container.Resolve[service.EventPublisher](c, svcEventBus)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü olay veri yolunu çözemedi (%q)", Name, svcEventBus)
	}

	repo := repository.New(pool.Pool())
	svc, err := service.New(service.Options{
		Repo:   repo,
		Links:  links,
		Query:  sorgu,
		Events: bus,
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
	// Modüller arası yüzey AYRI bir adla kaydedilir: servisin kendisi
	// product'ın zengin tipleriyle konuşur, bu yüzey ise yalnızca ilkel
	// tiplerle (ADR 0006).
	if err := c.Provide(InteropName, service.NewInterop(svc)); err != nil {
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
	m.handler = api.New(svc, m.opts.GraphQL)
	return nil
}

// Routes modülün store ve admin uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
//
// Vitrinin GraphQL okuma ucu (POST /store/v1/graphql) da buradan geçer; uçların
// tamamı [api.Handler.Routes] içinde, tek listede durur. GraphQL yüzeyinin
// kapsamı ve satış kanalı kuralı için bkz. graph paketi.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		return
	}
	m.handler.Routes(r)
}

// Describe modülün vitrin ve yönetim uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir ve iki sebeple oradadır. Sorgu
// parametreleri handler'ın gerçekten okuduklarıdır ve o okuma api paketindedir;
// liste burada dursaydı okumadan uzaklaşır ve ikisi sessizce ayrışırdı. Yönetim
// uçlarının istek gövdeleri ise o paketin DIŞA KAPALI DTO'larıdır; tipleri
// yalnızca belge uğruna dışa açmak modülün yüzeyini genişletirdi.
//
// [Module.Routes]'un tersine Register kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

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
