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

// Yalnızca yerel geliştirme için varsayılan bağlantı adresleri.
// deploy/docker-compose.yml ile eşleşirler. Validate, APP_ENV=production iken
// bu değerlerin ezilmiş olmasını ZORUNLU kılar; aksi hâlde eksik secret
// enjeksiyonu sessizce sabit-kodlu kimlik bilgisiyle üretime çıkardı.
//
// DİKKAT: Aşağıdaki envDefault etiketleri bu sabitlerle birebir aynı olmalıdır
// (Go struct etiketleri sabit referansı kabul etmez). TestDefaultTagsMatchConstants
// bu eşleşmeyi denetler.
const (
	// Buradaki gosec bastırmaları bilinçlidir: bu sabitler gizli bilgi DEĞİL,
	// tam tersine üretimde REDDEDİLMESİ gereken, bilinen yerel geliştirme
	// değerleridir. Validate bunlarla karşılaştırma yaparak korumayı uygular.
	DefaultDatabaseURL = "postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable" //nolint:gosec // G101: kasıtlı yerel geliştirme varsayılanı; üretimde Validate reddeder
	DefaultRedisURL    = "redis://:gobit@localhost:6379/0"                             //nolint:gosec // G101: kasıtlı yerel geliştirme varsayılanı; üretimde Validate reddeder
)

// Geçerli enum değerleri; Validate bunlara göre doğrulama yapar.
var (
	validAppEnvs    = []string{"development", "staging", "production"}
	validLogLevels  = []string{"debug", "info", "warn", "error"}
	validLogFormats = []string{"json", "text"}
	validEventBuses = []string{"inmemory", "redis"}
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
	RedisURL string `env:"REDIS_URL" envDefault:"redis://:gobit@localhost:6379/0"`
	// EventBus olay veri yolunun arka ucudur: inmemory | redis.
	// inmemory tek süreçlidir ve süreç ölünce olaylar kaybolur; birden çok
	// örnek çalıştırılıyorsa redis kullanılmalıdır (plan Bölüm 3).
	EventBus string `env:"EVENT_BUS" envDefault:"inmemory"`

	// LogLevel yapısal log seviyesidir: debug | info | warn | error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat log çıktı biçimidir: json | text.
	LogFormat string `env:"LOG_FORMAT" envDefault:"json"`

	// ShutdownTimeout, SIGTERM sonrası açık isteklerin tamamlanması için
	// tanınan azami süredir.
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"15s"`
	// ReadHeaderTimeout, yalnızca istek BAŞLIKLARININ okunması için tanınan süredir.
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" envDefault:"10s"`
	// ReadTimeout, başlık + gövdenin tamamının okunması için tanınan süredir.
	// ReadHeaderTimeout tek başına gövdeyi bayt bayt akıtan Slowloris türevini
	// durdurmaz; bu sınır olmadan her bağlantı süresiz goroutine + fd tutar.
	ReadTimeout time.Duration `env:"READ_TIMEOUT" envDefault:"15s"`
	// WriteTimeout, yanıtın yazılması için tanınan süredir.
	WriteTimeout time.Duration `env:"WRITE_TIMEOUT" envDefault:"30s"`
	// IdleTimeout, keep-alive bağlantısının boşta bekleyebileceği süredir.
	IdleTimeout time.Duration `env:"IDLE_TIMEOUT" envDefault:"120s"`
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
	if !slices.Contains(validEventBuses, c.EventBus) {
		return fmt.Errorf("config: geçersiz EVENT_BUS %q (beklenen: %s)", c.EventBus, strings.Join(validEventBuses, ", "))
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
	for _, t := range []struct {
		name  string
		value time.Duration
	}{
		{"READ_HEADER_TIMEOUT", c.ReadHeaderTimeout},
		{"READ_TIMEOUT", c.ReadTimeout},
		{"WRITE_TIMEOUT", c.WriteTimeout},
		{"IDLE_TIMEOUT", c.IdleTimeout},
	} {
		if t.value <= 0 {
			return fmt.Errorf("config: %s pozitif olmalı, %s verildi", t.name, t.value)
		}
	}
	if c.ReadTimeout < c.ReadHeaderTimeout {
		return fmt.Errorf("config: READ_TIMEOUT (%s), READ_HEADER_TIMEOUT'tan (%s) küçük olamaz", c.ReadTimeout, c.ReadHeaderTimeout)
	}

	// Üretimde yerel geliştirme varsayılanlarına düşmek, sabit-kodlu kimlik
	// bilgisi ve TLS'siz bağlantı demektir. Eksik/boş secret enjeksiyonu bu
	// kontrol olmadan sessizce buraya düşerdi.
	if c.IsProduction() {
		if c.DatabaseURL == DefaultDatabaseURL {
			return fmt.Errorf("config: APP_ENV=production iken DATABASE_URL ezilmelidir (yerel geliştirme varsayılanı kullanılıyor)")
		}
		if c.RedisURL == DefaultRedisURL {
			return fmt.Errorf("config: APP_ENV=production iken REDIS_URL ezilmelidir (yerel geliştirme varsayılanı kullanılıyor)")
		}
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
