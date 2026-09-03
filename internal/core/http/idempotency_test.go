package http_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// sayanHandler kaç kez çağrıldığını sayan ve sabit yanıt dönen handler'dır.
type sayanHandler struct {
	mu     sync.Mutex
	cagri  int
	status int
	govde  string
}

// ServeHTTP çağrıyı sayar ve yapılandırılmış yanıtı yazar.
func (h *sayanHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	h.cagri++
	n := h.cagri
	h.mu.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(h.status)
	_, _ = fmt.Fprintf(w, `{"data":{"id":"order_%d"},"body":%q}`, n, h.govde)
}

// sayisi handler'ın çağrı sayısını güvenle okur.
func (h *sayanHandler) sayisi() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.cagri
}

// postIstek verilen anahtar ve gövdeyle bir POST isteği kurar.
func postIstek(anahtar, yol, govde string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, yol, strings.NewReader(govde))
	if anahtar != "" {
		r.Header.Set(corehttp.IdempotencyKeyHeader, anahtar)
	}

	return r
}

// kimlikli isteği verilen çağıranın doğrulanmış kimliğiyle etiketler.
//
// Gerçekte bu context'i [corehttp.RequireAdmin] / [corehttp.RequireStore]
// kurar; testte aradaki kimlik doğrulama katmanını taklit etmeye gerek yoktur.
func kimlikli(r *http.Request, kind, id string) *http.Request {
	return r.WithContext(corehttp.WithPrincipal(r.Context(),
		corehttp.Principal{ID: id, Kind: kind}))
}

// TestIdempotencyTekrarIkinciKezCalistirmaz aynı anahtar ve gövdeyle gelen
// ikinci isteğin handler'ı HİÇ çalıştırmadığını doğrular.
//
// Bu fazın asıl amacı budur: ağ zaman aşımından sonra tekrar denenen bir
// ödeme isteği ikinci bir tahsilat yaratmamalıdır.
func TestIdempotencyTekrarIkinciKezCalistirmaz(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/store/v1/orders", `{"cart":"c1"}`))

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/store/v1/orders", `{"cart":"c1"}`))

	assert.Equal(t, 1, h.sayisi(), "handler yalnızca bir kez çalışmalı")
	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.JSONEq(t, w1.Body.String(), w2.Body.String(), "yanıt aynen çalınmalı")
	assert.Equal(t, "application/json; charset=utf-8", w2.Header().Get("Content-Type"))
	assert.Empty(t, w1.Header().Get(corehttp.IdempotencyReplayedHeader),
		"ilk yanıt tekrar olarak işaretlenmemeli")
	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyFarkliGovdeCakismaDoner aynı anahtarın FARKLI bir gövdeyle
// kullanılmasının reddedildiğini doğrular.
//
// Kaydedilen yanıt sessizce çalınsaydı, istemci "B siparişini oluştur"
// dediği hâlde A siparişinin kaydını alırdı: sessiz veri bozulması.
func TestIdempotencyFarkliGovdeCakismaDoner(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/store/v1/orders", `{"cart":"c1"}`))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/store/v1/orders", `{"cart":"BASKA"}`))

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), corehttp.CodeIdempotencyConflict)
	assert.Equal(t, 1, h.sayisi(), "çakışan istek handler'a ulaşmamalı")
}

// TestIdempotencyFarkliYolCakismaDoner parmak izinin gövdeyle sınırlı
// olmadığını, yol ve sorgu dizesini de kapsadığını doğrular.
func TestIdempotencyFarkliYolCakismaDoner(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"farklı yol":          "/store/v1/returns",
		"farklı sorgu dizesi": "/store/v1/orders?expand=items",
	}

	for name, yol := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			h := &sayanHandler{status: http.StatusCreated}
			mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

			w1 := httptest.NewRecorder()
			mw.ServeHTTP(w1, postIstek("idem_1", "/store/v1/orders", `{"cart":"c1"}`))
			require.Equal(t, http.StatusCreated, w1.Code)

			w2 := httptest.NewRecorder()
			mw.ServeHTTP(w2, postIstek("idem_1", yol, `{"cart":"c1"}`))

			assert.Equal(t, http.StatusConflict, w2.Code)
		})
	}
}

