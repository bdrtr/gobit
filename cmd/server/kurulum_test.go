package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"log/slog"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/plugins/paymentstripe"
)

// temelConfig testlerin başlangıç noktası olan geçerli bir yapılandırmadır.
func temelConfig() config.Config {
	return config.Config{
		ServiceName:        "gobit-test",
		JWTTTL:             time.Hour,
		RateLimitPerMinute: 600,
		IdempotencyTTL:     time.Hour,
		GuardBackend:       "memory",
	}
}

// korumaliRouter verilen yapılandırmayla üretilmiş koruma yığınını taşıyan
// bir router kurar.
//
// Redis istemcisi nil verilir: bu testler bellek içi arka ucu sınar, Redis
// yolunun kendi testleri redisguard paketindedir.
func korumaliRouter(t *testing.T, cfg config.Config, authn corehttp.Authenticator) http.Handler {
	t.Helper()

	yigin, err := korumaYigini(cfg, authn, nil, slogYut())
	require.NoError(t, err, "koruma yığını kurulamadı")

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version:     "test",
		Middlewares: yigin,
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

	r := korumaliRouter(t, temelConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusUnauthorized, cagir(r, http.MethodGet, "/admin/v1/users").Code)
	assert.Equal(t, http.StatusUnauthorized, cagir(r, http.MethodGet, "/store/v1/products").Code)
}

// TestKorumaYiginiGirisUcunuMuafTutar giriş yolunun auth modülünün SABİTİNDEN
// okunduğunu ve muafiyetin gerçekten uygulandığını doğrular.
//
// Muafiyet kaybolsaydı kimse giriş yapamaz ve sistem kilitlenirdi.
func TestKorumaYiginiGirisUcunuMuafTutar(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(t, temelConfig(), &corehttp.DeferredAuthenticator{})

	assert.Equal(t, http.StatusOK, cagir(r, http.MethodPost, "/admin/v1/auth/login").Code,
		"giriş ucu doğrulayıcı bağlanmamışken bile erişilebilir olmalı")
}

