package config_test

import (
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"time"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/core/config"
)

// envKeys Config'in okuduğu tüm ortam değişkenleridir.

var envKeys = []string{
	"APP_ENV", "APP_PORT", "DATABASE_URL", "REDIS_URL",
	"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_INSECURE", "OTEL_SERVICE_NAME",
	"OTEL_TRACES_SAMPLER_ARG", "METRIC_EXPORT_INTERVAL",
	"RATE_LIMIT_PER_MINUTE", "TRUSTED_PROXY_HOPS", "IDEMPOTENCY_TTL",
	"LOG_LEVEL", "LOG_FORMAT", "SHUTDOWN_TIMEOUT", "READ_HEADER_TIMEOUT",
	"READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "READINESS_DEGRADED_TIMEOUT",
	"EVENT_BUS", "EVENT_BUS_CONSUMER",
	"JWT_SECRET", "JWT_TTL",
	"ADMIN_BOOTSTRAP_EMAIL", "ADMIN_BOOTSTRAP_PASSWORD",
	"GUARD_BACKEND", "REDIS_KEY_PREFIX", "NOTIFICATION_PROVIDER",
	"FILE_PROVIDER", "FILE_ROOT", "FILE_MAX_UPLOAD_BYTES", "FILE_ALLOWED_TYPES",
	"GRAPHQL_MAX_DEPTH", "GRAPHQL_MAX_COMPLEXITY", "GRAPHQL_INTROSPECTION",
	"DB_MAX_CONNS", "DB_MIN_CONNS",
}

// uretimJWTSirri üretim senaryolarında kullanılan 32 karakterlik imza sırrıdır.
const uretimJWTSirri = "0123456789abcdef0123456789abcdef"

// clearEnv testin çalıştığı kabukta tanımlı olabilecek değişkenleri
// geçici olarak siler; böylece varsayılan davranış izole biçimde sınanır.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		if old, ok := os.LookupEnv(k); ok {
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("os.Unsetenv(%q): %v", k, err)
			}
			t.Cleanup(func() { _ = os.Setenv(k, old) })
		}
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)

	// Ortam boşken varsayılanlar docker-compose ile uyumlu olmalı.
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() beklenmedik hata: %v", err)
	}

	if cfg.AppEnv != "development" {
		t.Errorf("AppEnv = %q, beklenen %q", cfg.AppEnv, "development")
	}
	if cfg.AppPort != 9000 {
		t.Errorf("AppPort = %d, beklenen 9000", cfg.AppPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, beklenen %q", cfg.LogLevel, "info")
	}
	if cfg.LogFormat != "json" {
		t.Errorf("LogFormat = %q, beklenen %q", cfg.LogFormat, "json")
	}
	if !strings.HasPrefix(cfg.DatabaseURL, "postgres://") {
		t.Errorf("DatabaseURL = %q, postgres:// ile başlamalı", cfg.DatabaseURL)
	}
	if !strings.HasPrefix(cfg.RedisURL, "redis://") {
		t.Errorf("RedisURL = %q, redis:// ile başlamalı", cfg.RedisURL)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %s, beklenen 15s", cfg.ShutdownTimeout)
	}
}

func TestLoadFromEnv(t *testing.T) {
	clearEnv(t)

	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_PORT", "8080")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/x")
	t.Setenv("REDIS_URL", "redis://cache:6379/1")
	t.Setenv("SHUTDOWN_TIMEOUT", "30s")
	t.Setenv("JWT_SECRET", uretimJWTSirri)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() beklenmedik hata: %v", err)
	}

	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, beklenen true")
	}
	if got, want := cfg.Addr(), ":8080"; got != want {
		t.Errorf("Addr() = %q, beklenen %q", got, want)
	}
	if got, want := cfg.SlogLevel(), slog.LevelDebug; got != want {
		t.Errorf("SlogLevel() = %v, beklenen %v", got, want)
	}
	if cfg.ShutdownTimeout != 30*time.Second {
		t.Errorf("ShutdownTimeout = %s, beklenen 30s", cfg.ShutdownTimeout)
	}
}

func TestLoadInvalidEnv(t *testing.T) {
	clearEnv(t)

	tests := map[string]struct {
		key, value string
	}{
		"bilinmeyen ortam":    {"APP_ENV", "staging-2"},
		"port sıfır":          {"APP_PORT", "0"},
		"port aralık dışı":    {"APP_PORT", "70000"},
		"bilinmeyen seviye":   {"LOG_LEVEL", "trace"},
		"bilinmeyen biçim":    {"LOG_FORMAT", "logfmt"},
		"bilinmeyen bus":      {"EVENT_BUS", "kafka"},
		"negatif timeout":     {"SHUTDOWN_TIMEOUT", "-1s"},
		"sıfır probe bütçesi": {"READINESS_DEGRADED_TIMEOUT", "0s"},
		"sayı olmayan port":   {"APP_PORT", "abc"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv(tt.key, tt.value)
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() hata dönmeliydi (%s=%s)", tt.key, tt.value)
			}
		})
	}
}

func TestSlogLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}

	for level, want := range tests {
		t.Run(level, func(t *testing.T) {
			cfg := config.Config{LogLevel: level}
			if got := cfg.SlogLevel(); got != want {
				t.Errorf("SlogLevel() = %v, beklenen %v", got, want)
			}
		})
	}
}

func TestValidateRejectsEmptyURLs(t *testing.T) {
	// Taban, elle kurulmuş bir literal DEĞİL, varsayılanlardan yüklenmiş
	// geçerli bir config'tir. Literal olsaydı Config'e eklenen her zorunlu
	// alan bu testi kırardı ve testin ilgilendiği şeyle (boş URL reddi)
	// hiç ilgisi olmayan bir bakım yükü doğardı.
	base := gecerliConfig(t)
	if err := base.Validate(); err != nil {
		t.Fatalf("geçerli config reddedildi: %v", err)
	}

	noDB := base
	noDB.DatabaseURL = ""
	if err := noDB.Validate(); err == nil {
		t.Error("boş DATABASE_URL kabul edildi")
	}

	noRedis := base
	noRedis.RedisURL = ""
	if err := noRedis.Validate(); err == nil {
		t.Error("boş REDIS_URL kabul edildi")
	}
}

