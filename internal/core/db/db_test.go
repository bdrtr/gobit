package db

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// testParola ayıklama testlerinde DSN'lere gömülen ve çıktıda ASLA
// görünmemesi gereken sabittir.
const testParola = "cok-gizli-parola"

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	const dsn = "postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable"
	cfg := DefaultConfig(dsn)

	assert.Equal(t, dsn, cfg.URL)
	assert.Equal(t, int32(defaultMaxConns), cfg.MaxConns)
	assert.Equal(t, int32(defaultMinConns), cfg.MinConns)
	assert.Equal(t, defaultMaxConnLifetime, cfg.MaxConnLifetime)
	assert.Equal(t, defaultMaxConnIdleTime, cfg.MaxConnIdleTime)
	assert.Equal(t, defaultConnectTimeout, cfg.ConnectTimeout)

	// Varsayılanların kendi içinde tutarlı olması, DefaultConfig'in tek
	// başına kullanılabilir olmasının şartıdır.
	require.NoError(t, cfg.Validate())
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	base := DefaultConfig("postgres://localhost:5432/gobit")

	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr bool
	}{
		{"varsayılanlar geçerli", func(*Config) {}, false},
		{"boş url", func(c *Config) { c.URL = "   " }, true},
		{"sıfır MaxConns", func(c *Config) { c.MaxConns = 0 }, true},
		{"negatif MinConns", func(c *Config) { c.MinConns = -1 }, true},
		{"MinConns > MaxConns", func(c *Config) { c.MinConns = c.MaxConns + 1 }, true},
		{"sıfır MaxConnLifetime", func(c *Config) { c.MaxConnLifetime = 0 }, true},
		{"sıfır MaxConnIdleTime", func(c *Config) { c.MaxConnIdleTime = 0 }, true},
		{"idle > lifetime", func(c *Config) { c.MaxConnIdleTime = c.MaxConnLifetime + time.Second }, true},
		{"sıfır ConnectTimeout", func(c *Config) { c.ConnectTimeout = 0 }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := base
			tt.mutate(&cfg)

			err := cfg.Validate()
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata sınıfı KindInvalid olmalı, %v alındı", errors.KindOf(err))
			assert.Equal(t, "db_config_invalid", errors.CodeOf(err))
		})
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "url biçimi parolayı ayıklar",
			dsn:  "postgres://gobit:" + testParola + "@db.internal:5432/gobit?sslmode=require",
			want: "db.internal:5432/gobit",
		},
		{
			name: "url biçimi portsuz",
			dsn:  "postgres://gobit:" + testParola + "@localhost/gobit",
			want: "localhost/gobit",
		},
		{
			name: "url biçimi veritabanı adı yok",
			dsn:  "postgres://gobit:" + testParola + "@localhost:5432",
			want: "localhost:5432/" + unknownDatabase,
		},
		{
			name: "sorgu parametresindeki parola da düşer",
			dsn:  "postgres://localhost:5432/gobit?password=" + testParola,
			want: "localhost:5432/gobit",
		},
		{
			name: "anahtar=değer biçimi",
			dsn:  "host=db.internal port=5432 user=gobit password=" + testParola + " dbname=gobit",
			want: "db.internal:5432/gobit",
		},
		{
			name: "anahtar=değer biçimi portsuz",
			dsn:  "host=db.internal user=gobit password=" + testParola,
			want: "db.internal/" + unknownDatabase,
		},
		{
			name: "ayrıştırılamayan girdi yer tutucuya düşer",
			dsn:  "bu bir dsn degil " + testParola,
			want: unknownTarget,
		},
		{
			name: "boş girdi",
			dsn:  "",
			want: unknownTarget,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Redact(tt.dsn)
			assert.Equal(t, tt.want, got)
			assert.NotContains(t, got, testParola, "ayıklanmış gösterim parola içeremez")
		})
	}
}

// TestNewRejectsBadDSNWithoutLeakingPassword ağ erişimi olmadan çalışır:
// geçersiz port pgx'in DSN ayrıştırıcısını bağlantı denemesinden ÖNCE hataya
// düşürür.
func TestNewRejectsBadDSNWithoutLeakingPassword(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig("postgres://gobit:" + testParola + "@localhost:port-degil/gobit")

	pool, err := New(context.Background(), cfg, nil)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, errors.IsInvalid(err), "hata sınıfı KindInvalid olmalı, %v alındı", errors.KindOf(err))
	assert.Equal(t, "db_dsn_invalid", errors.CodeOf(err))
	assert.NotContains(t, err.Error(), testParola, "hata mesajı parola sızdıramaz")
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	pool, err := New(context.Background(), Config{}, nil)

	require.Error(t, err)
	assert.Nil(t, pool)
	assert.True(t, errors.IsInvalid(err))
	assert.Equal(t, "db_config_invalid", errors.CodeOf(err))
}

