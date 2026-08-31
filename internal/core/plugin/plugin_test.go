package plugin_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// sahteSaglayici testlerde kullanılan en küçük ödeme sağlayıcısıdır.
type sahteSaglayici struct{ id string }

// ID sağlayıcının kimliğini döner.
func (p sahteSaglayici) ID() string { return p.id }

// CreateSession testte çağrılmaz; arayüzü karşılamak için vardır.
func (p sahteSaglayici) CreateSession(
	_ context.Context, _ coreprovider.CreateSessionInput,
) (coreprovider.Session, error) {
	return coreprovider.Session{}, nil
}

// Authorize testte çağrılmaz.
func (p sahteSaglayici) Authorize(
	_ context.Context, _ string,
) (coreprovider.AuthResult, error) {
	return coreprovider.AuthResult{}, nil
}

// Capture testte çağrılmaz.
func (p sahteSaglayici) Capture(_ context.Context, _ string, _ int64) error { return nil }

// Refund testte çağrılmaz.
func (p sahteSaglayici) Refund(_ context.Context, _ string, _ int64) error { return nil }

// Cancel testte çağrılmaz.
func (p sahteSaglayici) Cancel(_ context.Context, _ string) error { return nil }

// sahteKayit payment modülünün sağlayıcı kaydını taklit eder.
type sahteKayit struct {
	kayitli []string
	err     error
}

// Register sağlayıcıyı kaydeder ya da yapılandırılmış hatayı döner.
func (k *sahteKayit) Register(p coreprovider.PaymentProvider) error {
	if k.err != nil {
		return k.err
	}

	k.kayitli = append(k.kayitli, p.ID())

	return nil
}

// sahteBildirimSaglayici testlerde kullanılan en küçük bildirim sağlayıcısıdır.
type sahteBildirimSaglayici struct{ id string }

// ID sağlayıcının kimliğini döner.
func (p sahteBildirimSaglayici) ID() string { return p.id }

// Send testte çağrılmaz; arayüzü karşılamak için vardır.
func (p sahteBildirimSaglayici) Send(_ context.Context, _ coreprovider.Notification) error {
	return nil
}

// sahteBildirimKayit notification modülünün sağlayıcı kaydını taklit eder.
type sahteBildirimKayit struct {
	kayitli []string
}

// Register sağlayıcıyı kaydeder.
func (k *sahteBildirimKayit) Register(p coreprovider.NotificationProvider) error {
	k.kayitli = append(k.kayitli, p.ID())

	return nil
}

// testEklenti Setup'ta verilen işlevi çalıştıran eklentidir.
type testEklenti struct {
	ad    string
	setup func(ctx context.Context, h *coreplugin.Host) error
}

// Name eklentinin adını döner.
func (e testEklenti) Name() string { return e.ad }

// Setup yapılandırılmış işlevi çalıştırır.
func (e testEklenti) Setup(ctx context.Context, h *coreplugin.Host) error {
	if e.setup == nil {
		return nil
	}

	return e.setup(ctx, h)
}

// kurulum test için container, kayıt ve host üçlüsünü hazırlar.
func kurulum(t *testing.T, ayarlar map[string]string) (
	*container.Container, *coreplugin.Registry, *coreplugin.Host,
) {
	t.Helper()

	log := slog.New(slog.DiscardHandler)
	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	return c, coreplugin.NewRegistry(log), coreplugin.NewHost(c, nil, nil, log, ayarlar)
}

// TestSaglayiciKaydiStartaKadarBeklenir kaydın Setup'ta DEĞİL Start'ta
// uygulandığını doğrular.
//
// Setup'ta uygulansaydı, payment modülü henüz kayıtlı olmadığı için her
// sağlayıcı eklentisi kurulumda patlardı; sıralamayı doğru kurmak eklentinin
// değil çekirdeğin işidir.
func TestSaglayiciKaydiStartaKadarBeklenir(t *testing.T) {
	t.Parallel()

	c, reg, h := kurulum(t, nil)

	reg.Add(testEklenti{ad: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(sahteSaglayici{id: "stripe"})
		return nil
	}})

	// payment modülü HENÜZ yok.
	require.NoError(t, reg.Install(t.Context(), h), "kurulum modül olmadan da geçmeli")

	kayit := &sahteKayit{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, kayit))

	require.NoError(t, reg.Start(t.Context(), h))
	assert.Equal(t, []string{"stripe"}, kayit.kayitli)
}

