// Package file dosya modülüdür (plan Bölüm 5.6 — FileProvider soyutlaması).
//
// Sorumluluğu tek cümleyle: istemciden gelen RASTGELE BAYTLARI denetleyip bir
// depoya yazdırmak, yazılanı kalıcı bir deftere geçirmek ve gerektiğinde geri
// sunmak. Modül file_uploads verisinin TEK yazma yetkilisidir (Prensip 2.3).
//
// # Neden var
//
// Ürün görseli bugüne kadar yalnızca URL alıyordu; bir dosyayı sisteme
// vermenin hiçbir yolu yoktu. Bu modülün ürettiği adres, mevcut ürün görseli
// akışına DOĞRUDAN takılır — yani product modülüne hiç dokunmadan gerçek bir
// tüketici yolu doğar.
//
// # Sağlayıcı soyutlaması
//
// Baytları saklayan taraf modül değil, internal/core/provider'daki FileProvider
// sözleşmesini karşılayan bir SAĞLAYICIDIR. Modül sağlayıcıları kimlikleriyle
// bir kayıtta tutar ([service.ProviderRegistry]) ve yükleme anında ADLA çözer.
// Kutudan çıkan tek sağlayıcı, dosyaları yerel diske yazan "local"dır
// (internal/modules/file/local); eklenti sistemi, çekirdeğe ve bu modüle
// dokunmadan container'daki kayda kendi sağlayıcısını ekleyebilir
// (coreplugin.Host.RegisterFileProvider).
//
// Hangi sağlayıcının kullanılacağını FILE_PROVIDER seçer. Adın GERÇEKTEN
// kayıtlı olup olmadığı burada doğrulanamaz — eklenti sağlayıcıları modüller
// ayağa kalktıktan SONRA kaydedilir — ve denetim bu yüzden kompozisyon
// kökündedir (cmd/server): bilinmeyen bir ad açılışı DURDURUR.
//
// # Güvenlik kararları
//
// Bu, depoda istemciden rastgele bayt kabul edilen İLK yerdir. Kararlar tek tek
// gerekçeleriyle ilgili dosyalarda yazılıdır; özeti:
//
//   - İstemcinin dosya adı ASLA yol olmaz; depo anahtarını sağlayıcı üretir
//     (local paketi). Yol geçişi "temizlenerek" değil, YAPISAL olarak
//     imkânsızdır.
//   - İçerik tipi istemciye SORULMAZ, içerikten tespit edilir (api paketi).
//   - İzin listesi (yasak listesi değil) yapılandırmadan gelir ve depoya tek
//     bayt yazılmadan uygulanır (service paketi). Varsayılanda SVG YOKTUR.
//   - Boyut sınırı hem gövdeye hem dosyaya ayrı ayrı zorlanır ve
//     yapılandırılabilirdir.
//   - Sunumda Content-Type SAKLANAN tipten yazılır ve her yanıt
//     X-Content-Type-Options: nosniff taşır (api paketi).
//
// # Neyi bilmez
//
// Modül hiçbir modülü import etmez ve dosyanın NEYE ait olduğunu bilmez:
// yükleme kaydı bir ürüne, bir varyanta ya da hiçbir şeye bağlı olabilir.
// uploaded_by serbest bir metindir, foreign key DEĞİLDİR (Prensip 2.2).
//
// # Dışarıya açtığı yüzeyler
//
//   - "file.service" — modül içi zengin yüzey (domain tipleriyle).
//   - "file.providers" — sağlayıcı kaydı; eklentiler buraya sağlayıcı ekler.
//   - POST/GET /admin/v1/uploads ve DELETE /admin/v1/uploads/{id} — yönetim.
//   - GET /files/{key} — KORUMASIZ sunum ucu; gerekçesi api paketindedir.
//
// Modüller arası bir "interop" yüzeyi ve Query sağlayıcısı BİLİNÇLİ OLARAK
// YOKTUR: yüklemeyi okuyan başka bir modül yoktur ve okumak isteseydi
// ihtiyacı olan tek şey ADRESTİR — o da ürün görseli kaydında zaten duruyor.
// Tüketicisi olmayan bir sözleşme açmak, bir daha kapatılamayacak bir alan
// üretirdi.
package file

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
	"github.com/bdrtr/gobit/internal/modules/file/api"
	"github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/file/repository"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// ModuleName modülün adıdır; container adlarının ve migration sürüm defterinin
