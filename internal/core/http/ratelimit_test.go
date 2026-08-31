package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// sayanLimiter çağrı sayısını tutan ve sabit karar dönen sahte sınırlayıcıdır.
type sayanLimiter struct {
	karar      corehttp.Decision
	err        error
	anahtarlar []string
}

// Allow kararı olduğu gibi döner ve gelen anahtarı kaydeder.
func (l *sayanLimiter) Allow(_ context.Context, key string) (corehttp.Decision, error) {
	l.anahtarlar = append(l.anahtarlar, key)

	return l.karar, l.err
}

// istekYap verilen uzak adres ve başlıklarla bir test isteği kurar.
func istekYap(remoteAddr string, basliklar map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody)
	r.RemoteAddr = remoteAddr

	for k, v := range basliklar {
		r.Header.Set(k, v)
	}

	return r
}

// gecirenHandler çağrıldığında 200 dönen ve çağrılmayı kaydeden handler'dır.
func gecirenHandler(cagrildi *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*cagrildi = true
		w.WriteHeader(http.StatusOK)
	})
}

// TestRateLimitKotaBitinceCagriYapmaz sınır aşıldığında handler'ın HİÇ
// çalışmadığını doğrular.
//
// Yalnızca status koduna bakmak yeterli değildir: handler çalışıp yan etkisini
// bıraktıktan sonra 429 yazılsaydı sipariş yine oluşurdu.
func TestRateLimitKotaBitinceCagriYapmaz(t *testing.T) {
	t.Parallel()

	lim := &sayanLimiter{karar: corehttp.Decision{
		Allowed: false, Limit: 10, Remaining: 0, RetryAfter: 3 * time.Second,
	}}

	var cagrildi bool
	h := corehttp.RateLimit(lim, nil)(gecirenHandler(&cagrildi))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, istekYap("203.0.113.7:1234", nil))

	assert.False(t, cagrildi, "sınır aşıldıysa handler hiç çalışmamalı")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "3", w.Header().Get("Retry-After"))
	assert.Equal(t, "10", w.Header().Get("RateLimit-Limit"))
	assert.Equal(t, "0", w.Header().Get("RateLimit-Remaining"))
	assert.Contains(t, w.Body.String(), "rate_limited")
}

// TestRateLimitRetryAfterYukariYuvarlar kesirli sürenin AŞAĞI değil YUKARI
// yuvarlandığını doğrular.
//
// Aşağı yuvarlansaydı istemci kota dolmadan tekrar dener ve ikinci bir 429
// alırdı; sunucu kendi tavsiyesiyle çelişirdi.
func TestRateLimitRetryAfterYukariYuvarlar(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		sure     time.Duration
		beklenen string
	}{
		"1.1 saniye":   {1100 * time.Millisecond, "2"},
		"2.9 saniye":   {2900 * time.Millisecond, "3"},
		"tam 2 saniye": {2 * time.Second, "2"},
		"sıfır":        {0, "1"},
		"negatif":      {-time.Second, "1"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			lim := &sayanLimiter{karar: corehttp.Decision{Limit: 5, RetryAfter: tc.sure}}
			var cagrildi bool
			h := corehttp.RateLimit(lim, nil)(gecirenHandler(&cagrildi))

			w := httptest.NewRecorder()
			h.ServeHTTP(w, istekYap("203.0.113.7:1234", nil))

			assert.Equal(t, tc.beklenen, w.Header().Get("Retry-After"))
		})
	}
}

// TestRateLimitSinirlayiciArizasindaGecirir sınırlayıcı hata döndüğünde
// isteğin GEÇTİĞİNİ doğrular.
//
// Redis erişilemez olduğunda tüm trafiği reddetmek, hız sınırlayıcıyı
// tam bir kesinti kaynağına çevirirdi.
func TestRateLimitSinirlayiciArizasindaGecirir(t *testing.T) {
	t.Parallel()

	lim := &sayanLimiter{err: errors.New("redis erişilemiyor")}
	var cagrildi bool
	h := corehttp.RateLimit(lim, nil)(gecirenHandler(&cagrildi))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, istekYap("203.0.113.7:1234", nil))

	assert.True(t, cagrildi, "sınırlayıcı arızası isteği kesmemeli")
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestRateLimitNilSinirlayiciNoOptur yapılandırılmamış sınırlayıcının
// trafiği kesmediğini doğrular.
func TestRateLimitNilSinirlayiciNoOptur(t *testing.T) {
	t.Parallel()

	var cagrildi bool
	h := corehttp.RateLimit(nil, nil)(gecirenHandler(&cagrildi))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, istekYap("203.0.113.7:1234", nil))

	assert.True(t, cagrildi)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestClientIPKeyForwardedForaGuvenmez uydurulmuş X-Forwarded-For'un anahtarı
