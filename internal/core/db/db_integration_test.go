//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle ayrılmıştır.
// Çalıştırmak için: make test-integration
package db_test

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// Testlerde kullanılan iki sahte modülün migration'ları. İkisi de aynı
// veritabanına yazar; amaç ayrı versiyon tablolarının birbirini bozmadığını
// göstermektir (plan Bölüm 2.1/2.3).
//
//go:embed testdata/alpha
var alphaMigrations embed.FS

//go:embed testdata/beta
var betaMigrations embed.FS

// Kasten patlayan bir migration: yürütme hatasının tipli hata olarak dışarı
// çıktığını doğrulamak için.
//
//go:embed testdata/broken
var brokenMigrations embed.FS

// Geri alma testleri kendi migration'larıyla çalışır; alpha/beta durumundan
// bağımsızdır.
//
//go:embed testdata/rollback
var rollbackMigrations embed.FS

const postgresImage = "postgres:16-alpine"

// testDSN TestMain'in kaldırdığı konteynerin bağlantı adresidir.
var testDSN string

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırıp tüm testleri onun
// üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)
		return 1
	}

	return m.Run()
}

// TestMigrateIsolatesOwners iki modülün aynı veritabanında birbirinden bağımsız
// migration defterleri tuttuğunu baştan sona doğrular. Alt testler sıralı
// çalışır; her biri bir öncekinin bıraktığı durumu devralır.
func TestMigrateIsolatesOwners(t *testing.T) {
	ctx := context.Background()
	pool := openPool(ctx, t)

	alphaSrc := migrationsFor(t, alphaMigrations, "alpha")
	betaSrc := migrationsFor(t, betaMigrations, "beta")

	t.Run("her modül kendi versiyon tablosunu oluşturur", func(t *testing.T) {
		require.NoError(t, db.Migrate(ctx, testDSN, alphaSrc, "alpha"))
		require.NoError(t, db.Migrate(ctx, testDSN, betaSrc, "beta"))

		assert.True(t, tableExists(ctx, t, pool, migrationsTable(t, "alpha")),
			"alpha_schema_migrations oluşmalı")
		assert.True(t, tableExists(ctx, t, pool, migrationsTable(t, "beta")),
			"beta_schema_migrations oluşmalı")
		assert.False(t, tableExists(ctx, t, pool, "schema_migrations"),
			"ortak schema_migrations tablosu ASLA oluşmaz; oluştuysa modüller izole değil demektir")

		assert.True(t, tableExists(ctx, t, pool, "alpha_items"))
		assert.True(t, tableExists(ctx, t, pool, "beta_items"))
	})

	t.Run("sürümler bağımsız ilerler", func(t *testing.T) {
		alphaVersion, dirty, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(2), alphaVersion, "alpha'nın iki migration'ı var")
		assert.False(t, dirty)

		betaVersion, dirty, err := db.Version(ctx, testDSN, "beta")
		require.NoError(t, err)
		assert.Equal(t, uint(1), betaVersion, "beta'nın tek migration'ı var")
		assert.False(t, dirty)
	})

	t.Run("tekrar çalıştırmak hata vermez", func(t *testing.T) {
		// migrate.ErrNoChange yutulmalı: idempotent açılış akışının şartı.
		require.NoError(t, db.Migrate(ctx, testDSN, alphaSrc, "alpha"))
		require.NoError(t, db.Migrate(ctx, testDSN, betaSrc, "beta"))

		alphaVersion, _, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(2), alphaVersion)
	})

	t.Run("tek adım geri alma yalnızca sahibini etkiler", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, alphaSrc, "alpha", 1))

		alphaVersion, dirty, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(1), alphaVersion)
		assert.False(t, dirty)

		assert.False(t, columnExists(ctx, t, pool, "alpha_items", "label"),
			"ikinci migration geri alındığı için label sütunu düşmeli")
		assert.True(t, tableExists(ctx, t, pool, "alpha_items"),
			"ilk migration hâlâ uygulanmış olmalı")

		betaVersion, _, err := db.Version(ctx, testDSN, "beta")
		require.NoError(t, err)
		assert.Equal(t, uint(1), betaVersion, "beta, alpha'nın geri almasından etkilenmemeli")
		assert.True(t, tableExists(ctx, t, pool, "beta_items"))
	})

	t.Run("tümünü geri alma diğer modülün tablolarına dokunmaz", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, alphaSrc, "alpha", 0))

		alphaVersion, dirty, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(0), alphaVersion)
		assert.False(t, dirty)
		assert.False(t, tableExists(ctx, t, pool, "alpha_items"))

		assert.True(t, tableExists(ctx, t, pool, "beta_items"), "beta'nın verisi ayakta kalmalı")
		betaVersion, _, err := db.Version(ctx, testDSN, "beta")
		require.NoError(t, err)
		assert.Equal(t, uint(1), betaVersion)
	})

	t.Run("geri alacak bir şey kalmayınca hata vermez", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, alphaSrc, "alpha", 0))
	})

	t.Run("yeniden uygulanabilir", func(t *testing.T) {
		require.NoError(t, db.Migrate(ctx, testDSN, alphaSrc, "alpha"))

		alphaVersion, _, err := db.Version(ctx, testDSN, "alpha")
		require.NoError(t, err)
		assert.Equal(t, uint(2), alphaVersion)
		assert.True(t, columnExists(ctx, t, pool, "alpha_items", "label"))
	})
}

