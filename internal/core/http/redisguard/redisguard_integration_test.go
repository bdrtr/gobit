//go:build integration

// Bu dosya gerçek bir Redis gerektirir ve yalnızca `-tags=integration` ile
// derlenir (`make test-integration`). Böylece `make test` Docker'sız ve hızlı kalır.
package redisguard_test

import (
	"net/http"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/core/http/redisguard"
)

// redisImage entegrasyon testlerinde kullanılan Redis imajıdır.
const redisImage = "redis:7-alpine"

// redisBaslat test süresince yaşayan bir Redis başlatır ve bağlantı dizesini döner.
func redisBaslat(t *testing.T) string {
	t.Helper()

	ctx := t.Context()
	container, err := tcredis.Run(ctx, redisImage)
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err, "redis konteyneri başlatılamadı")

	uri, err := container.ConnectionString(ctx)
	require.NoError(t, err, "bağlantı dizesi alınamadı")

	return uri
}

// istemciAc verilen bağlantı dizesine YENİ bir Redis istemcisi açar.
//
// Ayrı istemci, ayrı bir SÜRECİN yerine geçer: sayacın ve kaydın gerçekten
// Redis'te paylaşıldığını (istemcinin belleğinde durmadığını) ancak ikinci bir
// bağlantı kanıtlayabilir. Bellek içi uygulamaların düzeltmeye çalıştığımız
// arızası tam olarak buydu.
func istemciAc(t *testing.T, uri string) *redis.Client {
	t.Helper()

	opts, err := redis.ParseURL(uri)
	require.NoError(t, err, "bağlantı dizesi ayrıştırılamadı")

	client := redis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.Ping(t.Context()).Err(), "redis'e ping atılamadı")

	return client
}

// redisIstemcisi tek istemci yeten testler için Redis başlatıp istemcisini döner.
func redisIstemcisi(t *testing.T) *redis.Client {
	t.Helper()
	return istemciAc(t, redisBaslat(t))
}

// varsayilanOnek testlerin çoğunda kullanılan ad alanı önekidir.
//
// Değeri config.DefaultRedisKeyPrefix ile aynıdır; ad alanının kendisi bu
// testlerin konusu olmadığı için gerçek kurulumun kullandığı önek seçilir.
const varsayilanOnek = "gobit"

// Ad alanı ayrımını sınayan testlerin kullandığı, birbirinden FARKLI iki önek.
//
// İkisi de AYNI Redis örneğine ve AYNI DB'ye yazar; ayrımı yapan tek şey
// önektir. Ayrı DB/örnek kullanmak testi anlamsızlaştırırdı: kanıtlanmak
// istenen şey tam olarak "aynı Redis'te ayrışıyorlar mı" sorusudur.
const (
	stagingOnek = "gobit-staging"
	uretimOnek  = "gobit-prod"
)

// --- Hız sınırlayıcı ---

func TestKotaBitinceIstekReddedilir(t *testing.T) {
	const limit = 3

	lim, err := redisguard.NewLimiter(redisIstemcisi(t), varsayilanOnek, limit, time.Minute)
	require.NoError(t, err)

	for i := range limit {
		d, err := lim.Allow(t.Context(), "istemci-a")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "%d. istek kotanın içinde, geçmeli", i+1)
		assert.Equal(t, limit, d.Limit)
		assert.Equal(t, limit-i-1, d.Remaining, "kalan hak her istekte bir azalmalı")
	}

	d, err := lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "kota bitince istek reddedilmeli")
	assert.Zero(t, d.Remaining)
	assert.Positive(t, d.RetryAfter, "reddedilen istek pozitif bir bekleme süresi almalı")
	assert.LessOrEqual(t, d.RetryAfter, time.Minute, "bekleme süresi pencereyi aşamaz")
}

