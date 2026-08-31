package redisguard

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// limitBetigi sayacı artırır ve pencerenin kalan süresini döner.
//
// Neden Lua: INCR ve PEXPIRE AYRI komut olarak gönderilirse aralarında bir şey
// ters gidebilir (süreç ölür, bağlantı kopar, context iptal olur) ve geriye
// TTL'siz, yani SÜRESİZ yaşayan bir sayaç kalır. O sayaç bir daha hiç
// sıfırlanmaz: anahtar sonsuza dek bloke olur ve elle silinene kadar o istemci
// hiçbir istek yapamaz. EVAL sunucuda tek parça çalışır; araya girilebilecek
// bir an yoktur.
//
// Neden MULTI/EXEC değil: o da atomiktir ama komutları ARA SONUÇLARI GÖRMEDEN
// sıraya dizer. PEXPIRE'ı "sayaç 1'e eşitse" koşuluna bağlayamayacağımız için
// TTL'i her istekte yenilemek zorunda kalırdık; bu, sabit pencereyi hiç
// sıfırlanmayan kayan bir pencereye çevirir ve sınıra çarpmış bir istemci
// istek göndermeyi sürdürdüğü sürece SONSUZA DEK bloke kalır. Betik INCR'ın
// sonucunu gördüğü için TTL'i yalnızca pencerenin ilk isteğinde kurar.
//
// PTTL'in negatif dönmesi beklenmez (sayaç her zaman TTL'le doğar); yine de
// TTL'siz bir anahtar bulunursa onarılır. Onarmazsak yukarıdaki "sonsuza dek
// bloke" durumu, bu kez elle SET edilmiş ya da eski bir sürümün bıraktığı bir
// anahtar üzerinden geri gelirdi.
var limitBetigi = redis.NewScript(`
local sayac = redis.call('INCR', KEYS[1])
if sayac == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local kalan = redis.call('PTTL', KEYS[1])
if kalan < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
  kalan = tonumber(ARGV[1])
end
return {sayac, kalan}
`)

// Limiter Redis üzerinde SABİT PENCERE sayan hız sınırlayıcıdır.
//
// Anahtar başına tek bir sayaç tutulur; sayaç ilk istekte doğar, pencere
// süresi kadar yaşar ve süresi dolunca yok olur. Kota, sayaç yok olduğu anda
// bir kerede yenilenir.
//
// # corehttp.MemoryLimiter'dan farkı
//
// Bellek içi sürüm JETON KOVASIDIR: jetonlar sürekli ve eşit hızda birikir,
// yani kota pencere boyunca yumuşakça açılır. Sabit pencerede ise kota
// pencerenin başında topluca açılır. Pratik farkı PENCERE SINIRINDA görülür:
// bir istemci N. pencerenin sonunda limit kadar, N+1'in başında yine limit
// kadar istek gönderebilir; yani çok kısa bir aralıkta 2×limit isteğe kadar
// geçebilir. Jeton kovasında bu patlama olmaz.
//
// Bu takas bilerek kabul edilir: sabit pencere anahtar başına TEK bir sayaç ve
// istek başına TEK bir gidiş-dönüş demektir. Kayan pencere (sorted set'te
// istek zaman damgaları) sınır patlamasını yok ederdi ama anahtar başına
// istek sayısı kadar üye saklar, her istekte O(log n) ekleme + eski üyeleri
// kırpma yapar ve saldırı altında belleği isteklerle orantılı büyütür — yani
// hız sınırlayıcının kendisi saldırı yüzeyi olurdu. 2× patlama, kötüye
// kullanımı durdurmak için yeterince sıkı bir sınırdır; hassas eşik isteyen
// uçlar zaten sınırlayıcıya değil kotaya/lisansa aittir.
//
// # Anahtar biçimi
//
// Sayaçlar "<önek>:rl:<anahtar>" adresine yazılır; varsayılan önekle
// "istemci-a" anahtarı "gobit:rl:istemci-a" sayacına düşer. Önek kurucudan
// gelir ve aynı Redis'i paylaşan iki kurulumu ayıran şeydir (bkz. paket
// godoc'u).
type Limiter struct {
	client *redis.Client
	// onek sayaç anahtarlarının TAM önekidir (örn. "gobit:rl:").
	//
	// Ad alanı önekiyle bölüm adı her istekte yeniden birleştirilmesin diye
	// kurucuda bir kez kurulur.
	onek string
	// limit pencere başına izin verilen istek sayısıdır.
	limit int
	// window kotanın tamamen yenilendiği süredir.
	window time.Duration
	// pencereMs window'un milisaniye karşılığıdır; her istekte yeniden
	// hesaplanmaması için kurucuda bir kez çıkarılır.
	pencereMs int64
}