// DEĞİŞTİRMEDİĞİNİ doğrular.
//
// Bu testin koruduğu açık şudur: başlığa güvenilseydi, saldırgan her istekte
// farklı bir X-Forwarded-For göndererek her seferinde taze bir kova alır ve
// hız sınırını tamamen etkisiz kılardı.
func TestClientIPKeyForwardedForaGuvenmez(t *testing.T) {
	t.Parallel()

	lim := &sayanLimiter{karar: corehttp.Decision{Allowed: true, Limit: 100, Remaining: 99}}
	var cagrildi bool
	h := corehttp.RateLimit(lim, corehttp.ClientIPKey)(gecirenHandler(&cagrildi))

	for _, sahte := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, istekYap("203.0.113.7:1234",
			map[string]string{"X-Forwarded-For": sahte}))
		require.Equal(t, http.StatusOK, w.Code)
	}

	assert.Equal(t, []string{"203.0.113.7", "203.0.113.7", "203.0.113.7"}, lim.anahtarlar,
		"anahtar uydurulabilir başlıktan değil RemoteAddr'dan gelmeli")
}

// TestTrustedProxyIPKey hops=0/1/2 için XFF'te 0/1/2/3 girdi bulunan her
// durumda hangi adresin seçildiğini kanıtlar.
//
// Sözleşme: zincir soldan sağa "istemci, proxy1, proxy2, ..." diye büyür, en
// sağdaki girdiyi bize en yakın güvenilen proxy yazar. hops güvenilen atlama
// varsa doğrulanmış adres SAĞDAN hops.'tur (parts[len-hops]). Tablo bilerek
// tam çarpım olarak yazıldı: indeks aritmetiğindeki bir eksik/bir fazla kayma,
// tek bir uzunlukta test edilirse fark edilmeden geçebilir.
//
// Kısa zincirde (len < hops) RemoteAddr'a düşülür; istemcinin verdiği ilk
// girdiye DEĞİL. Aksi hâlde istemci hop sayısını azaltarak anahtarı seçerdi.
func TestTrustedProxyIPKey(t *testing.T) {
	t.Parallel()

	const remoteAddr = "203.0.113.7"

	tests := map[string]struct {
		hops     int
		xff      string
		beklenen string
		neden    string
	}{
		// hops=0: proxy arkasında değiliz, başlık hiç okunmaz.
		"hops 0 · 0 girdi": {0, "", remoteAddr, "başlık okunmamalı"},
		"hops 0 · 1 girdi": {0, "1.1.1.1", remoteAddr, "başlık okunmamalı"},
		"hops 0 · 2 girdi": {0, "1.1.1.1, 2.2.2.2", remoteAddr, "başlık okunmamalı"},
		"hops 0 · 3 girdi": {
			0, "1.1.1.1, 2.2.2.2, 3.3.3.3", remoteAddr, "başlık okunmamalı",
		},

		// hops=1: tek güvenilen proxy; doğrulanmış adres SON girdidir.
		"hops 1 · 0 girdi": {1, "", remoteAddr, "zincir yok, RemoteAddr'a düşülmeli"},
		"hops 1 · 1 girdi": {
			1, "198.51.100.9", "198.51.100.9", "tek girdiyi güvenilen proxy yazmış",
		},
		"hops 1 · 2 girdi": {
			1, "198.51.100.9, 10.0.0.1", "10.0.0.1",
			"sağdan 1. girdi seçilmeli; soldaki istemcinin uydurması olabilir",
		},
		"hops 1 · 3 girdi": {
			1, "198.51.100.9, 10.0.0.1, 10.0.0.2", "10.0.0.2", "sağdan 1. girdi",
		},

		// hops=2: iki güvenilen proxy; doğrulanmış adres SONDAN İKİNCİdir.
		"hops 2 · 0 girdi": {2, "", remoteAddr, "zincir yok"},
		"hops 2 · 1 girdi": {
			2, "198.51.100.9", remoteAddr,
			"zincir beklenenden kısa; istemcinin tek girdisine güvenilmemeli",
		},
		"hops 2 · 2 girdi": {
			2, "198.51.100.9, 10.0.0.1", "198.51.100.9",
			"zincir tam boyunda; ilk girdiyi dış proxy yazmış",
		},
		"hops 2 · 3 girdi": {
			2, "198.51.100.9, 10.0.0.1, 10.0.0.2", "10.0.0.1", "sağdan 2. girdi",
		},

		// Biçim kenarları.
		"boşluklar kırpılır": {
			1, "  198.51.100.9 ,   10.0.0.1   ", "10.0.0.1", "girdiler kırpılmalı",
		},
		"seçilen girdi boş": {
			2, "198.51.100.9, , 10.0.0.1", remoteAddr, "boş girdi IP değildir",
		},
		"seçilen girdi geçersiz IP": {
			1, "198.51.100.9, yok-boyle-ip", remoteAddr, "ayrıştırılamayan adrese güvenilmez",
		},
		"IPv6 girdi": {
			1, "198.51.100.9, 2001:db8::1", "2001:db8::1", "IPv6 de geçerli adrestir",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			basliklar := map[string]string{}
			if tc.xff != "" {
				basliklar["X-Forwarded-For"] = tc.xff
			}

			anahtar := corehttp.TrustedProxyIPKey(tc.hops)(
				istekYap(remoteAddr+":1234", basliklar))
			assert.Equal(t, tc.beklenen, anahtar, tc.neden)
		})
	}
}

