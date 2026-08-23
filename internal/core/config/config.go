// Package config uygulamanın tüm ayarlarını 12-factor ilkesine uygun biçimde
// ortam değişkenlerinden yükler ve doğrular.
package config

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Geçerli enum değerleri; Validate bunlara göre doğrulama yapar.
var (
	validAppEnvs    = []string{"development", "staging", "production"}
	validLogLevels  = []string{"debug", "info", "warn", "error"}
	validLogFormats = []string{"json", "text"}
)

// Config sunucunun çalışması için gereken tüm ayarları tutar.
//
// Varsayılan değerler deploy/docker-compose.yml ile uyumludur; yerelde
// "make up && make run" ek ayar gerektirmeden çalışır. Üretimde DatabaseURL
// ve RedisURL mutlaka ortam değişkeniyle ezilmelidir.
type Config struct {
	// AppEnv çalışma ortamıdır: development | staging | production.
	AppEnv string `env:"APP_ENV" envDefault:"development"`
	// AppPort HTTP sunucusunun dinleyeceği TCP portudur.
	AppPort int `env:"APP_PORT" envDefault:"9000"`

	// DatabaseURL PostgreSQL bağlantı adresidir (pgx DSN formatı).
	DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable"`
	// RedisURL Redis bağlantı adresidir.
	RedisURL string `env:"REDIS_URL" envDefault:"redis://localhost:6379/0"`

	// LogLevel yapısal log seviyesidir: debug | info | warn | error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat log çıktı biçimidir: json | text.
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`

	// ShutdownTimeout, SIGTERM sonrası açık isteklerin tamamlanması için
	// tanınan azami süredir.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	// ReadHeaderTimeout, Slowloris tipi saldırılara karşı zorunlu sınırdır.
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"10s"`
}

// Load ortam değişkenlerini okuyup doğrulanmış bir Config döner.
func Load() (Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: ortam değişkenleri okunamadı: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate Config alanlarının kendi içinde tutarlı olduğunu doğrular.
// Load bunu otomatik çağırır; elle kurulan Config'ler için de kullanılabilir.
func (c Config) Validate() error {
	if !slices.Contains(validAppEnvs, c.AppEnv) {
		return fmt.Errorf("config: geçersiz APP_ENV %q (beklenen: %s)", c.AppEnv, strings.Join(validAppEnvs, ", "))
	}
	if c.AppPort < 1 || c.AppPort > 65535 {
		return fmt.Errorf("config: geçersiz APP_PORT %d (beklenen: 1-65535)", c.AppPort)
	}
	if !slices.Contains(validLogLevels, c.LogLevel) {
		return fmt.Errorf("config: geçersiz LOG_LEVEL %q (beklenen: %s)", c.LogLevel, strings.Join(validLogLevels, ", "))
	}
	if !slices.Contains(validLogFormats, c.LogFormat) {
		return fmt.Errorf("config: geçersiz LOG_FORMAT %q (beklenen: %s)", c.LogFormat, strings.Join(validLogFormats, ", "))
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("config: DATABASE_URL boş olamaz")
	}
	if c.RedisURL == "" {
		return fmt.Errorf("config: REDIS_URL boş olamaz")
	}
	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: SHUTDOWN_TIMEOUT pozitif olmalı, %s verildi", c.ShutdownTimeout)
	}
	if c.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("config: READ_HEADER_TIMEOUT pozitif olmalı, %s verildi", c.ReadHeaderTimeout)
	}
	return nil
}

// SlogLevel LogLevel alanını slog.Level'a çevirir.
// Validate geçmiş bir Config için her zaman geçerli bir seviye döner.
func (c Config) SlogLevel() slog.Level {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// IsProduction üretim ortamında çalışılıp çalışılmadığını bildirir.
func (c Config) IsProduction() bool { return c.AppEnv == "production" }

// Addr HTTP sunucusunun dinleyeceği adresi döner.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.AppPort) }
