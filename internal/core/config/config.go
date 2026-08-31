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

// BackendRedis paylaşılan Redis arka ucunun adıdır.
//
// Hem [Config.EventBus] hem [Config.GuardBackend] bu değeri alabilir ve ikisi
// AYNI istemciyi paylaşır (bkz. [Config.NeedsRedis]). Adın tek bir sabitten
// okunması, iki alanın sessizce farklı yazımlara ayrışmasını önler.
const BackendRedis = "redis"

// MinBootstrapPasswordLen paylaşılan ortamlarda ilk yönetici parolasının kabul
// edilen en kısa uzunluğudur.
//
// 16, auth modülünün HER yöneticiye uyguladığı 12'lik tabanın (service
// paketindeki MinPasswordLen) ÜSTÜNDEDİR ve bu bilinçlidir: buradaki değer bir
// kullanıcı parolası değil, DAĞITIM SIRRIDIR. Ortam dosyasında ya da secret
// deposunda durur, bir kez yazılır ve kimsenin ezberlemesi gerekmez — yani
// uzunluğun kullanıcıya maliyeti yokken karşılığında sistemin ilk ve en
// yetkili hesabının arama uzayı büyür.
//
// Sayı auth modülünden kopyalanmaz, ondan BAĞIMSIZ olarak seçilir; çekirdek
// modülleri tanımadığı için (Prensip 2.4) sabite bağlanmak zaten mümkün
// değildir. Tek şartı auth'un tabanının altına düşmemektir: düşseydi config
// kabul ettiği bir parolayı tohum adımı reddeder ve açılış anlaşılmaz biçimde
// dururdu.
//
// Yerel geliştirmede ZORLANMAZ: "make up && make run" ile denemek isteyen
// geliştirici kısa bir parola yazabilmelidir. Taban orada da kaybolmaz, auth
// modülünün kendi politikası yine uygulanır; yalnızca bu ek kat düşer.
//
// DIŞA AÇIKTIR ki auth modülünün genel parola tabanıyla ilişkisi bir testle
// sabitlenebilsin (bkz. internal/arch). Bağ elle tutulsaydı, auth'un tabanı bir
// gün bu değerin üstüne çıktığında buradaki kapı SESSİZCE etkisizleşirdi:
// auth'un zaten reddettiği bir parolayı burada ayrıca reddetmek hiçbir şey
// eklemez ve "paylaşılan ortamda daha uzun parola isteniyor" iddiası
// gerçekliğini kaybederdi.
const MinBootstrapPasswordLen = 16