// TestTrustedProxyIPKeySahteGirdiAnahtariDegistirmez istemcinin X-Forwarded-For
// başına kaç sahte girdi eklerse eklesin anahtarı DEĞİŞTİREMEDİĞİNİ doğrular.
//
// Korunan açık şudur: seçim indeksi bir eleman sola kayarsa (len-hops-1),
// seçilen girdi artık güvenilen proxy'nin yazdığı değil istemcinin yazdığı olur.
// Saldırgan her istekte farklı bir sahte girdi göndererek her seferinde taze bir
// kova alır ve hız sınırı tamamen etkisiz kalırdı.
//
// Kurgu: tek güvenilen proxy (hops=1) istemcinin gönderdiği başlığın SONUNA
// gerçek adresi (198.51.100.9) ekler; soldaki her şey istemcinin uydurmasıdır.
func TestTrustedProxyIPKeySahteGirdiAnahtariDegistirmez(t *testing.T) {
	t.Parallel()

	const gercek = "198.51.100.9"

	anahtarla := corehttp.TrustedProxyIPKey(1)

	uydurmalar := []string{
		"1.1.1.1",
		"2.2.2.2",
		"9.9.9.9, 8.8.8.8",
		"203.0.113.7, 1.1.1.1, 2.2.2.2, 3.3.3.3",
	}

	anahtarlar := make([]string, 0, len(uydurmalar))
	for _, uydurma := range uydurmalar {
		// Proxy'nin eklediği gerçek adres her zaman en sağdadır.
		anahtarlar = append(anahtarlar, anahtarla(istekYap("10.0.0.1:1234",
			map[string]string{"X-Forwarded-For": uydurma + ", " + gercek})))
	}

	assert.Equal(t, []string{gercek, gercek, gercek, gercek}, anahtarlar,
		"anahtar yalnızca güvenilen proxy'nin yazdığı adresten gelmeli")
}

// TestTrustedProxyIPKeyKisaZincirdeRemoteAddraDuser istemcinin zinciri
// KISALTARAK anahtarı seçemediğini doğrular.
//
// Sahte girdi eklemek işe yaramıyorsa saldırganın ikinci hamlesi hiç girdi
// göndermemek ya da beklenenden az göndermektir. O durumda başlıktaki tek girdi
// istemcinin kendi yazdığıdır; ona düşmek anahtarı yine istemciye seçtirirdi.
func TestTrustedProxyIPKeyKisaZincirdeRemoteAddraDuser(t *testing.T) {
	t.Parallel()

	anahtarla := corehttp.TrustedProxyIPKey(2)

	for _, uydurma := range []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"} {
		anahtar := anahtarla(istekYap("203.0.113.7:1234",
			map[string]string{"X-Forwarded-For": uydurma}))
		assert.Equal(t, "203.0.113.7", anahtar,
			"kısa zincirde RemoteAddr'a düşülmeli, istemcinin girdisine değil")
	}
}

