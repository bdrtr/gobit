package searchpg

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Hata kodları; çağıran taraf coreerrors.CodeOf ile bunlara bakabilir.
const (
	codeSetupFailed   = "searchpg_module_setup_failed"
	codeNotRegistered = "searchpg_module_not_registered"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// db.Migrate kaynağı KÖKTEN okur.
var migrationsRoot = mustSub(migrationFiles, "migrations")

// modul eklentinin getirdiği commerce modülüdür.
//
// Eklentiye ait olması davranışını değiştirmez: çekirdek onu diğer modüllerden
// AYIRT ETMEZ ve aynı yaşam döngüsünden geçirir (Register -> migration ->
// route). Modülün ilginç yanı, verisinin KENDİNE ait olması ama kayıtların
// başka bir modüle ait olmasıdır; ikisi arasındaki tek bağ, kimlik dizgesi ve
// "product.interop" yüzeyidir.
type modul struct {
	// katalog vitrin kayıtlarının TEMBEL çözülen okuma yüzeyidir.
	katalog *katalog
	// log modülün adıyla etiketlenmiş logger'dır.
	log *slog.Logger

	// indeks arama tablosudur; Register'da kurulur, öncesinde nil'dir.
	indeks depo
	// graph yeniden indeksleme sırasında ürün kimliklerini sayfalar.
	// Çekirdeğin Query katmanıdır; Register'da çözülür.
	graph query.Query
}

// Modülün çekirdek sözleşmesini karşıladığı derleme zamanında sabitlenir.
var _ module.Module = (*modul)(nil)

// newModul kaydedilmeye hazır bir modül üretir.
//
// Bağımlılıklar burada DEĞİL Register'da çözülür; container yalnızca katalog
// yüzeyinin tembel çözümü için saklanır (bkz. [katalog]).
func newModul(c *container.Container, log *slog.Logger) *modul {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &modul{katalog: newKatalog(c), log: log.With("modul", ModuleName)}
}

// Name modülün benzersiz adını döner.
func (m *modul) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
//
// Sürüm defteri modül adına göre ayrıdır ("searchpg_schema_migrations"), yani
// eklentinin şeması hiçbir modülün defteriyle karışmaz; eklenti kaldırıldığında
// geriye yalnızca kendi tablosu kalır.
func (m *modul) Migrations() fs.FS { return migrationsRoot }

// Register indeks deposunu ve Query katmanını kurar.
//
// Yalnızca ÇEKİRDEK servisler çözülür: "core.db" ve "core.query" modüller ayağa
// kalkmadan önce container'a konur. BAŞKA MODÜLLERİN servisleri burada
// çözülmez — bu aşamada kayıtlı olmayabilirler (bkz. module.Module belgesi) ve
// eklentinin modülü, kayıt sırasının SONUNDA eklendiği için "product zaten
// kayıtlıdır" varsayımı bugün doğru olsa da yarın sessizce bozulurdu. Katalog
// yüzeyi bu yüzden ilk kullanımda çözülür.
//
// İkisinden biri eksikse açılış DURUR. Sessiz atlama seçilmedi: indekssiz
// çalışan bir arama ucu, her sorguya boş liste dönerdi ve bu ancak müşteriler
// hiçbir ürünü bulamadığında, yani üretimde fark edilirdi.
func (m *modul) Register(_ context.Context, c *container.Container) error {
	pool, err := container.Resolve[*db.Pool](c, svcDB)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"%s modülü veritabanı havuzunu çözemedi (%q)", ModuleName, svcDB)
	}
	graph, err := container.Resolve[query.Query](c, svcQuery)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindOf(err), codeSetupFailed,
			"%s modülü query katmanını çözemedi (%q)", ModuleName, svcQuery)
	}

	m.indeks = newIndeks(pool.Pool())
	m.graph = graph

	return nil
}

// Routes modülün vitrin ve yönetim uçlarını router'a bağlar.
//
// Register çalışmadıysa hiçbir uç bağlanmaz: indeksi olmayan bir handler'ın
// ilk istekte hata üretmesindense ucun hiç var olmaması yeğdir (product
// modülünün Routes'uyla aynı gerekçe).
//
// # Yetki
//
// Vitrin ucu yetki İSTEMEZ: /store/v1'in kimliği publishable anahtardır ve o
// anahtar tanımı gereği yetki taşımaz. Yönetim ucu [ScopeWrite] ister; kimlik
// katmanını (corehttp.RequireAdmin) router'ı kuran taraf takar.
func (m *modul) Routes(r chi.Router) {
	if m.indeks == nil {
		return
	}

	r.Get(SearchPath, m.ara)
	r.With(corehttp.RequireScope(ScopeWrite)).Post(ReindexPath, m.yenidenIndeksleUcu)
}

// hazir modülün istek karşılamaya hazır olup olmadığını bildirir.
//
// Register çalışmadan bir handler'a girilmesi normal akışta imkânsızdır
// (Routes o durumda hiçbir uç bağlamaz), ama olay işleyicileri Routes'tan
// bağımsız çalışır: abonelik çekirdek tarafından kurulur ve modül Register
// edilmemişse indeks nil kalır. Panik yerine tipli hata dönmek, arızayı
// logda görünür kılar.
func (m *modul) hazir() error {
	if m.indeks == nil {
		return coreerrors.Unavailable(codeNotRegistered,
			"%s modülü kaydedilmedi; arama indeksi kullanılamaz", ModuleName)
	}
	return nil
}

// mustSub alt dizini açar; açılamazsa panikler.
//
// Panik burada güvenlidir: dizin adı derleme zamanında sabittir ve go:embed
// dosyaların varlığını zaten derleme zamanında doğrulamıştır.
func mustSub(files embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(files, dir)
	if err != nil {
		panic("searchpg: gömülü migration dizini açılamadı: " + err.Error())
	}
	return sub
}