// TestSaglayiciKaydiModulYoksaHataDoner payment modülü hiç kayıtlı değilken
// Start'ın SESSİZ KALMADIĞINI doğrular.
//
// Sessiz kalsaydı, "stripe eklentisi kurulu" sanılan bir kurulum hiç ödeme
// alamaz ve bu ancak ilk müşteri denemesinde fark edilirdi.
func TestSaglayiciKaydiModulYoksaHataDoner(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)

	reg.Add(testEklenti{ad: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(sahteSaglayici{id: "stripe"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "payment")
	assert.Contains(t, err.Error(), "stripe")
}

// TestSaglayiciKayitHatasiYayilir modülün kaydı reddetmesinin sessizce
// yutulmadığını doğrular (örn. aynı kimlikle iki sağlayıcı).
func TestSaglayiciKayitHatasiYayilir(t *testing.T) {
	t.Parallel()

	c, reg, h := kurulum(t, nil)
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName,
		&sahteKayit{err: errors.New("aynı kimlik zaten kayıtlı")}))

	reg.Add(testEklenti{ad: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(sahteSaglayici{id: "stripe"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "aynı kimlik zaten kayıtlı")
}

// TestBildirimSaglayiciKaydiStartaKadarBeklenir bildirim sağlayıcısının da
// Setup'ta DEĞİL Start'ta kaydedildiğini doğrular.
//
// Ödeme sağlayıcısındaki testin kopyası değildir: kuyruğa alma her sağlayıcı
// türü için AYRI yazılmıştır ve bu tür, kaydı doğrudan uygulayan bir kısayolla
// eklenmeye en açık olanıdır — bildirim modülü ödeme kadar erken ayağa
// kalkmadığı için hata ancak gerçek kurulumda görülürdü.
func TestBildirimSaglayiciKaydiStartaKadarBeklenir(t *testing.T) {
	t.Parallel()

	c, reg, h := kurulum(t, nil)

	reg.Add(testEklenti{ad: "postaci", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterNotificationProvider(sahteBildirimSaglayici{id: "smtp"})
		return nil
	}})

	// notification modülü HENÜZ yok.
	require.NoError(t, reg.Install(t.Context(), h), "kurulum modül olmadan da geçmeli")

	kayit := &sahteBildirimKayit{}
	require.NoError(t, c.Provide(coreplugin.NotificationProvidersName, kayit))

	require.NoError(t, reg.Start(t.Context(), h))
	assert.Equal(t, []string{"smtp"}, kayit.kayitli)
}

// TestBildirimSaglayiciKaydiModulYoksaHataDoner notification modülü hiç kayıtlı
// değilken Start'ın SESSİZ KALMADIĞINI doğrular.
//
// Sessiz kalsaydı arıza ödeme sağlayıcısındakinden daha geç fark edilirdi:
// bildirim gönderilmemesi hiçbir HTTP isteğini düşürmez, yalnızca müşteri
// sipariş e-postasını hiç almaz.
func TestBildirimSaglayiciKaydiModulYoksaHataDoner(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)

	reg.Add(testEklenti{ad: "postaci", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterNotificationProvider(sahteBildirimSaglayici{id: "smtp"})
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "notification")
	assert.Contains(t, err.Error(), "smtp")
}

// TestSetupHatasiKurulumuDurdurur bir eklentinin Setup hatasının sonrakileri
// çalıştırmadığını ve hangi eklentinin patladığını söylediğini doğrular.
func TestSetupHatasiKurulumuDurdurur(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)

	sonrakiCalisti := false

	reg.Add(testEklenti{ad: "bozuk", setup: func(_ context.Context, _ *coreplugin.Host) error {
		return coreerrors.Invalid("eksik_ayar", "STRIPE_API_KEY verilmemiş")
	}})
	reg.Add(testEklenti{ad: "sonraki", setup: func(_ context.Context, _ *coreplugin.Host) error {
		sonrakiCalisti = true
		return nil
	}})

	err := reg.Install(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bozuk")
	assert.Contains(t, err.Error(), "STRIPE_API_KEY")
	assert.False(t, sonrakiCalisti, "hatalı eklentiden sonra kurulum durmalı")
}

// TestAyniAdliEklentiReddedilir tekrarlanan eklenti adının yakalandığını
// doğrular.
func TestAyniAdliEklentiReddedilir(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	reg.Add(testEklenti{ad: "stripe"})
	reg.Add(testEklenti{ad: "stripe"})

	err := reg.Install(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tekrarland")
}

// TestBosAdliEklentiReddedilir adsız eklentinin yakalandığını doğrular.
func TestBosAdliEklentiReddedilir(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	reg.Add(testEklenti{ad: "   "})

	err := reg.Install(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boş")
}

// TestSettingBosDegeriVerilmemisSayar tanımlı ama boş bir ayarın "yok"
// sayıldığını doğrular.
//
// Aksi hâlde boş bir API anahtarıyla çalışmaya başlanır ve arıza ancak ilk
// gerçek çağrıda, üretimde görülürdü.
func TestSettingBosDegeriVerilmemisSayar(t *testing.T) {
	t.Parallel()

	_, _, h := kurulum(t, map[string]string{
		"STRIPE_API_KEY": "sk_test_1",
		"BOS":            "",
		"SADECE_BOSLUK":  "   ",
	})

	v, ok := h.Setting("STRIPE_API_KEY")
	assert.True(t, ok)
	assert.Equal(t, "sk_test_1", v)

	for _, k := range []string{"BOS", "SADECE_BOSLUK", "HIC_YOK"} {
		v, ok := h.Setting(k)
		assert.False(t, ok, "%s verilmemiş sayılmalı", k)
		assert.Empty(t, v)
	}
}

// TestAboneStartaKadarBeklenir aboneliğin de kuyruğa alındığını doğrular.
func TestAboneStartaKadarBeklenir(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)
	bus := eventbus.NewInMemory(log)
	t.Cleanup(func() { _ = bus.Shutdown(context.Background()) })

	c := container.New(log)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	reg := coreplugin.NewRegistry(log)
	h := coreplugin.NewHost(c, nil, bus, log, nil)

	geldi := make(chan string, 1)

	reg.Add(testEklenti{ad: "izleyici", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.Subscribe("order.placed", func(_ context.Context, e eventbus.Event) error {
			geldi <- e.Name
			return nil
		})

		return nil
	}})

	require.NoError(t, reg.Install(t.Context(), h))
	require.NoError(t, reg.Start(t.Context(), h))

	require.NoError(t, bus.Publish(t.Context(), eventbus.Event{
		Name: "order.placed", Data: map[string]any{"id": "order_1"},
	}))

	assert.Equal(t, "order.placed", <-geldi)
}

// TestAboneOtobussuzHataDoner event otobüsü olmadan abone olmanın sessizce
// başarısız OLMADIĞINI doğrular.
func TestAboneOtobussuzHataDoner(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)

	reg.Add(testEklenti{ad: "izleyici", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.Subscribe("order.placed", func(_ context.Context, _ eventbus.Event) error { return nil })
		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.Start(t.Context(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "order.placed")
}

// TestRouteBaglama eklentinin route'unun gerçekten bağlandığını doğrular.
func TestRouteBaglama(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)

	reg.Add(testEklenti{ad: "webhook", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.AddRoutes(func(r chi.Router) {
			r.Post("/hooks/stripe", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
		})

		return nil
	}})
	require.NoError(t, reg.Install(t.Context(), h))

	router := chi.NewRouter()
	require.NoError(t, reg.MountRoutes(router, h))

	rctx := chi.NewRouteContext()
	assert.True(t, router.Match(rctx, http.MethodPost, "/hooks/stripe"),
		"eklenti route'u bağlanmış olmalı")
}

// modulRouterKur gerçek kurulumdaki gibi Mount edilmiş bir modül yüzeyi kurar.
//
// Doğrudan router.Get ile kaydetmek yeterli olmazdı: chi Mount'ta yolu
// "/store/v1/products/*" biçiminde saklar ve çakışma denetiminin asıl sınavı
// bu kalıntıyı görebilmesidir.
func modulRouterKur(t *testing.T, yol, govde string) chi.Router {
	t.Helper()

	modul := chi.NewRouter()
	modul.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(govde))
	})

	router := chi.NewRouter()
	router.Mount(yol, modul)

	return router
}