// TestVersionOnUnmigratedOwner hiç migration uygulanmamış bir modülün sıfır
// sürüm bildirdiğini doğrular.
func TestVersionOnUnmigratedOwner(t *testing.T) {
	ctx := context.Background()

	version, dirty, err := db.Version(ctx, testDSN, "gamma")
	require.NoError(t, err)
	assert.Equal(t, uint(0), version)
	assert.False(t, dirty)
}

// TestPoolLifecycle havuzun açılıp sağlık kontrolünden geçtiğini ve
// kapatıldıktan sonra kullanılamadığını doğrular.
func TestPoolLifecycle(t *testing.T) {
	ctx := context.Background()

	cfg := db.DefaultConfig(testDSN)
	cfg.MaxConns = 4
	cfg.MinConns = 1

	pool, err := db.New(ctx, cfg, nil)
	require.NoError(t, err)

	require.NoError(t, pool.Ping(ctx))
	require.NotNil(t, pool.Pool())
	assert.NotContains(t, pool.Target(), "gobit:gobit@", "hedef gösterimi kimlik bilgisi içermemeli")

	var one int
	require.NoError(t, pool.Pool().QueryRow(ctx, "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one)

	pool.Close()

	// Kapatılmış havuz üzerinde yapılan çağrı tipli bir hata döner.
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	require.Error(t, pool.Ping(pingCtx))
}

// TestNewFailsOnUnreachableDatabase erişilemeyen bir hedefin KindUnavailable
// sınıfında ve parola sızdırmayan bir hata ürettiğini doğrular.
func TestNewFailsOnUnreachableDatabase(t *testing.T) {
	ctx := context.Background()

	const parola = "cok-gizli-parola"
	cfg := db.DefaultConfig("postgres://gobit:" + parola + "@127.0.0.1:1/gobit?sslmode=disable")
	cfg.ConnectTimeout = 2 * time.Second

	pool, err := db.New(ctx, cfg, nil)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
	assert.NotContains(t, err.Error(), parola, "hata mesajı parola sızdıramaz")
}

// TestMigrateReportsFailedMigration bozuk SQL içeren bir migration'ın tipli
// hata ürettiğini ve versiyon defterini kirli bıraktığını doğrular. Bu, paketin
// en kritik hata yoludur: sessizce yutulursa bozuk şema "migration başarılı"
// loguyla üretime çıkar.
func TestMigrateReportsFailedMigration(t *testing.T) {
	ctx := context.Background()
	src := migrationsFor(t, brokenMigrations, "broken")

	err := db.Migrate(ctx, testDSN, src, "broken")

	require.Error(t, err, "başarısız migration hata döndürmeli")
	assert.Equal(t, "db_migration_failed", errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindInternal),
		"hata sınıfı KindInternal olmalı, %v alındı", errors.KindOf(err))

	version, dirty, verErr := db.Version(ctx, testDSN, "broken")
	require.NoError(t, verErr)
	assert.Equal(t, uint(1), version)
	assert.True(t, dirty, "yarıda kalan migration kirli bayrağı bırakmalı")
}

// TestMigrateDownWithNothingToRollBack MigrateDown'ın godoc'undaki "geri
// alınacak migration kalmamışsa hata dönmez" sözünün steps > 0 yolunda da
// geçerli olduğunu doğrular. golang-migrate bu iki durumu ErrNoChange DEĞİL,
// os.ErrNotExist ve ErrShortLimit ile bildirir.
func TestMigrateDownWithNothingToRollBack(t *testing.T) {
	ctx := context.Background()
	src := migrationsFor(t, rollbackMigrations, "rollback")

	t.Run("hiç migration uygulanmamış modül", func(t *testing.T) {
		require.NoError(t, db.MigrateDown(ctx, testDSN, src, "rollbackfresh", 1))

		version, dirty, err := db.Version(ctx, testDSN, "rollbackfresh")
		require.NoError(t, err)
		assert.Equal(t, uint(0), version)
		assert.False(t, dirty)
	})

	t.Run("mevcut olandan fazla adım", func(t *testing.T) {
		require.NoError(t, db.Migrate(ctx, testDSN, src, "rollbacksteps"))
		require.NoError(t, db.MigrateDown(ctx, testDSN, src, "rollbacksteps", 5))

		version, dirty, err := db.Version(ctx, testDSN, "rollbacksteps")
		require.NoError(t, err)
		assert.Equal(t, uint(0), version, "eldeki tüm migration'lar geri alınmalı")
		assert.False(t, dirty)
	})
}

// TestMigrateReportsCancellationMidRun migration akışı ORTASINDA süresi dolan
// bir bağlamın başarı olarak raporlanmadığını doğrular. golang-migrate zarif
// duruşta nil döndüğü için bu, sessiz veri bozulmasına açılan bir yoldur.
func TestMigrateReportsCancellationMidRun(t *testing.T) {
	pool := openPool(context.Background(), t)

	// İlk migration bağlam sınırından uzun sürer; ikinciye sıra GELMEZ.
	src := fstest.MapFS{
		"000001_slow.up.sql":     &fstest.MapFile{Data: []byte("SELECT pg_sleep(5);")},
		"000001_slow.down.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_second.up.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE slowcancel_items (id TEXT PRIMARY KEY);")},
		"000002_second.down.sql": &fstest.MapFile{Data: []byte("DROP TABLE IF EXISTS slowcancel_items;")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := db.Migrate(ctx, testDSN, src, "slowcancel")
	elapsed := time.Since(start)

	require.Error(t, err, "yarıda kesilen migration ASLA başarı olarak raporlanamaz")
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))
	assert.True(t, errors.HasKind(err, errors.KindUnavailable),
		"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
	assert.Less(t, elapsed, 4*time.Second, "çağrı bağlam sınırında dönmeli")

	assert.False(t, tableExists(context.Background(), t, pool, "slowcancel_items"),
		"iptalden sonraki migration uygulanmamalı")
}

