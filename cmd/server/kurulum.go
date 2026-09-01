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

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/http/redisguard"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/modules/file"
	filelocal "github.com/bdrtr/gobit/internal/modules/file/local"
	fileservice "github.com/bdrtr/gobit/internal/modules/file/service"
	"github.com/bdrtr/gobit/internal/modules/notification"
	notificationservice "github.com/bdrtr/gobit/internal/modules/notification/service"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
	"github.com/bdrtr/gobit/plugins/searchpg"
)

// API yüzeylerinin yol önekleri. Koruma, hız sınırı ve idempotency bu iki
// önekle kapsamlanır; /health ve /ready bilinçli olarak dışarıda kalır.
const (
	adminPrefix = "/admin/v1"
	storePrefix = "/store/v1"
)

// codeFlowSetupFailed modüller arası akışların kurulamadığını bildirir.
const codeFlowSetupFailed = "workflow_setup_failed"

// codeUnknownPlugin PLUGINS listesinde tanınmayan bir ad olduğunu bildirir.
const codeUnknownPlugin = "plugin_unknown"

// codeBootstrapFailed ilk yönetici tohumunun başarısız olduğunu bildirir.
const codeBootstrapFailed = "admin_bootstrap_failed"

// codeAdminBootstrapRequired taze bir kurulumun yönetilebilir olmadığını
// bildirir: hiç kullanıcı yok ve tohum yapılandırılmamış.
const codeAdminBootstrapRequired = "admin_bootstrap_required"

// codeUnknownNotificationProvider NOTIFICATION_PROVIDER'ın kayıtlı bir
// sağlayıcıya karşılık gelmediğini bildirir.
const codeUnknownNotificationProvider = "notification_provider_unknown"

// codeUnknownFileProvider FILE_PROVIDER'ın kayıtlı bir sağlayıcıya karşılık
// gelmediğini bildirir.
const codeUnknownFileProvider = "file_provider_unknown"

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
//
// İki eklenti iki farklı uzatma biçimini gösterir: paymentstripe yalnızca bir
// SAĞLAYICI kaydeder (payment modülünün genişleme noktası), searchpg ise KENDİ
// MODÜLÜNÜ getirir — kendi tablosu, kendi migration'ı ve kendi route'larıyla.
// İkincisi, aşağıdaki satır dışında hiçbir yerde adı geçmeden yeni bir uç
// (GET /store/v1/search) açar.
var eklentiKatalogu = map[string]func() coreplugin.Plugin{
	paymentstripe.Name: func() coreplugin.Plugin { return paymentstripe.New() },
	searchpg.Name:      func() coreplugin.Plugin { return searchpg.New() },
}

// belgeyiAnlat OpenAPI belgesini kurar ve kendini anlatabilen modüllere işletir.
//
// [openapi.Describer] OPSİYONEL bir arayüzdür ve tip iddiası BURADA yapılır.
// Sözleşmeye ([module.Module]) metot eklemek tüm modülleri aynı anda kıran bir
// değişiklikti; üstelik anlatılmamış bir modül GEÇERLİ bir modeldir — belgede
// yolu, metodu ve güvenliğiyle görünür, yalnızca gövdesi olmaz.
//
// Çağrının kompozisyon kökünde olması zorunlu: çekirdek modülleri tanımaz
// (Prensip 2.4) ve modül listesini gören tek yer burasıdır.
func belgeyiAnlat(baslik, surum string, moduller []module.Module) *openapi.Doc {
	doc := openapi.New(baslik, surum)

	for _, mod := range moduller {
		anlatici, anlatabilir := mod.(openapi.Describer)
		if !anlatabilir {
			continue
		}

		anlatici.Describe(doc)
	}

	return doc
}

