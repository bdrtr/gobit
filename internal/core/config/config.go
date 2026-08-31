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

// minJWTSecretLen paylaşılan ortamlarda kabul edilen en kısa imza sırrıdır.
//
// HS256 için sır, çıktı uzunluğu kadar (32 bayt) entropi taşımalıdır; daha
// kısası kaba kuvvetle bulunabilir.
const minJWTSecretLen = 32

// devAppEnv sırların ve TLS zorunluluğunun GEVŞETİLDİĞİ tek ortamdır.
const devAppEnv = "development"

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
	validAppEnvs    = []string{devAppEnv, "staging", "production"}
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
	// JWTSecret admin oturum jetonlarının imzalandığı sırdır.
	//
	// Varsayılanı YOKTUR ve olmamalıdır: tahmin edilebilir bir imza sırrı,
	// herkesin kendine admin jetonu üretebilmesi demektir. Boş bırakıldığında
	// yalnızca YEREL GELİŞTİRMEDE uygulama açılır; paylaşılan her ortamda
	// (staging ve production) Validate bunu REDDEDER. Bkz. IsShared.
	JWTSecret string `env:"JWT_SECRET"`
	// JWTTTL admin oturum jetonunun geçerlilik süresidir.
	JWTTTL time.Duration `env:"JWT_TTL" envDefault:"12h"`
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

	// OTLPEndpoint OpenTelemetry toplayıcısının gRPC adresidir (host:port).
	//
	// Varsayılanı YOKTUR: boş bırakıldığında izleme tamamen kapanır ve
	// uygulama hiçbir dış bağlantı denemez. Varsayılan bir adres koymak,
	// toplayıcısı olmayan her geliştirme ortamında sürekli bağlantı hatası
	// üretirdi.
	OTLPEndpoint string `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	// OTLPInsecure toplayıcıya TLS'siz bağlanılacağını bildirir.
	//
	// Paylaşılan ortamlarda (staging ve production) true olması Validate
	// tarafından REDDEDİLİR: trace'ler istek yollarını, kimlikleri ve hata
	// mesajlarını taşır; şifresiz göndermek bunları ağda dinlenebilir kılar.
	OTLPInsecure bool `env:"OTEL_EXPORTER_OTLP_INSECURE" envDefault:"false"`
	// ServiceName trace ve metriklerde raporlanan servis adıdır.
	ServiceName string `env:"OTEL_SERVICE_NAME" envDefault:"gobit"`
	// TraceSampleRatio örneklenecek trace oranıdır (0.0 - 1.0).
	//
	// Varsayılan 1.0'dır çünkü örnekleme kararı geri alınamaz: kaydedilmemiş
	// bir trace sonradan kurtarılamaz. Yük arttıkça düşürülmelidir.
	TraceSampleRatio float64 `env:"OTEL_TRACES_SAMPLER_ARG" envDefault:"1.0"`
	// MetricInterval metriklerin toplayıcıya gönderilme sıklığıdır.
	MetricInterval time.Duration `env:"OTEL_METRIC_EXPORT_INTERVAL" envDefault:"60s"`

	// RateLimitPerMinute bir istemcinin dakikada yapabileceği istek sayısıdır.
	//
	// Sıfır ya da negatif değer hız sınırlamayı KAPATIR; "0 istek" anlamına
	// gelmez. Bkz. ADR 0007.
	RateLimitPerMinute int `env:"RATE_LIMIT_PER_MINUTE" envDefault:"600"`
	// TrustedProxyHops istekle aramızdaki GÜVENİLEN ters proxy sayısıdır.
	//
	// Sıfırsa X-Forwarded-For hiç okunmaz. Yanlış (fazla) bir değer, istemcinin
	// uydurduğu adresi gerçek sanmaya ve hız sınırının atlanmasına yol açar;
	// bu yüzden varsayılanı sıfırdır ve açıkça verilmelidir.
	TrustedProxyHops int `env:"TRUSTED_PROXY_HOPS" envDefault:"0"`
	// IdempotencyTTL idempotency kayıtlarının saklanma süresidir.
	IdempotencyTTL time.Duration `env:"IDEMPOTENCY_TTL" envDefault:"24h"`

	// Plugins kurulacak eklentilerin adlarıdır (virgülle ayrılır).
	//
	// Varsayılanı BOŞTUR: derlenmiş bir eklentinin varlığı onu kurmak için
	// yeterli değildir, açıkça seçilmesi gerekir. Sebep somut: eklentiler
	// yapılandırma ister (örn. payment-stripe için STRIPE_API_KEY) ve kurulum
	// o ayar yoksa açılışta HATA verir. Derlenen her eklentiyi otomatik
	// kurmak, tek bir eksik ortam değişkeni yüzünden uygulamanın hiç
	// açılmaması demek olurdu.
	//
	// Bilinmeyen bir ad açılışta hata verir; sessizce yok saymak, adı yanlış
	// yazılmış bir eklentinin "kurulu" sanılmasına yol açardı.
	Plugins []string `env:"PLUGINS" envSeparator:","`
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

	if c.JWTTTL <= 0 {
		return fmt.Errorf("config: JWT_TTL pozitif olmalı, %s verildi", c.JWTTTL)
	}

	if c.TraceSampleRatio < 0 || c.TraceSampleRatio > 1 {
		return fmt.Errorf("config: OTEL_TRACES_SAMPLER_ARG 0.0-1.0 aralığında olmalı, %v verildi", c.TraceSampleRatio)
	}
	if c.MetricInterval <= 0 {
		return fmt.Errorf("config: OTEL_METRIC_EXPORT_INTERVAL pozitif olmalı, %s verildi", c.MetricInterval)
	}
	if c.ServiceName == "" {
		return fmt.Errorf("config: OTEL_SERVICE_NAME boş olamaz")
	}
	if c.TrustedProxyHops < 0 {
		return fmt.Errorf("config: TRUSTED_PROXY_HOPS negatif olamaz, %d verildi", c.TrustedProxyHops)
	}
	if c.IdempotencyTTL <= 0 {
		return fmt.Errorf("config: IDEMPOTENCY_TTL pozitif olmalı, %s verildi", c.IdempotencyTTL)
	}
	if err := c.validatePlugins(); err != nil {
		return err
	}

	// Üretimde yerel geliştirme varsayılanlarına düşmek, sabit-kodlu gobit:gobit
	// kimlik bilgisi ve sslmode=disable demektir. Eksik/boş secret enjeksiyonu
	// bu kontrol olmadan sessizce buraya düşerdi.
	//
	// Bu kapı staging'e GENİŞLETİLMEDİ: staging'in localhost'a bakması çalışan
	// bir kurulumda zaten mümkün değil (bağlantı ilk sorguda patlar), yani
	// buradaki kontrolün sessiz arıza örtme değeri yok. Aşağıdaki iki kapı ise
	// tam tersine sessiz arızayı önler; bkz. IsShared.
	if c.IsProduction() {
		if c.DatabaseURL == DefaultDatabaseURL {
			return fmt.Errorf("config: APP_ENV=production iken DATABASE_URL ezilmelidir (yerel geliştirme varsayılanı kullanılıyor)")
		}
		if c.RedisURL == DefaultRedisURL {
			return fmt.Errorf("config: APP_ENV=production iken REDIS_URL ezilmelidir (yerel geliştirme varsayılanı kullanılıyor)")
		}
	}

	if c.IsShared() {
		// Trace'ler istek yollarını, kimlikleri ve hata mesajlarını taşır;
		// şifresiz göndermek bunları ağda dinlenebilir kılar. staging'in
		// trafiği "gerçek değil" sayılsa bile, ağı ve jetonları gerçektir.
		if c.OTLPEndpoint != "" && c.OTLPInsecure {
			return fmt.Errorf("config: APP_ENV=%s iken OTEL_EXPORTER_OTLP_INSECURE=true olamaz", c.AppEnv)
		}
		// Boş bir imza sırrı iki ayrı arızadır: sabit bir sır herkesin kendine
		// admin jetonu üretebilmesi, üretilmiş rastgele bir sır ise örnekler
		// arası jeton geçersizliğidir. İkisi de sessizce açılmaktansa açılışta
		// durmayı hak eder.
		if len(c.JWTSecret) < minJWTSecretLen {
			return fmt.Errorf("config: APP_ENV=%s iken JWT_SECRET en az %d karakter olmalıdır", c.AppEnv, minJWTSecretLen)
		}
	}
	return nil
}

// validatePlugins eklenti listesinin biçimini doğrular.
//
// Boş ve tekrarlanan adlar REDDEDİLİR: "PLUGINS=stripe,,stripe" gibi bir değer
// neredeyse her zaman elle düzenlenmiş bir ortam dosyasındaki hatadır ve
// tekrarlanan ad eklenti kaydında zaten çakışma üretirdi. Hangi adların
// GEÇERLİ olduğunu config bilmez; onu uygulamayı kuran taraf (cmd/server)
// bilir ve bilinmeyen adı orada reddeder.
func (c Config) validatePlugins() error {
	gorulen := make(map[string]struct{}, len(c.Plugins))
	for i, ad := range c.Plugins {
		if strings.TrimSpace(ad) == "" {
			return fmt.Errorf("config: PLUGINS listesinde %d. sırada boş ad var", i+1)
		}
		if _, dup := gorulen[ad]; dup {
			return fmt.Errorf("config: PLUGINS listesinde %q iki kez geçiyor", ad)
		}
		gorulen[ad] = struct{}{}
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

// IsShared ortamın PAYLAŞILAN (yerel geliştirme dışı) bir ortam olduğunu bildirir.
//
// Sır ve TLS zorunluluklarının kapısı budur, IsProduction değil: staging ile
// production arasında güvenlik açısından anlamlı bir fark yoktur, ikisi de
// birden çok geliştiricinin ve birden çok SUNUCU ÖRNEĞİNİN paylaştığı
// ortamlardır.
//
// Somut arıza: staging çok örnekli çalışırken JWT_SECRET boş bırakılırsa her
// örnek açılışta kendi rastgele sırrını üretir (bkz. cmd/server jwtSirri);
// A örneğinden alınan jeton B örneğinde 401 döner. Yük dengeleyicinin
// dağıtımına bağlı olduğu için arıza ARALIKLIDIR ve teşhisi zordur —
// üretime çıkmadan yakalanması gereken sınıftan bile değildir, çünkü üretimde
// zaten aynı ayar zorunludur.
//
// Kolaylık yalnızca yerel geliştirmeye tanınır: orada tek örnek çalışır,
// jetonun yeniden başlatmada düşmesinin bedeli yok denecek kadar azdır ve
// "make up && make run" ek ayar istememelidir.
func (c Config) IsShared() bool { return c.AppEnv != devAppEnv }

// Addr HTTP sunucusunun dinleyeceği adresi döner.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.AppPort) }