// TestIdempotencyHandlerGovdeyiOkuyabilir parmak izi için tüketilen gövdenin
// handler'a GERİ KONDUĞUNU doğrular.
//
// Konmasaydı idempotency açmak, anahtar gönderen her istemcinin gövdesini
// boşaltır ve tüm POST'ları bozardı.
func TestIdempotencyHandlerGovdeyiOkuyabilir(t *testing.T) {
	t.Parallel()

	var okunan string

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 64)
		n, _ := r.Body.Read(b)
		okunan = string(b[:n])
		w.WriteHeader(http.StatusOK)
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)
	mw.ServeHTTP(httptest.NewRecorder(), postIstek("idem_1", "/x", `{"cart":"c1"}`))

	assert.Equal(t, `{"cart":"c1"}`, okunan, "handler gövdeyi eksiksiz okuyabilmeli")
}

// TestIdempotencySunucuHatasiKaydedilmez 5xx yanıtın çalınmadığını ve
// anahtarın yeniden denenebildiğini doğrular.
//
// Kaydedilseydi geçici bir 500, aynı anahtarla 24 saat boyunca kalıcı bir
// 500'e dönüşürdü: kendini onaran arıza kalıcı arızaya çevrilirdi.
func TestIdempotencySunucuHatasiKaydedilmez(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusInternalServerError}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/x", `{"a":1}`))
	require.Equal(t, http.StatusInternalServerError, w1.Code)

	// Sunucu düzeldi.
	h.status = http.StatusCreated

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w2.Code, "5xx sonrası tekrar denenebilmeli")
	assert.Equal(t, 2, h.sayisi(), "handler ikinci kez çalışmalı")
	assert.Empty(t, w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyIstemciHatasiKaydedilir 4xx yanıtın çalındığını doğrular.
//
// 4xx istemci kaynaklıdır ve tekrar denemek aynı sonucu verir; kaydetmek
// gereksiz iş yapmaktan kurtarır ve yanıtı tutarlı kılar.
func TestIdempotencyIstemciHatasiKaydedilir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusUnprocessableEntity}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	mw.ServeHTTP(httptest.NewRecorder(), postIstek("idem_1", "/x", `{"a":1}`))

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusUnprocessableEntity, w2.Code)
	assert.Equal(t, 1, h.sayisi())
	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyPanikSonrasiTekrarDenenebilir handler panikleyince anahtarın
// KİLİTLİ KALMADIĞINI doğrular.
//
// Kilitli kalsaydı tek bir panik, o anahtarı kalıcı olarak kullanılamaz
// yapardı: istemci ne tekrar deneyebilir ne de yanıt alabilirdi.
func TestIdempotencyPanikSonrasiTekrarDenenebilir(t *testing.T) {
	t.Parallel()

	patlasin := true
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if patlasin {
			panic("handler patladı")
		}

		w.WriteHeader(http.StatusCreated)
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	assert.Panics(t, func() {
		mw.ServeHTTP(httptest.NewRecorder(), postIstek("idem_1", "/x", `{"a":1}`))
	})

	patlasin = false

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w.Code, "panik sonrası anahtar serbest kalmalı")
}

// TestIdempotencyAnahtarsizIstekAkar anahtar gönderilmeyen isteğin normal
// aktığını ve kaydedilmediğini doğrular.
func TestIdempotencyAnahtarsizIstekAkar(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	for range 3 {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postIstek("", "/x", `{"a":1}`))
		require.Equal(t, http.StatusCreated, w.Code)
	}

	assert.Equal(t, 3, h.sayisi(), "anahtarsız istekler kaydedilmemeli")
}

// TestIdempotencyGuvenliMetodlarKaydedilmez GET'in kaydedilmediğini doğrular.
//
// GET zaten idempotenttir; kaydetmek yalnızca depoyu şişirir ve yanlışlıkla
// bayat veri servis etme riski yaratır.
func TestIdempotencyGuvenliMetodlarKaydedilmez(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusOK}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	for range 2 {
		r := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
		r.Header.Set(corehttp.IdempotencyKeyHeader, "idem_1")

		w := httptest.NewRecorder()
		mw.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code)
	}

	assert.Equal(t, 2, h.sayisi(), "GET kaydedilmemeli")
}

