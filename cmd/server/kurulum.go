package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/http/redisguard"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// API yüzeylerinin yol önekleri. Koruma, hız sınırı ve idempotency bu iki
// önekle kapsamlanır; /health ve /ready bilinçli olarak dışarıda kalır.
const (
	adminPrefix = "/admin/v1"
	storePrefix = "/store/v1"
)

// codeUnknownPlugin PLUGINS listesinde tanınmayan bir ad olduğunu bildirir.
const codeUnknownPlugin = "plugin_unknown"

// codeBootstrapFailed ilk yönetici tohumunun başarısız olduğunu bildirir.
const codeBootstrapFailed = "admin_bootstrap_failed"

// openAPIPath üretilen API şemasının sunulduğu yoldur.
const openAPIPath = "/openapi.json"

// codeGuardBackendMissing paylaşılan arka uç istendiği hâlde Redis
// istemcisinin bulunmadığını bildirir.
const codeGuardBackendMissing = "guard_backend_unavailable"

// gecicSirBayt geliştirme için üretilen rastgele sırrın bayt uzunluğudur;
// HS256'nın çıktı uzunluğuyla aynıdır.
const gecicSirBayt = 32

// eklentiKatalogu bu ikiliye DERLENMİŞ eklentilerdir.
//
// Katalog burada, kurulum kökünde durur: eklenti eklemek çekirdeği ya da
// herhangi bir modülü değiştirmez, yalnızca bu haritaya bir satır ekler
// (plan Faz 9 DoD). Hangisinin kurulacağını PLUGINS ortam değişkeni seçer.
var eklentiKatalogu = map[string]func() coreplugin.Plugin{
	paymentstripe.Name: func() coreplugin.Plugin { return paymentstripe.New() },
}

// korumaYigini uygulamanın koruma middleware'lerini yapılandırmadan kurar.
//
// Sıra ve kapsam kararı çekirdekteki [corehttp.APIGuards] içindedir; burada
// yalnızca yapılandırmadan gelen parçalar (hız sınırlayıcı, idempotency
// deposu, muaf yol) seçilir. Ayrımın sebebi, uçtan uca testlerin AYNI yığını
// kurabilmesidir: sıra burada yazılsaydı test kendi kopyasını tutar ve iki
// kopya sessizce ayrışırdı.
//
// # Arka uç seçimi
//
// GUARD_BACKEND=memory (varsayılan) tek süreçlik kurulum içindir. Birden çok
// örnek çalıştırılıyorsa "redis" gerekir; gerekçesi ve iki uygulamanın
// farkı [redisguard] paketinin godoc'undadır.
//
// Redis seçilmişse istemci ZORUNLUDUR ve yoksa açılış durur: "paylaşılan depo
// istedim ama sessizce bellek içiyle çalıştım", tam da korumanın çalıştığı
// sanılırken çalışmadığı durumdur.
func korumaYigini(
	cfg config.Config,
	authn corehttp.Authenticator,
	rdb *redis.Client,
	log *slog.Logger,
) ([]func(http.Handler) http.Handler, error) {
	opts := corehttp.GuardOptions{
		Authenticator: authn,
		AdminPrefix:   adminPrefix,
		StorePrefix:   storePrefix,
		// Giriş ucu korumadan MUAFTIR: kimliği doğrulanacak istek, kimliği
		// daha yeni kuracaktır. Yol elle yazılmaz, auth modülünün sabitinden
		// okunur.
		AdminExempt: []string{authapi.LoginPath},
	}

	if cfg.GuardBackend == config.BackendRedis {
		if rdb == nil {
			return nil, errors.Invalid(codeGuardBackendMissing,
				"GUARD_BACKEND=%s seçildi ama Redis istemcisi yok", config.BackendRedis)
		}

		depo, err := redisguard.NewIdempotencyStore(rdb, cfg.IdempotencyTTL)
		if err != nil {
			return nil, err
		}
		opts.IdempotencyStore = depo

		// Sınır kapalıysa (limit <= 0) sınırlayıcı hiç kurulmaz; kurulsaydı
		// "0 istek" gibi davranıp tüm trafiği keserdi.
		if cfg.RateLimitPerMinute > 0 {
			limiter, limitErr := redisguard.NewLimiter(rdb, cfg.RateLimitPerMinute, time.Minute)
			if limitErr != nil {
				return nil, limitErr
			}

			opts.Limiter = limiter
			opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
		}

		log.Info("koruma arka ucu: redis (paylaşılan)")

		return corehttp.APIGuards(opts), nil
	}

	opts.IdempotencyStore = corehttp.NewMemoryIdempotencyStore(cfg.IdempotencyTTL)

	// NewMemoryLimiter, limit pozitif değilse nil döner (hız sınırı kapalı).
	// Arayüz alanına doğrudan atamak, nil *MemoryLimiter'ı "nil olmayan
	// arayüz"e çevirip sınırlayıcıyı yine takardı; bu yüzden önce kontrol.
	if limiter := corehttp.NewMemoryLimiter(cfg.RateLimitPerMinute, time.Minute); limiter != nil {
		opts.Limiter = limiter
		opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
	}

	// Bellek içi kurulum çok örnekli dağıtımda BOZUKTUR ve bunu sessizce
	// yapar; uyarı, tek fark edilme şansıdır.
	if cfg.IsShared() {
		log.Warn("koruma arka ucu: bellek içi",
			"uyari", "birden çok örnek çalışıyorsa idempotency koruması örnekler arasında ÇALIŞMAZ",
			"cozum", "GUARD_BACKEND=redis")
	}

	return corehttp.APIGuards(opts), nil
}

