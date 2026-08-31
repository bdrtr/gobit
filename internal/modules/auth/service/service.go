// Package service auth modülünün iş mantığını barındırır.
//
// # Modüller arası yüzey (ADR 0001)
//
// auth hiçbir modülü import ETMEZ. Ters yön vardır: çekirdeğin HTTP katmanı
// kimlik doğrulamak ister, product katalog süzmek için satış kanallarını
// bilmek ister. Bu yüzden auth'un yüzeyi ÜÇE ayrılmıştır:
//
//   - Modül içi zengin yüzey — [models] tiplerini kullanır ([Service.CreateUser],
//     [Service.CreateAPIKey] …). Bu metotları yalnızca auth'un kendi API
//     katmanı çağırır.
//   - Modüller arası yüzey — YALNIZCA ilkel ve stdlib tipleri kullanır
//     (bkz. interop.go).
//   - Kimlik doğrulama yüzeyi — çekirdeğin corehttp.Authenticator arayüzünü
//     YAPISAL olarak karşılayan [Interop] tipi (bkz. interop.go). corehttp
//     ÇEKİRDEKTİR ve import edilebilir; Principal tipi burada yeniden
//     TANIMLANMAZ, çekirdeğinki kullanılır.
//
// # Güvenlik kararları
//
// Modülün güvenlik kararları ve gerekçeleri tek tek belgelenmiştir:
//
//   - Parola saklama ve zamanlama eşitliği — password.go
//   - Oturumun düşürülmesi (çıkış ve parola değişimi) — session.go
//   - JWT üretimi ve doğrulaması — token.go
//   - API anahtarı üretimi, saklanması ve tür ayrımı — apikey.go ve
//     [models.APIKey]
//   - Giriş kilidi (art arda başarısız denemeler) — password.go, [Options]
//
// Ortak kural: düz parola ve düz API anahtarı ASLA saklanmaz, ASLA loglanmaz
// ve hiçbir hata mesajında geçmez.
package service

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	// CodeInvalidInput girdinin doğrulamadan geçmediğini bildirir.
	CodeInvalidInput = "auth_invalid_input"
	// CodeInvalidCredentials giriş bilgilerinin kabul edilmediğini bildirir.
	//
	// TEK bir kod bilinçlidir: "kullanıcı yok", "parola yanlış", "hesap
	// kilitli" ve "parola atanmamış" durumlarının HEPSİ bu kodla döner
	// (bkz. [Service.Login]).
	CodeInvalidCredentials = "auth_invalid_credentials" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU
	// CodeWeakPassword parolanın politikayı karşılamadığını bildirir.
	CodeWeakPassword = "auth_weak_password" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU
	// CodeAPIKeyRevoked anahtarın iptal edilmiş olduğunu bildirir.
	CodeAPIKeyRevoked = "auth_api_key_revoked" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU
	// CodeAPIKeyTypeMismatch anahtarın yanlış yüzeyde sunulduğunu bildirir.
	CodeAPIKeyTypeMismatch = "auth_api_key_type_mismatch" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU
	// CodeNoSalesChannel publishable anahtarın etkin bir kanala bağlı
	// olmadığını bildirir.
	CodeNoSalesChannel = "auth_no_sales_channel"
	// CodeTokenInvalid oturum jetonunun kabul edilmediğini bildirir.
	CodeTokenInvalid = "auth_token_invalid" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU
	// CodeNoSession çağıranın kapatılabilecek bir oturumu olmadığını bildirir.
	//
	// Bugünkü tek kaynağı, çıkış ucunu çağıran bir API anahtarıdır: anahtar
	// jetonla değil kalıcı bir sırla gelir ve o sırrın "kapatılması" diye bir
	// işlem yoktur (bkz. [Service.Logout]).
	CodeNoSession = "auth_no_session"
	// CodeUnconfigured servisin kurulmadığını bildirir.
	CodeUnconfigured = "auth_service_unconfigured"
	// CodeSecretMissing imza sırrının verilmediğini bildirir.
	CodeSecretMissing = "auth_jwt_secret_missing" //nolint:gosec // G101: kimlik bilgisi değil, istemciye dönen sabit hata KODU
)

// Sayfalama sınırları. Limit verilmezse varsayılan uygulanır; aşırı büyük bir
// limit reddedilir, böylece istemci tek istekle veritabanını tarayamaz.
const (
	// DefaultLimit limit verilmediğinde uygulanan sayfa boyutudur.
	DefaultLimit int64 = 50
	// MaxLimit tek istekte istenebilecek en büyük sayfa boyutudur.
	MaxLimit int64 = 100
)

