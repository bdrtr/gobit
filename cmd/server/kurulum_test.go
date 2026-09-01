package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"log/slog"

	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
	authservice "github.com/bdrtr/gobit/internal/modules/auth/service"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
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
		RedisKeyPrefix:     config.DefaultRedisKeyPrefix,
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

// kayitYakalayici üretilen log kayıtlarını toplayan bir slog handler'ıdır.
//
// Açılış uyarılarının tek işi GÖRÜNMEKTİR: hiçbir davranışı değiştirmezler,
// bu yüzden başka hiçbir iddiayla sınanamazlar. Sessizce silinen ya da kapısı
// yanlış kurulan bir uyarı hiçbir testi düşürmezdi — oysa o uyarı, kurulumu
// sessizce bozan bir ayarın tek fark edilme şansıdır.
type kayitYakalayici struct {
	mu       sync.Mutex
	kayitlar []slog.Record
}

// Enabled her seviyeyi kabul eder; test hangi seviyenin seçildiğini kendisi
// denetler.
func (k *kayitYakalayici) Enabled(context.Context, slog.Level) bool { return true }

// Handle kaydı kopyalayarak saklar.
func (k *kayitYakalayici) Handle(_ context.Context, kayit slog.Record) error {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.kayitlar = append(k.kayitlar, kayit.Clone())

	return nil
}

// WithAttrs aynı yakalayıcıyı döner; testler öznitelikleri kayıttan okur.
func (k *kayitYakalayici) WithAttrs([]slog.Attr) slog.Handler { return k }

// WithGroup aynı yakalayıcıyı döner.
func (k *kayitYakalayici) WithGroup(string) slog.Handler { return k }

// logger yakalayıcıya yazan bir logger döner.
func (k *kayitYakalayici) logger() *slog.Logger { return slog.New(k) }

// mesajlar verilen seviyedeki kayıtların mesajlarını döner.
func (k *kayitYakalayici) mesajlar(seviye slog.Level) []string {
	k.mu.Lock()
	defer k.mu.Unlock()

	var bulunan []string
	for i := range k.kayitlar {
		if k.kayitlar[i].Level == seviye {
			bulunan = append(bulunan, k.kayitlar[i].Message)
		}
	}

	return bulunan
}

// oznitelik verilen mesajı taşıyan ilk kaydın bir özniteliğini dize olarak döner.
func (k *kayitYakalayici) oznitelik(mesaj, ad string) string {
	k.mu.Lock()
	defer k.mu.Unlock()

	for i := range k.kayitlar {
		if k.kayitlar[i].Message != mesaj {
			continue
		}

		deger := ""
		k.kayitlar[i].Attrs(func(a slog.Attr) bool {
			if a.Key == ad {
				deger = a.Value.String()

				return false
			}

			return true
		})

		return deger
	}

	return ""
}

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

// TestTohumlaYoneticiYapilandirilmamissaYaratmaz tohumun İSTEĞE BAĞLI
// olduğunu doğrular: KURULMUŞ bir sistemin ortamında bu değişkenlerin durması
// gerekmez ve yoklukları hiçbir kullanıcı yaratmamalıdır.
//
// Sayım yine de OKUNUR ve bu bilinçli bir değişikliktir: "kurulmuş mu"
// sorusunun cevabı yalnızca oradadır ve sorulmadığında sıfır kullanıcılı bir
// kurulum sessizce açılıyordu (bkz. [yonetimsizKurulumuBildir]).
func TestTohumlaYoneticiYapilandirilmamissaYaratmaz(t *testing.T) {
	t.Parallel()

	sahte := &sahteKullanicilar{sayim: 3}

	require.NoError(t, tohumlaYonetici(context.Background(), sahte, temelConfig(), slogYut()))

	assert.True(t, sahte.listelendi, "kurulumun yönetilebilir olduğu sayımla anlaşılır")
	assert.Empty(t, sahte.yaratilan, "tohum kapalıyken kullanıcı yaratılmamalı")
}

