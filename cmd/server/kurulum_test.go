package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"log/slog"

	"github.com/bdrtr/gobit/internal/core/config"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// temelConfig testlerin başlangıç noktası olan geçerli bir yapılandırmadır.
func temelConfig() config.Config {
	return config.Config{
		ServiceName:        "gobit-test",
		JWTTTL:             time.Hour,
		RateLimitPerMinute: 600,
		IdempotencyTTL:     time.Hour,
	}
}

// korumaliRouter verilen yapılandırmayla üretilmiş koruma yığınını taşıyan
// bir router kurar.
func korumaliRouter(cfg config.Config, authn corehttp.Authenticator) http.Handler {
	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version:     "test",
		Middlewares: korumaYigini(cfg, authn),
	})
	r.Get("/admin/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	r.Post("/admin/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

// cagir isteği router'a gönderir ve kaydı döner.
func cagir(h http.Handler, method, yol string) *httptest.ResponseRecorder {
	kayit := httptest.NewRecorder()
	h.ServeHTTP(kayit, httptest.NewRequest(method, yol, http.NoBody))

	return kayit
}

// TestKorumaYiginiKimlikBaglanmadanReddeder kurulumun EN TEHLİKELİ yanlışını
// sınar: koruma takılı ama doğrulayıcı bağlanmamış.
//
// Bu durumda yüzey açık kalsaydı, auth modülünü kaydetmeyi unutan bir kurulum
// tüm yönetim API'sini kimliksiz servis ederdi ve bunu hiçbir hata bildirmezdi.
func TestKorumaYiginiKimlikBaglanmadanReddeder(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(temelConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusUnauthorized, cagir(r, http.MethodGet, "/admin/v1/users").Code)
	assert.Equal(t, http.StatusUnauthorized, cagir(r, http.MethodGet, "/store/v1/products").Code)
}

// TestKorumaYiginiGirisUcunuMuafTutar giriş yolunun auth modülünün SABİTİNDEN
// okunduğunu ve muafiyetin gerçekten uygulandığını doğrular.
//
// Muafiyet kaybolsaydı kimse giriş yapamaz ve sistem kilitlenirdi.
func TestKorumaYiginiGirisUcunuMuafTutar(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(temelConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusOK, cagir(r, http.MethodPost, "/admin/v1/auth/login").Code,
		"giriş ucu doğrulayıcı bağlanmamışken bile erişilebilir olmalı")
}

// TestKorumaYiginiSaglikUclarinaDokunmaz orkestratör yolunun yığın dışında
// kaldığını doğrular.
func TestKorumaYiginiSaglikUclarinaDokunmaz(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(temelConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusOK, cagir(r, http.MethodGet, "/health").Code)
}

// TestKorumaYiginiHizSiniriKapatilabilir sıfır limitin "her şeyi reddet"e
// DÖNÜŞMEDİĞİNİ doğrular.
//
// Sıfırı "0 istek" saymak, hız sınırını kapatmak isteyen bir operatörün tüm
// trafiği kapatmasına yol açardı.
func TestKorumaYiginiHizSiniriKapatilabilir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.RateLimitPerMinute = 0

	kapali := korumaYigini(cfg, &corehttp.DeferredAuthenticator{})

	cfg.RateLimitPerMinute = 600
	acik := korumaYigini(cfg, &corehttp.DeferredAuthenticator{})

	assert.Less(t, len(kapali), len(acik),
		"hız sınırı kapalıyken yığında daha az halka olmalı")

	// Kapalıyken bile korumanın kendisi durmalı.
	r := korumaliRouter(cfg, &corehttp.DeferredAuthenticator{})
	assert.Equal(t, http.StatusUnauthorized, cagir(r, http.MethodGet, "/admin/v1/users").Code)
}

// TestEklentileriSecKataloguKullanir seçimin katalogdan yapıldığını ve
// bilinmeyen adın SESSİZCE atlanmadığını doğrular.
func TestEklentileriSecKataloguKullanir(t *testing.T) {
	t.Parallel()

	t.Run("boş liste", func(t *testing.T) {
		t.Parallel()

		kayit, err := eklentileriSec(nil)
		require.NoError(t, err)
		assert.Empty(t, kayit.Plugins())
	})

	t.Run("tanınan ad", func(t *testing.T) {
		t.Parallel()

		kayit, err := eklentileriSec([]string{paymentstripe.Name})
		require.NoError(t, err)
		assert.Equal(t, []string{paymentstripe.Name}, kayit.Plugins())
	})

	t.Run("bilinmeyen ad", func(t *testing.T) {
		t.Parallel()

		_, err := eklentileriSec([]string{"olmayan-eklenti"})
		require.Error(t, err, "bilinmeyen eklenti sessizce atlanmamalı")
		assert.Contains(t, err.Error(), paymentstripe.Name,
			"hata mesajı tanınan adları saymalı ki yazım hatası görünsün")
	})
}

// TestJWTSirriVerilmisSirriKorur yapılandırmadaki sırrın olduğu gibi
// kullanıldığını doğrular.
func TestJWTSirriVerilmisSirriKorur(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.JWTSecret = "elle-verilmis-sir"

	assert.Equal(t, "elle-verilmis-sir", jwtSirri(cfg, slogYut()))
}

// TestJWTSirriBosluktaRastgeleUretir sırsız geliştirme açılışının SABİT bir
// varsayılana düşmediğini doğrular.
//
// Sabit varsayılan, kazara üretime taşınan bir yapılandırmada herkesin
// kendine tam yetkili admin jetonu üretebilmesi demek olurdu.
func TestJWTSirriBosluktaRastgeleUretir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()

	ilk := jwtSirri(cfg, slogYut())
	ikinci := jwtSirri(cfg, slogYut())

	assert.NotEmpty(t, ilk)
	assert.NotEqual(t, ilk, ikinci, "her açılış kendi sırrını üretmeli")
	assert.GreaterOrEqual(t, len(ilk), gecicSirBayt,
		"üretilen sır HS256 çıktı uzunluğu kadar entropi taşımalı")
}

// TestEklentiAyarlariOrtamdanOkur eklenti ayarlarının ortam değişkenlerinden
// geldiğini doğrular.
func TestEklentiAyarlariOrtamdanOkur(t *testing.T) {
	// t.Setenv paralel testle kullanılamaz; bu test bilinçli olarak seri koşar.
	t.Setenv("GOBIT_TEST_EKLENTI_AYARI", "deger-42")

	ayarlar := eklentiAyarlari()

	assert.Equal(t, "deger-42", ayarlar["GOBIT_TEST_EKLENTI_AYARI"])
}

// slogYut testlerin çıktısını kirletmeyen bir logger döner.
func slogYut() *slog.Logger { return slog.New(slog.DiscardHandler) }