// önekidir.
const ModuleName = "file"

// ServiceName modül servisinin container'daki adıdır.
const ServiceName = ModuleName + ".service"

// ProvidersName sağlayıcı kaydının container'daki adıdır.
//
// Eklenti sistemi kendi FileProvider'ını bu kaydı çözüp ekler; modülün kodunu
// değiştirmesi gerekmez. Değer coreplugin.FileProvidersName ile AYNI olmalıdır
// ve uyum internal/arch testiyle sabitlenmiştir.
const ProvidersName = ModuleName + ".providers"

// DefaultProviderID sağlayıcı seçilmediğinde kullanılan kimliktir.
//
// Değer local paketinden gelir: config'in varsayılanı ("local") ile
// sağlayıcının kimliği ayrışırsa kurulum, hiçbir sağlayıcı bulamayan bir
// yükleme yoluyla açılırdı.
const DefaultProviderID = local.ID

// svcDB çekirdek veritabanı havuzunun container'daki adıdır.
const svcDB = "core.db"

// Hata kodları.
const (
	codeSetupFailed      = "file_module_setup_failed"
	codeProviderRegister = "file_module_provider_register_failed"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı kökten okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// Options modülün kurulum ayarlarıdır.
type Options struct {
	// ProviderID yüklemede kullanılacak sağlayıcının kimliğidir
	// (FILE_PROVIDER). Boş verilirse [DefaultProviderID] uygulanır.
	ProviderID string
	// Root "local" sağlayıcısının kök dizinidir (FILE_ROOT).
	//
	// BOŞ verilirse yerel sağlayıcı KAYDEDİLMEZ ve bunun bir uyarı olarak
	// loglanması dışında hiçbir şey olmaz; geçici bir dizine DÜŞÜLMEZ.
	// Gerekçe [local.New] godoc'undadır (özet: geçici dizin, yeniden
	// başlatmada sessiz veri kaybıdır). Kaydedilmemiş sağlayıcı seçiliyse
	// açılış zaten kompozisyon kökünde durur.
	Root string
	// MaxUploadBytes tek bir yüklemenin azami boyutudur
	// (FILE_MAX_UPLOAD_BYTES); zorunludur.
	MaxUploadBytes int64
	// AllowedTypes kabul edilen İÇERİK tipleridir (FILE_ALLOWED_TYPES);
	// en az bir tip zorunludur.
	AllowedTypes []string
	// Logger nil verilirse slog.Default kullanılır.
	Logger *slog.Logger
}

// Module file modülünün çekirdeğe sunduğu uygulamadır.
type Module struct {
	opts      Options
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
// kırılmaz, yalnızca bu modülün ucu belgeden sessizce düşerdi.
var _ openapi.Describer = (*Module)(nil)

// New kaydedilmeye hazır bir file modülü üretir.
//
// Bağımlılıklar burada değil Register sırasında çözülür: container o ana kadar
// çekirdek servisleri kurmuş olmayabilir.
func New(opts Options) *Module {
	if opts.ProviderID == "" {
		opts.ProviderID = DefaultProviderID
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return &Module{opts: opts}
}

// Name modülün benzersiz adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi ve sağlayıcı kaydını container'a kaydeder.
//
// Yalnızca ÇEKİRDEK servisler çözülür; başka modüllerin servisleri bu aşamada
// henüz kayıtlı olmayabilir (bkz. module.Module belgesi).
//
// Varsayılan sağlayıcı ([local.Provider]) da burada kurulur ve kök dizini
// AÇILIŞTA yaratılır: yazılamayan bir kök, ilk yüklemeye kadar beklerse arıza
// müşteri karşısında ortaya çıkar — oysa yanlış yazılmış bir yol, açılışta
// düzeltilebilecek bir yapılandırma hatasıdır. Seçili sağlayıcı "local"
// olmasa bile kaydedilir, çünkü kayıt bir listedir, seçim değil: kurulum
// nesne deposuna geçtiğinde ESKİ kayıtlar hâlâ yerel diskte durur ve onları
// okuyup silebilecek tek şey bu sağlayıcıdır.
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, svcDB)
	}

	log := m.opts.Logger.With("modul", ModuleName)

	providers := service.NewProviderRegistry()
	if err := m.yerelSaglayiciyiKaydet(ctx, providers, log); err != nil {
		return err
	}

	svc, err := service.New(service.Options{
		Store:          repository.New(pool.Pool()),
		Providers:      providers,
		ProviderID:     m.opts.ProviderID,
		MaxUploadBytes: m.opts.MaxUploadBytes,
		AllowedTypes:   m.opts.AllowedTypes,
		Logger:         log,
	})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s servisi kurulamadı", ModuleName)
	}

	if err := c.Provide(ServiceName, svc); err != nil {
		return err
	}
	if err := c.Provide(ProvidersName, providers); err != nil {
		return err
	}

	m.svc = svc
	m.providers = providers
	m.handler = api.New(svc)

	log.DebugContext(ctx, "file modülü kaydedildi",
		"servis", ServiceName,
		"saglayicilar", providers.IDs(),
		"secili_saglayici", m.opts.ProviderID,
		"azami_boyut", m.opts.MaxUploadBytes,
		"izinli_tipler", m.opts.AllowedTypes,
	)

	return nil
}