// Varsayılan kurulum değerleri.
const (
	// DefaultBcryptCost varsayılan bcrypt maliyet parametresidir.
	//
	// bcrypt.DefaultCost (10) 2011'in donanımına göre seçilmiştir; 12, bugünün
	// sunucusunda parola başına ~250 ms demektir ve çevrimdışı deneme hızını
	// buna göre düşürür. Değer SABİT DEĞİLDİR ([Options.BcryptCost]) çünkü
	// donanım hızlanır ve maliyet onunla birlikte artırılmalıdır; maliyet
	// bcrypt hash'inin İÇİNDE saklandığı için artırma eski parolaları
	// geçersiz kılmaz — eski hash'ler kendi maliyetleriyle doğrulanmaya devam
	// eder.
	DefaultBcryptCost = 12
	// DefaultJWTTTL oturum jetonunun varsayılan ömrüdür.
	DefaultJWTTTL = 12 * time.Hour
	// DefaultIssuer oturum jetonunun varsayılan "iss" iddiasıdır.
	DefaultIssuer = "gobit"
	// DefaultLoginFailureThreshold kilidi tetikleyen art arda başarısız
	// deneme sayısıdır.
	DefaultLoginFailureThreshold = 5
	// DefaultLoginLockDuration kilidin varsayılan süresidir.
	DefaultLoginLockDuration = 15 * time.Minute
	// DefaultUsageThrottle api_key.last_used_at sütununun en fazla ne sıklıkta
	// yazılacağıdır.
	DefaultUsageThrottle = time.Minute
)

// Page sayfalanmış bir liste sonucudur.
//
// Limit ve Offset, isteğin ham değerleri değil UYGULANAN değerlerdir; API zarfı
// bu alanları olduğu gibi yazar, böylece istemci varsayılana düşen bir limitten
// haberdar olur.
type Page[T any] struct {
	// Items geçerli sayfadaki kayıtlardır.
	Items []T
	// Count filtreye uyan TOPLAM kayıt sayısıdır (sayfa boyu değil).
	Count int64
	// Limit uygulanan sayfa boyudur.
	Limit int64
	// Offset uygulanan atlama sayısıdır.
	Offset int64
}

// Repository servisin ihtiyaç duyduğu veri erişim yüzeyidir.
//
// Arayüz TÜKETEN tarafta (burada) tanımlıdır; somut uygulama
// internal/modules/auth/repository paketindedir. Bu, ADR 0001'in örüntüsünün
// modül İÇİNDEKİ karşılığıdır ve servisin veritabanı olmadan test edilmesini
// sağlar — parola zamanlaması, jeton doğrulaması ve anahtar tür ayrımı gibi
// kararların hepsi sahte bir depo ile sınanabilir.
type Repository interface {
	CreateUser(ctx context.Context, u models.User, identity *models.AuthIdentity) (models.User, error)
	GetUser(ctx context.Context, id string) (models.User, error)
	GetUserByEmail(ctx context.Context, email string) (models.User, error)
	ListUsers(ctx context.Context, filter models.UserFilter, limit, offset int64) ([]models.User, int64, error)
	GetUsersByIDs(ctx context.Context, ids []string) ([]models.User, error)
	UpdateUser(ctx context.Context, id string, patch models.UserPatch, now time.Time) (models.User, error)
	DeleteUser(ctx context.Context, id string, now time.Time) error

	GetIdentity(ctx context.Context, userID, provider string) (models.AuthIdentity, error)
	SetPasswordHash(ctx context.Context, userID, provider, providerIdentity, hash string, now time.Time) (models.AuthIdentity, error)
	RevokeSessions(ctx context.Context, userID, provider string, now time.Time) (models.AuthIdentity, error)
	RegisterLoginFailure(ctx context.Context, identityID string, threshold int, lockUntil, now time.Time) (models.AuthIdentity, error)
	RegisterLoginSuccess(ctx context.Context, identityID string, now time.Time) error

	CreateAPIKey(ctx context.Context, k models.APIKey) (models.APIKey, error)
	GetAPIKey(ctx context.Context, id string) (models.APIKey, error)
	GetAPIKeyByHash(ctx context.Context, tokenHash string) (models.APIKey, error)
	ListAPIKeys(ctx context.Context, filter models.APIKeyFilter, limit, offset int64) ([]models.APIKey, int64, error)
	RevokeAPIKey(ctx context.Context, id, revokedBy string, now time.Time) (models.APIKey, error)
	DeleteAPIKey(ctx context.Context, id string, now time.Time) error
	MarkAPIKeyUsed(ctx context.Context, id string, usedAt, staleBefore time.Time) error
	LinkSalesChannel(ctx context.Context, apiKeyID, channelID string, now time.Time) error
	UnlinkSalesChannel(ctx context.Context, apiKeyID, channelID string) error
	ChannelIDsOfKey(ctx context.Context, apiKeyID string) ([]string, error)
	ChannelsOfKey(ctx context.Context, apiKeyID string) ([]models.SalesChannel, error)

	CreateSalesChannel(ctx context.Context, c models.SalesChannel) (models.SalesChannel, error)
	GetSalesChannel(ctx context.Context, id string) (models.SalesChannel, error)
	ListSalesChannels(ctx context.Context, filter models.SalesChannelFilter, limit, offset int64) ([]models.SalesChannel, int64, error)
	GetSalesChannelsByIDs(ctx context.Context, ids []string) ([]models.SalesChannel, error)
	UpdateSalesChannel(ctx context.Context, id string, patch models.SalesChannelPatch, now time.Time) (models.SalesChannel, error)
	DeleteSalesChannel(ctx context.Context, id string, now time.Time) error
}