// TestIdempotencyNilDepoNoOptur yapılandırılmamış deponun trafiği
// kesmediğini doğrular.
func TestIdempotencyNilDepoNoOptur(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(nil)(h)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, h.sayisi())
}

// TestIdempotencyCokUzunAnahtarReddedilir sınırsız anahtarın depoyu
// şişirmesinin engellendiğini doğrular.
//
// Reddin KENDİ kodunu taşıması da burada kanıtlanır: "yeniden kullanım" kodu
// dönseydi, istemcinin doğru tepkisi (yeni anahtar üretip tekrar denemek)
// sonsuz döngü olurdu — üretilen her yeni anahtar da uzun olurdu.
func TestIdempotencyCokUzunAnahtarReddedilir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postIstek(strings.Repeat("a", 256), "/x", `{"a":1}`))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), corehttp.CodeIdempotencyKeyTooLong)
	assert.NotContains(t, w.Body.String(), corehttp.CodeIdempotencyConflict,
		"uzunluk reddi yeniden kullanım koduyla karıştırılmamalı")
	assert.Zero(t, h.sayisi(), "reddedilen istek handler'a ulaşmamalı")
}

// TestIdempotencyCokBuyukGovdeReddedilir sınırsız gövde okumanın
// engellendiğini doğrular.
//
// Sınır olmasaydı tek bir istek, parmak izi çıkarmak uğruna sunucunun
// belleğini tüketebilirdi.
func TestIdempotencyCokBuyukGovdeReddedilir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w := httptest.NewRecorder()
	mw.ServeHTTP(w, postIstek("idem_1", "/x", strings.Repeat("x", (1<<20)+1)))

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	// İstemcinin dallanma tutamağı KOD'dur, status değil: RFC 9110'un bu
	// durum için ayırdığı 413'e geçmek hata sınıfı eşlemesini değiştirmeyi
	// gerektirir ve o gün geldiğinde status değişebilir (bkz. readLimited).
	assert.Contains(t, w.Body.String(), "body_too_large")
	assert.Zero(t, h.sayisi())
}

// TestIdempotencyEszamanliIkinciIstekCakisir aynı anahtarla PARALEL gelen
// ikinci isteğin beklemek yerine 409 aldığını doğrular.
//
// Beklememek bilinçlidir: iki isteği sıraya sokmak, ilki yavaşsa ikincisini
// de asardı. 409 alan istemci geri çekilip tekrar deneyebilir.
func TestIdempotencyEszamanliIkinciIstekCakisir(t *testing.T) {
	t.Parallel()

	basladi := make(chan struct{})
	devam := make(chan struct{})

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(basladi)
		<-devam
		w.WriteHeader(http.StatusCreated)
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	ilk := make(chan int, 1)

	go func() {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postIstek("idem_1", "/x", `{"a":1}`))
		ilk <- w.Code
	}()

	<-basladi

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), corehttp.CodeIdempotencyInFlight)

	close(devam)
	assert.Equal(t, http.StatusCreated, <-ilk)
}

// TestIdempotencyAnahtarlariAyirir farklı anahtarların birbirini
// etkilemediğini doğrular.
func TestIdempotencyAnahtarlariAyirir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	for _, k := range []string{"a", "b", "c"} {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postIstek(k, "/x", `{"a":1}`))
		require.Equal(t, http.StatusCreated, w.Code)
	}

	assert.Equal(t, 3, h.sayisi())
}

// TestIdempotencyYarisAltindaTekCalisma yarış dedektörü altında aynı
// anahtarla gelen çok sayıda paralel isteğin handler'ı BİRDEN FAZLA KEZ
// çalıştırmadığını doğrular.
func TestIdempotencyYarisAltindaTekCalisma(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	var wg sync.WaitGroup

	for range 32 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			mw.ServeHTTP(httptest.NewRecorder(), postIstek("idem_1", "/x", `{"a":1}`))
		}()
	}

	wg.Wait()

	assert.Equal(t, 1, h.sayisi(), "aynı anahtar yalnızca bir kez işlenmeli")
}