// DefaultRedisKeyPrefix koruma anahtarlarının varsayılan ad alanı önekidir.
//
// Değer, önek yapılandırılabilir olmadan önce redisguard'a GÖMÜLÜ olan
// önekin ta kendisidir. Geriye uyumluluk burada bir tercih değil zorunluluktur:
// varsayılanı değiştirmek, yükseltilen bir kurulumun tüm hız sınırı sayaçlarını
// ve — çok daha kötüsü — işlemdeki idempotency kayıtlarını bir anda görünmez
// kılar; o an uçan her tekrar isteği ikinci kez işlenir, yani ikinci sipariş.
const DefaultRedisKeyPrefix = "gobit"

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
	validEventBuses = []string{"inmemory", BackendRedis}
	// validGuardBackends koruma bileşenlerinin geçerli arka uçlarıdır.
	validGuardBackends = []string{"memory", BackendRedis}
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

	// AdminBootstrapEmail açılışta yaratılacak İLK yönetim kullanıcısının
	// e-postasıdır.
	//
	// Boş bir veritabanıyla açılan sunucuda hiç yönetici yoktur ve yönetim
	// uçları korumalı olduğu için ilkini HTTP'den yaratmanın yolu da yoktur;
	// bu iki değişken olmadan taze bir kurulum KULLANILAMAZ kalırdı.
	//
	// [Config.AdminBootstrapPassword] ile BİRLİKTE verilir; yalnızca biri
	// verilirse Validate hata döner. İkisi de boşsa tohum adımı hiç çalışmaz
	// ve bu meşru bir seçimdir: kurulmuş bir sistemin ortamında bu
	// değişkenlerin durması gerekmez.
	AdminBootstrapEmail string `env:"ADMIN_BOOTSTRAP_EMAIL"`
	// AdminBootstrapPassword ilk yönetim kullanıcısının parolasıdır.
	//
	// ASLA loglanmaz ve hiçbir hata mesajında geçmez; doğrulama yalnızca
	// UZUNLUĞUNU bildirir. Paylaşılan ortamlarda en az
	// [MinBootstrapPasswordLen] karakter olmalıdır.
	//
	// Tohum adımı yalnızca hiç kullanıcı yokken çalıştığı için (bkz. cmd/server
	// tohumlaYonetici) bu değerin ortamda unutulması var olan bir yöneticinin
	// parolasını DEĞİŞTİRMEZ.
	AdminBootstrapPassword string `env:"ADMIN_BOOTSTRAP_PASSWORD"`
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
	//
	// Adı bilinçli olarak OTEL_ önekli DEĞİLDİR, oysa komşuları öyle.
	// OpenTelemetry belirtimi OTEL_METRIC_EXPORT_INTERVAL adını AYIRMIŞTIR ve
	// değerini MİLİSANİYE TAMSAYI olarak tanımlar; bu paket ise her süreyi Go
	// süresi olarak okur. İki anlam aynı ada sığmaz ve çakışma iki yönde de
	// keser:
	//
	//   - Belirtime uyan değer (60000) burada "birimi eksik" hatası verir ve
	//     uygulama HİÇ AÇILMAZ.
	//   - Buraya uyan değer (60s) OTel SDK'sının kendi okuyucusunda her
	//     açılışta ayrıştırma hatası loglar.
	//
	// Komşu OTEL_* adları korunur çünkü onların anlamı belirtimle UYUŞUR;
	// ödünç alınan ad ancak anlam da ödünç alınabildiğinde doğrudur.
	MetricInterval time.Duration `env:"METRIC_EXPORT_INTERVAL" envDefault:"60s"`

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
	// GuardBackend hız sınırı ve idempotency deposunun arka ucudur:
	// memory | redis.
	//
	// Varsayılan "memory"dir ve TEK ÖRNEKLİ kurulum içindir. Birden çok örnek
	// çalıştırılıyorsa "redis" ZORUNLUDUR; bellek içi depoyla hız sınırı örnek
	// sayısıyla çarpılır (bir hız sorunu) ve idempotency koruması örnekler
	// arasında HİÇ çalışmaz — aynı anahtarla farklı örneklere düşen iki istek
	// iki kez işlenir, yani iki sipariş. İkincisi bir doğruluk sorunudur.
	//
	// Tek bir anahtarın ikisini birden seçmesi bilinçlidir: ayrı ayrı
	// seçilebilseydi, idempotency'yi paylaşılan yapıp hız sınırını unutmak
	// gibi yarım bir yapılandırma mümkün olurdu ve o yarımlık ancak yük
	// altında görünürdü.
	GuardBackend string `env:"GUARD_BACKEND" envDefault:"memory"`
	// RedisKeyPrefix koruma anahtarlarının Redis'teki ad alanı önekidir.
	//
	// Anahtarlar "<önek>:rl:<istemci>" ve "<önek>:idem:<anahtar>" biçiminde
	// yazılır (bkz. internal/core/http/redisguard paket godoc'u).
	//
	// AYNI Redis'i paylaşan iki gobit kurulumu (staging ile production, ya da
	// aynı kümedeki iki mağaza) bu değeri FARKLI vermelidir. Aynı bırakılırsa
	// birbirlerinin hız sınırı kotasını harcarlar — bu bir hız sorunudur — ve
	// birbirlerinin idempotency kaydını OKURLAR: bir kurulumun yanıtı ötekinin
	// istemcisine gider. İkincisi doğruluk sorunudur.
	//
	// Ayrı Redis DB'si (redis://.../1) ya da ayrı örnek de ayırır ama ikisi de
	// ALTYAPI kararıdır: Redis Cluster numaralı DB'leri desteklemez ve ayrı
	// örnek para/operasyon maliyetidir. Önek aynı ayrımı yapılandırmayla yapar.
	//
	// AYRI bir değişkendir; var olan OTEL_SERVICE_NAME'e bağlanmaz. O ad
	// panolarda görünen servis adıdır ve gözlemlenebilirlik için değiştirilmesi
	// SIRADAN bir iştir; ikisini tek değişkene bağlamak, panoda yapılan bir
	// yeniden adlandırmanın çalışan kurulumun tüm idempotency kayıtlarını
	// sessizce terk etmesi demek olurdu.
	//
	// Varsayılanı [DefaultRedisKeyPrefix]'tir ve bugünkü davranışı korur; tek
	// kurulumlu ortamlarda dokunulması gerekmez. Biçimi GUARD_BACKEND'den
	// bağımsız doğrulanır: bellek içi arka uçta değer atıldır, ama yalnızca
	// backend değiştirilince patlayan bir yazım hatası, arızayı tam da en
	// kötü ana — canlı geçiş anına — saklamak olurdu.
	RedisKeyPrefix string `env:"REDIS_KEY_PREFIX" envDefault:"gobit"`

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
	if !slices.Contains(validGuardBackends, c.GuardBackend) {
		return fmt.Errorf("config: geçersiz GUARD_BACKEND %q (beklenen: %s)", c.GuardBackend, strings.Join(validGuardBackends, ", "))
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
		return fmt.Errorf("config: METRIC_EXPORT_INTERVAL pozitif olmalı, %s verildi", c.MetricInterval)
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
	if err := c.validateRedisKeyPrefix(); err != nil {
		return err
	}
	if err := c.validatePlugins(); err != nil {
		return err
	}
	// Ortama bağlı kapıyı kendi içinde taşır; bu yüzden aşağıdaki IsShared
	// bloğuna değil, sıradan doğrulamaların yanına konur.
	if err := c.validateAdminBootstrap(); err != nil {
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

// validateRedisKeyPrefix koruma anahtarı ad alanı önekinin biçimini doğrular.
//
// Kabul edilen: en az bir karakter, ve yalnızca ASCII harf, rakam, '-', '_',
// '.'. Kural burada TEKRARLANIR; redisguard kurucuları aynı denetimi kendi
// içlerinde de yapar. Tekrar bilinçlidir: config bu paketi import EDEMEZ
// (redisguard bir Redis istemcisi taşır ve config en alt katmandır), ayrıca
// bir kütüphane çağıranına güvenmemelidir. Buradaki kopya arızayı AÇILIŞA
// taşır ve operatöre hangi ortam değişkeninin yanlış olduğunu söyler;
// redisguard'daki kopya ise config'ten geçmeyen çağıranları korur.
//
// Reddedilen karakterlerin gerekçesi redisguard.dogrulaOnek godoc'undadır;
// özeti: ':' iki kurulumun anahtarlarını ÇAKIŞTIRABİLİR, glob imleri
// operatörün "<önek>:idem:*" taramasını bozar, boşluk ve kontrol karakterleri
// görünmez oldukları için kurulumu fark edilmeden başka bir ad alanına taşır.
func (c Config) validateRedisKeyPrefix() error {
	if c.RedisKeyPrefix == "" {
		return fmt.Errorf("config: REDIS_KEY_PREFIX boş olamaz (varsayılan: %q)", DefaultRedisKeyPrefix)
	}
	if strings.ContainsFunc(c.RedisKeyPrefix, func(r rune) bool { return !validPrefixRune(r) }) {
		return fmt.Errorf(
			"config: geçersiz REDIS_KEY_PREFIX %q (yalnızca ASCII harf, rakam, '-', '_' ve '.' kabul edilir)",
			c.RedisKeyPrefix)
	}
	return nil
}

// validPrefixRune karakterin ad alanı önekinde kullanılabildiğini bildirir.
func validPrefixRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return r == '-' || r == '_' || r == '.'
	}
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

// validateAdminBootstrap ilk yönetici tohumunun yapılandırmasını doğrular.
//
// YARIM YAPILANDIRMA REDDEDİLİR. İki değişkenden birini yazıp diğerini unutan
// operatör, sessiz atlamada tohumun çalıştığını sanır ve eksikliği ancak ilk
// giriş denemesinde — çoğu zaman kurulumdan günler sonra, kimsenin ortam
// dosyasına bakmayacağı bir anda — keşfeder. Açılışta durmak bu arızayı
// yapılandırmanın hâlâ elde olduğu ana taşır.
//
// Parola HATA MESAJINDA GEÇMEZ; yalnızca beklenen uzunluk bildirilir. Hata
// metni stderr'e ve oradan çoğu kurulumda log toplayıcısına düşer.
func (c Config) validateAdminBootstrap() error {
	if (c.AdminBootstrapEmail == "") != (c.AdminBootstrapPassword == "") {
		return fmt.Errorf("config: ADMIN_BOOTSTRAP_EMAIL ve ADMIN_BOOTSTRAP_PASSWORD birlikte verilmelidir (yalnızca biri verildi)")
	}
	if c.AdminBootstrapPassword == "" {
		return nil
	}
	if c.IsShared() && len(c.AdminBootstrapPassword) < MinBootstrapPasswordLen {
		return fmt.Errorf("config: APP_ENV=%s iken ADMIN_BOOTSTRAP_PASSWORD en az %d karakter olmalıdır",
			c.AppEnv, MinBootstrapPasswordLen)
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

// NeedsRedis yapılandırmanın bir Redis bağlantısı gerektirip gerektirmediğini
// bildirir.
//
// İki bağımsız özellik aynı istemciyi paylaşır: olay veri yolu ve koruma arka
// ucu. Sorunun tek bir yerde sorulması, "Redis'i kim açacak"ın iki ayrı yerde
// farklı cevaplanmasını önler — o ayrışma, biri Redis'teyken diğerinin
// sessizce bellek içinde kalması demek olurdu.
func (c Config) NeedsRedis() bool {
	return c.EventBus == BackendRedis || c.GuardBackend == BackendRedis
}

// Addr HTTP sunucusunun dinleyeceği adresi döner.
func (c Config) Addr() string { return fmt.Sprintf(":%d", c.AppPort) }