// TestYonetimsizKurulumPaylasilanOrtamdaAcilisiDurdurur sıfır kullanıcılı ve
// tohumsuz bir kurulumun SESSİZCE açılmadığını doğrular.
//
// O kurulumda yönetim yüzeyi giriş ucu dışında tamamen korumalıdır ve ilk
// kullanıcıyı HTTP'den yaratmanın yolu yoktur; mağaza yüzeyi de kapalıdır,
// çünkü publishable anahtarı da yönetim ucu üretir. Sunucu yine de açılır,
// /health ve /ready yeşil döner — yani hiçbir denetim olmadan arıza ilk giriş
// denemesine kadar görünmez.
func TestYonetimsizKurulumPaylasilanOrtamdaAcilisiDurdurur(t *testing.T) {
	t.Parallel()

	for _, ortam := range []string{"staging", "production"} {
		t.Run(ortam, func(t *testing.T) {
			t.Parallel()

			cfg := temelConfig()
			cfg.AppEnv = ortam
			sahte := &sahteKullanicilar{sayim: 0}

			err := tohumlaYonetici(context.Background(), sahte, cfg, slogYut())

			require.Error(t, err, "yönetilemez bir kurulum sessizce açılmamalı")
			assert.Equal(t, "admin_bootstrap_required", errors.CodeOf(err),
				"hata, hangi ayarın eksik olduğunu bildiren kendi koduyla dönmeli")
			assert.Contains(t, err.Error(), "ADMIN_BOOTSTRAP_EMAIL",
				"mesaj operatöre hangi değişkenleri vereceğini söylemeli")
		})
	}
}

// TestYonetimsizKurulumGelistirmedeAcilir aynı durumun yerel geliştirmede
// açılışı DURDURMADIĞINI doğrular.
//
// Deponun sözü ".env olmadan da make up && make run çalışır"dır ve taze bir
// veritabanıyla ilk kez açan geliştirici tam olarak bu hâle düşer. Ayrım
// JWT_SECRET'inkiyle aynıdır: geliştirmede uyarı, paylaşılan ortamda ret.
func TestYonetimsizKurulumGelistirmedeAcilir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.AppEnv = "development"
	sahte := &sahteKullanicilar{sayim: 0}

	require.NoError(t, tohumlaYonetici(context.Background(), sahte, cfg, slogYut()),
		"yerel geliştirme ek ayar istemeden açılabilmeli")
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

	// ÇAKIŞMA burada YOKTUR ve bu bilinçlidir: o durum bir arıza değil,
	// eşzamanlı açılış yarışıdır ve ayrıca sınanır (bkz.
	// [TestTohumlaYoneticiEszamanliYarisiYutar]).
	tests := map[string]*sahteKullanicilar{
		"sayım okunamıyor":      {listeHata: errors.Unavailable("db_down", "veritabanı yok")},
		"kullanıcı yazılamıyor": {yaratHata: errors.Unavailable("db_down", "veritabanı yok")},
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

// TestHizSiniriKapaliykenPaylasilanOrtamdaUyarilir sınırlayıcının HİÇ
// kurulmadığı hâlin bildirildiğini doğrular.
//
// RATE_LIMIT_PER_MINUTE <= 0 meşru bir seçimdir (ADR 0007'de sıfır "kapat"
// demektir) ama tek satır iz bırakmadığında, kazayla yazılmış bir sıfırdan
// ayırt edilemez: giriş ucu da dâhil hiçbir uç kotalı değildir.
func TestHizSiniriKapaliykenPaylasilanOrtamdaUyarilir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.AppEnv = "production"
	cfg.RateLimitPerMinute = 0
	yakalayici := &kayitYakalayici{}

	_, err := korumaYigini(cfg, gecerliKimlik{}, nil, yakalayici.logger())

	require.NoError(t, err)
	assert.Contains(t, yakalayici.mesajlar(slog.LevelWarn), "hız sınırlayıcı TAKILMADI",
		"kapalı bir hız sınırı paylaşılan ortamda sessiz kalmamalı")
}

