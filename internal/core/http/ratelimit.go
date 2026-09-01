package http

import (
	"context"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// CodeRateLimited hız sınırı aşıldığında dönen makine okunur hata kodudur.
const CodeRateLimited = "rate_limited"

// forwardedForHeader ters proxy'lerin istemci IP zincirini taşıdığı başlıktır.
const forwardedForHeader = "X-Forwarded-For"

// gcInterval bellek içi kovaların temizlenme sıklığıdır.
//
// Temizlik olmazsa her yeni IP kalıcı bir kova bırakır ve bellek sınırsız
// büyür; bu, hız sınırlayıcının kendisini bir DoS vektörüne çevirir.
const gcInterval = time.Minute

// Decision tek bir hız sınırı sorgusunun sonucudur.
type Decision struct {
	// Allowed isteğin geçirilip geçirilmeyeceğini bildirir.
	Allowed bool
	// Limit pencere başına izin verilen toplam istek sayısıdır.
	Limit int
	// Remaining bu pencerede kalan istek hakkıdır; negatif olmaz.
	Remaining int
	// RetryAfter yeniden denemeden önce beklenmesi gereken süredir.
	// Allowed true iken anlamlı değildir.
	RetryAfter time.Duration
}

// RateLimiter bir anahtarın kotasını tüketmeye çalışır.
//
// Uygulamalar eşzamanlı çağrıya güvenli olmalıdır. Hata dönerse middleware
// isteği GEÇİRİR: sınırlayıcının arızası (örn. Redis erişilemez) tüm trafiği
// kesmemelidir. Bu bilinçli bir "fail-open" tercihidir ve karşılığı, arıza
// penceresinde sınırın uygulanmamasıdır.
type RateLimiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}

// KeyFunc bir isteği hız sınırı anahtarına çevirir.
//
// Boş dize dönerse istek sınırlanmaz.
//
// Anahtar, isteğin KENDİSİNDEN türetilebilecek şeylerle sınırlıdır. Bu
// middleware koruma yığınında kimlik doğrulamadan ÖNCE koşar (gerekçesi
// [APIGuards]'ın sıra bölümünde) ve o noktada context'te henüz [Principal]
// yoktur: çağıranın kimliğine bakan bir KeyFunc her istekte IP'ye düşer,
// üstelik [TrustedProxyIPKey]'in proxy düzeltmesini de kaybederek. Böyle bir
// anahtar hiçbir şeyi bölmez, yalnızca bölüyormuş gibi görünür.
//
// Çağıranın kimliğine göre ad alanı ayıran halka, yığında kimlikten SONRA
// koşan tek halkadır: idempotency (bkz. [Idempotency]).
type KeyFunc func(r *http.Request) string