// akislariKaydet modüller arası akışları kurar ve container'a bırakır.
//
// # Neden BURADA ve neden Bootstrap'tan SONRA
//
// Akışlar hiçbir modülün içinde kurulamaz: her biri BİRDEN ÇOK modülün
// yüzeyini container'dan adla çözer (sepet hesabı altı, sipariş tamamlama yedi
// yüzey) ve o yüzeyler ancak Register döngüsünün TAMAMI bittiğinde kayıtlıdır.
// Bir modülün Register'ında kurulmaya çalışılsalardı, kayıt sırasına bağlı
// olarak bazen çalışan bazen çalışmayan bir kurulum elde edilirdi.
//
// Ters yön de doğrudur: akışların HTTP uçlarının sahibi MODÜLDÜR (cart), yani
// handler akışa ihtiyaç duyar ve handler Register sırasında kurulur. Daire bu
// yüzden iki yerden birden kırılır — kayıt burada, ÇÖZÜM ise modül tarafında
// ilk isteğe ertelenmiş olarak yapılır (bkz. cart modülündeki linePricing).
// Bileşim köküne handler kodu girmez; buraya giren tek şey KİMİN KURULACAĞI
// kararıdır.
//
// # Neden açılışı DURDURUYOR
//
// Kurulamayan bir akış, sepete satır ekleyemeyen ve siparişe çevrilemeyen bir
// mağaza demektir; ayakta ama satış yapamayan bir sunucu, açılışta duran bir
// sunucudan çok daha geç fark edilir. Hata mesajı hangi adın çözülemediğini
// yazar (bkz. cartwf.FromContainer).
func akislariKaydet(c *container.Container) error {
	sepetAkislari, err := cartwf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"sepet akışları kurulamadı")
	}
	if err := c.Provide(cartwf.InteropName, cartwf.NewInterop(sepetAkislari)); err != nil {
		return err
	}

	// Sipariş tamamlama akışı sepet hesabını KENDİ kurar (aynı container
	// üzerinde, bkz. checkoutwf.FromContainer); yukarıdaki örnekle
	// paylaşılmaz ve paylaşılmaması bilinçlidir — akış kendi bağımlılık
	// kümesini kendi çözer, biz ona bir nesne enjekte etmeyiz.
	siparisAkisi, err := checkoutwf.FromContainer(c)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeFlowSetupFailed,
			"sipariş tamamlama akışı kurulamadı")
	}
	return c.Provide(checkoutwf.InteropName, checkoutwf.NewInterop(siparisAkisi))
}

