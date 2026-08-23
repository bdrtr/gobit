package db

import (
	"context"
	"database/sql"
	"io/fs"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql için "pgx" sürücüsünü kaydeder

	"github.com/bdrtr/gobit/internal/core/errors"
)

const (
	// migrationsTableSuffix modül başına açılan versiyon tablosunun son ekidir.
	migrationsTableSuffix = "_schema_migrations"
	// sourceName iofs sürücüsünün golang-migrate'e tanıtıldığı addır.
	sourceName = "iofs"
	// databaseDriverName golang-migrate'in postgres sürücüsünün adıdır.
	databaseDriverName = "postgres"
	// sqlDriverName database/sql'e pgx/stdlib tarafından kaydedilen ad.
	sqlDriverName = "pgx"
	// cancelGracePeriod ctx iptal edilip bağlantı kapatıldıktan sonra çalışan
	// işin gerçekten sonlanması için tanınan süredir. Bu süre dolarsa çağıran
	// yine hata alır, ama goroutine'in terk edildiği AÇIKÇA raporlanır.
	cancelGracePeriod = 5 * time.Second
)

// ownerPattern geçerli modül adlarını tanımlar. owner doğrudan bir SQL tablo
// adına dönüştüğü için serbest metne izin verilmez; bu, tablo adı üzerinden
// enjeksiyonu yapısal olarak imkânsız kılar.
var ownerPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// supportedSchemes migration yolunun kabul ettiği DSN şemalarıdır.
var supportedSchemes = []string{"postgres", "postgresql"}

// MigrationsTable owner modülüne ait versiyon tablosunun adını döner.
//
// owner BURADA doğrulanır ve geçersizse KindInvalid sınıfında bir hatayla
// birlikte boş ad döner. Tablo adları SQL'de parametrelenemez, yani ad
// zorunlu olarak dizge birleştirmesiyle üretilir; doğrulamanın fonksiyonun
// KENDİSİNDE olması, dışarıdan gelen bir modül adının doğrulanmadan tablo
// adına dönüşmesini yapısal olarak imkânsız kılar.
//
// Her modül KENDİ tablosunu kullanır. Ortak bir schema_migrations tablosu
// paylaşılsaydı, bir modülün migration'ı diğerinin versiyon defterini yeniden
// yazar ve modüller birbirini bozardı; ayrı tablo, plan Bölüm 2.1/2.3'teki
// modül izolasyonunun veritabanı düzeyindeki karşılığıdır.
func MigrationsTable(owner string) (string, error) {
	if err := validateOwner(owner); err != nil {
		return "", err
	}
	return owner + migrationsTableSuffix, nil
}

// validateScheme DSN'in desteklenen bir PostgreSQL şeması taşıdığını doğrular.
//
// database/sql tembeldir: sql.Open bağlanmaz, dolayısıyla desteklenmeyen bir
// şema ancak ilk bağlantı denemesinde ve "erişilemez" gibi yanıltıcı bir
// sınıfla ortaya çıkardı. Şema, bağlanmadan önce burada denetlenir.
func validateScheme(databaseURL string) error {
	u, err := url.Parse(databaseURL)
	if err != nil {
		return errors.Wrap(err, errors.KindInvalid, "db_dsn_invalid",
			"DSN çözümlenemedi (hedef: %s)", Redact(databaseURL))
	}
	if !slices.Contains(supportedSchemes, u.Scheme) {
		return errors.Invalid("db_dsn_unsupported",
			"desteklenmeyen DSN şeması %q (beklenen: %s)", u.Scheme, strings.Join(supportedSchemes, ", "))
	}
	return nil
}

// validateOwner modül adının tablo adına güvenle çevrilebileceğini doğrular.
func validateOwner(owner string) error {
	if !ownerPattern.MatchString(owner) {
		return errors.Invalid("db_migration_owner_invalid",
			"geçersiz modül adı %q (beklenen desen: %s)", owner, ownerPattern.String())
	}
	return nil
}

// Migrate owner modülünün bekleyen migration'larını uygular.
//
// Uygulanacak migration kalmamışsa hata dönmez. ctx iptal edilir veya süresi
// dolarsa çalışan iş DURDURULUR (bkz. [session.run]) ve KindUnavailable
// sınıfında db_migration_canceled hatası dönülür; bu durumda şemanın kısmen
// uygulanmış olabileceği varsayılmalı ve [Version] ile denetlenmelidir.
func Migrate(ctx context.Context, databaseURL string, src fs.FS, owner string) error {
	return runMigration(ctx, databaseURL, src, owner, "up", func(m *migrate.Migrate) error {
		return m.Up()
	})
}

// MigrateDown owner modülünün migration'larını geri alır.
//
// steps sıfır veya negatifse TÜM migration'lar geri alınır; pozitifse yalnızca
// o kadar adım. Geri alınacak migration kalmamışsa hata dönmez — bu, hiç
// migrate edilmemiş bir ortamda rollback çalıştırmanın normal sonucudur.
func MigrateDown(ctx context.Context, databaseURL string, src fs.FS, owner string, steps int) error {
	return runMigration(ctx, databaseURL, src, owner, "down", func(m *migrate.Migrate) error {
		if steps <= 0 {
			return m.Down()
		}
		return asNoChange(m.Steps(-steps))
	})
}