// routeEklentisi verilen route işlevini kaydeden bir eklenti üretir.
func routeEklentisi(ad string, fn func(r chi.Router)) testEklenti {
	return testEklenti{ad: ad, setup: func(_ context.Context, h *coreplugin.Host) error {
		h.AddRoutes(fn)

		return nil
	}}
}

// govdeyiOku yolu router'a sorup yanıt gövdesini döner.
func govdeyiOku(t *testing.T, router chi.Router, metod, yol string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(metod, yol, http.NoBody))

	return rec.Body.String()
}

// TestEklentiModulYolunuGolgeleyemez eklentinin modül yolunu ezmesinin ÖNCEDEN
// yakalandığını doğrular.
//
// chi aynı deseni ikinci kez kaydedince handler'ı sessizce ezer; "eklentileri
// modüllerden sonra bağla" kuralı tek başına koruma sağlamaz. Denetim olmasa
// mağaza ürün listesi eklentinin handler'ına düşer ve arıza ancak müşteri boş
// liste gördüğünde fark edilirdi.
func TestEklentiModulYolunuGolgeleyemez(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	reg.Add(routeEklentisi("kotu", func(r chi.Router) {
		r.Get("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("eklenti"))
		})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := modulRouterKur(t, "/store/v1/products", "modul")

	err := reg.MountRoutes(router, h)
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindConflict, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "kotu", "hata hangi eklentinin suçlu olduğunu söylemeli")
	assert.Contains(t, err.Error(), "/store/v1/products")

	assert.Equal(t, "modul", govdeyiOku(t, router, http.MethodGet, "/store/v1/products"),
		"modülün handler'ı yerinde kalmalı")
}

// TestCakismadaSonrakiEklentiDeBaglanmaz çakışmada kurulumun durduğunu
// doğrular.
//
// Kısmen bağlanmış bir yüzey, hiç açılmamış bir sunucudan daha zor teşhis
// edilir: bazı eklenti uçları çalışır, bazıları 404 döner.
func TestCakismadaSonrakiEklentiDeBaglanmaz(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	reg.Add(routeEklentisi("kotu", func(r chi.Router) {
		r.Get("/store/v1/products", func(http.ResponseWriter, *http.Request) {})
	}))
	reg.Add(routeEklentisi("masum", func(r chi.Router) {
		r.Post("/hooks/masum", func(http.ResponseWriter, *http.Request) {})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := modulRouterKur(t, "/store/v1/products", "modul")
	require.Error(t, reg.MountRoutes(router, h))

	assert.False(t, router.Match(chi.NewRouteContext(), http.MethodPost, "/hooks/masum"),
		"çakışmadan sonraki eklenti bağlanmamalı")
}

// TestIkiEklentiAyniYoluBaglayamaz çakışma denetiminin eklentiler ARASINDA da
// çalıştığını doğrular.
//
// İlk eklentinin route'ları gerçek router'a girdiği için ikinci eklenti onu da
// ezebilirdi; denetim yalnızca modül yollarını korusaydı eksik olurdu.
func TestIkiEklentiAyniYoluBaglayamaz(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	ayniYol := func(r chi.Router) {
		r.Post("/hooks/stripe", func(http.ResponseWriter, *http.Request) {})
	}
	reg.Add(routeEklentisi("ilk", ayniYol))
	reg.Add(routeEklentisi("ikinci", ayniYol))
	require.NoError(t, reg.Install(t.Context(), h))

	err := reg.MountRoutes(chi.NewRouter(), h)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ikinci", "suçlu ikinci eklenti olmalı")
	assert.Contains(t, err.Error(), "/hooks/stripe")
}

// TestFarkliMetodCakismaSayilmaz aynı yolda BAŞKA bir metodun
// engellenmediğini doğrular.
//
// chi metodları ayrı tutar; POST eklemek GET'i ezmez. Denetim yalnızca yola
// bakarsa meşru bir eklenti gereksiz yere reddedilirdi.
func TestFarkliMetodCakismaSayilmaz(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	reg.Add(routeEklentisi("abonelik", func(r chi.Router) {
		r.Post("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("eklenti"))
		})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := modulRouterKur(t, "/store/v1/products", "modul")
	require.NoError(t, reg.MountRoutes(router, h))

	assert.Equal(t, "modul", govdeyiOku(t, router, http.MethodGet, "/store/v1/products"))
	assert.Equal(t, "eklenti", govdeyiOku(t, router, http.MethodPost, "/store/v1/products"))
}

// TestGecersizRouteAnlasilirHataDoner chi'nin panic'inin hataya çevrildiğini
// doğrular.
//
// Panik olduğu gibi bırakılsaydı açılışta yalnızca chi'nin iç yığın izi
// görünür, hangi eklentinin suçlu olduğu yazmazdı.
func TestGecersizRouteAnlasilirHataDoner(t *testing.T) {
	t.Parallel()

	_, reg, h := kurulum(t, nil)
	reg.Add(routeEklentisi("bozuk", func(r chi.Router) {
		// chi "/" ile başlamayan deseni reddeder.
		r.Get("hooks/stripe", func(http.ResponseWriter, *http.Request) {})
	}))
	require.NoError(t, reg.Install(t.Context(), h))

	router := modulRouterKur(t, "/store/v1/products", "modul")

	var err error
	require.NotPanics(t, func() { err = reg.MountRoutes(router, h) })
	require.Error(t, err)
	assert.Equal(t, coreerrors.KindInvalid, coreerrors.KindOf(err))
	assert.Contains(t, err.Error(), "bozuk")
	assert.Equal(t, "modul", govdeyiOku(t, router, http.MethodGet, "/store/v1/products"),
		"geçersiz kayıt gerçek router'a hiç dokunmamalı")
}

// TestStartIkiKezCagrilirsaTekrarKaydetmez kuyruğun boşaltıldığını doğrular.
//
// Boşaltılmasaydı ikinci Start aynı sağlayıcıyı yeniden kaydetmeye çalışır ve
// "aynı kimlik zaten kayıtlı" hatasıyla kurulumu düşürürdü.
func TestStartIkiKezCagrilirsaTekrarKaydetmez(t *testing.T) {
	t.Parallel()

	c, reg, h := kurulum(t, nil)
	kayit := &sahteKayit{}
	require.NoError(t, c.Provide(coreplugin.PaymentProvidersName, kayit))

	reg.Add(testEklenti{ad: "stripe", setup: func(_ context.Context, h *coreplugin.Host) error {
		h.RegisterPaymentProvider(sahteSaglayici{id: "stripe"})
		return nil
	}})

	require.NoError(t, reg.Install(t.Context(), h))
	require.NoError(t, reg.Start(t.Context(), h))
	require.NoError(t, reg.Start(t.Context(), h))

	assert.Equal(t, []string{"stripe"}, kayit.kayitli, "sağlayıcı bir kez kaydedilmeli")
}

// TestPluginsAdlariDoner kurulu eklentilerin listelenebildiğini doğrular.
func TestPluginsAdlariDoner(t *testing.T) {
	t.Parallel()

	_, reg, _ := kurulum(t, nil)
	reg.Add(testEklenti{ad: "stripe"})
	reg.Add(testEklenti{ad: "shippo"})

	assert.Equal(t, []string{"stripe", "shippo"}, reg.Plugins())
}