// semayiDenetle belgeyi açılışta BİR KEZ üretip ayrışmaları raporlar.
//
// Belge ÖNBELLEKLENİR ve önbellek route ağacı ya da anlatım sürümü
// değiştiğinde kendiliğinden tazelenir (bkz. [openapi.Doc.Handler]); buradaki
// üretim yalnızca denetim içindir ve ilk isteği de ucuzlatır.
// Denetim olmadan iki arıza da SESSİZ kalırdı: yolu değişmiş bir
// route'un açıklaması belgeden düşer, iki modülün aynı adlı DTO'su ise
// belgeyi tümden üretilemez kılar — ikisi de ancak biri /openapi.json'ı
// açtığında görülürdü.
//
// Açılış DURMAZ (ADR 0007'nin ayrımı): şema belgedir, ürünün doğruluğu değil.
// Yanlış bir şema hiçbir siparişi bozmaz; mağazayı belge hatası yüzünden
// kapatmak, arızanın bedelinden büyük bir bedel ödemek olurdu.
func semayiDenetle(ctx context.Context, doc *openapi.Doc, r chi.Routes, log *slog.Logger) {
	_, err := doc.Build(r)

	if eksik := doc.UnmatchedDescriptions(); len(eksik) > 0 {
		log.WarnContext(ctx, "openapi: hiçbir route ile eşleşmeyen açıklama var",
			"kayitlar", eksik,
			"anlami", "route'un yolu değişmiş ya da silinmiş olabilir; açıklama belgeye girmiyor")
	}

	if err != nil {
		log.ErrorContext(ctx, "openapi şeması üretilemedi; /openapi.json 500 dönecek",
			"error", err)
	}
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
//
// Anahtar ad alanı öneki (REDIS_KEY_PREFIX) de buradan geçer ve AYNI Redis'i
// paylaşan iki kurulumu ayıran tek şeydir; gerekçesi [config.Config]
// RedisKeyPrefix alanındadır. Önek loglanır: iki kurulumun aynı ad alanına
// düştüğü, ancak iki açılış logu yan yana konduğunda görülebilecek bir
// arızadır ve yanlışın sonucu (birinin yanıtının ötekine gitmesi) sessizdir.
//
// Hız sınırının SESSİZ kalan iki hâli de burada bildirilir; bkz.
// [hizSiniriniUyar].
func korumaYigini(
	cfg config.Config,
	authn corehttp.Authenticator,
	rdb *redis.Client,
	log *slog.Logger,
) ([]func(http.Handler) http.Handler, error) {
	hizSiniriniUyar(cfg, log)

	opts := corehttp.GuardOptions{
		Authenticator: authn,
		AdminPrefix:   adminPrefix,
		StorePrefix:   storePrefix,
		// Giriş ucu korumadan MUAFTIR: kimliği doğrulanacak istek, kimliği
		// daha yeni kuracaktır. Yol elle yazılmaz, auth modülünün sabitinden
		// okunur.
		AdminExempt: []string{authapi.LoginPath},
		// Yüklenen dosyalar KİMLİKSİZ sunulur (vitrindeki <img> başlık
		// gönderemez) ama kotasız DEĞİLDİR: her istek bir veritabanı okuması
		// ve bir disk erişimi yapar. Önek elle yazılmaz, sağlayıcının
		// sabitinden okunur.
		//
		// /openapi.json aynı sınıftadır ve aynı sebeple buradadır: istemci
		// bir kod üreteci ya da IDE'dir, başlık göndermez — ama uç bedava
		// değildir. Belge önbelleklense bile her istek route ağacını gezip
		// önbelleğin hâlâ geçerli olduğunu doğrular, ağaç değiştiğinde ise
		// tüm modüllerin DTO'ları yansımayla yeniden çevrilir. Kimlik ve kota
		// AYRI kararlardır; bu uç için verilen karar "kimliksiz ama kotalı".
		OpenPrefixes: []string{filelocal.DefaultURLPrefix, openAPIPath},
		// GraphQL vitrin ucu POST'tur ama bir OKUMADIR; idempotency kaydının
		// koruyacağı bir yan etki yoktur ve GraphQL sözleşmesi gereği iç
		// hatada bile 200 döndüğü için kayıt, geçici bir arızayı TTL boyunca
		// çalardı. Gerekçenin tamamı
		// [corehttp.GuardOptions.IdempotencyExempt] alanındadır; yol elle
		// yazılmaz, modülün sabitinden okunur.
		IdempotencyExempt: []string{graph.Path},
	}

	if cfg.GuardBackend == config.BackendRedis {
		if rdb == nil {
			return nil, errors.Invalid(codeGuardBackendMissing,
				"GUARD_BACKEND=%s seçildi ama Redis istemcisi yok", config.BackendRedis)
		}

		depo, err := redisguard.NewIdempotencyStore(rdb, cfg.RedisKeyPrefix, cfg.IdempotencyTTL)
		if err != nil {
			return nil, err
		}
		opts.IdempotencyStore = depo

		// Sınır kapalıysa (limit <= 0) sınırlayıcı hiç kurulmaz; kurulsaydı
		// "0 istek" gibi davranıp tüm trafiği keserdi.
		if cfg.RateLimitPerMinute > 0 {
			limiter, limitErr := redisguard.NewLimiter(rdb, cfg.RedisKeyPrefix, cfg.RateLimitPerMinute, time.Minute)
			if limitErr != nil {
				return nil, limitErr
			}

			opts.Limiter = limiter
			opts.LimitKey = corehttp.TrustedProxyIPKey(cfg.TrustedProxyHops)
		}

		log.Info("koruma arka ucu: redis (paylaşılan)",
			"anahtar_oneki", cfg.RedisKeyPrefix)

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

// hizSiniriniUyar hız sınırının sessiz kalan iki hâlini bildirir.
//
// İkisi de yapılandırmadan doğar, ikisi de bugüne kadar TEK SATIR iz
// bırakmıyordu ve ikisi de ancak yük altında — yani en pahalı anda —
// görülürdü:
//
//  1. RATE_LIMIT_PER_MINUTE <= 0 iken sınırlayıcı HİÇ kurulmaz (ADR 0007'de
//     sıfır "kapat" demektir, "0 istek" değil). Meşru bir seçimdir ama
//     paylaşılan bir ortamda giriş ucunu da korumasız bırakır: parola deneyen
//     bir saldırgan kotasız çalışır. Kimsenin bilmediği bir "kapalı", kazayla
//     yazılmış bir sıfırdan ayırt edilemez; log ikisini de görünür kılar.
//  2. Sınır AÇIKKEN kotanın istemci başına DÜŞMEDİĞİ hâl. Gerekçesi ve
//     varsayılanın neden değişmediği [config.Config.RateLimitKeyIsPerClient]
//     godoc'undadır.
//
// Yerel geliştirmede ikisi de sessizdir ya da INFO'dur: orada tek örnek
// çalışır, ters proxy yoktur ve her açılışta uyarı basmak gerçek bir uyarıyı
// gürültüde boğardı. Aynı kapı [dosyaKokunuUyar] ve bellek içi koruma
// uyarısında da açıktır.
func hizSiniriniUyar(cfg config.Config, log *slog.Logger) {
	if cfg.RateLimitPerMinute <= 0 {
		if !cfg.IsShared() {
			log.Info("hız sınırlayıcı takılmadı",
				"sebep", "RATE_LIMIT_PER_MINUTE <= 0")

			return
		}

		log.Warn("hız sınırlayıcı TAKILMADI",
			"rate_limit_per_minute", cfg.RateLimitPerMinute,
			"uyari", "hiçbir uç için kota uygulanmaz; giriş ucu da (POST /admin/v1/auth/login) "+
				"sınırsız denemeye açıktır",
			"cozum", "kapatmak bilinçli değilse RATE_LIMIT_PER_MINUTE'a pozitif bir değer verin")

		return
	}

	if !cfg.IsShared() || cfg.RateLimitKeyIsPerClient() {
		return
	}

	log.Warn("hız sınırı anahtarı istemciye DEĞİL bağlantıya düşüyor",
		"trusted_proxy_hops", cfg.TrustedProxyHops,
		"rate_limit_per_minute", cfg.RateLimitPerMinute,
		"uyari", "X-Forwarded-For hiç okunmaz; ters proxy, ingress ya da CDN arkasında her isteğin "+
			"kaynağı proxy'nin IP'sidir, yani kota müşteri başına değil TÜM MAĞAZA için tek bir "+
			"kovadır ve tek müşteri vitrini kilitleyebilir",
		"cozum", "güvendiğiniz ters proxy sayısını TRUSTED_PROXY_HOPS ile verin; doğrudan internete "+
			"bakan bir kurulumda 0 DOĞRUDUR ve bu uyarı yok sayılmalıdır")
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

// bildirimSaglayicilari kurulumun bildirim sağlayıcı kaydından istediği DAR
// yüzeydir.
//
// Somut kayıt tipi yerine iki metotluk bir arayüz kullanılır: kurulum,
// notification modülünün servis yüzeyine değil yalnızca burada sayılan iki
// çağrıya bağlanır ve denetim, modülün tamamı ayağa kaldırılmadan sahte bir
// kayıtla sınanabilir.
type bildirimSaglayicilari interface {
	Get(id string) (coreprovider.NotificationProvider, error)
	IDs() []string
}

// Gerçek kaydın bu dar yüzeyi karşıladığı DERLEME zamanında sabitlenir.
//
// Uyum çalışma zamanında container.Resolve'un tip iddiasıyla denetlenir ve
// orada kayması, açılışın "kayıt beklenen yüzeyi karşılamıyor" diyerek
// durması demektir — yani en geç fark edilecek yerde. Bu satır aynı soruyu
// derleyiciye sorar; notification modülünü import etmek kompozisyon kökü için
// zaten serbesttir (bkz. [yoneticiKullanicilari]).
var _ bildirimSaglayicilari = (*notificationservice.ProviderRegistry)(nil)

// bildirimSaglayicisiniDogrula seçili sağlayıcının GERÇEKTEN kayıtlı
// olduğunu doğrular.
//
// # Neden burada, config'te değil
//
// Geçerli adları config bilemez: sağlayıcılar eklentilerden gelir ve eklenti
// listesi derleme zamanında belirlenir. Aynı ayrım PLUGINS için de geçerlidir
// (bkz. [eklentileriSec]) — biçimi config, ANLAMI kompozisyon kökü doğrular.
//
// # Neden açılış DURUR
//
// Alternatif — bilinmeyen adı yok sayıp varsayılan "log" sağlayıcısına düşmek —
// tam olarak kaçınılması gereken şeydir: kurulum açılır, hiçbir hata görünmez
// ve sipariş onayları yalnızca loga yazılır. Arıza, müşteriler onay beklerken
// ve genellikle günler sonra fark edilir. Açılışta durmak, arızayı
// yapılandırmanın hâlâ elde olduğu ana taşır.
//
// # Neden bu adım Start'tan SONRA
//
// Eklentilerin sağlayıcı kayıtları [coreplugin.Registry.Start] sırasında
// uygulanır; daha erken bir denetim, eklentiden gelen geçerli bir adı
// "bilinmiyor" diye reddederdi.
func bildirimSaglayicisiniDogrula(c *container.Container, id string) error {
	kayit, err := container.Resolve[bildirimSaglayicilari](c, notification.ProvidersName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeUnknownNotificationProvider,
			"bildirim sağlayıcı kaydı %q çözülemedi", notification.ProvidersName)
	}

	if _, err := kayit.Get(id); err != nil {
		return errors.Wrap(err, errors.KindInvalid, codeUnknownNotificationProvider,
			"NOTIFICATION_PROVIDER=%q kayıtlı bir bildirim sağlayıcısı değil (kayıtlı olanlar: %s); "+
				"eklenti getiriyorsa PLUGINS listesine eklenmiş mi?",
			id, strings.Join(kayit.IDs(), ", "))
	}

	return nil
}

// dosyaSaglayicilari kurulumun dosya sağlayıcı kaydından istediği DAR
// yüzeydir.
//
// Gerekçe [bildirimSaglayicilari] ile birebir aynıdır ve tekrarlanmıyor. İki
// arayüzün tek bir jenerik tiple birleştirilmesi denenmedi çünkü kazancı
// yalnızca iki satırlık bir tanımdır; karşılığında container.Resolve çağrısı
// jenerik bir arayüz tipiyle yazılır ve tip uyuşmazlığı hatasının okunması
// zorlaşırdı — oysa bu kodun tek işi teşhis edilebilir bir hata üretmek.
type dosyaSaglayicilari interface {
	Get(id string) (coreprovider.FileProvider, error)
	IDs() []string
}

// Gerçek kaydın bu dar yüzeyi karşıladığı DERLEME zamanında sabitlenir.
var _ dosyaSaglayicilari = (*fileservice.ProviderRegistry)(nil)

// dosyaSaglayicisiniDogrula seçili dosya sağlayıcısının GERÇEKTEN kayıtlı
// olduğunu doğrular.
//
// Denetimin neden config'te değil burada, ve neden [coreplugin.Registry.Start]
// SONRASINDA olduğu [bildirimSaglayicisiniDogrula] godoc'unda yazılıdır.
//
// # Neden açılış DURUR
//
// Bedeli bildirimdekinden FARKLIDIR ve daha erken görünür: bilinmeyen bir ad
// yok sayılıp varsayılana düşülseydi, kurulum "local" sağlayıcısıyla açılır ve
// nesne deposuna gittiğini sanan bir kurulum dosyaları YEREL DİSKE yazardı.
// Kap yeniden başlatıldığında o dosyalar gider; kayıtlar ve ürün görseli
// adresleri ise yerinde kalır. Yani hata, en pahalı hâliyle — veri kaybı
// olarak — ortaya çıkardı.
//
// Ters yön de aynı derecede kötüdür: kök dizini verilmediği için "local"
// KAYDEDİLMEMİŞ bir kurulumda (bkz. file.Options.Root) bu denetim, yükleme
// ucunun her isteği reddedeceğini açılışta söyler.
func dosyaSaglayicisiniDogrula(c *container.Container, id string) error {
	kayit, err := container.Resolve[dosyaSaglayicilari](c, file.ProvidersName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeUnknownFileProvider,
			"dosya sağlayıcı kaydı %q çözülemedi", file.ProvidersName)
	}

	if _, err := kayit.Get(id); err != nil {
		return errors.Wrap(err, errors.KindInvalid, codeUnknownFileProvider,
			"FILE_PROVIDER=%q kayıtlı bir dosya sağlayıcısı değil (kayıtlı olanlar: %s); "+
				"eklenti getiriyorsa PLUGINS listesine eklenmiş mi, yerel sağlayıcı için FILE_ROOT verilmiş mi?",
			id, strings.Join(kayit.IDs(), ", "))
	}

	return nil
}

// dosyaKokunuUyar kalıcı olmayan bir yerel kök dizini için uyarı loglar.
//
// Uyarının açılışı DURDURMAMASININ gerekçesi ve "kalıcı" ölçütünün ne olduğu
// [config.Config.LocalFileRootIsDurable] godoc'undadır. Buradaki tek iş,
// riskin görünür kalmasıdır: göreli ya da geçici bir kökle çıkılan bir üretim
// dağıtımı, bu satır olmadan hiçbir iz bırakmaz ve arıza ancak ilk yeniden
// dağıtımdan sonra — görseller kaybolduğunda — fark edilir.
//
// Mesaj İKİ sebebi birden sayar çünkü ölçüt ikisini birden kapsar; kökün
// kendisi loglandığı için operatör hangisine düştüğünü tek bakışta görür.
//
// Yerel geliştirmede SUSAR: orada göreli kök doğru olandır ve her açılışta
// uyarı basmak, gerçek bir uyarıyı gürültüde boğardı.
func dosyaKokunuUyar(cfg config.Config, log *slog.Logger) {
	if !cfg.IsShared() || cfg.LocalFileRootIsDurable() {
		return
	}

	log.Warn("dosya kök dizini KALICI DEĞİL",
		"kok", cfg.FileRoot,
		"uyari", "göreli bir yol sürecin ÇALIŞMA DİZİNİNE göre çözülür, geçici bir kök (/tmp, "+
			"/var/tmp, /dev/shm ya da TMPDIR) ise işletim sistemi tarafından temizlenir; iki hâlde de "+
			"yeniden dağıtımda yüklenen dosyalar kaybolur (adresler kayıtlarda kalır)",
		"cozum", "FILE_ROOT olarak bağlanmış KALICI bir birimin mutlak yolunu verin")
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
	// Kullanıcı sayısı HER HÂLDE okunur. İki farklı soruyu da o yanıtlar: tohum
	// yapılandırılmışsa "ikinci kez yaratmalı mıyım", yapılandırılmamışsa "bu
	// kurulum yönetilebilir mi". İkincisi eskiden hiç sorulmuyordu ve cevabı
	// "hayır" olan bir kurulum sessizce açılıyordu.
	//
	// Sayfa boyu 1'dir: burada gereken tek bilgi "hiç kullanıcı var mı",
	// listenin kendisi değil. Page.Count süzgece uyan TOPLAMI verir, sayfadaki
	// kayıt sayısını değil.
	sayfa, err := kullanicilar.ListUsers(ctx, authservice.ListUsersInput{Limit: 1})
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"ilk yönetici tohumu için kullanıcı sayısı okunamadı")
	}

	// config.Validate ikisinin BİRLİKTE verilmesini zorlar; buraya yalnızca
	// "hiç verilmemiş" hâli düşer. Yine de iki alan da denetlenir: doğrulamadan
	// geçmemiş elle kurulmuş bir Config yarım girdiyle tohum çalıştıramamalı.
	if cfg.AdminBootstrapEmail == "" || cfg.AdminBootstrapPassword == "" {
		return yonetimsizKurulumuBildir(ctx, cfg, sayfa.Count, log)
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

	switch {
	// Çakışma bir ARIZA DEĞİL, YARIŞTIR. Birden çok örnek boş bir veritabanına
	// aynı anda açıldığında hepsi "hiç kullanıcı yok" görür ve hepsi yaratmayı
	// dener; e-posta benzersizliği birinden fazlasını reddeder.
	//
	// Bunu hata sayıp açılışı durdurmak, üç kopyalı ilk dağıtımda ikisinin
	// yeniden başlatma döngüsüne girmesi demekti — kendini onaran ama bozuk
	// görünen bir dağıtım. İstenen son durum ("bir yönetici var") kaybeden
	// örnekler için de sağlanmıştır; yapılacak tek doğru şey devam etmektir.
	//
	// Yalnızca ÇAKIŞMA yutulur: bağlantı hatası ya da geçersiz parola hâlâ
	// açılışı durdurur, çünkü onlarda istenen son durum sağlanmamıştır.
	case errors.IsConflict(err):
		log.InfoContext(ctx, "ilk yönetici tohumu atlandı: başka bir örnek aynı anda oluşturdu")

		return nil
	case err != nil:
		return errors.Wrap(err, errors.KindOf(err), codeBootstrapFailed,
			"ilk yönetici oluşturulamadı")
	}

	log.InfoContext(ctx, "ilk yönetici oluşturuldu", slog.String("user_id", kullanici.ID))

	return nil
}

// yonetimsizKurulumuBildir tohum yapılandırılmamışken kurulumun yönetilebilir
// olduğunu denetler.
//
// # Hangi arıza
//
// Taze bir veritabanı + boş ADMIN_BOOTSTRAP_* ikilisi config.Validate'ten
// GEÇER, çünkü ikisini birden boş bırakmak KURULMUŞ bir sistem için meşru bir
// seçimdir (bkz. [config.Config.AdminBootstrapEmail]) ve "kurulmuş mu"
// sorusunu doğrulama göremez. Veritabanı da boşsa sonuç yönetilemez bir
// kurulumdur: hiç kullanıcı yoktur, /admin/v1 giriş ucu dışında tamamen
// korumalıdır ve ilk kullanıcıyı HTTP'den yaratmanın YOLU YOKTUR. Mağaza
// yüzeyi de kapalıdır, çünkü publishable anahtarı da yönetim ucu üretir.
//
// Sunucu yine de sorunsuz açılır: /health ve /ready yeşil döner, tüm route'lar
// mount edilmiştir, hiçbir log satırı eksik bir şey söylemez. Arıza ilk giriş
// denemesinde görülür.
//
// # Neden paylaşılan ortamda DURDURUYOR
//
// Burada belirsizlik YOKTUR ve ölçüt budur:
// [config.Config.LocalFileRootIsDurable] uyarıyla yetinir çünkü yapılandırmanın
// yanlış olduğu kesin değildir; sıfır kullanıcılı bir kurulumun yönetilemez
// olduğu ise kesindir. Aynı kesinlik main.go'da kimlik doğrulayıcı
// çözülemediğinde de açılışı durdurur ve gerekçe birebir aynıdır: korumalı
// görünen ama hiçbir yönetim isteğini kabul edemeyen bir yüzeyle çalışmaya
// devam etmek, arızayı ilk giriş denemesine — çoğu zaman kurulumdan günler
// sonrasına — saklar. O noktada düzeltmenin yolu da yapılandırma değil, üretim
// veritabanına elle SQL'dir.
//
// # Neden geliştirmede DURDURMUYOR
//
// Deponun sözü ".env olmadan da make up && make run çalışır"dır ve taze bir
// veritabanıyla ilk kez açan geliştirici tam olarak bu hâle düşer. Orada bedel
// yok denecek kadar azdır: uyarıyı yazan terminalin başında oturan kişidir ve
// iki ortam değişkeniyle saniyeler içinde yeniden açar. Ayrım JWT_SECRET'inkiyle
// aynıdır — geliştirmede uyarı, paylaşılan ortamda ret.
func yonetimsizKurulumuBildir(
	ctx context.Context,
	cfg config.Config,
	kullaniciSayisi int64,
	log *slog.Logger,
) error {
	if kullaniciSayisi > 0 {
		return nil
	}

	if cfg.IsShared() {
		return errors.Invalid(codeAdminBootstrapRequired,
			"kurulumda hiç kullanıcı yok ve ADMIN_BOOTSTRAP_EMAIL/ADMIN_BOOTSTRAP_PASSWORD verilmedi: "+
				"yönetim yüzeyi giriş ucu dışında tamamen korumalı olduğu için ilk yöneticiyi "+
				"HTTP'den yaratmanın yolu yoktur (APP_ENV=%s)", cfg.AppEnv)
	}

	log.WarnContext(ctx, "kurulumda hiç kullanıcı yok",
		"uyari", "yönetim yüzeyi giriş ucu dışında tamamen korumalıdır ve ilk yöneticiyi HTTP'den "+
			"yaratmanın yolu yoktur; mağaza yüzeyi de kapalıdır, çünkü publishable anahtarı da "+
			"yönetim ucu üretir",
		"cozum", "ADMIN_BOOTSTRAP_EMAIL ve ADMIN_BOOTSTRAP_PASSWORD verip yeniden başlatın")

	return nil
}
