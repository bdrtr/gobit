package config_test

import (
	"log/slog"
	"os"
	"reflect"
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
	"OTEL_TRACES_SAMPLER_ARG", "OTEL_METRIC_EXPORT_INTERVAL",
	"RATE_LIMIT_PER_MINUTE", "TRUSTED_PROXY_HOPS", "IDEMPOTENCY_TTL",
	"LOG_LEVEL", "LOG_FORMAT", "SHUTDOWN_TIMEOUT", "READ_HEADER_TIMEOUT",
	"READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "EVENT_BUS",
	"JWT_SECRET", "JWT_TTL",
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
		"bilinmeyen ortam":  {"APP_ENV", "staging-2"},
		"port sıfır":        {"APP_PORT", "0"},
		"port aralık dışı":  {"APP_PORT", "70000"},
		"bilinmeyen seviye": {"LOG_LEVEL", "trace"},
		"bilinmeyen biçim":  {"LOG_FORMAT", "logfmt"},
		"bilinmeyen bus":    {"EVENT_BUS", "kafka"},
		"negatif timeout":   {"SHUTDOWN_TIMEOUT", "-1s"},
		"sayı olmayan port": {"APP_PORT", "abc"},
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
		"DatabaseURL": config.DefaultDatabaseURL,
		"RedisURL":    config.DefaultRedisURL,
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
// sırrını üretir (bkz. cmd/server jwtSirri) ve bir örnekten alınan jeton
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