// TestHizSiniriProxyArkasindaAnahtarUyarisiVerir kotanın istemci başına
// düşmediği hâlin bildirildiğini doğrular.
//
// TRUSTED_PROXY_HOPS=0 iken X-Forwarded-For hiç okunmaz ve anahtar bağlantının
// adresine düşer; ters proxy arkasında o adres her istekte proxy'nindir, yani
// kota tüm mağaza için tek bir kovadır.
func TestHizSiniriProxyArkasindaAnahtarUyarisiVerir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.AppEnv = "production"
	cfg.RateLimitPerMinute = 600
	cfg.TrustedProxyHops = 0
	yakalayici := &kayitYakalayici{}

	_, err := korumaYigini(cfg, gecerliKimlik{}, nil, yakalayici.logger())

	require.NoError(t, err, "uyarı açılışı DURDURMAMALI: sıfır atlama, doğrudan "+
		"internete bakan bir kurulumda doğru cevaptır")
	assert.Contains(t, yakalayici.mesajlar(slog.LevelWarn),
		"hız sınırı anahtarı istemciye DEĞİL bağlantıya düşüyor")
}

// TestHizSiniriDogruKuruldugundaSessizdir uyarının GÜRÜLTÜ olmadığını
// doğrular.
//
// Her açılışta basılan bir uyarı, gerçek bir uyarıyı gürültüde boğar; kapının
// kapalı kaldığı hâller bu yüzden ayrıca sabitlenir.
func TestHizSiniriDogruKuruldugundaSessizdir(t *testing.T) {
	t.Parallel()

	tests := map[string]func(c *config.Config){
		"proxy atlaması verilmiş": func(c *config.Config) {
			c.AppEnv = "production"
			c.TrustedProxyHops = 1
		},
		"yerel geliştirme, sınır kapalı": func(c *config.Config) {
			c.AppEnv = "development"
			c.RateLimitPerMinute = 0
		},
		"yerel geliştirme, proxy yok": func(c *config.Config) {
			c.AppEnv = "development"
			c.TrustedProxyHops = 0
		},
	}

	for ad, ayarla := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			cfg := temelConfig()
			ayarla(&cfg)
			yakalayici := &kayitYakalayici{}

			_, err := korumaYigini(cfg, gecerliKimlik{}, nil, yakalayici.logger())

			require.NoError(t, err)
			// Bellek içi koruma uyarısı bu kapının DIŞINDADIR ve paylaşılan
			// ortamda basılmaya devam eder; burada yalnızca hız sınırına ait
			// iki mesaj aranır.
			uyarilar := yakalayici.mesajlar(slog.LevelWarn)
			assert.NotContains(t, uyarilar, "hız sınırlayıcı TAKILMADI",
				"doğru kurulmuş bir hız sınırı için uyarı basılmamalı")
			assert.NotContains(t, uyarilar, "hız sınırı anahtarı istemciye DEĞİL bağlantıya düşüyor",
				"doğru kurulmuş bir hız sınırı için uyarı basılmamalı")
		})
	}
}

// TestOlayVeriYoluAdAlaniniAnahtarOnegindenAlir Redis veri yolunun sıfır
// değerli bir yapılandırmayla KURULMADIĞINI doğrular.
//
// Sıfır değerli RedisConfig, stream önekini de consumer group'u da paketin
// varsayılanına düşürür ve REDIS_KEY_PREFIX olay tarafına HİÇ ulaşmaz: aynı
// Redis'i paylaşan iki kurulumun koruma anahtarları ayrılır, olayları
// ayrılmaz. En ağır sonucu grubun paylaşılmasıdır — bir olayı iki kurulumdan
// yalnızca biri alır.
//
// İddia LOG üzerinden kurulur çünkü kurulan yapılandırma [eventbus.EventBus]
// arayüzünün arkasında kalır; log satırının kendisi de gereklidir (bkz.
// [eventbus.ConsumerName]).
func TestOlayVeriYoluAdAlaniniAnahtarOnegindenAlir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.EventBus = config.BackendRedis
	cfg.RedisKeyPrefix = "gobit-staging"
	cfg.EventBusConsumer = "gobit-0"
	yakalayici := &kayitYakalayici{}

	bus, err := setupEventBus(context.Background(), cfg, yapayRedis(), yakalayici.logger())

	require.NoError(t, err)
	require.NotNil(t, bus)

	const mesaj = "olay veri yolu: Redis Streams"
	assert.Equal(t, "gobit-staging:events", yakalayici.oznitelik(mesaj, "stream_oneki"))
	assert.Equal(t, "gobit-staging", yakalayici.oznitelik(mesaj, "grup"),
		"consumer group da ayrılmalı; ayrılmazsa olayı iki kurulumdan yalnızca biri alır")
	assert.Equal(t, "gobit-0", yakalayici.oznitelik(mesaj, "tuketici"))
}

