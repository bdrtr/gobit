package config_test

import (
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bdrtr/gobit/internal/core/config"
)

// envKeys Config'in okuduğu tüm ortam değişkenleridir.
var envKeys = []string{
	"APP_ENV", "APP_PORT", "DATABASE_URL", "REDIS_URL",
	"LOG_LEVEL", "LOG_FORMAT", "SHUTDOWN_TIMEOUT", "READ_HEADER_TIMEOUT",
	"READ_TIMEOUT", "WRITE_TIMEOUT", "IDLE_TIMEOUT", "EVENT_BUS",
}

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
	base := config.Config{
		AppEnv: "development", AppPort: 9000,
		LogLevel: "info", LogFormat: "json", EventBus: "inmemory",
		DatabaseURL: "postgres://x", RedisURL: "redis://x",
		ShutdownTimeout: time.Second, ReadHeaderTimeout: time.Second,
		ReadTimeout: time.Second, WriteTimeout: time.Second, IdleTimeout: time.Second,
	}
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

// TestProductionRejectsLocalDefaults, üretimde yerel geliştirme
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

// TestDefaultTagsMatchConstants, envDefault etiketleri ile sabitlerin
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