// TestProductionRejectsLocalDefaults üretimde yerel geliştirme
// varsayılanlarına sessizce düşülmediğini doğrular.
//
// Regresyon: envDefault dolu olduğu için Validate'in `== ""` kontrolü Load
// yolundan asla tetiklenmiyordu. Eksik (ya da boş) secret enjeksiyonu
// sabit-kodlu gobit:gobit kimlik bilgisi ve sslmode=disable ile üretime çıkardı.
func TestProductionRejectsLocalDefaults(t *testing.T) {
	tests := map[string]func(t *testing.T){
		"env hic set edilmemis": func(t *testing.T) {},
		"env bos string": func(t *testing.T) {
			t.Setenv("DATABASE_URL", "")
			t.Setenv("REDIS_URL", "")
		},
		"acikca varsayilanla ayni": func(t *testing.T) {
			t.Setenv("DATABASE_URL", config.DefaultDatabaseURL)
			t.Setenv("REDIS_URL", config.DefaultRedisURL)
		},
	}

	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("APP_ENV", "production")
			setup(t)

			cfg, err := config.Load()
			if err == nil {
				t.Fatalf("Load() hata dönmeliydi; DatabaseURL=%q", cfg.DatabaseURL)
			}
			if !strings.Contains(err.Error(), "production") {
				t.Errorf("hata mesajı üretim koşulunu anlatmalı: %v", err)
			}
		})
	}
}

func TestProductionAcceptsOverriddenURLs(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
	t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
	t.Setenv("JWT_SECRET", uretimJWTSirri)

	if _, err := config.Load(); err != nil {
		t.Fatalf("ezilmiş URL'lerle Load() hata verdi: %v", err)
	}
}

func TestDevelopmentAllowsLocalDefaults(t *testing.T) {
	// Yerel geliştirme "make up && make run" ile ek ayar gerektirmemeli.
	clearEnv(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() hata verdi: %v", err)
	}
	if cfg.DatabaseURL != config.DefaultDatabaseURL {
		t.Errorf("DatabaseURL = %q, beklenen varsayılan", cfg.DatabaseURL)
	}
}

// TestDefaultTagsMatchConstants envDefault etiketleri ile sabitlerin
// birbirinden kaymadığını denetler. Go struct etiketleri sabit referansı kabul
// etmediği için değer iki yerde tekrarlanmak zorunda; kayma olursa üretim
// koruması sessizce devre dışı kalırdı.
func TestDefaultTagsMatchConstants(t *testing.T) {
	want := map[string]string{
		"DatabaseURL":          config.DefaultDatabaseURL,
		"RedisURL":             config.DefaultRedisURL,
		"RedisKeyPrefix":       config.DefaultRedisKeyPrefix,
		"NotificationProvider": config.DefaultNotificationProvider,
		"FileProvider":         config.DefaultFileProvider,
		"FileRoot":             config.DefaultFileRoot,
		"FileAllowedTypes":     config.DefaultFileAllowedTypes,
		"FileMaxUploadBytes":   strconv.FormatInt(config.DefaultFileMaxUploadBytes, 10),
		"GraphQLMaxDepth":      strconv.Itoa(config.DefaultGraphQLMaxDepth),
		"GraphQLMaxComplexity": strconv.Itoa(config.DefaultGraphQLMaxComplexity),
		"GraphQLIntrospection": strconv.FormatBool(config.DefaultGraphQLIntrospection),
		"DBMaxConns":           strconv.FormatInt(int64(config.DefaultDBMaxConns), 10),
		"DBMinConns":           strconv.FormatInt(int64(config.DefaultDBMinConns), 10),
	}

	typ := reflect.TypeOf(config.Config{})
	for field, expected := range want {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("Config.%s alanı yok", field)
		}
		if got := f.Tag.Get("envDefault"); got != expected {
			t.Errorf("Config.%s envDefault etiketi %q, sabit %q — kaymış", field, got, expected)
		}
	}
}

func TestTimeoutValidation(t *testing.T) {
	tests := map[string]struct{ key, value string }{
		"read timeout sifir":            {"READ_TIMEOUT", "0s"},
		"write timeout negatif":         {"WRITE_TIMEOUT", "-1s"},
		"idle timeout sifir":            {"IDLE_TIMEOUT", "0s"},
		"read < read-header (tutarsiz)": {"READ_TIMEOUT", "5s"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			if tt.key == "READ_TIMEOUT" && tt.value == "5s" {
				t.Setenv("READ_HEADER_TIMEOUT", "10s")
			}
			t.Setenv(tt.key, tt.value)
			if _, err := config.Load(); err == nil {
				t.Fatalf("Load() hata dönmeliydi (%s=%s)", tt.key, tt.value)
			}
		})
	}
}

// TestProductionRequiresStrongJWTSecret üretimde zayıf ya da boş bir imza
// sırrının REDDEDİLDİĞİNİ doğrular.
//
// Tahmin edilebilir bir imza sırrı, herkesin kendine admin jetonu
// üretebilmesi demektir. Uygulamanın sessizce açılması, açığın ancak
// istismar edildiğinde fark edilmesi olurdu.
func TestProductionRequiresStrongJWTSecret(t *testing.T) {
	tests := map[string]string{
		"sır hiç verilmemiş": "",
		"sır çok kısa":       "kisa",
		"31 karakter":        "0123456789abcdef0123456789abcde",
	}

	for name, secret := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("APP_ENV", "production")
			t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
			t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
			if secret != "" {
				t.Setenv("JWT_SECRET", secret)
			}

			_, err := config.Load()
			require.Error(t, err, "zayıf imza sırrı üretimde kabul edilemez")
			assert.Contains(t, err.Error(), "JWT_SECRET")
		})
	}
}

// TestDevelopmentAllowsEmptyJWTSecret geliştirmede imza sırrının zorunlu
// OLMADIĞINI doğrular; auth modülü kayıtlı değilken de sunucu açılabilmeli.
func TestDevelopmentAllowsEmptyJWTSecret(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err, "geliştirmede imza sırrı zorunlu olmamalı")
	assert.Empty(t, cfg.JWTSecret)
	assert.Positive(t, cfg.JWTTTL, "jeton ömrü varsayılanı dolu olmalı")
}

// gecerliConfig varsayılanlardan yüklenmiş, doğrulamayı geçen bir config döner.
func gecerliConfig(t *testing.T) config.Config {
	t.Helper()

	clearEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("varsayılan config yüklenemedi: %v", err)
	}

	return cfg
}