// patlayanDepo Complete çağrısında hata dönen sahte depodur.
type patlayanDepo struct {
	ic          *corehttp.MemoryIdempotencyStore
	completeErr error
}

// Begin çağrıyı gerçek depoya devreder.
func (d *patlayanDepo) Begin(
	ctx context.Context, key, fp string,
) (*corehttp.IdempotentResponse, bool, error) {
	return d.ic.Begin(ctx, key, fp)
}

// Complete yapılandırılmış hatayı döner ve kaydı YAZMAZ.
func (d *patlayanDepo) Complete(
	_ context.Context, _ string, _ corehttp.IdempotentResponse,
) error {
	return d.completeErr
}

// Abort çağrıyı gerçek depoya devreder.
func (d *patlayanDepo) Abort(ctx context.Context, key string) error {
	return d.ic.Abort(ctx, key)
}

// TestIdempotencyKayitYazilamazsaAnahtarKilitliKalmaz depo yazamadığında
// anahtarın SERBEST bırakıldığını doğrular.
//
// Serbest bırakılmasaydı anahtar sonsuza dek "işlemde" kalır ve istemci
// ne yanıt alabilir ne tekrar deneyebilirdi: tek bir depo hatası o anahtarı
// kalıcı olarak öldürürdü.
func TestIdempotencyKayitYazilamazsaAnahtarKilitliKalmaz(t *testing.T) {
	t.Parallel()

	depo := &patlayanDepo{
		ic:          corehttp.NewMemoryIdempotencyStore(time.Hour, 0),
		completeErr: errors.New("depo yazılamadı"),
	}

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(depo)(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/x", `{"a":1}`))
	require.Equal(t, http.StatusCreated, w1.Code, "yanıt istemciye yine de gitmeli")

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.NotEqual(t, http.StatusConflict, w2.Code, "anahtar kilitli kalmamalı")
	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Equal(t, 2, h.sayisi(), "kayıt yazılamadıysa tekrar işlenmeli")
}

// TestIdempotencyCokBuyukYanitKaydedilmez tampon sınırını aşan yanıtın
// istemciye TAM gittiğini ama kaydedilmediğini doğrular.
//
// Eksik tamponu kaydedip sonra çalmak, istemciye kesik ve bozuk bir gövde
// vermek olurdu; bu, tekrarı yeniden işlemekten çok daha kötüdür.
func TestIdempotencyCokBuyukYanitKaydedilmez(t *testing.T) {
	t.Parallel()

	const boyut = (1 << 20) + 100

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, boyut))
	})

	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/x", `{"a":1}`))
	assert.Equal(t, boyut, w1.Body.Len(), "istemci tam yanıtı almalı")

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Empty(t, w2.Header().Get(corehttp.IdempotencyReplayedHeader),
		"kaydedilmemiş yanıt çalınmamalı")
	assert.Equal(t, boyut, w2.Body.Len(), "tekrar da tam yanıt almalı")
}

