package http_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// isaretleyen çağrıldığını bir sayaca yazan middleware üretir.
func isaretleyen(sayac *int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*sayac++
			next.ServeHTTP(w, r)
		})
	}
}

// reddeden isteği 418 ile kesen middleware'dir; kapsam dışı kalması gereken
// yolların gerçekten kesilmediğini status koduyla kanıtlar.
func reddeden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
}

// TestScopedYalnizcaOnekAltindaCalisir kapsam kuralının hem yakaladığı hem
// yakalamadığı yolları tek tabloda doğrular.
//
// Sınır durumları bilinçlidir: "/admin/v1x" öneki paylaşıyormuş GİBİ görünür
// ama segment sınırında değildir; oraya sızan bir koruma, tasarlanmadığı bir
// uçta çalışırdı.
func TestScopedYalnizcaOnekAltindaCalisir(t *testing.T) {
	t.Parallel()

	testler := map[string]struct {
		yol     string
		bekleme int
	}{
		"önek tam eşleşir":      {yol: "/admin/v1", bekleme: http.StatusTeapot},
		"önek altındaki uç":     {yol: "/admin/v1/users", bekleme: http.StatusTeapot},
		"derin yol":             {yol: "/admin/v1/users/usr_1/password", bekleme: http.StatusTeapot},
		"benzer ama farklı ön":  {yol: "/admin/v1x/users", bekleme: http.StatusOK},
		"başka yüzey":           {yol: "/store/v1/products", bekleme: http.StatusOK},
		"sağlık ucu":            {yol: "/health", bekleme: http.StatusOK},
		"önek dize olarak içte": {yol: "/x/admin/v1/users", bekleme: http.StatusOK},
	}

	for ad, tt := range testler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			h := corehttp.Scoped("/admin/v1", nil, reddeden)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))

			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.yol, http.NoBody))

			assert.Equal(t, tt.bekleme, w.Code)
		})
	}
}

// TestScopedMuafYolMiddlewareyiAtlar giriş ucunun korumadan muaf kalabildiğini
// doğrular. Muafiyet çalışmazsa kimse giriş yapamaz ve sistem kilitlenir.
func TestScopedMuafYolMiddlewareyiAtlar(t *testing.T) {
	t.Parallel()

	h := corehttp.Scoped("/admin/v1", []string{"/admin/v1/auth/login"}, reddeden)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", http.NoBody))
	assert.Equal(t, http.StatusOK, w.Code, "muaf yol middleware'e girmemeli")

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/logout", http.NoBody))
	assert.Equal(t, http.StatusTeapot, w.Code, "muaf olmayan komşu yol korunmalı")
}

// TestScopedZinciriSirayiKorur birden çok middleware'in VERİLDİĞİ SIRAYLA
// çalıştığını doğrular: hız sınırı kimlik doğrulamadan sonra çalışsaydı,
// reddedilen istek de kotayı harcamış olurdu.
func TestScopedZinciriSirayiKorur(t *testing.T) {
	t.Parallel()

	var sira []string
	kaydeden := func(ad string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sira = append(sira, ad)
				next.ServeHTTP(w, r)
			})
		}
	}

	h := corehttp.Scoped("/admin/v1", nil, kaydeden("bir"), kaydeden("iki"))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			sira = append(sira, "handler")
		}))

	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody))

	assert.Equal(t, []string{"bir", "iki", "handler"}, sira)
}

// TestScopedNilMiddlewareAtlanir yapılandırılmamış bir bileşenin nil olarak
// verilmesinin paniğe değil, o halkanın atlanmasına yol açtığını doğrular.
func TestScopedNilMiddlewareAtlanir(t *testing.T) {
	t.Parallel()

	sayac := 0
	h := corehttp.Scoped("/admin/v1", nil, nil, isaretleyen(&sayac))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

	w := httptest.NewRecorder()
	assert.NotPanics(t, func() {
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody))
	})
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, sayac)
}

// TestScopedChiRouteDesenineDokunmaz kapsamlayıcının route eşleşmesini
// bozmadığını doğrular: middleware isteği sarmalar ama yolu değiştirmez.
func TestScopedChiRouteDesenineDokunmaz(t *testing.T) {
	t.Parallel()

	sayac := 0
	r := chi.NewRouter()
	r.Use(corehttp.Scoped("/admin/v1", nil, isaretleyen(&sayac)))
	r.Get("/admin/v1/users/{id}", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte(chi.URLParam(req, "id")))
	})
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/v1/users/usr_42", http.NoBody))
	assert.Equal(t, "usr_42", w.Body.String())
	assert.Equal(t, 1, sayac)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
	assert.Equal(t, 1, sayac, "sağlık ucu kapsam dışında kalmalı")
}

