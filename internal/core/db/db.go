// Package db PostgreSQL bağlantı havuzunu ve migration çalıştırıcısını sağlar.
//
// Paketin iki bağımsız sorumluluğu vardır:
//
//   - Bağlantı havuzu (Pool): pgxpool üzerine ince bir sarmalayıcı. Repository'ler
//     ham havuza Pool.Pool() ile erişir; havuzun yaşam döngüsü bu pakette kalır.
//   - Migration çalıştırıcısı (migrate.go): golang-migrate'i modül başına AYRI
//     versiyon tablosuyla çalıştırır. Bu, plan Bölüm 2.1/2.3'teki modül
//     izolasyonunun veritabanı düzeyindeki karşılığıdır.
//
// Paket genelinde geçerli bir kural: DSN hiçbir hata mesajında, hiçbir log
// kaydında ham hâliyle görünmez (plan Bölüm 8, "hassas veri loglanmaz"). Dışarı
// çıkan her hedef gösterimi Redact'ten geçer.
package db

import (
	"context"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Havuzun varsayılan ayarları; DefaultConfig bunları kullanır.
const (
	defaultMaxConns        = 10
	defaultMinConns        = 2
	defaultMaxConnLifetime = time.Hour
	defaultMaxConnIdleTime = 30 * time.Minute
	defaultConnectTimeout  = 5 * time.Second
)

// Ayıklanmış gösterimde bilinmeyen parçalar için kullanılan yer tutucular.
const (
	unknownTarget   = "<bilinmeyen-hedef>"
	unknownDatabase = "<veritabanı-yok>"
)

// Config PostgreSQL bağlantı havuzunun ayarlarıdır.
type Config struct {
	// URL pgx biçimindeki bağlantı adresidir
	// (örn. postgres://kullanici:parola@host:5432/veritabani?sslmode=disable).
	URL string
	// MaxConns havuzun açabileceği azami bağlantı sayısıdır.
	MaxConns int32
	// MinConns havuzun boşta bile korumaya çalıştığı asgari bağlantı sayısıdır.
	MinConns int32
	// MaxConnLifetime bir bağlantının azami yaşam süresidir; dolduğunda bağlantı
	// kapatılıp yenisiyle değiştirilir. Uzun ömürlü bağlantıların sunucu tarafı
	// kaynak sızıntısını ve yük dengeleyici kopmalarını gizlemesini engeller.
	MaxConnLifetime time.Duration
	// MaxConnIdleTime bir bağlantının boşta kalabileceği azami süredir.
	MaxConnIdleTime time.Duration
	// ConnectTimeout tek bir bağlantı kurulumu ve açılıştaki sağlık kontrolü için
	// tanınan azami süredir.
	ConnectTimeout time.Duration
}

// DefaultConfig verilen DSN için makul varsayılanlarla dolu bir Config döner.
// Çağıran yalnızca değiştirmek istediği alanları ezer.
func DefaultConfig(dsn string) Config {
	return Config{
		URL:             dsn,
		MaxConns:        defaultMaxConns,
		MinConns:        defaultMinConns,
		MaxConnLifetime: defaultMaxConnLifetime,
		MaxConnIdleTime: defaultMaxConnIdleTime,
		ConnectTimeout:  defaultConnectTimeout,
	}
}

// Validate Config alanlarının kendi içinde tutarlı olduğunu doğrular.
// New bunu otomatik çağırır; elle kurulan Config'ler için de kullanılabilir.
func (c Config) Validate() error {
	if strings.TrimSpace(c.URL) == "" {
		return errors.Invalid("db_config_invalid", "veritabanı adresi (URL) boş olamaz")
	}
	if c.MaxConns < 1 {
		return errors.Invalid("db_config_invalid", "MaxConns en az 1 olmalı, %d verildi", c.MaxConns)
	}
	if c.MinConns < 0 {
		return errors.Invalid("db_config_invalid", "MinConns negatif olamaz, %d verildi", c.MinConns)
	}
	if c.MinConns > c.MaxConns {
		return errors.Invalid("db_config_invalid",
			"MinConns (%d), MaxConns'tan (%d) büyük olamaz", c.MinConns, c.MaxConns)
	}
	if c.MaxConnLifetime <= 0 {
		return errors.Invalid("db_config_invalid",
			"MaxConnLifetime pozitif olmalı, %s verildi", c.MaxConnLifetime)
	}
	if c.MaxConnIdleTime <= 0 {
		return errors.Invalid("db_config_invalid",
			"MaxConnIdleTime pozitif olmalı, %s verildi", c.MaxConnIdleTime)
	}
	if c.MaxConnIdleTime > c.MaxConnLifetime {
		return errors.Invalid("db_config_invalid",
			"MaxConnIdleTime (%s), MaxConnLifetime'dan (%s) büyük olamaz",
			c.MaxConnIdleTime, c.MaxConnLifetime)
	}
	if c.ConnectTimeout <= 0 {
		return errors.Invalid("db_config_invalid",
			"ConnectTimeout pozitif olmalı, %s verildi", c.ConnectTimeout)
	}
	return nil
}

// Pool uygulamanın paylaşılan PostgreSQL bağlantı havuzudur.
// Eşzamanlı kullanıma güvenlidir; uygulama ömrü boyunca tek örnek yeterlidir.
type Pool struct {
	pool *pgxpool.Pool
	log  *slog.Logger
	// target loglanması güvenli hedef gösterimidir (host/veritabanı).
	target string
}

// New verilen ayarlarla bir bağlantı havuzu açar ve veritabanının gerçekten
// erişilebilir olduğunu doğrular. Dönen havuz kullanıldıktan sonra Close ile
// kapatılmalıdır. log nil ise loglama yapılmaz.
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Pool, error) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	target := Redact(cfg.URL)

	pgCfg, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		// Alttaki hata metni DSN'i olduğu gibi taşıyabildiği için SARMALANMAZ;
		// yalnızca ayıklanmış hedef gösterimi dışarı verilir.
		return nil, errors.Invalid("db_dsn_invalid",
			"veritabanı adresi ayrıştırılamadı (hedef: %s)", target)
	}

	pgCfg.MaxConns = cfg.MaxConns
	pgCfg.MinConns = cfg.MinConns
	pgCfg.MaxConnLifetime = cfg.MaxConnLifetime
	pgCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pgCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, pgCfg)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_pool_open_failed",
			"veritabanı havuzu açılamadı (hedef: %s)", target)
	}

	// pgxpool bağlantıyı tembel kurar; havuzun gerçekten çalıştığını burada
	// doğrulamazsak hata ilk isteğe kadar gizlenir.
	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, errors.Wrap(err, errors.KindUnavailable, "db_unreachable",
			"veritabanına ulaşılamıyor (hedef: %s)", target)
	}

	log.InfoContext(ctx, "postgres bağlantı havuzu hazır",
		slog.String("target", target),
		slog.Int64("max_conns", int64(cfg.MaxConns)),
		slog.Int64("min_conns", int64(cfg.MinConns)),
	)

	return &Pool{pool: pool, log: log, target: target}, nil
}