// TestValidateYeniAyarlariDogrular Faz 9 ile gelen ayarların sınırlarının
// gerçekten zorlandığını doğrular.
func TestValidateYeniAyarlariDogrular(t *testing.T) {
	base := gecerliConfig(t)

	tests := map[string]func(c *config.Config){
		"örnekleme oranı negatif":      func(c *config.Config) { c.TraceSampleRatio = -0.1 },
		"örnekleme oranı birden büyük": func(c *config.Config) { c.TraceSampleRatio = 1.1 },
		"metrik aralığı sıfır":         func(c *config.Config) { c.MetricInterval = 0 },
		"metrik aralığı negatif":       func(c *config.Config) { c.MetricInterval = -time.Second },
		"servis adı boş":               func(c *config.Config) { c.ServiceName = "" },
		"proxy atlaması negatif":       func(c *config.Config) { c.TrustedProxyHops = -1 },
		"idempotency TTL sıfır":        func(c *config.Config) { c.IdempotencyTTL = 0 },
	}

	for name, boz := range tests {
		t.Run(name, func(t *testing.T) {
			cfg := base
			boz(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Error("geçersiz değer kabul edildi")
			}
		})
	}

	// Sınır değerler KABUL edilmeli.
	for name, ayarla := range map[string]func(c *config.Config){
		"örnekleme oranı sıfır": func(c *config.Config) { c.TraceSampleRatio = 0 },
		"örnekleme oranı bir":   func(c *config.Config) { c.TraceSampleRatio = 1 },
		"proxy atlaması sıfır":  func(c *config.Config) { c.TrustedProxyHops = 0 },
		"hız sınırı sıfır":      func(c *config.Config) { c.RateLimitPerMinute = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			ayarla(&cfg)

			if err := cfg.Validate(); err != nil {
				t.Errorf("geçerli sınır değeri reddedildi: %v", err)
			}
		})
	}
}

// TestUretimdeSifresizTraceReddedilir üretimde TLS'siz OTLP bağlantısının
// kabul edilmediğini doğrular.
//
// Trace'ler istek yollarını, kimlikleri ve hata mesajlarını taşır; şifresiz
// göndermek onları ağda dinlenebilir kılar.
func TestUretimdeSifresizTraceReddedilir(t *testing.T) {
	clearEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
	t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
	t.Setenv("JWT_SECRET", uretimJWTSirri)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "collector.internal:4317")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")

	if _, err := config.Load(); err == nil {
		t.Error("üretimde şifresiz OTLP kabul edildi")
	}

	// Toplayıcı hiç yapılandırılmamışsa insecure bayrağının bir anlamı yoktur
	// ve kurulumu engellememelidir.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	if _, err := config.Load(); err != nil {
		t.Errorf("toplayıcısız kurulum reddedildi: %v", err)
	}
}

// paylasilanOrtamKur verilen ortamı, varsayılan OLMAYAN bağlantı adresleriyle
// hazırlar.
//
// URL'lerin ezilmesi testin konusu değildir; ezilmemiş bırakılsaydı üretim
// senaryosunda Validate JWT/OTLP kapısına hiç gelmeden URL hatası dönerdi ve
// test sınamak istediği şeyi sınamamış olurdu.
func paylasilanOrtamKur(t *testing.T, ortam string) {
	t.Helper()

	clearEnv(t)
	t.Setenv("APP_ENV", ortam)
	t.Setenv("DATABASE_URL", "postgres://u:p@db.internal:5432/gobit?sslmode=require")
	t.Setenv("REDIS_URL", "rediss://:s3cret@cache.internal:6380/0")
}

// TestPaylasilanOrtamlardaImzaSirriZorunlu imza sırrı kapısının yerel
// geliştirme DIŞINDAKİ her ortamda işlediğini doğrular.
//
// Regresyon: kapı yalnızca APP_ENV=production içindeydi. staging çoğu zaman
// çok örneklidir; sır boş bırakıldığında her örnek açılışta kendi rastgele
// sırrını üretir (bkz. cmd/server jwtSecret) ve bir örnekten alınan jeton
// diğerinde 401 döner. Yük dengeleyicinin dağıtımına bağlı olduğu için arıza
// aralıklıdır ve teşhisi zordur.
func TestPaylasilanOrtamlardaImzaSirriZorunlu(t *testing.T) {
	tests := map[string]struct {
		ortam     string
		sir       string
		reddedili bool
	}{
		"staging sır hiç verilmemiş": {ortam: "staging", sir: "", reddedili: true},
		"staging sır çok kısa":       {ortam: "staging", sir: "kisa", reddedili: true},
		"staging 31 karakter":        {ortam: "staging", sir: "0123456789abcdef0123456789abcde", reddedili: true},
		"staging güçlü sır":          {ortam: "staging", sir: uretimJWTSirri},
		// Üretim satırları mevcut korumanın regresyon kalkanıdır: kapı
		// genişletilirken production'ın gevşetilmediğini de sınıyoruz.
		"production sır hiç verilmemiş": {ortam: "production", sir: "", reddedili: true},
		"production güçlü sır":          {ortam: "production", sir: uretimJWTSirri},
		// Yerel geliştirmede kolaylık: tek örnek çalışır, jetonun yeniden
		// başlatmada düşmesinin bedeli yok denecek kadar azdır.
		"development sır hiç verilmemiş": {ortam: "development", sir: ""},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			paylasilanOrtamKur(t, tt.ortam)
			if tt.sir != "" {
				t.Setenv("JWT_SECRET", tt.sir)
			}

			_, err := config.Load()
			if !tt.reddedili {
				require.NoError(t, err, "geçerli yapılandırma reddedildi")

				return
			}

			require.Error(t, err, "zayıf imza sırrı paylaşılan ortamda kabul edilemez")
			assert.Contains(t, err.Error(), "JWT_SECRET")
			assert.Contains(t, err.Error(), tt.ortam, "hata mesajı hangi ortamın zorladığını söylemeli")
		})
	}
}

// TestPaylasilanOrtamlardaSifresizTraceReddedilir TLS'siz OTLP yasağının yerel
// geliştirme DIŞINDAKİ her ortamda işlediğini doğrular.
//
// Trace'ler istek yollarını, kimlikleri ve hata mesajlarını taşır; staging'in
// trafiği "gerçek değil" sayılsa bile ağı ve jetonları gerçektir.
func TestPaylasilanOrtamlardaSifresizTraceReddedilir(t *testing.T) {
	tests := map[string]struct {
		ortam     string
		endpoint  string
		insecure  string
		reddedili bool
	}{
		"staging şifresiz toplayıcı": {
			ortam: "staging", endpoint: "collector.internal:4317", insecure: "true", reddedili: true,
		},
		"staging TLS'li toplayıcı": {
			ortam: "staging", endpoint: "collector.internal:4317", insecure: "false",
		},
		// Toplayıcı hiç yapılandırılmamışsa bayrağın bir anlamı yoktur ve
		// kurulumu engellememelidir.
		"staging toplayıcı yok": {
			ortam: "staging", endpoint: "", insecure: "true",
		},
		"production şifresiz toplayıcı": {
			ortam: "production", endpoint: "collector.internal:4317", insecure: "true", reddedili: true,
		},
		// Yerelde toplayıcı çoğu zaman sertifikasız bir docker konteyneridir.
		"development şifresiz toplayıcı": {
			ortam: "development", endpoint: "localhost:4317", insecure: "true",
		},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			paylasilanOrtamKur(t, tt.ortam)
			t.Setenv("JWT_SECRET", uretimJWTSirri)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", tt.insecure)

			_, err := config.Load()
			if !tt.reddedili {
				require.NoError(t, err, "geçerli yapılandırma reddedildi")

				return
			}

			require.Error(t, err, "şifresiz OTLP paylaşılan ortamda kabul edilemez")
			assert.Contains(t, err.Error(), "OTEL_EXPORTER_OTLP_INSECURE")
			assert.Contains(t, err.Error(), tt.ortam, "hata mesajı hangi ortamın zorladığını söylemeli")
		})
	}
}