// sabitDogrulayici verilen kimliği dönen, hiçbir şeyi sorgulamayan
// doğrulayıcıdır.
type sabitDogrulayici struct {
	principal corehttp.Principal
	err       error
}

// AuthenticateAdmin sabit kimliği ya da sabit hatayı döner.
func (d sabitDogrulayici) AuthenticateAdmin(
	_ context.Context, _, _ string,
) (corehttp.Principal, error) {
	return d.principal, d.err
}

// AuthenticateStore sabit kimliği ya da sabit hatayı döner.
func (d sabitDogrulayici) AuthenticateStore(_ context.Context, _ string) (corehttp.Principal, error) {
	return d.principal, d.err
}

// korumaliRouter verilen seçeneklerle üretilmiş yığını taşıyan router kurar.
func korumaliRouter(t *testing.T, opts corehttp.GuardOptions) chi.Router {
	t.Helper()

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version:     "test",
		Middlewares: corehttp.APIGuards(opts),
	})
	r.Post("/admin/v1/users", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	r.Get("/store/v1/products", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	return r
}

// cagir verilen isteği router'a gönderir.
func cagir(r chi.Router, istek *http.Request) *httptest.ResponseRecorder {
	kayit := httptest.NewRecorder()
	r.ServeHTTP(kayit, istek)

	return kayit
}

// TestAPIGuardsHizSiniriKimlikDogrulamadanOnceCalisir yığın SIRASINI kanıtlar.
//
// Sıra tersine dönseydi, parola deneyen bir saldırganın her isteği önce
// kimlik doğrulama maliyetini (bcrypt + veritabanı araması) ödetir, kota
// ancak ondan sonra düşerdi. Aşağıdaki ikinci istek 429 yerine 401 dönerdi.
func TestAPIGuardsHizSiniriKimlikDogrulamadanOnceCalisir(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(t, corehttp.GuardOptions{
		Authenticator: sabitDogrulayici{err: errors.New("geçersiz")},
		Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
	})

	ilk := cagir(r, httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, ilk.Code,
		"ilk istek kotayı harcar ve kimlikte reddedilir")

	ikinci := cagir(r, httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))
	assert.Equal(t, http.StatusTooManyRequests, ikinci.Code,
		"kota bitince kimlik doğrulamaya HİÇ gidilmemeli")
	assert.NotEmpty(t, ikinci.Header().Get("Retry-After"),
		"429 istemciye ne zaman döneceğini söylemeli")
}

// TestAPIGuardsSaglikUclariniKapsamaz orkestratör yolunun yığından muaf
// olduğunu doğrular.
//
// Kapsasaydı, hız sınırına takılan bir /ready isteği süreci "sağlıksız"
// gösterir ve orkestratör sağlıklı bir örneği trafikten çekerdi.
func TestAPIGuardsSaglikUclariniKapsamaz(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(t, corehttp.GuardOptions{
		Authenticator: sabitDogrulayici{err: errors.New("geçersiz")},
		Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
	})

	for i := range 5 {
		kayit := cagir(r, httptest.NewRequest(http.MethodGet, "/health", http.NoBody))
		require.Equal(t, http.StatusOK, kayit.Code, "%d. sağlık isteği geçmeli", i+1)
	}
}