func TestPencereDoluncaKotaYenilenir(t *testing.T) {
	const window = time.Second

	lim, err := redisguard.NewLimiter(redisIstemcisi(t), varsayilanOnek, 1, window)
	require.NoError(t, err)

	ilk, err := lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	require.True(t, ilk.Allowed)

	reddedilen, err := lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	require.False(t, reddedilen.Allowed, "pencere içindeki ikinci istek reddedilmeli")

	// Sabit pencere sayacın TTL'i dolunca topluca yenilenir; payı, testin
	// konteyner gecikmesinde kıl payı erken uyanmasını engellemek için.
	time.Sleep(window + 500*time.Millisecond)

	yeni, err := lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	assert.True(t, yeni.Allowed, "pencere dolduktan sonra kota yenilenmeli")
	assert.Zero(t, yeni.Remaining, "yenilenen kotadan da bir hak düşülmeli")
}

func TestFarkliIstemcilerBirbirininKotasiniHarcamaz(t *testing.T) {
	lim, err := redisguard.NewLimiter(redisIstemcisi(t), varsayilanOnek, 1, time.Minute)
	require.NoError(t, err)

	a, err := lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	require.True(t, a.Allowed)

	aTekrar, err := lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	require.False(t, aTekrar.Allowed, "istemci-a kotasını bitirdi")

	b, err := lim.Allow(t.Context(), "istemci-b")
	require.NoError(t, err)
	assert.True(t, b.Allowed, "istemci-b'nin kotası istemci-a'dan bağımsız olmalı")
	assert.Zero(t, b.Remaining)
}

// TestIkiSurecAyniKotayiPaylasir bu paketin var olma sebebini sabitler.
//
// Bellek içi sınırlayıcıda her örnek kendi sayacını tuttuğu için iki örnekli
// bir kurulumda gerçek sınır İKİYE KATLANIRDI. Ayrı bağlantılar üzerinden
// kurulan iki sınırlayıcı burada tek bir kotayı paylaşıyor.
func TestIkiSurecAyniKotayiPaylasir(t *testing.T) {
	uri := redisBaslat(t)

	birinci, err := redisguard.NewLimiter(istemciAc(t, uri), varsayilanOnek, 2, time.Minute)
	require.NoError(t, err)
	ikinci, err := redisguard.NewLimiter(istemciAc(t, uri), varsayilanOnek, 2, time.Minute)
	require.NoError(t, err)

	d1, err := birinci.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	assert.True(t, d1.Allowed)
	assert.Equal(t, 1, d1.Remaining)

	d2, err := ikinci.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	assert.True(t, d2.Allowed)
	assert.Zero(t, d2.Remaining, "ikinci örnek BİRİNCİNİN harcadığı hakkı görmeli")

	d3, err := birinci.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)
	assert.False(t, d3.Allowed, "kota örnek sayısıyla çarpılmamalı")
}

// --- Idempotency deposu ---

func TestBeginAyniAnahtarlaIkinciKezIslemdeDoner(t *testing.T) {
	depo, err := redisguard.NewIdempotencyStore(redisIstemcisi(t), varsayilanOnek, time.Hour)
	require.NoError(t, err)

	kayit, tamam, err := depo.Begin(t.Context(), "kiracı-1:anahtar", "izi-1")
	require.NoError(t, err)
	assert.Nil(t, kayit, "yeni anahtarda kayıt olmamalı")
	assert.False(t, tamam)

	_, _, err = depo.Begin(t.Context(), "kiracı-1:anahtar", "izi-1")
	assert.ErrorIs(t, err, corehttp.ErrIdempotencyKeyInFlight,
		"işlemdeki anahtar ikinci kez ayrılamamalı")
}

func TestCompleteSonrasiBeginKaydiDondurur(t *testing.T) {
	depo, err := redisguard.NewIdempotencyStore(redisIstemcisi(t), varsayilanOnek, time.Hour)
	require.NoError(t, err)

	const anahtar = "kiracı-1:anahtar"

	_, tamam, err := depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.False(t, tamam)

	yanit := corehttp.IdempotentResponse{
		Status: http.StatusCreated,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"Location":     []string{"/store/v1/orders/order_01"},
		},
		Body:        []byte(`{"id":"order_01"}`),
		Fingerprint: "izi-1",
	}
	require.NoError(t, depo.Complete(t.Context(), anahtar, yanit))

	kayit, tamam, err := depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.True(t, tamam, "tamamlanmış anahtar kaydı döndürmeli")
	require.NotNil(t, kayit)
	assert.Equal(t, yanit.Status, kayit.Status)
	assert.Equal(t, yanit.Header, kayit.Header, "başlıklar olduğu gibi saklanmalı")
	assert.Equal(t, yanit.Body, kayit.Body)
	assert.Equal(t, yanit.Fingerprint, kayit.Fingerprint,
		"parmak izi saklanmalı; middleware anahtarın farklı bir istekle "+
			"kullanılmasını buna bakarak yakalıyor")
}