// TestOlayVeriYoluTuketiciAdiVerilmezseUretilir çözülen tüketici adının her
// hâlde loglandığını doğrular.
//
// Aynı adı iki sürece vermek sessizce çift işlemeye yol açar ve doğrulama bunu
// göremez; iki açılış logunu yan yana koymak tek fark edilme şansıdır. Ad
// loglanmasaydı o şans da olmazdı.
func TestOlayVeriYoluTuketiciAdiVerilmezseUretilir(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.EventBus = config.BackendRedis
	yakalayici := &kayitYakalayici{}

	_, err := setupEventBus(context.Background(), cfg, yapayRedis(), yakalayici.logger())

	require.NoError(t, err)
	assert.NotEmpty(t, yakalayici.oznitelik("olay veri yolu: Redis Streams", "tuketici"),
		"tüketici adı loglanmazsa iki sürecin aynı adı kullandığı hiç görülemez")
}

// TestBellekIciOlayVeriYoluPaylasilanOrtamdaUyarir kalıcı olmayan veri
// yolunun paylaşılan ortamda sessiz kalmadığını doğrular.
//
// Bedeli GUARD_BACKEND=memory ile aynı sınıftadır ve o zaten uyarıyordu;
// ikisinin farklı seviyede loglanması, aynı ödünün birinde görünüp ötekinde
// görünmemesi demekti.
func TestBellekIciOlayVeriYoluPaylasilanOrtamdaUyarir(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		ortam  string
		seviye slog.Level
	}{
		"üretim":           {"production", slog.LevelWarn},
		"staging":          {"staging", slog.LevelWarn},
		"yerel geliştirme": {"development", slog.LevelInfo},
	}

	for ad, tt := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			cfg := temelConfig()
			cfg.AppEnv = tt.ortam
			cfg.EventBus = "inmemory"
			yakalayici := &kayitYakalayici{}

			_, err := setupEventBus(context.Background(), cfg, nil, yakalayici.logger())

			require.NoError(t, err)
			assert.Contains(t, yakalayici.mesajlar(tt.seviye), "olay veri yolu: bellek içi (tek süreç)")
		})
	}
}

// yapayRedis bağlanmayan ama nil de olmayan bir Redis istemcisi üretir.
//
// go-redis bağlantıyı ilk komutta kurar; redisguard kurucuları yalnızca ayar
// doğrular, hiç komut çalıştırmaz. Böylece Redis yolunun KURULUM mantığı
// Docker'sız sınanabilir.
func yapayRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
}