// TestIdempotencyButceDolunucaDusenAnahtarYenidenIslenir bellek bütçesi
// dolduğunda DÜŞEN kaydın tekrarının handler'ı yeniden çalıştırdığını
// doğrular.
//
// Test, sınırın BEDELİNİ görünür kılmak için vardır. "Bellek sınırlandı"
// cümlesi bedelsiz gibi okunur; oysa bedeli, düşen anahtarla gelen tekrarın
// ikinci bir sipariş yaratmasıdır. Bu davranış bir kaza değil bilinçli bir
// seçimdir (bkz. corehttp.MemoryIdempotencyStore godoc'u: reddetmek yerine
// düşürmek) ve o seçimin kanıtı yalnızca burada, middleware'in gördüğü yerde
// verilebilir.
func TestIdempotencyButceDolunucaDusenAnahtarYenidenIslenir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	// Bu yanıtların her biri ~937 bayta yüklenir; bütçe İKİ kaydı alır,
	// üçüncüsü yazıldığında en eskisi düşer.
	depo := corehttp.NewMemoryIdempotencyStore(time.Hour, 2000)
	mw := corehttp.Idempotency(depo)(h)

	for _, anahtar := range []string{"idem_1", "idem_2", "idem_3"} {
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, postIstek(anahtar, "/store/v1/orders", `{"cart":"c1"}`))
		require.Equal(t, http.StatusCreated, w.Code)
	}

	require.Equal(t, 3, h.sayisi())

	// İlk anahtarın TEKRARI: kaydı düştüğü için yeniden işlenir.
	w4 := httptest.NewRecorder()
	mw.ServeHTTP(w4, postIstek("idem_1", "/store/v1/orders", `{"cart":"c1"}`))

	assert.Equal(t, 4, h.sayisi(), "düşen anahtarla gelen tekrar yeniden işlenir")
	assert.Empty(t, w4.Header().Get(corehttp.IdempotencyReplayedHeader),
		"düşmüş kayıt çalınmış gibi işaretlenmemeli")

	// Üçüncü anahtar hâlâ korunuyor: sınır TÜM korumayı kapatmaz, yalnızca en
	// eski kaydı bırakır.
	w5 := httptest.NewRecorder()
	mw.ServeHTTP(w5, postIstek("idem_3", "/store/v1/orders", `{"cart":"c1"}`))

	assert.Equal(t, 4, h.sayisi(), "düşmemiş kaydın tekrarı handler'ı çalıştırmamalı")
	assert.Equal(t, "true", w5.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyBaskaCagiraninYanitiCalinmaz aynı anahtarı seçen İKİ FARKLI
// çağıranın birbirinin kaydını görmediğini doğrular.
//
// İstekler bayt bayt aynı olduğu için ad alanı olmasaydı ikinci çağıran
// birincinin yanıtını (örn. başka bir kiracının sipariş kimliğini) oynatırdı:
// çapraz kiracı veri sızıntısı. "1", "order-1" gibi sıradan anahtarlar
// düşünüldüğünde bu bir kenar durumu değil, beklenen durumdur.
func TestIdempotencyBaskaCagiraninYanitiCalinmaz(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, kimlikli(postIstek("1", "/store/v1/orders", `{"cart":"c1"}`), "user", "usr_1"))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, kimlikli(postIstek("1", "/store/v1/orders", `{"cart":"c1"}`), "user", "usr_2"))

	assert.Equal(t, http.StatusCreated, w2.Code)
	assert.Empty(t, w2.Header().Get(corehttp.IdempotencyReplayedHeader),
		"ikinci çağıranın isteği bir tekrar değildir")
	assert.NotEqual(t, w1.Body.String(), w2.Body.String(),
		"her çağıran KENDİ yanıtını almalı")
	assert.Equal(t, 2, h.sayisi(), "iki farklı çağıranın isteği de işlenmeli")

	// Birinci çağıranın kendi tekrarı hâlâ çalınmalı: ad alanı, koruduğu
	// davranışı bozmamalıdır.
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, kimlikli(postIstek("1", "/store/v1/orders", `{"cart":"c1"}`), "user", "usr_1"))

	assert.Equal(t, "true", w3.Header().Get(corehttp.IdempotencyReplayedHeader))
	assert.JSONEq(t, w1.Body.String(), w3.Body.String())
	assert.Equal(t, 2, h.sayisi(), "kendi tekrarı yeniden işlenmemeli")
}

// TestIdempotencyBaskaCagiranAnahtarAlaniniIsgalEtmez aynı anahtarı FARKLI
// gövdeyle kullanan ikinci çağıranın 409 almadığını doğrular.
//
// Ad alanı olmasaydı bir çağıran, seçtiği anahtarla diğerinin anahtar alanını
// işgal ederdi: karşı taraf kendi isteği için 409 alır ve o anahtarı bir daha
// kullanamazdı.
func TestIdempotencyBaskaCagiranAnahtarAlaniniIsgalEtmez(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, kimlikli(postIstek("order-1", "/x", `{"cart":"c1"}`), "api_key", "ak_1"))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, kimlikli(postIstek("order-1", "/x", `{"cart":"BASKA"}`), "api_key", "ak_2"))

	assert.Equal(t, http.StatusCreated, w2.Code, "ikinci çağıran kendi anahtar alanında olmalı")
	assert.Equal(t, 2, h.sayisi())
}