// TestIsSharedYalnizcaGelistirmeyiDisardaTutar kapının hangi ortamları
// kapsadığını tek bakışta belgeler.
func TestIsSharedYalnizcaGelistirmeyiDisardaTutar(t *testing.T) {
	tests := map[string]bool{
		"development": false,
		"staging":     true,
		"production":  true,
	}

	for ortam, beklenen := range tests {
		t.Run(ortam, func(t *testing.T) {
			cfg := config.Config{AppEnv: ortam}
			assert.Equal(t, beklenen, cfg.IsShared())
		})
	}
}

// tohumEPostasi ilk yönetici senaryolarında kullanılan e-postadır.
const tohumEPostasi = "ilk.yonetici@ornek.com"

// TestIlkYoneticiTohumuIkisiniBirlikteIster yarım yapılandırmanın SESSİZCE
// atlanmadığını doğrular.
//
// İki değişkenden birini yazıp diğerini unutan operatör, sessiz atlamada
// tohumun çalıştığını sanır; eksikliği ancak ilk giriş denemesinde, çoğu zaman
// kurulumdan günler sonra keşfeder. Açılışta durmak arızayı yapılandırmanın
// hâlâ elde olduğu ana taşır.
func TestIlkYoneticiTohumuIkisiniBirlikteIster(t *testing.T) {
	tests := map[string]struct {
		eposta    string
		parola    string
		reddedili bool
	}{
		// Tohum yapılandırması ZORUNLU değildir: kurulmuş bir sistemin
		// ortamında bu değişkenlerin durması gerekmez.
		"ikisi de verilmemiş": {},
		"ikisi de verilmiş":   {eposta: tohumEPostasi, parola: "gelistirme-parolasi"},
		"yalnızca e-posta":    {eposta: tohumEPostasi, reddedili: true},
		"yalnızca parola":     {parola: "gelistirme-parolasi", reddedili: true},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			clearEnv(t)
			if tt.eposta != "" {
				t.Setenv("ADMIN_BOOTSTRAP_EMAIL", tt.eposta)
			}
			if tt.parola != "" {
				t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", tt.parola)
			}

			cfg, err := config.Load()
			if !tt.reddedili {
				require.NoError(t, err, "geçerli tohum yapılandırması reddedildi")
				assert.Equal(t, tt.eposta, cfg.AdminBootstrapEmail)
				assert.Equal(t, tt.parola, cfg.AdminBootstrapPassword)

				return
			}

			require.Error(t, err, "yarım tohum yapılandırması sessizce atlanmamalı")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_EMAIL")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_PASSWORD",
				"hata mesajı eksik olanın hangisi olabileceğini görünür kılmalı")
		})
	}
}

// TestPaylasilanOrtamdaTohumParolasiUzunlukIster asgari uzunluk kapısının
// yalnızca paylaşılan ortamlarda işlediğini doğrular.
//
// İlk yönetici parolası bir kullanıcı parolası değil, dağıtım sırrıdır: ortam
// dosyasında durur ve kimsenin ezberlemesi gerekmez, yani uzunluğun maliyeti
// yoktur. Yerelde ise kolaylık kazanır — "make up && make run" ile denemek
// isteyen geliştirici kısa bir parola yazabilmelidir.
func TestPaylasilanOrtamdaTohumParolasiUzunlukIster(t *testing.T) {
	tests := map[string]struct {
		ortam     string
		parola    string
		reddedili bool
	}{
		"staging 15 karakter":     {ortam: "staging", parola: "onbes-karakter1", reddedili: true},
		"staging 16 karakter":     {ortam: "staging", parola: "onalti-karakter1"},
		"production kısa parola":  {ortam: "production", parola: "kisa", reddedili: true},
		"production uzun parola":  {ortam: "production", parola: "yeterince-uzun-bir-parola"},
		"development kısa parola": {ortam: "development", parola: "kisa"},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			paylasilanOrtamKur(t, tt.ortam)
			t.Setenv("JWT_SECRET", uretimJWTSirri)
			t.Setenv("ADMIN_BOOTSTRAP_EMAIL", tohumEPostasi)
			t.Setenv("ADMIN_BOOTSTRAP_PASSWORD", tt.parola)

			_, err := config.Load()
			if !tt.reddedili {
				require.NoError(t, err, "geçerli tohum yapılandırması reddedildi")

				return
			}

			require.Error(t, err, "kısa tohum parolası paylaşılan ortamda kabul edilemez")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_PASSWORD")
			assert.Contains(t, err.Error(), tt.ortam, "hata mesajı hangi ortamın zorladığını söylemeli")
			assert.NotContains(t, err.Error(), tt.parola,
				"parola hata mesajında GEÇMEMELİ; mesaj stderr'den log toplayıcısına düşer")
		})
	}
}

// TestVarsayilanRedisAnahtarOnegiGeriyeUyumlu önek yapılandırılabilir olurken
// bugünkü davranışın korunduğunu doğrular.
//
// Beklenen değer sabitten okunmaz, ELLE yazılır: sabit değişirse test düşer ve
// değişikliğin bedeli görünür olur. O bedel somut — yükseltilen bir kurulumun
// tüm hız sınırı sayaçları ve işlemdeki idempotency kayıtları bir anda başka
// bir ad alanına taşınır, yani o an uçan her tekrar isteği ikinci kez işlenir.
func TestVarsayilanRedisAnahtarOnegiGeriyeUyumlu(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()
	require.NoError(t, err, "önek yapılandırılmadan da açılabilmeli")
	assert.Equal(t, "gobit", cfg.RedisKeyPrefix,
		"varsayılan önek, redisguard'a gömülü olan eski önekle aynı kalmalı")
}