// TestIkiliGovdeBozulmadanSaklanir gövdenin metin varsayılmadığını doğrular.
//
// Kayıt JSON'a çevriliyor; gövde string alan olsaydı encoding/json geçersiz
// UTF-8 baytlarını sessizce U+FFFD ile değiştirir ve çalınan yanıt BOZUK
// çıkardı. Buradaki bayt dizisi bilinçli olarak geçerli UTF-8 değildir.
func TestIkiliGovdeBozulmadanSaklanir(t *testing.T) {
	depo, err := redisguard.NewIdempotencyStore(redisIstemcisi(t), varsayilanOnek, time.Hour)
	require.NoError(t, err)

	govde := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe, 0x00}
	require.False(t, utf8.Valid(govde), "test verisi geçersiz UTF-8 olmalı")

	require.NoError(t, depo.Complete(t.Context(), "kiracı-1:ikili", corehttp.IdempotentResponse{
		Status:      http.StatusOK,
		Header:      http.Header{"Content-Type": []string{"image/png"}},
		Body:        govde,
		Fingerprint: "izi-1",
	}))

	kayit, tamam, err := depo.Begin(t.Context(), "kiracı-1:ikili", "izi-1")
	require.NoError(t, err)
	require.True(t, tamam)
	assert.Equal(t, govde, kayit.Body, "ikili gövde bayt bayt korunmalı")
}

func TestAbortSonrasiAnahtarYenidenAyrilabilir(t *testing.T) {
	depo, err := redisguard.NewIdempotencyStore(redisIstemcisi(t), varsayilanOnek, time.Hour)
	require.NoError(t, err)

	const anahtar = "kiracı-1:anahtar"

	_, _, err = depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)

	require.NoError(t, depo.Abort(t.Context(), anahtar))

	kayit, tamam, err := depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err, "geri alınan anahtar yeniden ayrılabilmeli")
	assert.Nil(t, kayit)
	assert.False(t, tamam)
}