// TestPrincipalKeyKimligeGoreAyirir kimliği doğrulanmış çağrıların IP'ye
// değil kimliğe göre anahtarlandığını doğrular.
//
// Aksi hâlde aynı NAT arkasındaki iki farklı müşteri birbirinin kotasını
// tüketirdi.
func TestPrincipalKeyKimligeGoreAyirir(t *testing.T) {
	t.Parallel()

	r := istekYap("203.0.113.7:1234", nil)
	assert.Equal(t, "ip:203.0.113.7", corehttp.PrincipalKey(r),
		"kimlik yoksa IP'ye düşmeli")

	ctx := corehttp.WithPrincipal(r.Context(),
		corehttp.Principal{ID: "user_01", Kind: "user"})
	assert.Equal(t, "user:user_01", corehttp.PrincipalKey(r.WithContext(ctx)))

	ctx = corehttp.WithPrincipal(r.Context(),
		corehttp.Principal{ID: "apikey_01", Kind: "api_key"})
	assert.Equal(t, "api_key:apikey_01", corehttp.PrincipalKey(r.WithContext(ctx)))
}

// TestMemoryLimiterKotayiTuketir kotanın tam olarak limit kadar istekte
// bittiğini doğrular.
func TestMemoryLimiterKotayiTuketir(t *testing.T) {
	t.Parallel()

	lim := corehttp.NewMemoryLimiter(3, time.Minute)
	require.NotNil(t, lim)

	for i := range 3 {
		d, err := lim.Allow(t.Context(), "k")
		require.NoError(t, err)
		assert.True(t, d.Allowed, "%d. istek geçmeliydi", i+1)
		assert.Equal(t, 3-i-1, d.Remaining)
	}

	d, err := lim.Allow(t.Context(), "k")
	require.NoError(t, err)
	assert.False(t, d.Allowed, "kota bittiğinde reddetmeli")
	assert.Positive(t, d.RetryAfter)
}

// TestMemoryLimiterAnahtarlariAyirir bir anahtarın kotasını tüketmenin
// diğerini etkilemediğini doğrular.
func TestMemoryLimiterAnahtarlariAyirir(t *testing.T) {
	t.Parallel()

	lim := corehttp.NewMemoryLimiter(1, time.Minute)
	require.NotNil(t, lim)

	d, err := lim.Allow(t.Context(), "a")
	require.NoError(t, err)
	require.True(t, d.Allowed)

	d, err = lim.Allow(t.Context(), "a")
	require.NoError(t, err)
	require.False(t, d.Allowed, "a'nın kotası bitmiş olmalı")

	d, err = lim.Allow(t.Context(), "b")
	require.NoError(t, err)
	assert.True(t, d.Allowed, "b'nin kotası a'dan bağımsız olmalı")
}

// TestNewMemoryLimiterGecersizYapilandirmadaNilDoner sıfır limitin "her şeyi
// reddet" anlamına GELMEDİĞİNİ doğrular.
func TestNewMemoryLimiterGecersizYapilandirmadaNilDoner(t *testing.T) {
	t.Parallel()

	assert.Nil(t, corehttp.NewMemoryLimiter(0, time.Minute))
	assert.Nil(t, corehttp.NewMemoryLimiter(-1, time.Minute))
	assert.Nil(t, corehttp.NewMemoryLimiter(10, 0))
	assert.Nil(t, corehttp.NewMemoryLimiter(10, -time.Minute))
}

// TestMemoryLimiterEszamanliKullanimdaTutarli yarış dedektörü altında toplam
// geçen istek sayısının limiti AŞMADIĞINI doğrular.
func TestMemoryLimiterEszamanliKullanimdaTutarli(t *testing.T) {
	t.Parallel()

	const limit = 50
	lim := corehttp.NewMemoryLimiter(limit, time.Hour)
	require.NotNil(t, lim)

	sonuc := make(chan bool, 200)
	for range 200 {
		go func() {
			d, err := lim.Allow(context.Background(), "ortak")
			assert.NoError(t, err)
			sonuc <- d.Allowed
		}()
	}

	gecen := 0
	for range 200 {
		if <-sonuc {
			gecen++
		}
	}

	assert.Equal(t, limit, gecen, "eşzamanlılık kotayı aştıramamalı")
}