// TestNilPoolMethodsAreSafe kurulum yarıda kaldığında defer edilmiş
// Close/Ping çağrılarının panik atmamasını garanti eder.
func TestNilPoolMethodsAreSafe(t *testing.T) {
	t.Parallel()

	var p *Pool

	assert.NotPanics(t, p.Close)
	assert.Nil(t, p.Pool())
	assert.Equal(t, unknownTarget, p.Target())

	err := p.Ping(context.Background())
	require.Error(t, err)
	assert.True(t, errors.HasKind(err, errors.KindUnavailable))
}

func TestMigrationsTable(t *testing.T) {
	t.Parallel()

	t.Run("geçerli sahip için tablo adı üretir", func(t *testing.T) {
		t.Parallel()

		product, err := MigrationsTable("product")
		require.NoError(t, err)
		assert.Equal(t, "product_schema_migrations", product)

		alpha, err := MigrationsTable("alpha")
		require.NoError(t, err)
		assert.Equal(t, "alpha_schema_migrations", alpha)

		beta, err := MigrationsTable("beta")
		require.NoError(t, err)
		// İki farklı modülün tablosu asla eşit olamaz.
		assert.NotEqual(t, alpha, beta)
	})

	// Tablo adları SQL'de parametrelenemez; doğrulama bu fonksiyonun
	// KENDİSİNDE olmazsa dışarıdan gelen bir modül adı doğrudan sorguya
	// gömülebilir.
	t.Run("geçersiz sahip için ad üretmez", func(t *testing.T) {
		t.Parallel()

		bad := map[string]string{
			"sql enjeksiyonu": `x"; DROP TABLE users; --`,
			"boş":             "",
			"büyük harf":      "Product",
			"nokta":           "public.product",
		}
		for name, owner := range bad {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				table, err := MigrationsTable(owner)
				require.Error(t, err)
				assert.Empty(t, table, "geçersiz sahip için tablo adı üretilmemeli")
				assert.True(t, errors.IsInvalid(err))
				assert.Equal(t, "db_migration_owner_invalid", errors.CodeOf(err))
			})
		}
	})
}

func TestValidateOwner(t *testing.T) {
	t.Parallel()

	valid := []string{"product", "p", "order_line", "tax2", "a0_9"}
	for _, owner := range valid {
		t.Run("geçerli/"+owner, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, validateOwner(owner))
		})
	}

	invalid := map[string]string{
		"boş":                 "",
		"büyük harf":          "Product",
		"tire":                "order-line",
		"boşluk":              "order line",
		"rakamla başlıyor":    "1product",
		"alt çizgiyle başlar": "_product",
		"sql enjeksiyonu":     `product"; DROP TABLE users; --`,
		"nokta":               "public.product",
		"çok uzun":            strings.Repeat("a", 41),
	}
	for name, owner := range invalid {
		t.Run("geçersiz/"+name, func(t *testing.T) {
			t.Parallel()

			err := validateOwner(owner)
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err))
			assert.Equal(t, "db_migration_owner_invalid", errors.CodeOf(err))
		})
	}
}