// TestAbortTamamlanmisKaydiSilmez geç gelen bir Abort'un çalınabilir yanıtı
// yok etmediğini doğrular.
//
// Handler yanıtı yazdıktan sonra paniklerse defer'daki Abort yine çalışır;
// koşulsuz silen bir uygulama, kaydı silip tekrar gelen isteğin baştan
// işlenmesine — yani ikinci bir siparişe — yol açardı.
func TestAbortTamamlanmisKaydiSilmez(t *testing.T) {
	depo, err := redisguard.NewIdempotencyStore(redisIstemcisi(t), varsayilanOnek, time.Hour)
	require.NoError(t, err)

	const anahtar = "kiracı-1:anahtar"

	_, _, err = depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.NoError(t, depo.Complete(t.Context(), anahtar, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"order_01"}`),
		Fingerprint: "izi-1",
	}))

	require.NoError(t, depo.Abort(t.Context(), anahtar))

	kayit, tamam, err := depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.True(t, tamam, "tamamlanmış kayıt Abort ile silinmemeli")
	require.NotNil(t, kayit)
	assert.Equal(t, http.StatusCreated, kayit.Status)
}

func TestTTLDoluncaKayitKaybolur(t *testing.T) {
	const ttl = 800 * time.Millisecond

	depo, err := redisguard.NewIdempotencyStore(redisIstemcisi(t), varsayilanOnek, ttl)
	require.NoError(t, err)

	const anahtar = "kiracı-1:anahtar"

	_, _, err = depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.NoError(t, depo.Complete(t.Context(), anahtar, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Fingerprint: "izi-1",
	}))

	_, tamam, err := depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.True(t, tamam, "ttl dolmadan kayıt durmalı")

	time.Sleep(ttl + 500*time.Millisecond)

	kayit, tamam, err := depo.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	assert.Nil(t, kayit, "ttl dolunca kayıt kaybolmalı")
	assert.False(t, tamam, "süresi dolan anahtar yeniden ayrılabilmeli")
}

// TestIkiSurecAyniKaydiGorur bu paketin DOĞRULUK gerekçesini sabitler.
//
// Bellek içi depoda ikinci örnek birincinin ayırmasını hiç görmez; aynı
// anahtarla farklı örneklere düşen iki istek iki kez işlenir. Burada ikinci
// örnek hem ayırmayı hem de tamamlanan kaydı görüyor.
func TestIkiSurecAyniKaydiGorur(t *testing.T) {
	uri := redisBaslat(t)

	birinci, err := redisguard.NewIdempotencyStore(istemciAc(t, uri), varsayilanOnek, time.Hour)
	require.NoError(t, err)
	ikinci, err := redisguard.NewIdempotencyStore(istemciAc(t, uri), varsayilanOnek, time.Hour)
	require.NoError(t, err)

	const anahtar = "kiracı-1:anahtar"

	_, _, err = birinci.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)

	_, _, err = ikinci.Begin(t.Context(), anahtar, "izi-1")
	require.ErrorIs(t, err, corehttp.ErrIdempotencyKeyInFlight,
		"ikinci örnek birincinin ayırmasını görmeli")

	require.NoError(t, birinci.Complete(t.Context(), anahtar, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"order_01"}`),
		Fingerprint: "izi-1",
	}))

	kayit, tamam, err := ikinci.Begin(t.Context(), anahtar, "izi-1")
	require.NoError(t, err)
	require.True(t, tamam, "ikinci örnek birincinin yazdığı kaydı okumalı")
	assert.Equal(t, []byte(`{"id":"order_01"}`), kayit.Body)
}

// --- Ad alanı ayrımı ---

// TestFarkliOnekliSinirlayicilarBirbirininKotasiniHarcamaz aynı Redis'i
// paylaşan iki KURULUMUN hız sınırı sayaçlarının ayrıldığını doğrular.
//
// Önek sabitken bu ayrım yoktu: staging ile production aynı Redis'e bakıyorsa
// staging'in yük testi production'ın kotasını yer ve production'daki gerçek
// istemciler 429 alır. Sayaçlar tek istemci üzerinden, yani AYNI Redis DB'sinde
// tutuluyor; ayrımı yapan tek şey önektir.
func TestFarkliOnekliSinirlayicilarBirbirininKotasiniHarcamaz(t *testing.T) {
	client := redisIstemcisi(t)

	staging, err := redisguard.NewLimiter(client, stagingOnek, 1, time.Minute)
	require.NoError(t, err)
	uretim, err := redisguard.NewLimiter(client, uretimOnek, 1, time.Minute)
	require.NoError(t, err)

	// AYNI sınır anahtarı bilinçlidir: iki kurulumun aynı IP'den istek alması
	// olağandır ve ayrımın anahtara değil ÖNEKE dayandığı ancak böyle görünür.
	const anahtar = "istemci-a"

	ilk, err := staging.Allow(t.Context(), anahtar)
	require.NoError(t, err)
	require.True(t, ilk.Allowed)

	tekrar, err := staging.Allow(t.Context(), anahtar)
	require.NoError(t, err)
	require.False(t, tekrar.Allowed, "staging kendi kotasını bitirdi")

	uretimKarari, err := uretim.Allow(t.Context(), anahtar)
	require.NoError(t, err)
	assert.True(t, uretimKarari.Allowed,
		"başka önekteki kurulumun harcadığı kota bu kurulumu etkilememeli")
	assert.Zero(t, uretimKarari.Remaining, "üretim kendi kotasından ilk hakkını kullandı")
}