// TestRedisAnahtarOnegiBicimDogrular ayırıcı içeren ya da görünmez karakterli
// bir önekin SESSİZCE kabul edilmediğini doğrular.
//
// Önek, aynı Redis'i paylaşan iki kurulumu ayıran tek şeydir. Kabul edilen
// bozuk bir önek iki ayrı arıza üretir: ':' iki kurulumun anahtarlarını
// çakıştırabilir, sondaki bir boşluk ise kurulumu kimsenin fark etmeyeceği
// biçimde BAŞKA bir ad alanına taşır — sayaçlar sıfırlanır, işlemdeki
// idempotency kayıtları yok sayılır.
func TestRedisAnahtarOnegiBicimDogrular(t *testing.T) {
	tests := map[string]struct {
		onek      string
		reddedili bool
	}{
		"sade ad":           {onek: "gobit"},
		"tireli ad":         {onek: "gobit-staging"},
		"alt çizgili ad":    {onek: "gobit_prod"},
		"noktalı ad":        {onek: "magaza.42"},
		"ayırıcı içeren":    {onek: "gobit:staging", reddedili: true},
		"ayırıcıyla biten":  {onek: "gobit:", reddedili: true},
		"sondan boşluklu":   {onek: "gobit ", reddedili: true},
		"glob imi içeren":   {onek: "gobit*", reddedili: true},
		"eğik çizgi içeren": {onek: "gobit/prod", reddedili: true},
		"latin dışı harfli": {onek: "mağaza", reddedili: true},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("REDIS_KEY_PREFIX", tt.onek)

			cfg, err := config.Load()
			if !tt.reddedili {
				require.NoError(t, err, "geçerli önek reddedildi")
				assert.Equal(t, tt.onek, cfg.RedisKeyPrefix)

				return
			}

			require.Error(t, err, "bozuk önek sessizce kabul edilmemeli")
			assert.Contains(t, err.Error(), "REDIS_KEY_PREFIX",
				"hata mesajı hangi değişkenin yanlış olduğunu söylemeli")
		})
	}
}

// TestBosRedisAnahtarOnegiReddedilir elle kurulmuş bir Config'in ad alanı
// önekini boş bırakamayacağını doğrular.
//
// Ortam değişkeni yolundan boş değer zaten varsayılana düşer; bu kapı,
// Load'dan geçmeden Validate çağıran (örn. gömen ya da test eden) çağıranlar
// içindir. Boş önek anahtarları ":idem:..." yapar; ad alanı yok demektir ve
// önek yapılandırılabilir olmasının tek sebebi ad alanıdır.
func TestBosRedisAnahtarOnegiReddedilir(t *testing.T) {
	cfg := gecerliConfig(t)
	cfg.RedisKeyPrefix = ""

	err := cfg.Validate()

	require.Error(t, err, "boş önek kabul edilmemeli")
	assert.Contains(t, err.Error(), "REDIS_KEY_PREFIX")
}

// TestBildirimSaglayicisiVarsayilaniGondermeyendir varsayılan sağlayıcının
// GERÇEKTEN göndermeyen "log" olduğunu doğrular.
//
// Varsayılanın kayması sessiz bir arıza olurdu: kurulum açılır, uçlar çalışır
// ve yalnızca müşteriler sipariş onayı beklerken fark edilirdi.
func TestBildirimSaglayicisiVarsayilaniGondermeyendir(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, config.DefaultNotificationProvider, cfg.NotificationProvider)
	assert.Equal(t, "log", cfg.NotificationProvider,
		"varsayılan sağlayıcının adı gönderim YAPMADIĞINI söylemeli")
}

// TestBildirimSaglayicisiBicimDogrular adın biçim denetimini doğrular.
//
// Config, adın KAYITLI olup olmadığını bilemez (sağlayıcılar eklentilerden
// gelir); burada sınanan yalnızca biçimdir. Tanınmayan bir adın açılışı
// durdurduğu cmd/server tarafında sınanır.
func TestBildirimSaglayicisiBicimDogrular(t *testing.T) {
	tests := map[string]struct {
		deger     string
		reddedili bool
	}{
		"eklenti adı":       {deger: "sendgrid"},
		"varsayılan":        {deger: "log"},
		"baştaki boşluk":    {deger: " log", reddedili: true},
		"sondaki boşluk":    {deger: "log ", reddedili: true},
		"yalnızca boşlukla": {deger: "   ", reddedili: true},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			clearEnv(t)
			t.Setenv("NOTIFICATION_PROVIDER", tt.deger)

			cfg, err := config.Load()
			if !tt.reddedili {
				require.NoError(t, err, "geçerli sağlayıcı adı reddedildi")
				assert.Equal(t, tt.deger, cfg.NotificationProvider)

				return
			}

			require.Error(t, err, "bozuk sağlayıcı adı sessizce kabul edilmemeli")
			assert.Contains(t, err.Error(), "NOTIFICATION_PROVIDER",
				"hata mesajı hangi değişkenin yanlış olduğunu söylemeli")
		})
	}
}

// TestBosBildirimSaglayicisiReddedilir elle kurulmuş bir Config'in sağlayıcı
// adını boş bırakamayacağını doğrular.
//
// Ortam değişkeni yolundan boş değer zaten varsayılana düşer; bu kapı,
// Load'dan geçmeden Validate çağıran (örn. gömen ya da test eden) çağıranlar
// içindir. Boş ad, sağlayıcı kaydında boş kimlik aramak demektir ve hiçbir
// sağlayıcı boş kimlikle kaydedilemeyeceği için her bildirim hata dönerdi.
func TestBosBildirimSaglayicisiReddedilir(t *testing.T) {
	cfg := gecerliConfig(t)
	cfg.NotificationProvider = ""

	err := cfg.Validate()

	require.Error(t, err, "boş sağlayıcı adı kabul edilmemeli")
	assert.Contains(t, err.Error(), "NOTIFICATION_PROVIDER")
}

// TestDosyaAyarlariVarsayilaniKaliciDizindir kutudan çıkan yüklemenin GEÇİCİ
// dizine yazmadığını doğrular.
//
// Geçici dizin cazip olurdu ("hiçbir şey yapılandırmadan çalışsın") ama
// yeniden başlatmada görselleri sessizce kaybettirirdi: adres ürün kaydında
// kalıcı olarak durur, dosya ise gitmiştir. İddia bu yüzden yalnızca "varsayılan
// nedir"i değil, "ne DEĞİLDİR"i de sabitler.
func TestDosyaAyarlariVarsayilaniKaliciDizindir(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Equal(t, config.DefaultFileProvider, cfg.FileProvider)
	assert.Equal(t, config.DefaultFileRoot, cfg.FileRoot)
	assert.NotContains(t, cfg.FileRoot, os.TempDir(),
		"varsayılan kök GEÇİCİ dizin olamaz; yeniden başlatmada sessiz veri kaybı demektir")
	assert.Equal(t, config.DefaultFileMaxUploadBytes, cfg.FileMaxUploadBytes)
	assert.Equal(t,
		[]string{"image/jpeg", "image/png", "image/gif", "image/webp"}, cfg.FileAllowedTypes)
	assert.NotContains(t, cfg.FileAllowedTypes, "image/svg+xml",
		"SVG bir belgedir ve script taşır; varsayılan izin listesinde OLMAMALI")
}