// TestKorumaYiginiAnahtarOneginiKuruculariGecirir önekin yapılandırmadan
// redisguard kurucularına GERÇEKTEN ulaştığını doğrular.
//
// Kurucular önek geçmezse hiçbir şey patlamaz: her kurulum sessizce aynı ad
// alanına yazmayı sürdürür, yani aynı Redis'i paylaşan iki kurulum
// birbirinin hız sınırı kotasını harcar ve birbirinin idempotency kaydını
// okur. Test bunu, kurucuların REDDEDECEĞİ bir önek vererek yakalar: hata
// dönmüyorsa önek yolda kaybolmuş demektir.
//
// config.Validate aynı biçimi zaten zorluyor; buradaki tekrar boşuna değil,
// çünkü yığın Config'i elle kuran çağıranlarla (testler, gömen kurulumlar)
// da çağrılabilir.
func TestKorumaYiginiAnahtarOneginiKuruculariGecirir(t *testing.T) {
	t.Parallel()

	for ad, onek := range map[string]string{
		"boş önek":       "",
		"ayırıcı içeren": "gobit:staging",
	} {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			cfg := temelConfig()
			cfg.GuardBackend = config.BackendRedis
			cfg.RedisKeyPrefix = onek

			_, err := korumaYigini(cfg, &corehttp.DeferredAuthenticator{}, yapayRedis(), slogYut())

			require.Error(t, err, "geçersiz önek kurucuya ulaşıp açılışı durdurmalı")
			assert.Equal(t, "redisguard_invalid_config", errors.CodeOf(err),
				"hata redisguard kurucusundan gelmeli")
		})
	}
}

// TestKorumaYiginiRedisArkaUcunuKurar geçerli bir önekle Redis yolunun sonuna
// kadar kurulduğunu doğrular.
//
// Yalnızca hata yolunu sınamak yanıltıcı olurdu: her öneki reddeden bir
// kurucu da o testi geçerdi.
func TestKorumaYiginiRedisArkaUcunuKurar(t *testing.T) {
	t.Parallel()

	cfg := temelConfig()
	cfg.GuardBackend = config.BackendRedis
	cfg.RedisKeyPrefix = "gobit-staging"

	yigin, err := korumaYigini(cfg, &corehttp.DeferredAuthenticator{}, yapayRedis(), slogYut())

	require.NoError(t, err, "geçerli önekle redis arka ucu kurulabilmeli")
	assert.NotEmpty(t, yigin, "koruma yığını boş dönmemeli")
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

// TestTohumlaYoneticiEszamanliYarisiYutar birden çok örneğin boş bir
// veritabanına AYNI ANDA açılmasının açılışı düşürmediğini doğrular.
//
// Yarış gerçektir ve gerçek sunucuyla gözlemlenmiştir: üç örnek eşzamanlı
// açıldığında üçü de "hiç kullanıcı yok" görür, üçü de yaratmayı dener ve
// e-posta benzersizliği ikisini reddeder. Çakışmayı hata saymak, üç kopyalı
// ilk dağıtımda ikisinin yeniden başlatma döngüsüne girmesi demekti.
//
// Testin sınadığı şey "hata yutuluyor mu" değil, İSTENEN SON DURUMUN
// sağlanmış olmasıdır: kaybeden örnek için de bir yönetici vardır.
func TestTohumlaYoneticiEszamanliYarisiYutar(t *testing.T) {
	t.Parallel()

	sahte := &sahteKullanicilar{
		sayim: 0,
		yaratHata: errors.Conflict("auth_email_taken",
			"%q e-postası zaten kullanılıyor", "ilk.yonetici@ornek.com"),
	}

	err := tohumlaYonetici(context.Background(), sahte, tohumluConfig(), slogYut())

	assert.NoError(t, err, "eşzamanlı tohum çakışması açılışı DÜŞÜRMEMELİ")
}

// TestTohumlaYoneticiCakismaDisindakiHatayiYutmaz yutmanın yalnızca ÇAKIŞMAYA
// özel olduğunu doğrular.
//
// Bağlantı hatası ya da geçersiz parola gibi durumlarda istenen son durum
// sağlanMAMIŞTIR: yönetici yoktur ve sistem kullanılamaz. Onları da yutmak,
// yöneticisiz bir kurulumu sessizce sağlıklı göstermek olurdu.
func TestTohumlaYoneticiCakismaDisindakiHatayiYutmaz(t *testing.T) {
	t.Parallel()

	testler := map[string]error{
		"geçersiz girdi": errors.Invalid("auth_weak_password", "parola çok kısa"),
		"alt sistem":     errors.Unavailable("db_unreachable", "veritabanına ulaşılamadı"),
		"iç hata":        errors.Internal("beklenmeyen", "beklenmeyen hata"),
	}

	for ad, hata := range testler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			sahte := &sahteKullanicilar{sayim: 0, yaratHata: hata}

			err := tohumlaYonetici(context.Background(), sahte, tohumluConfig(), slogYut())

			require.Error(t, err, "çakışma dışındaki hata açılışı durdurmalı")
			assert.Contains(t, err.Error(), "ilk yönetici oluşturulamadı")
		})
	}
}