// asNoChange "yapacak iş yok" anlamına gelen sürücü hatalarını ErrNoChange'e
// çevirir. golang-migrate, adım sayısı verilen geri alma yolunda hiç migration
// uygulanmamışsa os.ErrNotExist, mevcut olandan fazla adım istenirse
// ErrShortLimit döner; ikisi de bir arıza değildir.
func asNoChange(err error) error {
	var short migrate.ErrShortLimit
	if errors.Is(err, os.ErrNotExist) || errors.As(err, &short) {
		return migrate.ErrNoChange
	}
	return err
}

// Version owner modülünün veritabanındaki geçerli migration sürümünü döner.
//
// Hiç migration uygulanmamışsa (0, false, nil) döner. dirty true ise yarıda
// kalmış bir migration vardır; bu durumda otomatik ilerleme mümkün değildir ve
// elle müdahale (golang-migrate force) gerekir.
//
// Not: golang-migrate sürücüsü versiyon tablosunu yoksa oluşturur; bu çağrı
// bu nedenle tümüyle yan etkisiz değildir.
func Version(ctx context.Context, databaseURL, owner string) (version uint, dirty bool, err error) {
	if err = ctx.Err(); err != nil {
		return 0, false, errors.Wrap(err, errors.KindUnavailable, "db_migration_canceled",
			"%s modülünün migration sürümü okunmadan iptal edildi", owner)
	}

	s, err := openSession(ctx, databaseURL, nil, owner)
	if err != nil {
		return 0, false, err
	}
	defer s.close()

	var (
		current int
		isDirty bool
	)
	runErr := s.run(ctx, "version", func(*migrate.Migrate) error {
		var verErr error
		current, isDirty, verErr = s.driver.Version()
		return verErr
	})
	if runErr != nil {
		return 0, false, runErr
	}

	// database.NilVersion (-1) "hiç migration uygulanmadı" demektir.
	if current < 0 {
		return 0, isDirty, nil
	}
	return uint(current), isDirty, nil
}

// runMigration girdileri doğrular, oturumu açar ve işlemi ctx sınırları içinde
// çalıştırır. action yalnızca hata mesajında geçen okunabilir eylem adıdır.
func runMigration(
	ctx context.Context,
	databaseURL string,
	src fs.FS,
	owner, action string,
	run func(*migrate.Migrate) error,
) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "db_migration_canceled",
			"%s modülünün migration'ı (%s) başlamadan iptal edildi", owner, action)
	}
	if src == nil {
		return errors.Invalid("db_migration_source_missing",
			"%s modülü için migration kaynağı verilmedi", owner)
	}

	s, err := openSession(ctx, databaseURL, src, owner)
	if err != nil {
		return err
	}
	defer s.close()

	return s.run(ctx, action, run)
}

// session ctx'e bağlı, TEK bağlantılı bir migration oturumudur.
//
// Bağlantının tek olması zorunludur: golang-migrate'in postgres sürücüsü
// advisory lock'u alıp bırakırken aynı bağlantıyı kullanmak zorundadır.
type session struct {
	db      *sql.DB
	conn    *sql.Conn
	driver  database.Driver
	source  source.Driver
	migrate *migrate.Migrate
	owner   string
}