var _ corehttp.RateLimiter = (*Limiter)(nil)

// NewLimiter window süresinde limit isteğe izin veren Redis sınırlayıcısı kurar.
//
// keyPrefix sayaçların ad alanı önekidir; sayaçlar "<keyPrefix>:rl:<anahtar>"
// adresine yazılır. Biçimi [dogrulaOnek] denetler ve geçersiz önek HATA döner:
// aynı Redis'i paylaşan iki kurulumun ayrılması buna bağlıdır, sessizce
// düzeltmek (kırpmak ya da varsayılana düşmek) iki kurulumu yine aynı sayaca
// bindirirdi.
//
// client nil ya da limit/window pozitif değilse de HATA döner.
// corehttp.NewMemoryLimiter'ın aksine nil DÖNMEZ: nil bir *Limiter,
// corehttp.RateLimit'e arayüz olarak verildiğinde "nil olmayan ama içi nil"
// bir arayüz değeri üretir, middleware'in limiter == nil kontrolü FALSE çıkar
// ve ilk istekte panik olur. Kurucu zaten hata dönüyorken bu tuzağı taşımanın
// bir gerekçesi yok; sınır istemeyen çağıran corehttp.RateLimit'e doğrudan
// nil verir.
func NewLimiter(client *redis.Client, keyPrefix string, limit int, window time.Duration) (*Limiter, error) {
	if client == nil {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "redis istemcisi nil olamaz")
	}

	if err := dogrulaOnek(keyPrefix); err != nil {
		return nil, err
	}

	if limit <= 0 {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "hız sınırı pozitif olmalı, verilen: %d", limit)
	}

	if window <= 0 {
		return nil, coreerrors.Invalid(CodeInvalidConfig, "hız sınırı penceresi pozitif olmalı, verilen: %s", window)
	}

	return &Limiter{
		client: client,
		onek:   keyPrefix + ayirici + hizSinirBolumu + ayirici,
		limit:  limit,
		window: window,
		// Redis'in en küçük çözünürlüğü milisaniyedir; daha kısa bir pencere
		// PEXPIRE 0 ile komut hatasına dönerdi.
		pencereMs: max(window.Milliseconds(), 1),
	}, nil
}

// Allow anahtarın kotasından bir istek düşmeye çalışır.
//
// Redis erişilemezse KindUnavailable döner; corehttp.RateLimit bu hatayı
// loglayıp isteği geçirir (fail-open, bkz. paket godoc'u).
//
// RetryAfter, izin verilen istekte de doldurulur: değeri pencerenin KALAN
// süresidir, yani "kota ne zaman yenilenir" sorusunun yanıtıdır ve
// middleware onu RateLimit-Reset başlığına yazar.
func (l *Limiter) Allow(ctx context.Context, key string) (corehttp.Decision, error) {
	sonuc, err := limitBetigi.Run(ctx, l.client,
		[]string{l.onek + key},
		l.pencereMs,
	).Int64Slice()
	if err != nil {
		return corehttp.Decision{}, coreerrors.Wrap(err, coreerrors.KindUnavailable,
			CodeRateLimiterFailed, "hız sınırı sayacı güncellenemedi")
	}

	if len(sonuc) != 2 {
		return corehttp.Decision{}, coreerrors.Internal(CodeRateLimiterFailed,
			"hız sınırı betiği %d değer döndürdü, 2 bekleniyordu", len(sonuc))
	}

	sayac, kalanMs := sonuc[0], sonuc[1]

	yenilenme := time.Duration(kalanMs) * time.Millisecond
	if yenilenme <= 0 {
		// Sayaç okuduğumuz anda son nefesindeydi. Sıfır süre "bekleme"
		// demektir; istemciyi anında tekrar denemeye ve ikinci bir 429 almaya
		// yollamamak için tam pencereye yuvarlanır.
		yenilenme = l.window
	}

	if sayac > int64(l.limit) {
		return corehttp.Decision{
			Limit:      l.limit,
			Remaining:  0,
			RetryAfter: yenilenme,
		}, nil
	}

	// sayac bu dalda 1..limit aralığındadır; int'e daralması güvenlidir.
	return corehttp.Decision{
		Allowed:    true,
		Limit:      l.limit,
		Remaining:  l.limit - int(sayac),
		RetryAfter: yenilenme,
	}, nil
}