// yerelSaglayiciyiKaydet kök dizin verilmişse "local" sağlayıcısını kurar.
//
// Kök BOŞSA sağlayıcı kaydedilmez ve GEÇİCİ DİZİNE düşülmez; uyarı loglanır.
// Uyarının hata olmamasının sebebi, kurulumun meşru olabilmesidir: nesne
// deposuna yazan (ya da hiç dosya yüklemeyen) bir kurulumda yerel kök gereksiz
// bir ayardır ve onu istemek, karşılığı olmayan bir zorunluluk olurdu. Seçili
// sağlayıcı buysa açılış zaten durur — ama kompozisyon kökünde ve "hangi
// sağlayıcı kayıtlı" listesiyle birlikte.
func (m *Module) yerelSaglayiciyiKaydet(
	ctx context.Context,
	providers *service.ProviderRegistry,
	log *slog.Logger,
) error {
	if m.opts.Root == "" {
		log.WarnContext(ctx, "yerel dosya sağlayıcısı kaydedilmedi: kök dizin verilmemiş",
			"cozum", "FILE_ROOT ayarlayın",
			"uyari", "geçici dizine DÜŞÜLMEZ; yeniden başlatmada sessiz veri kaybı olurdu")

		return nil
	}

	prov, err := local.New(local.Options{Root: m.opts.Root})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"%s modülü yerel dosya sağlayıcısını kuramadı (%s)", ModuleName, m.opts.Root)
	}

	if err := providers.Register(prov); err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeProviderRegister,
			"%s modülü varsayılan sağlayıcıyı kaydedemedi", ModuleName)
	}

	return nil
}

// Routes modülün route'larını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: servisi olmayan bir handler'ın
// ilk istekte panik üretmesindense ucun hiç var olmaması yeğdir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		slog.Default().Warn("file modülü Register edilmeden Routes çağrıldı, route bağlanmadı")

		return
	}

	m.handler.Routes(r)
}

// Describe modülün yönetim uçlarını OpenAPI belgesine işler.
//
// [Module.Routes]'un tersine Register kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil.
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
		panic("file: gömülü migration dizini açılamadı: " + err.Error())
	}

	return sub
}