// openSession migration için gereken bağlantıyı ve sürücüleri kurar.
// src nil ise migrate örneği kurulmaz; yalnızca sürücü üzerinden sürüm okumak
// isteyen çağıranlar (bkz. [Version]) için yeterlidir.
func openSession(ctx context.Context, databaseURL string, src fs.FS, owner string) (*session, error) {
	table, err := MigrationsTable(owner)
	if err != nil {
		return nil, err
	}

	if err := validateScheme(databaseURL); err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open(sqlDriverName, databaseURL)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, "db_migration_dsn_invalid",
			"%s modülü için migration DSN'i çözümlenemedi (hedef: %s)", owner, Redact(databaseURL))
	}
	sqlDB.SetMaxOpenConns(1)

	s := &session{db: sqlDB, owner: owner}

	// Conn(ctx) bağlantı kurulumunu ctx'e bağlar; erişilemeyen bir sunucuda
	// çağrı ctx sınırında döner, işletim sistemi TCP zaman aşımını beklemez.
	s.conn, err = sqlDB.Conn(ctx)
	if err != nil {
		s.close()
		// Başarısızlığın sebebi ctx'in dolması ise bunu iptal olarak raporla;
		// çağıran "sunucu erişilemez" ile "benim bütçem doldu" arasında ayrım
		// yapabilmelidir.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Wrap(ctxErr, errors.KindUnavailable, "db_migration_canceled",
				"%s modülü için migration bağlantısı kurulurken iptal edildi (hedef: %s)",
				owner, Redact(databaseURL))
		}
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_migration_connect_failed",
			"%s modülü için migration bağlantısı kurulamadı (hedef: %s)", owner, Redact(databaseURL))
	}

	cfg := &postgres.Config{MigrationsTable: table}
	if deadline, ok := ctx.Deadline(); ok {
		// Savunma katmanı: bağlantı kapatma yolunun kaçırdığı bir durumda tek
		// bir ifadenin süresiz sürmesini engeller.
		if remaining := time.Until(deadline); remaining > 0 {
			cfg.StatementTimeout = remaining
		}
	}

	s.driver, err = postgres.WithConnection(ctx, s.conn, cfg)
	if err != nil {
		s.close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, errors.Wrap(ctxErr, errors.KindUnavailable, "db_migration_canceled",
				"%s modülü için migration sürücüsü kurulurken iptal edildi (hedef: %s)",
				owner, Redact(databaseURL))
		}
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_migration_connect_failed",
			"%s modülü için migration sürücüsü kurulamadı (hedef: %s)", owner, Redact(databaseURL))
	}

	if src == nil {
		return s, nil
	}

	s.source, err = iofs.New(src, ".")
	if err != nil {
		s.close()
		return nil, errors.Wrap(err, errors.KindInvalid, "db_migration_source_invalid",
			"%s modülünün migration kaynağı okunamadı", owner)
	}

	s.migrate, err = migrate.NewWithInstance(sourceName, s.source, databaseDriverName, s.driver)
	if err != nil {
		s.close()
		return nil, errors.Wrap(err, errors.KindInternal, "db_migration_init_failed",
			"%s modülü için migration örneği kurulamadı", owner)
	}
	return s, nil
}

// close oturumun tüm kaynaklarını kapatır. Birden çok kez çağrılabilir.
func (s *session) close() {
	// migrate.Close kaynak ve veritabanı sürücülerini kapatır; sürücü zaten
	// kapalıysa dönen hata anlamsızdır, bu yüzden yutulur.
	if s.migrate != nil {
		_, _ = s.migrate.Close()
	} else if s.driver != nil {
		_ = s.driver.Close()
	}
	if s.conn != nil {
		_ = s.conn.Close()
	}
	if s.db != nil {
		_ = s.db.Close()
	}
}

// run işlemi ayrı bir goroutine'de çalıştırır ve ctx iptal edilirse işi
// GERÇEKTEN durdurur.
//
// golang-migrate'in postgres sürücüsü kurulumdan sonra tüm sorgularında
// context.Background() kullanır (Lock ise pg_advisory_lock üzerinde süresiz
// bekler). Bu yüzden iptal iki koldan uygulanır:
//
//  1. GracefulStop ile bir sonraki migration'ın BAŞLAMASI engellenir,
//  2. bağlantı kapatılarak UÇUŞTAKİ ifade koparılır.
//
// Ardından işin gerçekten sonlandığı beklenir; goroutine terk edilmez.
// Sonlanma cancelGracePeriod içinde gerçekleşmezse bu durum hata mesajında
// açıkça belirtilir.
func (s *session) run(ctx context.Context, action string, fn func(*migrate.Migrate) error) error {
	done := make(chan error, 1)
	go func() { done <- fn(s.migrate) }()

	select {
	case err := <-done:
		return s.classify(err, action)

	case <-ctx.Done():
		// İş, ctx dolmadan hemen önce bitmiş olabilir; select hazır olan iki
		// koldan rastgele birini seçer. Başarıyı iptal sanmamak için önce
		// bakılır.
		select {
		case err := <-done:
			return s.classify(err, action)
		default:
		}

		// Version yolunda migrate örneği kurulmaz; GracefulStop yalnızca
		// gerçek bir migration çalışırken anlamlıdır.
		if s.migrate != nil {
			select {
			case s.migrate.GracefulStop <- true:
			default: // kanal doluysa sinyal zaten verilmiş demektir
			}
		}
		if s.conn != nil {
			_ = s.conn.Close()
		}

		select {
		case <-done:
			return errors.Wrap(ctx.Err(), errors.KindUnavailable, "db_migration_canceled",
				"%s modülünün migration'ı (%s) yarıda kesildi", s.owner, action)
		case <-time.After(cancelGracePeriod):
			return errors.Wrap(ctx.Err(), errors.KindUnavailable, "db_migration_canceled",
				"%s modülünün migration'ı (%s) iptal edildi ancak %s içinde durmadı",
				s.owner, action, cancelGracePeriod)
		}
	}
}

// classify golang-migrate'ten dönen ham hatayı tipli hataya çevirir.
func (s *session) classify(err error, action string) error {
	// ErrNoChange bir hata değildir: uygulanacak/geri alınacak migration
	// kalmamış olması migration runner'ının normal sonucudur.
	if err == nil || errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return errors.Wrap(err, errors.KindInternal, "db_migration_failed",
		"%s modülünün migration'ı (%s) uygulanamadı", s.owner, action)
}