// RateLimit istekleri anahtar başına sınırlayan middleware üretir.
//
// limiter nil ise middleware bir no-op'tur: hız sınırı ürünün doğruluğu için
// değil, kötüye kullanıma karşı vardır. Yapılandırılmamış bir sınırlayıcı
// yüzünden tüm trafiği reddetmek, korumaya çalıştığı servisi çökertmek olurdu.
// Bu, [RequireAdmin]'in nil kimlik doğrulayıcıda HER İSTEĞİ reddetmesinin
// tam tersidir; ikisi de kendi başarısızlık modeli için doğrudur.
//
// keyFunc nil ise [ClientIPKey] kullanılır.
func RateLimit(limiter RateLimiter, keyFunc KeyFunc) func(http.Handler) http.Handler {
	if keyFunc == nil {
		keyFunc = ClientIPKey
	}

	return func(next http.Handler) http.Handler {
		if limiter == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			if key == "" {
				next.ServeHTTP(w, r)
				return
			}

			d, err := limiter.Allow(r.Context(), key)
			if err != nil {
				// Fail-open: sınırlayıcı arızasını logla ama trafiği kesme.
				LoggerFromContext(r.Context()).WarnContext(r.Context(),
					"hız sınırlayıcı sorgulanamadı, istek geçiriliyor",
					"error", err)
				next.ServeHTTP(w, r)
				return
			}

			writeRateLimitHeaders(w, d)
			if !d.Allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
				WriteError(r.Context(), w, coreerrors.TooManyRequests(
					CodeRateLimited, "istek sınırı aşıldı, lütfen sonra tekrar deneyin"))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitHeaders kota durumunu yanıt başlıklarına yazar.
//
// Başlık adları RFC 9331 taslağındaki RateLimit-* ailesini izler; istemcinin
// sınıra çarpmadan önce yavaşlayabilmesi için başarılı yanıtlarda da yazılır.
func writeRateLimitHeaders(w http.ResponseWriter, d Decision) {
	if d.Limit <= 0 {
		return
	}

	w.Header().Set("RateLimit-Limit", strconv.Itoa(d.Limit))
	w.Header().Set("RateLimit-Remaining", strconv.Itoa(max(d.Remaining, 0)))
	w.Header().Set("RateLimit-Reset", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
}

// retryAfterSeconds süreyi yukarı yuvarlayarak saniyeye çevirir.
//
// Aşağı yuvarlamak, istemcinin kota dolmadan tekrar denemesine ve ikinci bir
// 429 almasına yol açardı. En az 1 döner: "0 saniye bekle" bir bekleme değildir.
func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}

	return max(int(math.Ceil(d.Seconds())), 1)
}

// ClientIPKey isteği istemcinin ağ adresine göre anahtarlar.
//
// YALNIZCA r.RemoteAddr'a bakar; X-Forwarded-For gibi başlıklara BAKMAZ.
// O başlıkları istemci uydurabilir; her istekte farklı bir değer göndermek
// sınırı tamamen etkisiz kılardı. Ters proxy arkasındaysanız
// [TrustedProxyIPKey] kullanın ve güvendiğiniz atlama sayısını verin.
func ClientIPKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Port yoksa RemoteAddr'ın kendisi adrestir.
		return strings.TrimSpace(r.RemoteAddr)
	}

	return host
}

// TrustedProxyIPKey X-Forwarded-For zincirinden istemci IP'sini çıkarır.
//
// hops, istekle aramızdaki GÜVENİLEN ters proxy sayısıdır. Zincir soldan sağa
// "istemci, proxy1, proxy2, ..." diye büyür: her proxy kendisine bağlanan
// tarafın adresini SONA ekler. Dolayısıyla en sağdaki girdi bize en yakın
// güvenilen proxy'nin yazdığıdır ve hops güvenilen atlama varken doğrulanmış
// en soldaki adres SAĞDAN hops. girdidir: parts[len-hops].
//
// Bir eleman daha sola kaymak (parts[len-hops-1]) doğrulanmış zincirin dışına
// çıkar ve istemcinin başlığa kendi elleriyle yazdığı girdiyi seçerdi; saldırgan
// her istekte farklı bir sahte girdi göndererek taze kova alır ve hız sınırını
// tamamen atlardı. Bu yüzden indeks tam olarak len-hops'tur, bir eksiği değil.
//
// hops sıfır ya da negatifse başlık hiç okunmaz ve [ClientIPKey]'e düşülür;
// "proxy arkasında değilim" durumunun güvenli karşılığı budur.
//
// Zincir hops'u karşılayacak kadar uzun değilse yine [ClientIPKey]'e düşülür:
// beklenenden kısa bir zincir, ya yapılandırma yanlıştır ya da istek proxy'yi
// atlayarak gelmiştir. Kısa zincirde istemcinin verdiği İLK girdiye düşmek,
// anahtarı istemciye seçtirmek olurdu — o yüzden RemoteAddr'a dönülür.
func TrustedProxyIPKey(hops int) KeyFunc {
	return func(r *http.Request) string {
		if hops <= 0 {
			return ClientIPKey(r)
		}

		ham := r.Header.Get(forwardedForHeader)
		if ham == "" {
			// strings.Split boş dizeden TEK elemanlı dilim üretir; erken dönmezsek
			// "hiç girdi yok" durumu bir girdilik zincir gibi sayılırdı.
			return ClientIPKey(r)
		}

		parts := strings.Split(ham, ",")

		// Sağdan hops. girdi: güvenilen proxy'lerin yazdığı en soldaki adres.
		idx := len(parts) - hops
		if idx < 0 {
			return ClientIPKey(r)
		}

		ip := strings.TrimSpace(parts[idx])
		if net.ParseIP(ip) == nil {
			return ClientIPKey(r)
		}

		return ip
	}
}