// TestMigrateValidatesBeforeConnecting veritabanına hiç dokunmadan
// reddedilmesi gereken girdileri doğrular.
func TestMigrateValidatesBeforeConnecting(t *testing.T) {
	t.Parallel()

	const dsn = "postgres://gobit:" + testParola + "@localhost:5432/gobit"
	src := fstest.MapFS{
		"000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}

	tests := []struct {
		name     string
		call     func(ctx context.Context) error
		wantCode string
	}{
		{
			name:     "geçersiz sahip",
			call:     func(ctx context.Context) error { return Migrate(ctx, dsn, src, "Product Modülü") },
			wantCode: "db_migration_owner_invalid",
		},
		{
			name:     "kaynak yok",
			call:     func(ctx context.Context) error { return Migrate(ctx, dsn, nil, "product") },
			wantCode: "db_migration_source_missing",
		},
		{
			name:     "desteklenmeyen şema",
			call:     func(ctx context.Context) error { return Migrate(ctx, "mysql://h/db", src, "product") },
			wantCode: "db_dsn_unsupported",
		},
		{
			name:     "geri alma da doğrulanır",
			call:     func(ctx context.Context) error { return MigrateDown(ctx, dsn, src, "BÜYÜK", 1) },
			wantCode: "db_migration_owner_invalid",
		},
		{
			name:     "sürüm sorgusu da doğrulanır",
			call:     func(ctx context.Context) error { _, _, err := Version(ctx, dsn, "-"); return err },
			wantCode: "db_migration_owner_invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.call(context.Background())
			require.Error(t, err)
			assert.Equal(t, tt.wantCode, errors.CodeOf(err))
			assert.True(t, errors.IsInvalid(err), "hata sınıfı KindInvalid olmalı, %v alındı", errors.KindOf(err))
			assert.NotContains(t, err.Error(), testParola, "hata mesajı parola sızdıramaz")
		})
	}
}

// TestMigrateHonorsCancelledContext iptal edilmiş bir bağlamla hiçbir
// bağlantı denemesi yapılmadığını doğrular.
func TestMigrateHonorsCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := fstest.MapFS{
		"000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}
	const dsn = "postgres://gobit:gobit@localhost:5432/gobit"

	err := Migrate(ctx, dsn, src, "product")
	require.Error(t, err)
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))
	assert.True(t, errors.Is(err, context.Canceled))

	_, _, verErr := Version(ctx, dsn, "product")
	require.Error(t, verErr)
	assert.Equal(t, "db_migration_canceled", errors.CodeOf(verErr))
}

// TestMigrationHonorsDeadlineOnStalledServer bağlantıyı kabul edip el
// sıkışmasını hiç tamamlamayan bir sunucuya karşı ctx sınırının GERÇEKTEN
// uygulandığını doğrular: paketleri düşüren bir güvenlik duvarının arkasındaki
// veritabanı da tam olarak böyle davranır.
func TestMigrationHonorsDeadlineOnStalledServer(t *testing.T) {
	t.Parallel()

	dsn := stalledServerDSN(t)
	src := fstest.MapFS{
		"000001_init.up.sql":   &fstest.MapFile{Data: []byte("SELECT 1;")},
		"000001_init.down.sql": &fstest.MapFile{Data: []byte("SELECT 1;")},
	}

	// Bağlam sınırı, çağrının dönmesi için tanınan süreden çok daha kısadır;
	// aradaki fark testi zamanlamaya duyarsız kılar.
	const deadline = 500 * time.Millisecond
	const tolerance = 15 * time.Second

	calls := map[string]func(ctx context.Context) error{
		"migrate":      func(ctx context.Context) error { return Migrate(ctx, dsn, src, "product") },
		"migrate down": func(ctx context.Context) error { return MigrateDown(ctx, dsn, src, "product", 1) },
		"version":      func(ctx context.Context) error { _, _, err := Version(ctx, dsn, "product"); return err },
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- call(ctx) }()

			select {
			case err := <-done:
				require.Error(t, err)
				assert.Equal(t, "db_migration_canceled", errors.CodeOf(err))
				assert.True(t, errors.HasKind(err, errors.KindUnavailable),
					"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(err))
				assert.True(t, errors.Is(err, context.DeadlineExceeded))
				assert.NotContains(t, err.Error(), testParola, "hata mesajı parola sızdıramaz")
			case <-time.After(tolerance):
				t.Fatalf("çağrı %s içinde dönmedi: ctx sınırı uygulanmıyor", tolerance)
			}
		})
	}
}

// stalledServerDSN bağlantıyı kabul edip hiçbir yanıt vermeyen bir dinleyici
// açar ve ona işaret eden bir DSN döner. Docker gerektirmez.
func stalledServerDSN(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var mu sync.Mutex
	var accepted []net.Conn

	t.Cleanup(func() {
		require.NoError(t, listener.Close())
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range accepted {
			_ = conn.Close()
		}
	})

	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			// Bağlantı bilerek sessiz bırakılır; kapatma Cleanup'a aittir.
			mu.Lock()
			accepted = append(accepted, conn)
			mu.Unlock()
		}
	}()

	return "postgres://gobit:" + testParola + "@" + listener.Addr().String() + "/gobit?sslmode=disable"
}