// TestFarkliOnekliDepolarBirbirininKaydiniGormez aynı Redis'i paylaşan iki
// KURULUMUN idempotency kayıtlarının ayrıldığını doğrular.
//
// Bu, ikisi arasında ağır basan arızadır: önek sabitken staging'in bir
// anahtara yazdığı yanıt, aynı anahtarla gelen production isteğine YANIT
// olarak dönerdi — istemci hiç yapmadığı bir siparişin kimliğini alır ve
// gerçek isteği hiç işlenmez.
func TestFarkliOnekliDepolarBirbirininKaydiniGormez(t *testing.T) {
	client := redisIstemcisi(t)

	staging, err := redisguard.NewIdempotencyStore(client, stagingOnek, time.Hour)
	require.NoError(t, err)
	uretim, err := redisguard.NewIdempotencyStore(client, uretimOnek, time.Hour)
	require.NoError(t, err)

	const anahtar = "kiracı-1:anahtar"

	_, _, err = staging.Begin(t.Context(), anahtar, "izi-staging")
	require.NoError(t, err)

	// Ayırma da ad alanına aittir: staging'in tuttuğu anahtar üretimi
	// bloklasaydı, bir ortamın trafiği ötekini durdurabilirdi.
	kayit, tamam, err := uretim.Begin(t.Context(), anahtar, "izi-uretim")
	require.NoError(t, err, "başka önekteki ayırma bu depoyu bloklamamalı")
	assert.Nil(t, kayit)
	assert.False(t, tamam)

	require.NoError(t, staging.Complete(t.Context(), anahtar, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"staging_01"}`),
		Fingerprint: "izi-staging",
	}))
	require.NoError(t, uretim.Complete(t.Context(), anahtar, corehttp.IdempotentResponse{
		Status:      http.StatusCreated,
		Body:        []byte(`{"id":"uretim_01"}`),
		Fingerprint: "izi-uretim",
	}))

	stagingKaydi, tamam, err := staging.Begin(t.Context(), anahtar, "izi-staging")
	require.NoError(t, err)
	require.True(t, tamam)
	assert.Equal(t, []byte(`{"id":"staging_01"}`), stagingKaydi.Body,
		"her kurulum KENDİ yanıtını okumalı")

	uretimKaydi, tamam, err := uretim.Begin(t.Context(), anahtar, "izi-uretim")
	require.NoError(t, err)
	require.True(t, tamam)
	assert.Equal(t, []byte(`{"id":"uretim_01"}`), uretimKaydi.Body,
		"bir kurulumun yanıtı ötekinin istemcisine gitmemeli")
}

// TestVarsayilanOnekEskiAnahtarBiciminiKorur önek yapılandırılabilir olurken
// GERİYE UYUMLULUĞUN korunduğunu doğrular.
//
// Anahtar biçimi değişseydi, yükseltilen bir kurulumun tüm hız sınırı
// sayaçları ve işlemdeki idempotency kayıtları bir anda görünmez olurdu; o an
// uçan her tekrar isteği ikinci kez işlenir, yani ikinci sipariş. Bu yüzden
// beklenen anahtarlar sabitten türetilmez, ELLE yazılır: sabit değişirse test
// düşer ve değişikliğin bedeli görünür olur.
func TestVarsayilanOnekEskiAnahtarBiciminiKorur(t *testing.T) {
	client := redisIstemcisi(t)

	lim, err := redisguard.NewLimiter(client, varsayilanOnek, 5, time.Minute)
	require.NoError(t, err)

	_, err = lim.Allow(t.Context(), "istemci-a")
	require.NoError(t, err)

	sayac, err := client.Get(t.Context(), "gobit:rl:istemci-a").Result()
	require.NoError(t, err, "sayaç eski anahtar biçiminde yazılmalı")
	assert.Equal(t, "1", sayac)

	depo, err := redisguard.NewIdempotencyStore(client, varsayilanOnek, time.Hour)
	require.NoError(t, err)

	_, _, err = depo.Begin(t.Context(), "kiracı-1:anahtar", "izi-1")
	require.NoError(t, err)

	require.NoError(t, client.Get(t.Context(), "gobit:idem:kiracı-1:anahtar").Err(),
		"idempotency kaydı eski anahtar biçiminde yazılmalı")
}
