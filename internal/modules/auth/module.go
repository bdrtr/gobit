// Package auth kimlik modülüdür (plan Bölüm 6, Faz 8).
//
// Sorumluluğu tek cümleyle: bir isteğin KİMDEN geldiğini ve NEYE yetkili
// olduğunu söylemek. Modül User (yönetim kullanıcısı), AuthIdentity, ApiKey ve
// SalesChannel verisinin TEK yazma yetkilisidir (Prensip 2.3).
//
// # Müşteri değil, yönetici
//
// Buradaki "user" mağazadan alışveriş yapan kişi DEĞİLDİR; o, customer
// modülünün verisidir. İki kavramın ayrı modüllerde durması bilinçlidir: bir
// müşterinin yönetim yetkisi kazanması diye bir yol yoktur ve iki tablo hiçbir
// yerde birleşmez.
//
// # Dışarıya açtığı yüzeyler
//
//   - "auth.interop" — çekirdeğin corehttp.Authenticator arayüzünü YAPISAL
//     olarak karşılayan kimlik doğrulayıcı. Çekirdek onu ADLA çözer ve auth'u
//     import etmez (ADR 0001).
//   - "auth.service" — modüller arası ilkel çağrı yüzeyi (bkz.
//     internal/modules/auth/service, interop.go).
//   - "sales_channel.query" — Query katmanına açılan okuma sağlayıcısı
//     (ADR 0004). Kullanıcılar ve API anahtarları BU YÜZEYE AÇILMAZ.
//   - /admin/v1/auth/login, /admin/v1/auth/me, /admin/v1/auth/logout,
//     /admin/v1/users, /admin/v1/api-keys, /admin/v1/sales-channels —
//     yönetim API'si.
//
// # Oturum kapatma TOPTANDIR
//
// POST /admin/v1/auth/logout çağıranın BÜTÜN oturumlarını düşürür; tek cihaz
// seçilemez. Jeton durum tutmaz ve tek bir jetonu geçersizleştirmek jti bazlı
// bir kara liste (yeni bir depo) isterdi; onun yerine kimlik başına tutulan
// tek bir zaman çapası ilerletilir ve ondan önce üretilmiş tüm jetonlar birden
// düşer (bkz. internal/modules/auth/service, session.go).
//
// # KORUMASIZ UÇ
//
// POST /admin/v1/auth/login doğası gereği korumasızdır ve router'ı kuran taraf
// corehttp.RequireAdmin'i bağlarken bu yolu DIŞARIDA BIRAKMALIDIR. Yol,
// api.LoginPath sabitiyle yayımlanır; elle yazılmamalıdır.
//
// # Sır ve ömür PARAMETREDİR
//
// Modül internal/core/config paketini tanımaz: JWT sırrı ve ömrü [Options] ile
// dışarıdan verilir ve uygulamayı kuran taraf (cmd/server) onları
// yapılandırmadan okuyup buraya geçirir. Sır boşsa [Module.Register] HATA
// DÖNER — imzasız bir yönetim yüzeyiyle açılmaktansa açılmamak doğrudur.
//
// # Link'i bildiren tarafa not
//
// Query, bir genişletmenin hedef sağlayıcısını link tanımının UCUNDAKİ MODÜL
// ADINDAN bulur (hedef ad + ".query" aranır). auth ucu ENTITY ADIYLA
// yazılmalıdır ve bu ad modül adından FARKLIDIR:
//
//	link.LinkDefinition{
//	    Name:        "product_sales_channel",
//	    From:        link.LinkSide{Module: "product", Field: "product_id"},
//	    To:          link.LinkSide{Module: "sales_channel", Field: "sales_channel_id"},
//	    Cardinality: link.ManyToMany,
//	}
//
// Sağlayıcı adı [ProviderName] sabitinden okunmalıdır.
package auth

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/repository"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// Container'daki adlar.
const (
	// ModuleName modülün benzersiz adıdır; migration versiyon tablosunun öneki
	// de budur.
	ModuleName = "auth"
	// ServiceName servisin container'daki adıdır. Tüketici modüller onu bu adla
	// ve KENDİ tanımladıkları dar arayüzle çözer (ADR 0001).
	ServiceName = ModuleName + ".service"
	// InteropName kimlik doğrulayıcının container'daki adıdır.
	//
	// Çekirdek corehttp.Authenticator'ı bu adla çözer; adın çekirdekte
	// yazılabilmesi için modülün import edilmesi GEREKMEZ (Prensip 2.4).
	InteropName = ModuleName + ".interop"
	// ProviderName query sağlayıcısının container'daki adıdır (ADR 0004).
	//
	// Ad MODÜL adından değil ENTITY adından türer: sağlayıcı "sales_channel"
	// entity'sini sunar.
	ProviderName = service.Entity + query.ProviderSuffix
	// dbServiceName çekirdek veritabanı havuzunun container'daki adıdır.
	dbServiceName = "core.db"
)