// TestIdempotencyAyniCagiranAyniAnahtarlaCakisir ad alanının çakışma
// tespitini KÖRLEŞTİRMEDİĞİNİ doğrular.
//
// Aynı çağıran aynı anahtarı farklı bir istekle kullanıyorsa bu hâlâ bir
// istemci hatasıdır ve 409 dönmelidir.
func TestIdempotencyAyniCagiranAyniAnahtarlaCakisir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, kimlikli(postIstek("1", "/x", `{"cart":"c1"}`), "user", "usr_1"))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, kimlikli(postIstek("1", "/x", `{"cart":"BASKA"}`), "user", "usr_1"))

	assert.Equal(t, http.StatusConflict, w2.Code)
	assert.Contains(t, w2.Body.String(), corehttp.CodeIdempotencyConflict)
	assert.Equal(t, 1, h.sayisi())
}

// TestIdempotencyKimliksizCagiranlarKovayiPaylasir korumasız uçtaki anonim
// çağıranların TEK bir ad alanını paylaştığını, yani birbirinin yanıtını
// oynatabildiğini doğrular.
//
// Bu bir kusur değil, belgelenmiş bir sınırdır: anonim isteği IP'ye göre
// ayırmak, anahtarı kiracıya bağlamadan (IP taklit edilebilir, NAT paylaşılır)
// idempotency'yi bozardı — ağı değişip tekrar deneyen istemci kendi kaydını
// bulamazdı. Test, davranışı sabitler ki sessizce değişmesin.
func TestIdempotencyKimliksizCagiranlarKovayiPaylasir(t *testing.T) {
	t.Parallel()

	h := &sayanHandler{status: http.StatusCreated}
	mw := corehttp.Idempotency(corehttp.NewMemoryIdempotencyStore(time.Hour, 0))(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("1", "/x", `{"cart":"c1"}`))
	require.Equal(t, http.StatusCreated, w1.Code)

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("1", "/x", `{"cart":"c1"}`))

	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader),
		"kimliksiz istekler ortak kovadadır")
	assert.Equal(t, 1, h.sayisi())

	// Kimlikli çağıran o ortak kovadan etkilenmez.
	w3 := httptest.NewRecorder()
	mw.ServeHTTP(w3, kimlikli(postIstek("1", "/x", `{"cart":"c1"}`), "user", "usr_1"))

	assert.Empty(t, w3.Header().Get(corehttp.IdempotencyReplayedHeader),
		"kimlikli çağıran anonim kovanın kaydını oynatmamalı")
	assert.Equal(t, 2, h.sayisi())
}

// kapanisDurumu bir kapanış çağrısının context'inden ÇAĞRI ANINDA okunanlardır.
//
// Context'in kendisi saklanmaz: middleware kendi kurduğu context'i çağrı
// biter bitmez iptal eder (defer), yani sonradan bakan bir test her hâlükârda
// "iptal edilmiş" görürdü ve düzeltmeyi kanıtlayamazdı.
type kapanisDurumu struct {
	// cagrildi çağrının hiç yapılıp yapılmadığını bildirir.
	cagrildi bool
	// err çağrı anındaki ctx.Err() değeridir; nil olmalıdır.
	err error
	// sonlu context'in bir süre sınırı taşıyıp taşımadığını bildirir.
	sonlu bool
}

// kapanisYakalayanDepo Complete/Abort'a hangi context'le gelindiğini kaydeder.
type kapanisYakalayanDepo struct {
	ic *corehttp.MemoryIdempotencyStore

	mu       sync.Mutex
	complete kapanisDurumu
	abort    kapanisDurumu
}

// Begin çağrıyı gerçek depoya devreder.
func (d *kapanisYakalayanDepo) Begin(
	ctx context.Context, key, fp string,
) (*corehttp.IdempotentResponse, bool, error) {
	return d.ic.Begin(ctx, key, fp)
}

// Complete context'in durumunu kaydeder ve yazmayı gerçek depoya devreder.
func (d *kapanisYakalayanDepo) Complete(
	ctx context.Context, key string, resp corehttp.IdempotentResponse,
) error {
	d.mu.Lock()
	d.complete = durumunuOku(ctx)
	d.mu.Unlock()

	return d.ic.Complete(ctx, key, resp)
}