// Pool ham pgxpool havuzunu döner. Repository'ler sorgularını bunun üzerinden
// çalıştırır. Dönen havuzun yaşam döngüsü sarmalayıcıya aittir; çağıran onun
// üzerinde Close çağırmamalıdır.
func (p *Pool) Pool() *pgxpool.Pool {
	if p == nil {
		return nil
	}
	return p.pool
}

// Ping veritabanının erişilebilir olduğunu doğrular; sağlık kontrolleri için.
func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return errors.Unavailable("db_pool_closed", "veritabanı havuzu kurulmamış")
	}
	if err := p.pool.Ping(ctx); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, "db_unreachable",
			"veritabanına ulaşılamıyor (hedef: %s)", p.target)
	}
	return nil
}

// Target loglanması güvenli hedef gösterimini döner (host/veritabanı adı).
// Parola ve diğer kimlik bilgileri bu gösterimde yer almaz.
func (p *Pool) Target() string {
	if p == nil {
		return unknownTarget
	}
	return p.target
}

// Close havuzdaki tüm bağlantıları kapatır ve açık olanların bitmesini bekler.
//
// pgxpool.Close bağlam almadığı ve iptal edilemediği için imza da almaz; bu,
// plan Bölüm 8'deki "her metot context alır" kuralının bilinçli istisnasıdır.
func (p *Pool) Close() {
	if p == nil || p.pool == nil {
		return
	}
	p.pool.Close()
	p.log.Info("postgres bağlantı havuzu kapatıldı", slog.String("target", p.target))
}

// Redact bir bağlantı adresinden kimlik bilgilerini ayıklayıp yalnızca host ve
// veritabanı adını içeren, loglanması güvenli bir gösterim döner.
//
// Hem URL biçimini (postgres://kullanici:parola@host:5432/ad) hem de pgx'in
// kabul ettiği anahtar=değer biçimini (host=... dbname=... password=...) tanır.
// Ayrıştıramadığı girdiler için sabit bir yer tutucu döner: ham DSN hiçbir
// koşulda geri verilmez, çünkü bu fonksiyonun tek amacı parola sızıntısını
// yapısal olarak imkânsız kılmaktır.
func Redact(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Host != "" {
		// u.User ayrı alanda durur; Host yalnızca host[:port] içerir.
		return u.Host + "/" + databaseOrPlaceholder(strings.TrimPrefix(u.Path, "/"))
	}

	var host, port, name string
	for _, field := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "host":
			host = value
		case "port":
			port = value
		case "dbname":
			name = value
		}
	}
	if host == "" {
		return unknownTarget
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return host + "/" + databaseOrPlaceholder(name)
}

// databaseOrPlaceholder boş veritabanı adını okunabilir bir yer tutucuya çevirir.
func databaseOrPlaceholder(name string) string {
	if name == "" {
		return unknownDatabase
	}
	return name
}