// eklentileriSec PLUGINS listesindeki adları katalogdan kurar.
//
// Bilinmeyen ad HATA döner: sessizce atlamak, adı yanlış yazılmış bir
// eklentinin "kurulu" sanılmasına ve eksikliğin ancak ilk kullanımda
// görülmesine yol açardı.
func eklentileriSec(adlar []string) (*coreplugin.Registry, error) {
	kayit := coreplugin.NewRegistry(nil)

	for _, ad := range adlar {
		ad = strings.TrimSpace(ad)

		yapici, ok := eklentiKatalogu[ad]
		if !ok {
			return nil, errors.Invalid(codeUnknownPlugin,
				"bilinmeyen eklenti %q (tanınanlar: %s)", ad, strings.Join(eklentiAdlari(), ", "))
		}

		kayit.Add(yapici())
	}

	return kayit, nil
}

// eklentiAdlari katalogdaki adları sıralı döner; hata mesajı içindir.
func eklentiAdlari() []string {
	adlar := make([]string, 0, len(eklentiKatalogu))
	for ad := range eklentiKatalogu {
		adlar = append(adlar, ad)
	}

	slices.Sort(adlar)

	return adlar
}

// eklentiAyarlari eklentilere verilecek ayar haritasını ortamdan kurar.
//
// Eklentiler ayarlarını ortam değişkeni ADIYLA ister (örn. STRIPE_API_KEY);
// çekirdek config'i bu adları BİLEMEZ çünkü eklenti derleme zamanında
// eklenir. Haritayı burada kurmak, eklentiye os paketini kullandırmadan
// aynı sonucu verir ve testte sahte ayar geçirmeyi mümkün kılar.
func eklentiAyarlari() map[string]string {
	ortam := os.Environ()
	ayarlar := make(map[string]string, len(ortam))

	for _, satir := range ortam {
		ad, deger, ok := strings.Cut(satir, "=")
		if !ok {
			continue
		}

		ayarlar[ad] = deger
	}

	return ayarlar
}

// jwtSirri imza sırrını döner; geliştirmede yoksa açılışa özel bir sır üretir.
//
// PAYLAŞILAN ortamlarda (production, staging) boş ya da kısa sır
// config.Validate tarafından zaten REDDEDİLİR, yani buraya yalnızca yerel
// geliştirme/test düşer. Orada sabit bir
// varsayılan koymak en kötü seçenekti: herkesin bildiği bir sırla imzalanmış
// jeton, kazara üretime taşınan bir yapılandırmada tam yetkili admin jetonu
// üretmeye yarar.
//
// Rastgele sır, jetonları YENİDEN BAŞLATMAYA kadar geçerli kılar. Bedeli
// geliştiricinin yeniden giriş yapmasıdır; karşılığında hiçbir ortamda
// tahmin edilebilir bir imza sırrı bulunmaz.
func jwtSirri(cfg config.Config, log *slog.Logger) string {
	if cfg.JWTSecret != "" {
		return cfg.JWTSecret
	}

	bayt := make([]byte, gecicSirBayt)
	if _, err := rand.Read(bayt); err != nil {
		// crypto/rand okunamıyorsa sistemde daha büyük bir sorun vardır ve
		// zayıf bir yedeğe düşmek yanlış olurdu; boş sır auth modülünü
		// açılışta durdurur.
		log.Error("rastgele JWT sırrı üretilemedi", "error", err)

		return ""
	}

	log.Warn("JWT_SECRET verilmedi; bu açılışa özel rastgele sır üretildi",
		"uyari", "yeniden başlatmada tüm yönetim oturumları düşer")

	return base64.RawURLEncoding.EncodeToString(bayt)
}