// TestAPIGuardsIdempotencyKimlikSonrasiCalisir yığının ÜÇÜNCÜ halkasının
// yerini kanıtlar.
//
// Reddedilen bir istek idempotency anahtarını TÜKETMEMELİDİR: tüketseydi,
// kimliğini düzeltip aynı anahtarla dönen istemciye 401 yanıtı çalınır ve
// istek hiç çalışmazdı.
func TestAPIGuardsIdempotencyKimlikSonrasiCalisir(t *testing.T) {
	t.Parallel()

	red := sabitDogrulayici{err: errors.New("geçersiz")}
	kabul := sabitDogrulayici{principal: corehttp.Principal{ID: "usr_1", Kind: "user"}}

	gecikmeli := &corehttp.DeferredAuthenticator{}
	gecikmeli.Bind(red)

	r := korumaliRouter(t, corehttp.GuardOptions{
		Authenticator:    gecikmeli,
		IdempotencyStore: corehttp.NewMemoryIdempotencyStore(time.Hour),
	})

	istekYap := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/admin/v1/users", http.NoBody)
		// Başlık HER İKİ durumda da vardır; değişen yalnızca doğrulayıcının
		// verdiği cevaptır. Başlıksız istek kimliğe hiç varmadan reddedilir
		// ve testin ayırt etmek istediği fark kaybolurdu.
		req.Header.Set("Authorization", "Bearer jeton")
		req.Header.Set(corehttp.IdempotencyKeyHeader, "ayni-anahtar")

		return req
	}

	reddedilen := cagir(r, istekYap())
	require.Equal(t, http.StatusUnauthorized, reddedilen.Code)

	gecikmeli.Bind(kabul)

	kabuledilen := cagir(r, istekYap())
	assert.Equal(t, http.StatusCreated, kabuledilen.Code,
		"401 anahtarı tüketmemeli; kimlik düzelince istek çalışmalı")
	assert.Empty(t, kabuledilen.Header().Get(corehttp.IdempotencyReplayedHeader),
		"ilk GERÇEK çalıştırma bir tekrar oynatma değildir")

	tekrar := cagir(r, istekYap())
	assert.Equal(t, http.StatusCreated, tekrar.Code)
	assert.Equal(t, "true", tekrar.Header().Get(corehttp.IdempotencyReplayedHeader),
		"aynı anahtarla ikinci istek kaydı oynatmalı")
}

// TestAPIGuardsYapilandirilmamisKimlikHerSeyiReddeder ADR 0007'nin kimlik
// satırını doğrular: doğrulayıcı yoksa yüzey KAPALIDIR.
func TestAPIGuardsYapilandirilmamisKimlikHerSeyiReddeder(t *testing.T) {
	t.Parallel()

	r := korumaliRouter(t, corehttp.GuardOptions{})

	yonetim := cagir(r, httptest.NewRequest(http.MethodPost, "/admin/v1/users", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, yonetim.Code)

	magaza := cagir(r, httptest.NewRequest(http.MethodGet, "/store/v1/products", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, magaza.Code)
}

// TestAPIGuardsMuafYolKimlikIstemez giriş ucunun yığından geçtiğini ama
// kimlik halkasını atladığını doğrular.
func TestAPIGuardsMuafYolKimlikIstemez(t *testing.T) {
	t.Parallel()

	r := corehttp.NewRouter(corehttp.RouterOptions{
		Version: "test",
		Middlewares: corehttp.APIGuards(corehttp.GuardOptions{
			Authenticator: sabitDogrulayici{err: errors.New("geçersiz")},
			AdminExempt:   []string{"/admin/v1/auth/login"},
			Limiter:       corehttp.NewMemoryLimiter(1, time.Minute),
		}),
	})
	r.Post("/admin/v1/auth/login", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	kayit := cagir(r, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", http.NoBody))
	assert.Equal(t, http.StatusOK, kayit.Code, "giriş ucu kimlik istememeli")

	// Muafiyet YALNIZCA kimlik halkasınadır: hız sınırı giriş ucunda da
	// çalışmalıdır, çünkü korumasız uç tam olarak kaba kuvvetin hedefidir.
	ikinci := cagir(r, httptest.NewRequest(http.MethodPost, "/admin/v1/auth/login", http.NoBody))
	assert.Equal(t, http.StatusTooManyRequests, ikinci.Code,
		"giriş ucu korumasızdır ama SINIRSIZ değildir")
}

// TestDeferredAuthenticatorBaglanmadanReddeder bağlanmamış doğrulayıcının
// sessizce geçirmediğini doğrular (ADR 0007).
func TestDeferredAuthenticatorBaglanmadanReddeder(t *testing.T) {
	t.Parallel()

	var d corehttp.DeferredAuthenticator

	_, err := d.AuthenticateAdmin(context.Background(), "bearer", "x")
	assert.True(t, coreerrors.IsUnauthorized(err),
		"bağlanmamış doğrulayıcı kimlik doğrulama hatası dönmeli, %v döndü", err)

	_, err = d.AuthenticateStore(context.Background(), "pk_x")
	assert.True(t, coreerrors.IsUnauthorized(err))
}