// codeSetupFailed modül kurulumunun başarısız olduğunu bildirir.
const codeSetupFailed = "auth_module_setup_failed"

// codeSecretMissing JWT imza sırrının verilmediğini bildirir.
const codeSecretMissing = "auth_jwt_secret_missing" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU

// minSecretLenWarn altında uyarı loglanan sır uzunluğudur.
//
// HS256 için sır, çıktı uzunluğu kadar (32 bayt) entropi taşımalıdır; daha
// kısası kaba kuvvetle bulunabilir. Kısa sır burada REDDEDİLMEZ — üretim kapısı
// internal/core/config, Validate içindedir ve yerel geliştirmede kısa bir sırla
// çalışmak pratiktir. Ama sessiz de geçilmez.
const minSecretLenWarn = 32

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationsRoot gömülü dosyaların "migrations/" öneki soyulmuş hâlidir:
// golang-migrate kaynağı KÖKTEN okur ve embed.FS dosyaları klasör adıyla
// birlikte taşırdı.
var migrationsRoot = mustSub(migrationsFS, "migrations")

// Options auth modülünün kurulum ayarlarıdır.
//
// JWT sırrı ve ömrü BURADAN gelir: modül internal/core/config paketini
// tanımaz ve container'da da config kayıtlı değildir, bu yüzden değerler
// uygulamayı kuran taraftan PARAMETRE olarak alınır.
type Options struct {
	// JWTSecret oturum jetonlarının HS256 ile imzalandığı sırdır; ZORUNLUDUR.
	//
	// Boş bırakılırsa [Module.Register] hata döner. ASLA loglanmaz.
	JWTSecret string
	// JWTTTL oturum jetonunun ömrüdür; 0 ise service.DefaultJWTTTL.
	JWTTTL time.Duration
	// JWTIssuer jetonun "iss" iddiasıdır; boş ise service.DefaultIssuer.
	JWTIssuer string
	// BcryptCost parola hash'inin maliyet parametresidir; 0 ise
	// service.DefaultBcryptCost. Donanım hızlandıkça artırılmalıdır.
	BcryptCost int
	// LoginFailureThreshold kilidi tetikleyen art arda başarısız deneme
	// sayısıdır; 0 ise service.DefaultLoginFailureThreshold.
	LoginFailureThreshold int
	// LoginLockDuration kilidin süresidir; 0 ise
	// service.DefaultLoginLockDuration.
	LoginLockDuration time.Duration
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
}

// Module auth modülünün [module.Module] uygulamasıdır.
type Module struct {
	opts    Options
	svc     *service.Service
	handler *api.Handler
	log     *slog.Logger
}

var _ module.Module = (*Module)(nil)

// Belgeyi anlatabildiği de derleme zamanında sabitlenir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve kompozisyon kökü onu TİP
// İDDİASIYLA arar; metot adı ya da imzası kayarsa hiçbir şey derlemede
// kırılmaz, yalnızca auth'un uçları belgeden sessizce düşerdi. Bedeli
// yönetim istemcisinin gövdesiz kalması olurdu: kullanıcı ve anahtar
// oluşturan uçlar, ne gönderileceği bilinmeyen metotlara dönüşürdü.
var _ openapi.Describer = (*Module)(nil)

// New kurulmamış bir auth modülü üretir; servis [Module.Register] içinde
// kurulur.
//
// Ana uygulama bunu şöyle çağırır:
//
//	registry.Add(auth.New(auth.Options{
//	    JWTSecret: cfg.JWTSecret,
//	    JWTTTL:    cfg.JWTTTL,
//	    Logger:    log,
//	}))
func New(opts Options) *Module {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Module{opts: opts, log: log}
}

// Name modülün adını döner.
func (m *Module) Name() string { return ModuleName }

// Migrations modülün migration dosyalarını döner.
func (m *Module) Migrations() fs.FS { return migrationsRoot }