// Abort context'in durumunu kaydeder ve ayırmayı gerçek depoda geri alır.
func (d *kapanisYakalayanDepo) Abort(ctx context.Context, key string) error {
	d.mu.Lock()
	d.abort = durumunuOku(ctx)
	d.mu.Unlock()

	return d.ic.Abort(ctx, key)
}

// kapanislar kaydedilen durumları güvenle okur.
func (d *kapanisYakalayanDepo) kapanislar() (complete, abort kapanisDurumu) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.complete, d.abort
}

// durumunuOku context'ten sınanacak alanları çıkarır.
func durumunuOku(ctx context.Context) kapanisDurumu {
	_, sonlu := ctx.Deadline()

	return kapanisDurumu{cagrildi: true, err: ctx.Err(), sonlu: sonlu}
}

// TestIdempotencyKayitIstemciKopsaDaYazilir istemci bağlantıyı kesmiş olsa
// bile kaydın yazılabildiğini doğrular.
//
// İsteğin context'iyle yazsaydık, kopan bağlantı Complete'i iptal ederdi:
// handler çalışmış (tahsilat yapılmış) olmasına rağmen kayıt oluşmaz ve
// tekrar denemeyi ikinci bir tahsilattan koruyacak şey kaybolurdu.
func TestIdempotencyKayitIstemciKopsaDaYazilir(t *testing.T) {
	t.Parallel()

	depo := &kapanisYakalayanDepo{ic: corehttp.NewMemoryIdempotencyStore(time.Hour, 0)}

	ctx, iptal := context.WithCancel(t.Context())
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// İstemci yanıt yazılırken bağlantıyı kesti.
		iptal()
		w.WriteHeader(http.StatusCreated)
	})

	mw := corehttp.Idempotency(depo)(h)

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/x", `{"a":1}`).WithContext(ctx))
	require.Equal(t, http.StatusCreated, w1.Code)

	complete, _ := depo.kapanislar()
	require.True(t, complete.cagrildi, "Complete çağrılmalı")
	assert.NoError(t, complete.err, "kapanış context'i isteğin iptalinden etkilenmemeli")
	assert.True(t, complete.sonlu, "iptalden koparılan context süresiz kalmamalı")

	// Kayıt gerçekten yazıldıysa tekrar oynatılır.
	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, "true", w2.Header().Get(corehttp.IdempotencyReplayedHeader))
}

// TestIdempotencyIptalIstemciKopsaDaGeriAlinir istemci bağlantıyı kesmiş olsa
// bile ayırmanın geri alınabildiğini doğrular.
//
// Abort iptal edilmiş bir context'le çağrılsaydı anahtar "işlemde" kilitli
// kalırdı: istemci ne yanıt alabilir ne de tekrar deneyebilirdi.
func TestIdempotencyIptalIstemciKopsaDaGeriAlinir(t *testing.T) {
	t.Parallel()

	depo := &kapanisYakalayanDepo{ic: corehttp.NewMemoryIdempotencyStore(time.Hour, 0)}

	ctx, iptal := context.WithCancel(t.Context())
	h := &sayanHandler{status: http.StatusInternalServerError}

	mw := corehttp.Idempotency(depo)(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			iptal()
			h.ServeHTTP(w, r)
		}))

	w1 := httptest.NewRecorder()
	mw.ServeHTTP(w1, postIstek("idem_1", "/x", `{"a":1}`).WithContext(ctx))
	require.Equal(t, http.StatusInternalServerError, w1.Code)

	_, abort := depo.kapanislar()
	require.True(t, abort.cagrildi, "5xx sonrası ayırma geri alınmalı")
	assert.NoError(t, abort.err, "kapanış context'i isteğin iptalinden etkilenmemeli")
	assert.True(t, abort.sonlu, "iptalden koparılan context süresiz kalmamalı")

	// Ayırma gerçekten serbest bırakıldıysa anahtar tekrar denenebilir.
	h.status = http.StatusCreated

	w2 := httptest.NewRecorder()
	mw.ServeHTTP(w2, postIstek("idem_1", "/x", `{"a":1}`))

	assert.Equal(t, http.StatusCreated, w2.Code, "anahtar kilitli kalmamalı")
}