// Options servisin kurulum ayarlarıdır.
//
// JWTSecret dışındaki her alanın makul bir varsayılanı vardır; sır ise
// varsayılan KABUL ETMEZ (bkz. [Options.JWTSecret]).
type Options struct {
	// Logger yapısal log hedefidir; nil ise loglar atılır.
	Logger *slog.Logger
	// Now zaman kaynağıdır; nil ise time.Now kullanılır. Testler burayı sabit
	// bir saatle doldurarak zamana bağlı dalları (jeton süresi, kilit süresi)
	// belirlenimci hâle getirir.
	Now func() time.Time

	// JWTSecret oturum jetonlarının HS256 ile imzalandığı sırdır.
	//
	// VARSAYILANI YOKTUR ve olmamalıdır: tahmin edilebilir bir imza sırrı,
	// herkesin kendine admin jetonu üretebilmesi demektir. Boş bırakılırsa
	// [New] servisi kurar ama jeton üretimi ve doğrulaması errors.Unavailable
	// döner — sessizce imzasız çalışmaktansa açıkça durmak yeğdir.
	//
	// Değer çekirdek yapılandırmasından (cfg.JWTSecret) PARAMETRE olarak
	// gelir; auth modülü config paketini tanımaz. ASLA loglanmaz.
	JWTSecret string
	// JWTTTL jetonun geçerlilik süresidir; 0 ise [DefaultJWTTTL].
	JWTTTL time.Duration
	// JWTIssuer jetonun "iss" iddiasıdır; boş ise [DefaultIssuer].
	JWTIssuer string

	// BcryptCost parola hash'inin maliyet parametresidir; 0 ise
	// [DefaultBcryptCost]. Aralık dışı bir değer varsayılana çekilir ve
	// uyarı loglanır (bkz. [DefaultBcryptCost]).
	BcryptCost int

	// LoginFailureThreshold kilidi tetikleyen art arda başarısız deneme
	// sayısıdır; 0 ise [DefaultLoginFailureThreshold].
	LoginFailureThreshold int
	// LoginLockDuration kilidin süresidir; 0 ise [DefaultLoginLockDuration].
	LoginLockDuration time.Duration
	// UsageThrottle api_key.last_used_at yazma sıklığı sınırıdır; 0 ise
	// [DefaultUsageThrottle].
	UsageThrottle time.Duration
}

// Service auth modülünün public servisidir. Eşzamanlı kullanıma güvenlidir.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time

	secret    []byte
	tokenTTL  time.Duration
	issuer    string
	cost      int
	threshold int
	lockFor   time.Duration
	throttle  time.Duration

	// dummyHash zamanlama eşitliği için kullanılan kukla bcrypt hash'idir;
	// ilk ihtiyaçta bir kez üretilir (bkz. password.go).
	dummyHash func() []byte
}

// New verilen depo üzerinde çalışan bir servis üretir.
//
// repo nil ise bu, kurulumda değil ilk çağrıda tipli bir hata olarak bildirilir;
// kurulum yolu panik üretmez.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	cost := opts.BcryptCost
	if cost == 0 {
		cost = DefaultBcryptCost
	}
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		// Aralık dışı maliyet sessizce kabul edilseydi bcrypt her çağrıda
		// hata döndürür ve hiçbir parola doğrulanamazdı.
		log.Warn("auth: geçersiz bcrypt maliyeti, varsayılana düşülüyor",
			slog.Int("verilen", cost), slog.Int("varsayilan", DefaultBcryptCost))
		cost = DefaultBcryptCost
	}

	svc := &Service{
		repo:      repo,
		log:       log,
		now:       now,
		secret:    []byte(opts.JWTSecret),
		tokenTTL:  orDuration(opts.JWTTTL, DefaultJWTTTL),
		issuer:    orString(opts.JWTIssuer, DefaultIssuer),
		cost:      cost,
		threshold: orInt(opts.LoginFailureThreshold, DefaultLoginFailureThreshold),
		lockFor:   orDuration(opts.LoginLockDuration, DefaultLoginLockDuration),
		throttle:  orDuration(opts.UsageThrottle, DefaultUsageThrottle),
	}
	svc.dummyHash = newDummyHash(cost)
	return svc
}

// ready deponun kurulu olduğunu doğrular.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable(CodeUnconfigured, "auth servisi kurulmamış")
	}
	return nil
}

// clock geçerli anı UTC olarak döner.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// orDuration sıfır süreyi varsayılanla değiştirir.
func orDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

// orInt sıfır ya da negatif sayıyı varsayılanla değiştirir.
func orInt(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// orString boş dizeyi varsayılanla değiştirir.
func orString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
