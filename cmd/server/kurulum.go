package main

import (
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
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

// openAPIPath üretilen API şemasının sunulduğu yoldur.
const openAPIPath = "/openapi.json"

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
// Hız sınırı ve idempotency deposu BELLEK İÇİDİR: tek süreçlik kurulum
// içindir. Yatay ölçeklenen bir dağıtımda hız sınırı örnek sayısıyla çarpılır
// ve idempotency koruması örnekler arasında hiç çalışmaz; ikisi de paylaşılan
// bir depo ister.
func korumaYigini(cfg config.Config, authn corehttp.Authenticator) []func(http.Handler) http.Handler {
	opts := corehttp.GuardOptions{
		Authenticator: authn,
		AdminPrefix:   adminPrefix,
		StorePrefix:   storePrefix,
		// Giriş ucu korumadan MUAFTIR: kimliği doğrulanacak istek, kimliği
		// daha yeni kuracaktır. Yol elle yazılmaz, auth modülünün sabitinden
		// okunur.
		AdminExempt:      []string{authapi.LoginPath},
		IdempotencyStore: corehttp.NewMemoryIdempotencyStore(cfg.IdempotencyTTL),
	}

	// NewMemoryLimiter, limit pozitif değilse nil döner (hız sınırı kapalı).
	// Arayüz alanına doğrudan atamak, nil *MemoryLimiter'ı "nil olmayan
	// arayüz"e çevirip sınırlayıcıyı yine takardı; bu yüzden önce kontrol.
	if limiter := corehttp.NewMemoryLimiter(cfg.RateLimitPerMinute, time.Minute); limiter != nil {
		opts.Limiter = limiter
		opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
	}

	return corehttp.APIGuards(opts)
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