// TestDosyaAyarlariBicimDogrular geçersiz dosya ayarlarının açılışı
// durdurduğunu doğrular.
//
// İzin listesindeki biçim iddiaları özellikle önemlidir: parametreli ya da
// büyük harfli bir tip, İÇERİKTEN tespit edilen tiple hiçbir zaman eşleşmez.
// Sessizce kabul edilseydi listede duran ama hiçbir dosyayı geçirmeyen bir
// satır kalırdı — operatör tipi "açtığını" sanardı.
func TestDosyaAyarlariBicimDogrular(t *testing.T) {
	base := gecerliConfig(t)

	tests := map[string]struct {
		boz      func(c *config.Config)
		degisken string
	}{
		"sağlayıcı boş":        {func(c *config.Config) { c.FileProvider = "" }, "FILE_PROVIDER"},
		"sağlayıcı boşluklu":   {func(c *config.Config) { c.FileProvider = " local" }, "FILE_PROVIDER"},
		"kök boş":              {func(c *config.Config) { c.FileRoot = "" }, "FILE_ROOT"},
		"kök boşluklu":         {func(c *config.Config) { c.FileRoot = "/veri/yuklemeler " }, "FILE_ROOT"},
		"azami boyut sıfır":    {func(c *config.Config) { c.FileMaxUploadBytes = 0 }, "FILE_MAX_UPLOAD_BYTES"},
		"azami boyut negatif":  {func(c *config.Config) { c.FileMaxUploadBytes = -1 }, "FILE_MAX_UPLOAD_BYTES"},
		"izin listesi boş":     {func(c *config.Config) { c.FileAllowedTypes = nil }, "FILE_ALLOWED_TYPES"},
		"tip boş":              {func(c *config.Config) { c.FileAllowedTypes = []string{"image/png", ""} }, "FILE_ALLOWED_TYPES"},
		"tip parametreli":      {func(c *config.Config) { c.FileAllowedTypes = []string{"text/plain; charset=utf-8"} }, "FILE_ALLOWED_TYPES"},
		"tip büyük harfli":     {func(c *config.Config) { c.FileAllowedTypes = []string{"Image/PNG"} }, "FILE_ALLOWED_TYPES"},
		"tip bölü işareti yok": {func(c *config.Config) { c.FileAllowedTypes = []string{"png"} }, "FILE_ALLOWED_TYPES"},
		"tip iki kez":          {func(c *config.Config) { c.FileAllowedTypes = []string{"image/png", "image/png"} }, "FILE_ALLOWED_TYPES"},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			cfg := base
			tt.boz(&cfg)

			err := cfg.Validate()

			require.Error(t, err, "bozuk dosya ayarı sessizce kabul edilmemeli")
			assert.Contains(t, err.Error(), tt.degisken,
				"hata mesajı hangi değişkenin yanlış olduğunu söylemeli")
		})
	}
}

// TestKaliciOlmayanDosyaKokuUyariKapisiniAcar uyarı kapısının hangi
// kurulumlarda açıldığını sabitler.
//
// Kural AÇILIŞI DURDURMAZ (gerekçe config.LocalFileRootIsDurable godoc'unda),
// bu yüzden tek koruması bu testtir: kapı sessizce kapanırsa uyarı hiç
// yazılmaz ve kalıcı olmayan bir kökle çıkılan üretim dağıtımı hiçbir iz
// bırakmaz.
//
// GEÇİCİ kök vakaları burada ayrıca sayılıyor çünkü ölçüt bir kez yalnızca
// filepath.IsAbs'e bakıyordu: "/tmp/gobit-uploads" mutlaktır, o ölçütü geçer
// ve yine de kap her yeniden başladığında boşalır — yani config'in kendi
// varsayılan gerekçesinde REDDETTİĞİ sessiz veri kaybı, uyarı hiç yazılmadan
// geri gelirdi.
func TestKaliciOlmayanDosyaKokuUyariKapisiniAcar(t *testing.T) {
	base := gecerliConfig(t)

	tests := map[string]struct {
		ortam     string
		saglayici string
		kok       string
		kalici    bool
	}{
		"geliştirme göreli kök":     {"development", "local", "./data/uploads", false},
		"üretim göreli kök":         {"production", "local", "./data/uploads", false},
		"üretim mutlak kök":         {"production", "local", "/var/lib/gobit/uploads", true},
		"staging göreli kök":        {"staging", "local", "data/uploads", false},
		"üretim eklenti deposu":     {"production", "s3", "./data/uploads", true},
		"üretim geçici kök":         {"production", "local", "/tmp/gobit-uploads", false},
		"üretim geçici kökün kendi": {"production", "local", "/tmp", false},
		"üretim var/tmp":            {"production", "local", "/var/tmp/gobit", false},
		"üretim dev/shm":            {"production", "local", "/dev/shm/gobit", false},
		// Önek benzerliği yetmez: "/tmpfoo" geçici dizinin ALTINDA değildir ve
		// sade bir strings.HasPrefix karşılaştırması onu haksız yere kalıcı
		// olmayan sayardı.
		"üretim benzer adlı kök": {"production", "local", "/tmpfoo/uploads", true},
		// Eklenti deposu seçiliyken kök hiç kullanılmaz; geçici bir yol bile
		// uyarı üretmemeli, yoksa uyarı hiçbir şey korumadan her açılışta basar.
		"üretim eklenti deposu geçici kök": {"production", "s3", "/tmp/gobit", true},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			cfg := base
			cfg.AppEnv = tt.ortam
			cfg.FileProvider = tt.saglayici
			cfg.FileRoot = tt.kok

			assert.Equal(t, tt.kalici, cfg.LocalFileRootIsDurable())
		})
	}
}

// TestHizSiniriAnahtariProxyArkasindaIstemciyeDusmez uyarı kapısının hangi
// kurulumlarda açıldığını sabitler.
//
// TRUSTED_PROXY_HOPS=0 iken X-Forwarded-For hiç okunmaz ve hız sınırı anahtarı
// bağlantının adresine düşer; ters proxy arkasında o adres HER İSTEKTE
// proxy'nindir, yani kota müşteri başına değil tüm mağaza için tek bir kova
// olur. Açılış DURMAZ (gerekçe config.RateLimitKeyIsPerClient godoc'unda), bu
// yüzden kapının tek koruması bu testtir.
func TestHizSiniriAnahtariProxyArkasindaIstemciyeDusmez(t *testing.T) {
	base := gecerliConfig(t)

	tests := map[string]struct {
		limit         int
		atlama        int
		istemciBasina bool
	}{
		"sınır açık, atlama yok":      {600, 0, false},
		"sınır açık, tek atlama":      {600, 1, true},
		"sınır açık, iki atlama":      {600, 2, true},
		"sınır kapalı, atlama yok":    {0, 0, true},
		"sınır negatif, atlama yok":   {-1, 0, true},
		"sınır kapalı, atlama verili": {0, 2, true},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			cfg := base
			cfg.RateLimitPerMinute = tt.limit
			cfg.TrustedProxyHops = tt.atlama

			assert.Equal(t, tt.istemciBasina, cfg.RateLimitKeyIsPerClient())
		})
	}
}