// TestKorumaYiginiSaglikUclarinaDokunmaz orkestratör yolunun yığın dışında
// kaldığını doğrular.
func TestKorumaYiginiSaglikUclarinaDokunmaz(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(t, temelConfig(), &corehttp.DeferredAuthenticator{})

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

	kapali, err := korumaYigini(cfg, &corehttp.DeferredAuthenticator{}, nil, slogYut())
	require.NoError(t, err)

	cfg.RateLimitPerMinute = 600
	acik, err := korumaYigini(cfg, &corehttp.DeferredAuthenticator{}, nil, slogYut())
	require.NoError(t, err)

	assert.Less(t, len(kapali), len(acik),
		"hız sınırı kapalıyken yığında daha az halka olmalı")

	// Kapalıyken bile korumanın kendisi durmalı.
	r := korumaliRouter(t, cfg, &corehttp.DeferredAuthenticator{})
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

// sahteKullanicilar tohum testlerinde dar auth yüzeyinin yerine geçer.
//
// Sahte kullanılmasının sebebi, tohumun sınanacak DAVRANIŞININ veritabanına
// hiç ihtiyaç duymamasıdır: karar tamamen "kaç kullanıcı var" sorusunun
// cevabına bağlıdır.
type sahteKullanicilar struct {
	// sayim ListUsers'ın döneceği toplam kullanıcı sayısıdır.
	sayim int64
	// listeHata ListUsers'ın döneceği hatadır.
	listeHata error
	// yaratHata CreateUser'ın döneceği hatadır.
	yaratHata error

	// listelendi ListUsers'ın hiç çağrılıp çağrılmadığını tutar.
	listelendi bool
	// yaratilan CreateUser'a verilen girdilerdir.
	yaratilan []authservice.CreateUserInput
	// parolalar CreateUser'a AYRI parametre olarak verilen parolalardır.
	parolalar []string
}

var _ yoneticiKullanicilari = (*sahteKullanicilar)(nil)

// ListUsers sayfa sayımını döner ve çağrıldığını kaydeder.
func (s *sahteKullanicilar) ListUsers(
	_ context.Context,
	_ authservice.ListUsersInput,
) (authservice.Page[authmodels.User], error) {
	s.listelendi = true
	if s.listeHata != nil {
		return authservice.Page[authmodels.User]{}, s.listeHata
	}

	return authservice.Page[authmodels.User]{Count: s.sayim, Limit: 1}, nil
}

// CreateUser girdiyi ve parolayı kaydedip sabit bir kullanıcı döner.
func (s *sahteKullanicilar) CreateUser(
	_ context.Context,
	in authservice.CreateUserInput,
	password string,
) (authmodels.User, error) {
	if s.yaratHata != nil {
		return authmodels.User{}, s.yaratHata
	}

	s.yaratilan = append(s.yaratilan, in)
	s.parolalar = append(s.parolalar, password)

	return authmodels.User{ID: "user_01TEST", Email: in.Email}, nil
}

// tohumluConfig tohumu etkinleştirilmiş bir yapılandırma döner.
func tohumluConfig() config.Config {
	cfg := temelConfig()
	cfg.AdminBootstrapEmail = "ilk.yonetici@ornek.com"
	cfg.AdminBootstrapPassword = "yeterince-uzun-parola"

	return cfg
}

// TestTohumlaYoneticiBosKurulumdaYaratir tohumun asıl işini doğrular: boş bir
// veritabanıyla açılan sunucuda ilk yönetici oluşur.
//
// Bu adım olmadan taze kurulum kullanılamaz durumdadır — yönetim uçları
// korumalıdır ve ilk yöneticiyi HTTP'den yaratmanın yolu yoktur.
func TestTohumlaYoneticiBosKurulumdaYaratir(t *testing.T) {
	t.Parallel()

	cfg := tohumluConfig()
	sahte := &sahteKullanicilar{sayim: 0}

	require.NoError(t, tohumlaYonetici(context.Background(), sahte, cfg, slogYut()))

	require.Len(t, sahte.yaratilan, 1, "boş kurulumda tam olarak bir yönetici yaratılmalı")
	assert.Equal(t, cfg.AdminBootstrapEmail, sahte.yaratilan[0].Email)
	assert.Equal(t, []string{cfg.AdminBootstrapPassword}, sahte.parolalar,
		"parola AYRI parametre olarak geçmeli; girdi yapısına konmamalı")
	// nil yetki listesi auth modülünde "tam yetki" demektir. Boş dilim
	// geçilseydi hiçbir yönetim ucuna erişemeyen bir hesap doğar ve sistem yine
	// kullanılamaz kalırdı.
	assert.Nil(t, sahte.yaratilan[0].Scopes,
		"ilk yönetici tam yetkili olmalı; yetki alanı verilmemeli")
}

// TestTohumlaYoneticiKullaniciVarkenAtlar yeniden başlatmanın güvenli
// olduğunu doğrular.
//
// Tohum var olan bir kurulumun yetkilerine ve parolasına dokunsaydı, ortam
// dosyasında unutulmuş bir ADMIN_BOOTSTRAP_PASSWORD her yeniden başlatmada
// üretim yöneticisinin parolasını sessizce geri alırdı.
func TestTohumlaYoneticiKullaniciVarkenAtlar(t *testing.T) {
	t.Parallel()

	sahte := &sahteKullanicilar{sayim: 1}

	require.NoError(t, tohumlaYonetici(context.Background(), sahte, tohumluConfig(), slogYut()))

	assert.True(t, sahte.listelendi, "atlama kararı sayımla verilmeli")
	assert.Empty(t, sahte.yaratilan, "kullanıcısı olan kuruluma yeni yönetici yazılmamalı")
}

// TestTohumlaYoneticiYapilandirilmamissaHicCalismaz tohumun İSTEĞE BAĞLI
// olduğunu doğrular: kurulmuş bir sistemin ortamında bu değişkenlerin durması
// gerekmez ve yoklukları veritabanına tek bir sorgu bile göndermemelidir.
func TestTohumlaYoneticiYapilandirilmamissaHicCalismaz(t *testing.T) {
	t.Parallel()

	sahte := &sahteKullanicilar{}

	require.NoError(t, tohumlaYonetici(context.Background(), sahte, temelConfig(), slogYut()))

	assert.False(t, sahte.listelendi, "tohum kapalıyken kullanıcı sayımı bile yapılmamalı")
	assert.Empty(t, sahte.yaratilan)
}

// TestTohumlaYoneticiHatayiYukariTasir tohum arızasının SESSİZ geçilmediğini
// doğrular.
//
// Yaratılamamış bir yönetici, yönetim yüzeyi olmayan bir sistem demektir; hata
// yutulsaydı sunucu açılır ama hiçbir yönetim isteğini kabul edemezdi ve arıza
// ancak ilk giriş denemesinde görülürdü.
func TestTohumlaYoneticiHatayiYukariTasir(t *testing.T) {
	t.Parallel()

	tests := map[string]*sahteKullanicilar{
		"sayım okunamıyor":      {listeHata: errors.Unavailable("db_down", "veritabanı yok")},
		"kullanıcı yazılamıyor": {yaratHata: errors.Conflict("auth_conflict", "e-posta kullanımda")},
	}

	for ad, sahte := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			err := tohumlaYonetici(context.Background(), sahte, tohumluConfig(), slogYut())

			require.Error(t, err, "tohum arızası açılışı durdurmalı")
			assert.Equal(t, "admin_bootstrap_failed", errors.CodeOf(err),
				"hata tohum adımına ait bir kodla sarmalanmalı")
		})
	}
}

// TestKorumaYiginiRedisSecilipIstemciYoksaDurur yarım yapılandırmanın sessizce
// bellek içine düşmediğini doğrular.
//
// Düşseydi operatör paylaşılan bir idempotency deposu istediğini sanır, oysa
// her örnek kendi kaydını tutar ve aynı anahtarla gelen iki istek iki kez
// işlenirdi — yani iki sipariş. Bu, ancak üretimde ve yük altında görülürdü.
func TestKorumaYiginiRedisSecilipIstemciYoksaDurur(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.GuardBackend = "redis"

	_, err := korumaYigini(cfg, &corehttp.DeferredAuthenticator{}, nil, slogYut())

	require.Error(t, err, "Redis istemcisi olmadan redis arka ucu kurulmamalı")
	assert.Contains(t, err.Error(), "GUARD_BACKEND")
}