// Register servisi, kimlik doğrulayıcıyı ve query sağlayıcısını container'a
// kaydeder.
//
// auth hiçbir MODÜLÜN servisine ihtiyaç duymaz; yalnızca çekirdek havuzunu
// çözer. Havuz Bootstrap'tan ÖNCE kaydedildiği için burada doğrudan çözmek
// güvenlidir.
//
// İmza sırrı boşsa kurulum HATA ile durur. Gerekçe: sırsız bir auth modülü
// giriş yapılamayan ama korumalı görünen bir yönetim yüzeyi üretirdi; hata
// açılışta görünür, sessiz kurulum ise ilk giriş denemesinde ortaya çıkardı.
// Yerel geliştirmede auth modülü hiç kaydedilmezse uygulama sırsız da açılır
// (bkz. internal/core/config, JWTSecret).
func (m *Module) Register(ctx context.Context, c *container.Container) error {
	if m.opts.JWTSecret == "" {
		return errors.Invalid(codeSecretMissing,
			"%s modülü JWT imza sırrı olmadan kaydedilemez; JWT_SECRET ayarlanmalı", ModuleName)
	}
	if len(m.opts.JWTSecret) < minSecretLenWarn {
		// Sır loglanmaz; yalnızca uzunluğu bildirilir.
		m.log.WarnContext(ctx, "auth: JWT imza sırrı kısa",
			slog.Int("uzunluk", len(m.opts.JWTSecret)),
			slog.Int("onerilen_en_az", minSecretLenWarn),
		)
	}

	pool, err := container.Resolve[*db.Pool](c, dbServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeSetupFailed,
			"%s modülü %q servisini çözemedi", ModuleName, dbServiceName)
	}

	repo := repository.New(pool.Pool())
	m.svc = service.New(repo, service.Options{
		Logger:                m.log,
		JWTSecret:             m.opts.JWTSecret,
		JWTTTL:                m.opts.JWTTTL,
		JWTIssuer:             m.opts.JWTIssuer,
		BcryptCost:            m.opts.BcryptCost,
		LoginFailureThreshold: m.opts.LoginFailureThreshold,
		LoginLockDuration:     m.opts.LoginLockDuration,
	})
	m.handler = api.New(m.svc)

	if err := c.Provide(ServiceName, m.svc); err != nil {
		return err
	}
	if err := c.Provide(InteropName, service.NewInterop(m.svc)); err != nil {
		return err
	}
	if err := c.Provide(ProviderName, service.NewQueryProvider(m.svc)); err != nil {
		return err
	}

	m.log.InfoContext(ctx, "auth modülü kaydedildi",
		slog.String("servis", ServiceName),
		slog.String("kimlik_dogrulayici", InteropName),
		slog.String("saglayici", ProviderName),
		slog.String("korumasiz_uc", api.LoginPath),
	)
	return nil
}

// Routes modülün yönetim route'larını router'a bağlar.
//
// Register'dan SONRA çağrılır (bkz. module.Registry.Bootstrap); handler bu
// yüzden kurulmuş olur. Yine de nil kontrolü vardır: Register hata verip
// Bootstrap yarıda kesilirse Routes hiç çağrılmaz, ama modül elle kullanılırsa
// panik yerine sessiz bir no-op daha güvenlidir.
func (m *Module) Routes(r chi.Router) {
	if m.handler == nil {
		return
	}
	m.handler.Routes(r)
}

// Describe modülün yönetim uçlarını OpenAPI belgesine işler.
//
// Anlatımın kendisi [api.Describe]'dedir: gövde şemaları o paketin dışa kapalı
// DTO'larından türetilir ve tipleri yalnızca belge uğruna dışa açmak modülün
// yüzeyini genişletirdi.
//
// [Module.Routes]'un tersine handler kontrolü YOKTUR ve gerekmez: şema
// tiplerden gelir, servisten değil. Kontrol koymak, kurulmamış bir modülün
// belgesini de sessizce boşaltırdı.
func (m *Module) Describe(d *openapi.Doc) { api.Describe(d) }

// Service kurulmuş servisi döner; Register çağrılmadıysa nil.
//
// Modülü doğrudan kullanan testler ve gömen uygulamalar içindir; normal akışta
// servis container'dan [ServiceName] adıyla çözülür.
func (m *Module) Service() *service.Service { return m.svc }

// mustSub gömülü dosya sisteminin alt ağacını açar.
//
// Yol derleme zamanında sabittir; buraya düşmek migrations klasörünün
// gömülmediği anlamına gelir ve sessiz geçilemez — migration'sız açılan bir
// modül, tabloları olmadan çalışmaya başlardı.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic("auth: migration kaynağı açılamadı: " + err.Error())
	}
	return sub
}