// TestOlayVeriYoluTuketiciAdiBicimDogrular tüketici adının biçim kapısını
// sabitler.
//
// Boş değer GEÇERLİDİR ve "adı sen üret" demektir; baştaki/sondaki boşluk ise
// reddedilir. Redis " gobit-1" gibi bir adı sorunsuz kabul eder, yani yazım
// hatası hiçbir hata üretmez — yalnızca süreç bir sonraki açılışta kendi
// bekleyen listesini bulamaz ve o mesajlar kimseye teslim edilmez.
func TestOlayVeriYoluTuketiciAdiBicimDogrular(t *testing.T) {
	base := gecerliConfig(t)

	tests := map[string]struct {
		ad        string
		reddedili bool
	}{
		"boş (otomatik ad)": {ad: ""},
		"pod adı":           {ad: "gobit-0"},
		"nokta içeren ad":   {ad: "gobit.eu.0"},
		"baştaki boşluk":    {ad: " gobit-0", reddedili: true},
		"sondaki boşluk":    {ad: "gobit-0 ", reddedili: true},
		"sondaki satır":     {ad: "gobit-0\n", reddedili: true},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			cfg := base
			cfg.EventBusConsumer = tt.ad

			err := cfg.Validate()

			if tt.reddedili {
				require.Error(t, err, "bozuk tüketici adı sessizce kabul edilmemeli")
				assert.Contains(t, err.Error(), "EVENT_BUS_CONSUMER",
					"hata mesajı hangi değişkenin yanlış olduğunu söylemeli")

				return
			}

			require.NoError(t, err)
		})
	}
}

// TestOlayVeriYoluTuketiciAdiVarsayilaniBostur ayarın varsayılanının BOŞ
// olduğunu sabitler.
//
// Boş, "adı süreç başına sen üret" demektir ve tek doğru varsayılan budur:
// sabit bir varsayılan (örn. "gobit") aynı gruptaki tüm süreçlere AYNI adı
// verir, yani her açılışta birbirlerinin işlemekte olduğu mesajları da alırlar
// ve aynı olay iki kez işlenir.
func TestOlayVeriYoluTuketiciAdiVarsayilaniBostur(t *testing.T) {
	clearEnv(t)

	cfg, err := config.Load()

	require.NoError(t, err)
	assert.Empty(t, cfg.EventBusConsumer,
		"varsayılan bir tüketici adı, tüm örneklere aynı adı vermek demektir")
}

// TestGraphQLSinirlariVarsayilanDolu ayar verilmemiş bir kurulumun GraphQL
// ucunu SINIRSIZ açmadığını doğrular.
//
// Bu, sertleştirmenin sessizce kaybolabileceği tek yoldur: ortam değişkeni
// yoksa alanlar Go'nun sıfır değerinde kalır ve sıfır, uygulayan tarafta
// "sınır uygulama" diye okunsaydı hiçbir hata görünmeden korumasız bir uç
// açılırdı.
func TestGraphQLSinirlariVarsayilanDolu(t *testing.T) {
	cfg := gecerliConfig(t)

	assert.Equal(t, config.DefaultGraphQLMaxDepth, cfg.GraphQLMaxDepth)
	assert.Equal(t, config.DefaultGraphQLMaxComplexity, cfg.GraphQLMaxComplexity)
	assert.Equal(t, config.DefaultGraphQLIntrospection, cfg.GraphQLIntrospection,
		"iç gözlemin varsayılanı bir karardır; sessizce değişmemeli")
	assert.Positive(t, cfg.GraphQLMaxDepth)
	assert.Positive(t, cfg.GraphQLMaxComplexity)
}

// TestGraphQLSinirlariAcilistaDogrulanir anlamsız bir sınırın uygulamayı
// AÇILIŞTA durdurduğunu doğrular.
//
// Sıfır ve negatif değerler bilinçli olarak reddedilir: sınır YÜKSELTİLEBİLİR,
// KALDIRILAMAZ. RATE_LIMIT_PER_MINUTE'ta sıfırın "kapat" demesiyle
// karıştırılmamalı — hız sınırını kapatmak bir kapasite tercihidir, derinlik
// sınırını kapatmak tek bir sorgunun sunucuyu tüketmesine izin vermektir.
//
// Sayı olmayan değerler zaten ayrıştırmada düşer; onlar da burada sınanır ki
// "geçersiz değer sessizce varsayılana düşsün" davranışı bir gün eklenmesin:
// o davranış, operatöre verdiği değerin uygulandığını sandırırdı.
func TestGraphQLSinirlariAcilistaDogrulanir(t *testing.T) {
	// beklenen, hatanın operatöre HANGİ ayarın yanlış olduğunu söylediğini
	// sınar. Ayrıştırma hataları kütüphaneden gelir ve ortam değişkeninin adını
	// değil alan adını taşır; ikisi de kullanıcıyı doğru yere götürdüğü için
	// beklenen metin durum başına yazılır.
	tests := map[string]struct{ key, value, beklenen string }{
		"derinlik sıfır":            {"GRAPHQL_MAX_DEPTH", "0", "GRAPHQL_MAX_DEPTH"},
		"derinlik negatif":          {"GRAPHQL_MAX_DEPTH", "-1", "GRAPHQL_MAX_DEPTH"},
		"derinlik sayı değil":       {"GRAPHQL_MAX_DEPTH", "derin", "GraphQLMaxDepth"},
		"karmaşıklık sıfır":         {"GRAPHQL_MAX_COMPLEXITY", "0", "GRAPHQL_MAX_COMPLEXITY"},
		"karmaşıklık negatif":       {"GRAPHQL_MAX_COMPLEXITY", "-100", "GRAPHQL_MAX_COMPLEXITY"},
		"karmaşıklık sayı değil":    {"GRAPHQL_MAX_COMPLEXITY", "çok", "GraphQLMaxComplexity"},
		"iç gözlem mantıksal değil": {"GRAPHQL_INTROSPECTION", "belki", "GraphQLIntrospection"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := config.Load()
			require.Error(t, err, "geçersiz değer açılışı durdurmalı (%s=%s)", tt.key, tt.value)
			assert.Contains(t, err.Error(), tt.beklenen)
		})
	}
}