// TestCancellationActuallyStopsRemainingMigrations iptalin yalnızca BEKLEMEYİ
// değil, İŞİ de durdurduğunu doğrular.
//
// Regresyon: iptal yolu işi ayrı bir goroutine'de terk edip ctx sınırında
// dönseydi, terk edilen goroutine kalan migration'ları uygulamaya devam
// ederdi. Çağıran "yarıda kesildi" hatası alır, ama şema arkadan tamamlanırdı.
//
// Senaryo bunu görünür kılacak biçimde kurulmuştur: her ifade TEK BAŞINA
// bağlam sınırının ALTINDA kalır, dolayısıyla ifade bazlı bir zaman aşımı bu
// akışı durduramaz. Kontrol, terk edilmiş bir goroutine'in kalan iki
// migration'ı bitirmesine yetecek kadar BEKLENDİKTEN sonra yapılır.
func TestCancellationActuallyStopsRemainingMigrations(t *testing.T) {
	pool := openPool(context.Background(), t)

	src := fstest.MapFS{
		"000001_slow_one.up.sql":   &fstest.MapFile{Data: []byte("SELECT pg_sleep(0.7);")},
		"000001_slow_one.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000002_slow_two.up.sql":   &fstest.MapFile{Data: []byte("SELECT pg_sleep(0.7);")},
		"000002_slow_two.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000003_marker.up.sql":     &fstest.MapFile{Data: []byte("CREATE TABLE stopafter_items (id TEXT PRIMARY KEY);")},
		"000003_marker.down.sql":   &fstest.MapFile{Data: []byte("DROP TABLE IF EXISTS stopafter_items;")},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := db.Migrate(ctx, testDSN, src, "stopafter")
	require.Error(t, err, "yarıda kesilen migration ASLA başarı olarak raporlanamaz")
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))

	// Terk edilmiş bir goroutine kalan iki migration'ı bu süre içinde
	// rahatlıkla bitirirdi.
	time.Sleep(3 * time.Second)

	assert.False(t, tableExists(context.Background(), t, pool, "stopafter_items"),
		"iptal edilen migration akışı DÖNÜŞTEN SONRA da ilerlememeli")

	// Sürüm defteri de sonraki migration'ları göstermemeli.
	version, _, verErr := db.Version(context.Background(), testDSN, "stopafter")
	require.NoError(t, verErr)
	assert.Less(t, version, uint(3), "iptalden sonra 3. migration kaydedilmemeli")
}

// migrationsFor gömülü dosya sisteminden modülün migration klasörünü ayırır.
func migrationsFor(t *testing.T, embedded embed.FS, owner string) fs.FS {
	t.Helper()

	sub, err := fs.Sub(embedded, "testdata/"+owner)
	require.NoError(t, err)
	return sub
}

// migrationsTable tablo adını hata denetimiyle birlikte üretir.
func migrationsTable(t *testing.T, owner string) string {
	t.Helper()

	table, err := db.MigrationsTable(owner)
	require.NoError(t, err)
	return table
}

// openPool test süresince açık kalan bir doğrulama havuzu açar.
func openPool(ctx context.Context, t *testing.T) *db.Pool {
	t.Helper()

	pool, err := db.New(ctx, db.DefaultConfig(testDSN), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// tableExists public şemada verilen tablonun var olup olmadığını bildirir.
func tableExists(ctx context.Context, t *testing.T, pool *db.Pool, table string) bool {
	t.Helper()

	var exists bool
	err := pool.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = $1
		)`, table).Scan(&exists)
	require.NoError(t, err)
	return exists
}

// columnExists verilen tabloda bir sütunun var olup olmadığını bildirir.
func columnExists(ctx context.Context, t *testing.T, pool *db.Pool, table, column string) bool {
	t.Helper()

	var exists bool
	err := pool.Pool().QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = $1 AND column_name = $2
		)`, table, column).Scan(&exists)
	require.NoError(t, err)
	return exists
}