// bucket tek bir anahtarın jeton kovasıdır.
type bucket struct {
	// tokens kalan jeton sayısıdır; kesirli birikimi koruduğu için float'tır.
	tokens float64
	// last kovanın en son yenilendiği andır.
	last time.Time
}

// MemoryLimiter süreç belleğinde çalışan jeton kovası sınırlayıcısıdır.
//
// Tek örnekli kurulumlar ve testler içindir. Yatay ölçeklenen bir dağıtımda
// her örnek kendi kotasını sayar; yani gerçek sınır örnek sayısıyla çarpılır.
// Çok örnekli kurulum için paylaşılan bir sayaç (Redis) gerekir.
type MemoryLimiter struct {
	// limit pencere başına izin verilen istek sayısıdır.
	limit int
	// window kotanın tamamen yenilendiği süredir.
	window time.Duration
	// refill saniye başına eklenen jeton sayısıdır.
	refill float64
	// now zamanı okur; testler saati ilerletebilsin diye alandır.
	now func() time.Time

	mu sync.Mutex
	// buckets anahtar başına kovadır; gcAt geldiğinde ölü kovalar atılır.
	buckets map[string]*bucket
	gcAt    time.Time
}

// NewMemoryLimiter window süresinde limit isteğe izin veren sınırlayıcı kurar.
//
// limit ya da window sıfır/negatifse nil döner: "sınırsız" bir sınırlayıcı,
// çağıranın onu hiç takmamasıyla aynı şeydir ve [RateLimit] nil'i zaten
// no-op olarak ele alır. Böylece "0 limit" yanlışlıkla "her şeyi reddet"e
// dönüşmez.
func NewMemoryLimiter(limit int, window time.Duration) *MemoryLimiter {
	if limit <= 0 || window <= 0 {
		return nil
	}

	return &MemoryLimiter{
		limit:   limit,
		window:  window,
		refill:  float64(limit) / window.Seconds(),
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Allow anahtarın kotasından bir jeton düşmeye çalışır.
//
// Hiçbir zaman hata dönmez; imza [RateLimiter] arayüzüne uymak içindir.
func (l *MemoryLimiter) Allow(_ context.Context, key string) (Decision, error) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.collect(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.limit), last: now}
		l.buckets[key] = b
	}

	// Geçen süre kadar jeton ekle; kova taşmasın.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(float64(l.limit), b.tokens+elapsed*l.refill)
		b.last = now
	}

	if b.tokens < 1 {
		// Bir jetonun birikmesi için gereken süre.
		eksik := 1 - b.tokens
		return Decision{
			Limit:      l.limit,
			Remaining:  0,
			RetryAfter: time.Duration(eksik / l.refill * float64(time.Second)),
		}, nil
	}

	b.tokens--

	return Decision{
		Allowed:    true,
		Limit:      l.limit,
		Remaining:  int(b.tokens),
		RetryAfter: l.window,
	}, nil
}

// collect dolmuş kovaları siler. Çağıran l.mu'yu tutuyor olmalıdır.
//
// Bir kova ancak jetonu tamamen dolduktan sonra silinir; erken silmek,
// sınıra çarpmış bir istemcinin anahtarını unutup kotasını sıfırlardı.
func (l *MemoryLimiter) collect(now time.Time) {
	if now.Before(l.gcAt) {
		return
	}

	l.gcAt = now.Add(gcInterval)

	for k, b := range l.buckets {
		if now.Sub(b.last) >= l.window {
			delete(l.buckets, k)
		}
	}
}
