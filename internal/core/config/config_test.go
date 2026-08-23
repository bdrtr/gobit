package config_test

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/turkbirdev/gobit/internal/core/config"
)

// envKeys Config'in okuduğu tüm ortam değişkenleridir.
var envKeys = []string{
	"APP_ENV", "APP_PORT", "DATABASE_URL", "REDIS_URL",
	"LOG_LEVEL", "LOG_FORMAT", "SHUTDOWN_TIMEOUT", "READ_HEADER_TIMEOUT",
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
		LogLevel: "info", LogFormat: "json",
		DatabaseURL: "postgres://x", RedisURL: "redis://x",
		ShutdownTimeout: time.Second, ReadHeaderTimeout: time.Second,
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