// yoneticiKullanicilari tohum adımının auth modülünden istediği DAR yüzeydir.
//
// Somut *service.Service yerine iki metotluk bir arayüz kullanılır: kurulum,
// auth'un tüm yüzeyine değil yalnızca burada sayılan iki çağrıya bağlanır ve
// tohum mantığı veritabanı olmadan sahte bir uygulamayla sınanabilir. Servis
// container'dan ADLA (auth.ServiceName) çözülür.
//
// İmzalar auth'un service paketindeki girdi/çıktı tiplerini KULLANIR ve bu
// serbesttir: yasak olan çekirdeğin modülleri (Prensip 2.4) ya da modüllerin
// birbirini tanımasıdır; cmd/server ise kompozisyon köküdür ve zaten her
// modülü import eder.
type yoneticiKullanicilari interface {
	ListUsers(ctx context.Context, in authservice.ListUsersInput) (authservice.Page[authmodels.User], error)
	CreateUser(ctx context.Context, in authservice.CreateUserInput, password string) (authmodels.User, error)
}

// tohumlaYonetici hiç kullanıcı yoksa ilk yönetim kullanıcısını yaratır.
//
// Boş bir veritabanıyla açılan sunucuda hiç yönetici yoktur ve yönetim uçları
// korumalı olduğu için ilkini HTTP'den yaratmanın yolu da yoktur; bu adım
// olmadan taze bir kurulum kullanılamaz durumdadır.
//
// # Yalnızca boş kurulumda
//
// Adım kullanıcı sayısı SIFIRKEN çalışır, aksi hâlde bilgi loguyla atlanır. Bu
// yalnızca "ikinci kez yaratmamak" değildir: tohum, var olan bir kurulumun
// parolasına ve yetkilerine ASLA dokunmaz. Dokunsaydı ortam dosyasında
// unutulmuş bir ADMIN_BOOTSTRAP_PASSWORD her yeniden başlatmada üretim
// yöneticisinin parolasını sessizce geri alır ve yeniden başlatma güvenli
// olmaktan çıkardı.
//
// # Ne loglanır
//
// Parola loglanmaz. E-POSTA DA LOGLANMAZ: auth modülü kullanıcı yaratırken
// bilinçli olarak yalnızca kimliği yazıyor (bkz. internal/modules/auth/service
// user.go) ve kurulumun o kararı burada delmesi anlamsız olurdu — log
// toplayıcısı yönetim yüzeyinden çok daha geniş bir kitleye açıktır ve
// "hangi hesap yaratıldı" sorusuna kullanıcı kimliği yeter.
//
// Hata yolu ayrıdır: orada açılış zaten DURUR ve operatörün reddedilen değeri
// görmesi teşhis için gereklidir, çünkü düzeltilecek şey tam olarak odur.
func tohumlaYonetici(
	ctx context.Context,
	kullanicilar yoneticiKullanicilari,
	cfg config.Config,
	log *slog.Logger,
) error {
	// config.Validate ikisinin BİRLİKTE verilmesini zorlar; buraya yalnızca
	// "hiç verilmemiş" hâli düşer. Yine de iki alan da denetlenir: doğrulamadan
	// geçmemiş elle kurulmuş bir Config yarım girdiyle tohum çalıştıramamalı.
	if cfg.AdminBootstrapEmail == "" || cfg.AdminBootstrapPassword == "" {
		return nil
	}

	// Sayfa boyu 1'dir: burada gereken tek bilgi "hiç kullanıcı var mı",
	// listenin kendisi değil. Page.Count süzgece uyan TOPLAMI verir, sayfadaki
	// kayıt sayısını değil.
	sayfa, err := kullanicilar.ListUsers(ctx, authservice.ListUsersInput{Limit: 1})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"ilk yönetici tohumu için kullanıcı sayısı okunamadı")
	}
	if sayfa.Count > 0 {
		log.InfoContext(ctx, "ilk yönetici tohumu atlandı: kurulumda kullanıcı var",
			slog.Int64("kullanici_sayisi", sayfa.Count))

		return nil
	}

	// Yetki alanı BİLİNÇLİ olarak verilmez: auth modülünde nil dilim "tam
	// yetki" demektir ve ilk yönetici tam yetkili olmalıdır — ona yetki
	// dağıtacak başka kimse yoktur. Boş dilim geçilseydi hiçbir yönetim ucuna
	// erişemeyen bir hesap doğar ve sistem yine kullanılamaz kalırdı.
	kullanici, err := kullanicilar.CreateUser(ctx, authservice.CreateUserInput{
		Email: cfg.AdminBootstrapEmail,
	}, cfg.AdminBootstrapPassword)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"ilk yönetici oluşturulamadı")
	}

	log.InfoContext(ctx, "ilk yönetici oluşturuldu", slog.String("user_id", kullanici.ID))

	return nil
}