// sahteBildirimKaydi bildirim sağlayıcı kaydının test karşılığıdır.
//
// Gerçek kaydı (notification/service.ProviderRegistry) kurmak da mümkündü ama
// yanlış olurdu: denetlenen şey KURULUMUN davranışıdır — bilinmeyen bir adı
// nasıl karşıladığı — ve o davranış, kaydın somut tipinden bağımsızdır.
// Sahte, kurulumun dar arayüzünü karşılar.
type sahteBildirimKaydi struct {
	kimlikler []string
}

func (s *sahteBildirimKaydi) Get(id string) (coreprovider.NotificationProvider, error) {
	if slices.Contains(s.kimlikler, id) {
		return nil, nil //nolint:nilnil // kurulum yalnızca HATAYA bakar, sağlayıcıyı kullanmaz
	}

	return nil, errors.NotFound("notification_provider_not_found",
		"%q bildirim sağlayıcısı kayıtlı değil", id)
}

func (s *sahteBildirimKaydi) IDs() []string { return s.kimlikler }

// bildirimContainer verilen kayıtla dolu bir container üretir.
func bildirimContainer(t *testing.T, kayit *sahteBildirimKaydi) *container.Container {
	t.Helper()

	c := container.New(slogYut())
	require.NoError(t, c.Provide(notification.ProvidersName, kayit))

	return c
}

// TestBildirimSaglayicisiBilinmeyenAdiReddeder yanlış yapılandırılmış bir
// kurulumun AÇILMADIĞINI doğrular.
//
// Sessizce varsayılana ("log") düşmek en kötü seçenekti: kurulum açılır,
// hiçbir hata görünmez ve sipariş onayları yalnızca loga yazılır — arıza
// ancak müşteriler onay beklerken, genellikle günler sonra fark edilir.
func TestBildirimSaglayicisiBilinmeyenAdiReddeder(t *testing.T) {
	t.Parallel()

	c := bildirimContainer(t, &sahteBildirimKaydi{kimlikler: []string{"log"}})

	err := bildirimSaglayicisiniDogrula(c, "sendgrid")

	require.Error(t, err, "bilinmeyen sağlayıcı adı açılışı DURDURMALI")
	assert.Equal(t, codeUnknownNotificationProvider, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "sendgrid", "reddedilen ad yazılmalı")
	assert.Contains(t, err.Error(), "log", "kayıtlı adlar yazılmalı ki yazım hatası görünsün")
	assert.Contains(t, err.Error(), "PLUGINS",
		"eklentiden gelen bir adı unutan operatöre yol gösterilmeli")
}

// TestBildirimSaglayicisiKayitliAdiKabulEder eklentiden gelen bir adın
// geçtiğini doğrular.
//
// Denetim Start'tan SONRA çalışır; daha erken bir kapı, eklentiden gelen
// GEÇERLİ bir adı "bilinmiyor" diye reddederdi.
func TestBildirimSaglayicisiKayitliAdiKabulEder(t *testing.T) {
	t.Parallel()

	c := bildirimContainer(t, &sahteBildirimKaydi{kimlikler: []string{"log", "sendgrid"}})

	assert.NoError(t, bildirimSaglayicisiniDogrula(c, "sendgrid"))
	assert.NoError(t, bildirimSaglayicisiniDogrula(c, config.DefaultNotificationProvider))
}