// TestGraphQLSinirlariYukseltilebilir ayarın gerçekten okunduğunu doğrular.
//
// Doğrulama testleri tek başına eksiktir: her değeri reddeden bir kapı da
// onları geçerdi. Sınırın YÜKSELTİLEBİLİR olması, kapının kabul ettiği tarafın
// da sınanmasını gerektirir.
func TestGraphQLSinirlariYukseltilebilir(t *testing.T) {
	clearEnv(t)

	t.Setenv("GRAPHQL_MAX_DEPTH", "25")
	t.Setenv("GRAPHQL_MAX_COMPLEXITY", "250000")
	t.Setenv("GRAPHQL_INTROSPECTION", "false")

	cfg, err := config.Load()
	require.NoError(t, err)

	assert.Equal(t, 25, cfg.GraphQLMaxDepth)
	assert.Equal(t, 250000, cfg.GraphQLMaxComplexity)
	assert.False(t, cfg.GraphQLIntrospection)
}

// TestHavuzSinirlariVarsayilanDolu ortam değişkeni verilmeyen bir kurulumun
// havuzu DEĞİŞMEDEN açtığını doğrular.
//
// Düğme geriye dönük olmalıdır: bu iki değişken var olmadan önce süreç 10/2 ile
// açılıyordu ve yükseltme, .env'ini hiç açmayan kurulumun havuzunu sessizce
// büyütmemeli. Değerin db paketininkiyle aynı kaldığını internal/arch'taki
// TestHavuzVarsayilanlariDbIleUyusuyor ayrıca bağlar.
func TestHavuzSinirlariVarsayilanDolu(t *testing.T) {
	cfg := gecerliConfig(t)

	assert.Equal(t, config.DefaultDBMaxConns, cfg.DBMaxConns)
	assert.Equal(t, config.DefaultDBMinConns, cfg.DBMinConns)
	assert.Equal(t, int32(10), cfg.DBMaxConns,
		"varsayılan bir karardır (bkz. DBMaxConns godoc'undaki ölçüm); sessizce değişmemeli")
	assert.Equal(t, int32(2), cfg.DBMinConns)
}

// TestHavuzSinirlariAcilistaDogrulanir anlamsız bir havuz boyutunun uygulamayı
// AÇILIŞTA durdurduğunu doğrular.
//
// Sıfır bağlantılık bir havuz "sınırsız" değil, HİÇBİR sorgunun çalışamaması
// demektir; alt sınırın üst sınırı aşması ise pgxpool'un kendi doğrulamasına
// takılır. İkisi de açılışta durur — ama hangi ortam değişkeninin yanlış
// olduğunu yalnızca buradaki kapı söyleyebilir: db paketinin hatası "MinConns"
// der ve operatörün elinde o adda bir kol yoktur.
//
// Aralık dışı bir sayı da sınanır: 2^31 AYRIŞTIRMADA düşer, yani havuz hiç
// açılmaz. Bu vaka alanın TİPİNE bağlı değildir ve öyle olduğu ölçüldü — env
// kütüphanesi int'i de 32 bitle sınırlıyor, dolayısıyla tip int'e çevrildiğinde
// bu satır yeşil kalır. Kalması yine de doğrudur; iddiası "tip int32'dir"
// değil, "anlamsız büyüklükte bir sayı sessizce kabul edilmez"dir.
//
// # Neden yalnızca ortam değişkeninin ADI beklenmiyor
//
// İlk yazımında bu tablo hatanın içinde "DB_MAX_CONNS" geçmesini arıyordu ve o
// hâliyle bir mutasyonu KAÇIRDI: taban denetimi "< 1" yerine "< 0" yapıldığında
// DB_MAX_CONNS=0 bu kez üçüncü kurala takılıyor ve onun mesajı da her iki adı
// birden taşıdığı için test yeşil kalıyordu. Yani ad, kuralları birbirinden
// AYIRT ETMİYOR. Beklenen metin bu yüzden kuralın kendi cümlesidir ve iki vaka
// DB_MIN_CONNS=0 ile kurulur: üçüncü kural devre dışı kalsın da taban denetimi
// tek başına sınansın.
func TestHavuzSinirlariAcilistaDogrulanir(t *testing.T) {
	tests := map[string]struct {
		env      map[string]string
		beklenen string
	}{
		"azami sıfır": {
			map[string]string{"DB_MAX_CONNS": "0", "DB_MIN_CONNS": "0"},
			"DB_MAX_CONNS en az 1 olmalı",
		},
		"azami negatif": {
			map[string]string{"DB_MAX_CONNS": "-1", "DB_MIN_CONNS": "0"},
			"DB_MAX_CONNS en az 1 olmalı",
		},
		"asgari negatif": {
			map[string]string{"DB_MIN_CONNS": "-1"},
			"DB_MIN_CONNS negatif olamaz",
		},
		"asgari azamiden büyük": {
			map[string]string{"DB_MAX_CONNS": "4", "DB_MIN_CONNS": "5"},
			"DB_MIN_CONNS (5), DB_MAX_CONNS'tan (4) büyük olamaz",
		},
		"azami sayı değil":  {map[string]string{"DB_MAX_CONNS": "çok"}, "DBMaxConns"},
		"azami aralık dışı": {map[string]string{"DB_MAX_CONNS": "2147483648"}, "DBMaxConns"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := config.Load()
			require.Error(t, err, "geçersiz havuz boyutu açılışı durdurmalı (%v)", tt.env)
			assert.Contains(t, err.Error(), tt.beklenen)
		})
	}
}

// TestHavuzSinirlariIkiYoneDeAyarlanabilir düğmenin gerçekten okunduğunu ve HER
// İKİ yöne de çevrildiğini doğrular.
//
// Yalnızca reddeden bir kapı, her değeri reddeden bir kapıyla aynı testi
// geçerdi; kabul eden taraf da sınanmalı. Küçültme yönü ayrıca sınanır çünkü
// asıl kırılgan olan odur: paylaşılan bir kümeye çok sayıda örnekle bağlanan
// kurulumun ihtiyacı havuzu DARALTMAKTIR ve alt sınır ezilemeseydi
// DB_MAX_CONNS=1 yalnızca açılışı kıran bir değer olurdu.
func TestHavuzSinirlariIkiYoneDeAyarlanabilir(t *testing.T) {
	t.Run("yukarı", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DB_MAX_CONNS", "40")
		t.Setenv("DB_MIN_CONNS", "8")

		cfg, err := config.Load()
		require.NoError(t, err)

		assert.Equal(t, int32(40), cfg.DBMaxConns)
		assert.Equal(t, int32(8), cfg.DBMinConns)
	})

	t.Run("aşağı", func(t *testing.T) {
		clearEnv(t)
		t.Setenv("DB_MAX_CONNS", "1")
		t.Setenv("DB_MIN_CONNS", "1")

		cfg, err := config.Load()
		require.NoError(t, err)

		assert.Equal(t, int32(1), cfg.DBMaxConns)
		assert.Equal(t, int32(1), cfg.DBMinConns)
	})
}