// TestBildirimSaglayicisiKayitYoksaDurur notification modülü hiç kurulmamışsa
// açılışın sessizce devam etmediğini doğrular.
//
// Kayıt yoksa hiçbir bildirim gönderilemez; "sağlayıcı seçtim" diyen bir
// yapılandırmayla açılmak, olmayan bir yeteneği varmış gibi göstermek olurdu.
func TestBildirimSaglayicisiKayitYoksaDurur(t *testing.T) {
	t.Parallel()

	err := bildirimSaglayicisiniDogrula(container.New(slogYut()), "log")

	require.Error(t, err)
	assert.Equal(t, codeUnknownNotificationProvider, errors.CodeOf(err))
	assert.Contains(t, err.Error(), notification.ProvidersName)
}

// gecerliKimlik her isteği kabul eden doğrulayıcıdır.
//
// Bu dosyadaki diğer testler bağlanmamış doğrulayıcıyla çalışır çünkü
// iddiaları REDDEDİLME üzerinedir; idempotency halkasına ulaşmak ise kimliğin
// GEÇMESİNİ gerektirir (halka kimlikten sonradır).
type gecerliKimlik struct{}

// AuthenticateAdmin sabit bir yönetici kimliği döner.
func (gecerliKimlik) AuthenticateAdmin(_ context.Context, _, _ string) (corehttp.Principal, error) {
	return corehttp.Principal{ID: "usr_1", Kind: "user"}, nil
}

// AuthenticateStore sabit bir mağaza kimliği döner.
func (gecerliKimlik) AuthenticateStore(_ context.Context, _ string) (corehttp.Principal, error) {
	return corehttp.Principal{ID: "pk_1", Kind: "api_key"}, nil
}

// TestKorumaYiginiGraphQLUcunuIdempotencydenMuafTutar muafiyetin GERÇEK yığında
// ve modülün SABİTİNDEN uygulandığını doğrular.
//
// Muafiyetin yeri burasıdır çünkü çekirdek yolu bilemez (modülleri import
// edemez); yol bileşim kökünden geçer ve elle yazılmış bir dize olsaydı,
// graph.Path değiştiği gün muafiyet sessizce düşerdi — GraphQL ucu yeniden
// kaydedilmeye başlar ve kimse fark etmezdi.
//
// Handler, GraphQL sözleşmesinin ölçülen davranışını taklit eder: iç hatada da
// 200 döner. Idempotency'nin "5xx kaydedilmez" koruması bu yüzden burada hiç
// devreye girmez; muafiyet olmasaydı geçici arıza IDEMPOTENCY_TTL boyunca
// çalınırdı.
func TestKorumaYiginiGraphQLUcunuIdempotencydenMuafTutar(t *testing.T) {
	t.Parallel()

	yigin, err := korumaYigini(temelConfig(), gecerliKimlik{}, nil, slogYut())
	require.NoError(t, err)

	arizali := true

	r := corehttp.NewRouter(corehttp.RouterOptions{Version: "test", Middlewares: yigin})
	r.Post(graph.Path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)

		if arizali {
			_, _ = w.Write([]byte(`{"errors":[{"message":"beklenmeyen bir sunucu hatası oluştu"}]}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":{"products":{"count":42}}}`))
	})

	istekYap := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, graph.Path,
			strings.NewReader(`{"query":"{ products { count } }"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(corehttp.PublishableKeyHeader, "pk_test")
		req.Header.Set(corehttp.IdempotencyKeyHeader, "ayni-anahtar")

		return req
	}

	ilk := httptest.NewRecorder()
	r.ServeHTTP(ilk, istekYap())
	require.Equal(t, http.StatusOK, ilk.Code)
	require.Contains(t, ilk.Body.String(), "sunucu hatası")

	arizali = false

	ikinci := httptest.NewRecorder()
	r.ServeHTTP(ikinci, istekYap())

	assert.Empty(t, ikinci.Header().Get(corehttp.IdempotencyReplayedHeader),
		"GraphQL ucu kaydedilmez, dolayısıyla oynatılamaz")
	assert.Contains(t, ikinci.Body.String(), `"count":42`,
		"arıza giderildikten sonra istemci GÜNCEL yanıtı almalı")
}
