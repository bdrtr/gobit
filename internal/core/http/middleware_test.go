package http_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// requestIDHeaderName testlerde kullanılan istek kimliği başlığıdır.
const requestIDHeaderName = "X-Request-Id"

// maskeliDeger maskelenen bir değerin yerine erişim loguna yazılan işarettir.
// Testler çıktıda tam olarak bunu arar; sabit, maskelemenin "sessizce boş
// dizeye dönüşme" ihtimalini de eleyerek iddiayı keskinleştirir.
const maskeliDeger = "REDACTED"

// testLogger çıktısı buffer'a giden bir JSON logger üretir.
// Loglanan alanların gerçekten ne içerdiğini kanıtlamak için kullanılır.
func testLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return log, &buf
}

// logRecords buffer'daki JSON log satırlarını ayrıştırır.
func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "log satırı JSON değil: %s", line)
		out = append(out, rec)
	}
	return out
}

func TestRequestIDGelenBasligiKorur(t *testing.T) {
	t.Parallel()

	var seen string
	h := corehttp.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = corehttp.RequestIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_gelen_kimlik")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, "req_gelen_kimlik", seen, "context'teki kimlik gelen başlıktan alınmalı")
	assert.Equal(t, "req_gelen_kimlik", rec.Header().Get(requestIDHeaderName), "yanıt başlığı gelen kimliği yansıtmalı")
}

func TestRequestIDBaslikYoksaUretir(t *testing.T) {
	t.Parallel()

	var seen []string
	h := corehttp.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = append(seen, corehttp.RequestIDFromContext(r.Context()))
	}))

	first := httptest.NewRecorder()
	h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	require.Len(t, seen, 2)
	assert.NotEmpty(t, seen[0], "kimlik üretilmeli")
	assert.NotEqual(t, seen[0], seen[1], "her istek için ayrı kimlik üretilmeli")
	assert.Equal(t, seen[0], first.Header().Get(requestIDHeaderName), "üretilen kimlik yanıt başlığına yazılmalı")
	assert.Equal(t, seen[1], second.Header().Get(requestIDHeaderName))
}

func TestRequestIDDusmanceDegeriReddeder(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"satır sonu":    "abc\r\nX-Injected: evil",
		"kontrol krkt":  "abc\x00def",
		"aşırı uzun":    strings.Repeat("a", 129),
		"sadece boşluk": "   ",
		"ascii dışı":    "kimlik-ışık",
	}

	for name, hostile := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var seen string
			h := corehttp.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				seen = corehttp.RequestIDFromContext(r.Context())
			}))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.Header.Set(requestIDHeaderName, hostile)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			assert.NotEqual(t, hostile, seen, "geçersiz kimlik olduğu gibi kabul edilmemeli")
			assert.NotEmpty(t, seen, "reddedilen kimliğin yerine yenisi üretilmeli")
			assert.Equal(t, seen, rec.Header().Get(requestIDHeaderName))
		})
	}
}

func TestRequestIDFromContextBossaBosDoner(t *testing.T) {
	t.Parallel()

	assert.Empty(t, corehttp.RequestIDFromContext(t.Context()), "middleware yoksa kimlik boş olmalı")
}

func TestRecovererPanikte500JSONDoner(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestID(corehttp.Recoverer(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("beklenmedik durum: gizli-detay-123")
	})))

	req := httptest.NewRequest(http.MethodGet, "/patlayan", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_panik")
	rec := httptest.NewRecorder()

	require.NotPanics(t, func() { h.ServeHTTP(rec, req) }, "panik middleware'i geçmemeli")

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	var body corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), "gövde JSON olmalı: %s", rec.Body.String())
	assert.Equal(t, "internal_error", body.Error.Code)
	assert.Equal(t, "req_panik", body.Error.RequestID)
	assert.NotContains(t, rec.Body.String(), "gizli-detay-123", "panik metni istemciye sızmamalı")

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "the handler panicked", records[0]["msg"])
	assert.Equal(t, "beklenmedik durum: gizli-detay-123", records[0]["panic"])
	assert.Contains(t, records[0]["stack"], "panic", "yığın izi loglanmalı")
	assert.Equal(t, "req_panik", records[0]["request_id"])

	// Süreç ayakta: aynı zincir sonraki isteği normal şekilde işler.
	okRec := httptest.NewRecorder()
	corehttp.Recoverer(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(okRec, httptest.NewRequest(http.MethodGet, "/saglikli", http.NoBody))
	assert.Equal(t, http.StatusNoContent, okRec.Code)
}

func TestRecovererErrAbortHandlerYenidenFirlatir(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.Recoverer(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	rec := httptest.NewRecorder()

	func() {
		defer func() {
			r := recover()
			require.NotNil(t, r, "ErrAbortHandler yeniden fırlatılmalı")
			assert.Equal(t, http.ErrAbortHandler, r, "aynı panik değeri korunmalı")
		}()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/abort", http.NoBody))
	}()

	assert.Empty(t, rec.Body.String(), "abort edilen isteğe gövde yazılmamalı")
	assert.Empty(t, buf.String(), "abort sözleşmesi loglanmamalı")
}

// seffafYazici hiçbir ek yetenek açığa vurmayan, yalnızca aktaran bir
// http.ResponseWriter sarmalayıcısıdır. Zincire yabancı bir middleware'in
// (örn. sıkıştırma) girmesini taklit eder.
type seffafYazici struct {
	http.ResponseWriter
}

// seffafKatman verilen handler'ı seffafYazici ile sarmalar.
func seffafKatman(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&seffafYazici{ResponseWriter: w}, r)
	})
}

func TestRecovererYarimYanitiBozmaz(t *testing.T) {
	t.Parallel()

	const yarimGovde = `{"items":[1,2`

	// Panikleyen ama gövdeyi yazmaya başlamış handler.
	yarimHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(yarimGovde))
		panic("gövde yazılırken patladı")
	})

	tests := map[string]func(log *slog.Logger) http.Handler{
		"recoverer tek başına": func(log *slog.Logger) http.Handler {
			return corehttp.Recoverer(log)(yarimHandler)
		},
		"recoverer en dışta, logger içte": func(log *slog.Logger) http.Handler {
			return corehttp.Recoverer(log)(corehttp.RequestLogger(log)(yarimHandler))
		},
		"araya yabancı sarmalayıcı girmiş": func(log *slog.Logger) http.Handler {
			return corehttp.RequestLogger(log)(seffafKatman(corehttp.Recoverer(log)(seffafKatman(yarimHandler))))
		},
	}

	for name, kur := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			log, buf := testLogger()
			rec := httptest.NewRecorder()
			require.NotPanics(t, func() {
				kur(log).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/akis", http.NoBody))
			})

			assert.Equal(t, yarimGovde, rec.Body.String(),
				"yanıt başladıktan sonra ikinci bir gövde eklenmemeli")
			assert.Equal(t, http.StatusOK, rec.Code, "gönderilmiş status değiştirilemez")
			assert.Contains(t, buf.String(), "the handler panicked", "panik yine de loglanmalı")
		})
	}
}

func TestRequestLoggerStatusVeSureyiKaydeder(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	const gecikme = 25 * time.Millisecond

	h := corehttp.RequestID(corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(gecikme)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})))

	req := httptest.NewRequest(http.MethodPost, "/store/v1/orders", http.NoBody)
	req.Header.Set(requestIDHeaderName, "req_log")
	h.ServeHTTP(httptest.NewRecorder(), req)

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	rec := records[0]

	assert.Equal(t, "request completed", rec["msg"])
	assert.Equal(t, http.MethodPost, rec["method"])
	assert.Equal(t, "/store/v1/orders", rec["path"])
	assert.InDelta(t, float64(http.StatusCreated), rec["status"], 0)
	assert.InDelta(t, float64(len(`{"ok":true}`)), rec["bytes"], 0, "yazılan bayt sayısı doğru olmalı")
	assert.Equal(t, "req_log", rec["request_id"])

	sure, ok := rec["duration_ms"].(float64)
	require.True(t, ok, "duration_ms sayı olmalı: %#v", rec["duration_ms"])
	assert.GreaterOrEqual(t, sure, float64(gecikme.Milliseconds()), "süre en az handler gecikmesi kadar olmalı")
	assert.Less(t, sure, 10_000.0, "süre milisaniye biriminde olmalı")
}

func TestRequestLoggerHatadaErrorSeviyesiKullanir(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "ERROR", records[0]["level"])
	assert.InDelta(t, float64(http.StatusServiceUnavailable), records[0]["status"], 0)
}

func TestRequestLoggerHassasVeriLoglamaz(t *testing.T) {
	t.Parallel()

	const (
		bearerToken  = "bearer-cok-gizli-jwt-degeri"
		cookieValue  = "oturum-cerezi-gizli-degeri"
		queryToken   = "sorgudaki-gizli-token-degeri"
		apiKeyValue  = "sk-canli-api-anahtari"
		passwordText = "hunter2-parolasi"
	)

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	target := "/store/v1/products?access_token=" + queryToken +
		"&api_key=" + apiKeyValue +
		"&password=" + passwordText +
		"&limit=20&offset=40"

	req := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Cookie", "session="+cookieValue)
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	require.NotEmpty(t, out, "istek loglanmalı")

	// KANIT: gerçek log çıktısında hiçbir hassas değer geçmiyor.
	for _, gizli := range []string{bearerToken, cookieValue, queryToken, apiKeyValue, passwordText} {
		assert.NotContains(t, out, gizli, "hassas değer log çıktısında geçmemeli")
	}
	lower := strings.ToLower(out)
	assert.NotContains(t, lower, "authorization", "Authorization başlığı hiç loglanmamalı")
	assert.NotContains(t, lower, "cookie", "Cookie başlığı hiç loglanmamalı")

	// Hassas olmayan sorgu parametreleri gözlemlenebilirlik için korunur.
	assert.Contains(t, out, "limit=20")
	assert.Contains(t, out, "offset=40")
	assert.Contains(t, out, maskeliDeger, "maskeleme uygulanmalı")
}

func TestRequestLoggerEpostaErisimLogunaDusmez(t *testing.T) {
	t.Parallel()

	// auth modülü e-postayı bilinçli olarak loglamıyor; aynı değer HTTP
	// katmanının erişim logundan da sızmamalı. Süzgeç uydurma değil:
	// GET /admin/v1/users "email" sorgu parametresini okuyor.
	const (
		yerelAd = "musteri.gizli"
		alanAdi = "ornek-magaza.test"
	)

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	sorgu := url.Values{"email": {yerelAd + "@" + alanAdi}, "limit": {"20"}}
	h.ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/admin/v1/users?"+sorgu.Encode(), http.NoBody))

	// E-posta parça parça aranıyor: "@" yüzde kodlamasıyla "%40"a dönüştüğü
	// için tam dizeyi aramak, maskeleme hiç çalışmasa bile geçen bir test
	// üretirdi.
	out := buf.String()
	assert.NotContains(t, out, yerelAd, "e-postanın yerel kısmı erişim loguna düşmemeli")
	assert.NotContains(t, out, alanAdi, "e-postanın alan adı erişim loguna düşmemeli")

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, "email="+maskeliDeger+"&limit=20", records[0]["query"],
		"e-posta maskelenmeli, sayfalama olduğu gibi kalmalı")
}

// erisimLoguSorgusu verilen ham sorguyla bir istek geçirir ve erişim
// kaydındaki "query" alanını döner.
func erisimLoguSorgusu(t *testing.T, rawQuery string) string {
	t.Helper()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/users", http.NoBody)
	req.URL.RawQuery = rawQuery
	h.ServeHTTP(httptest.NewRecorder(), req)

	records := logRecords(t, buf)
	require.Len(t, records, 1, "tek bir erişim kaydı beklenir")
	query, ok := records[0]["query"].(string)
	require.True(t, ok, "query alanı dize olmalı: %#v", records[0]["query"])
	return query
}

func TestRequestLoggerHassasSorguAnahtarlariniMaskeler(t *testing.T) {
	t.Parallel()

	const gizliDeger = "sizmamasi-gereken-deger"

	tests := map[string]struct {
		anahtar    string
		maskelenir bool
	}{
		// Kişisel veri.
		"e-posta":                 {anahtar: "email", maskelenir: true},
		"büyük harfli e-posta":    {anahtar: "EMail", maskelenir: true},
		"bileşik e-posta süzgeci": {anahtar: "customer_email", maskelenir: true},
		"telefon":                 {anahtar: "phone", maskelenir: true},
		"türkçe telefon":          {anahtar: "telefon", maskelenir: true},
		"kısa telefon":            {anahtar: "tel", maskelenir: true},
		"tc kimlik no":            {anahtar: "tckn", maskelenir: true},
		"kart doğrulama kodu":     {anahtar: "card_cvv", maskelenir: true},
		// Kimlik bilgisi ve jetonlar.
		"parola":              {anahtar: "password", maskelenir: true},
		"büyük harfli parola": {anahtar: "PASSWORD", maskelenir: true},
		"jeton":               {anahtar: "access_token", maskelenir: true},
		"api anahtarı":        {anahtar: "api_key", maskelenir: true},
		"imza":                {anahtar: "X-Signature", maskelenir: true},
		// Deny-list: tanınmayan ve masum adlar gözlemlenebilirlik için geçer.
		"sayfalama":          {anahtar: "limit", maskelenir: false},
		"sıralama":           {anahtar: "sort", maskelenir: false},
		"durum süzgeci":      {anahtar: "status", maskelenir: false},
		"bilinmeyen anahtar": {anahtar: "renk", maskelenir: false},
		// "shipping" içinde "pin" geçer: kısa adlar alt dize olarak
		// aranmadığı için kargo süzgeci okunabilir kalmalı.
		"kargo süzgeci": {anahtar: "shipping", maskelenir: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			query := erisimLoguSorgusu(t, url.Values{tc.anahtar: {gizliDeger}}.Encode())

			if tc.maskelenir {
				assert.NotContains(t, query, gizliDeger, "hassas anahtarın değeri loglanmamalı")
				assert.Contains(t, query, maskeliDeger, "değer maske işaretiyle değiştirilmeli")
				return
			}
			assert.Contains(t, query, gizliDeger,
				"deny-list: tanınmayan anahtar erişim logunda olduğu gibi kalmalı")
		})
	}
}

func TestRequestLoggerBozukSorguyuTamamenMaskeler(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.URL.RawQuery = "gecersiz=%zz&sifre=acik-deger"
	h.ServeHTTP(httptest.NewRecorder(), req)

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.Equal(t, maskeliDeger, records[0]["query"], "ayrıştırılamayan sorgu tamamen maskelenmeli")
	assert.NotContains(t, buf.String(), "acik-deger")
}

func TestRequestLoggerPanikteDeErisimKaydiYazar(t *testing.T) {
	t.Parallel()

	t.Run("panik zincirden geçerse", func(t *testing.T) {
		t.Parallel()

		log, buf := testLogger()
		h := corehttp.RequestLogger(log)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			panic("handler patladı")
		}))

		assert.Panics(t, func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/patlayan", http.NoBody))
		}, "RequestLogger paniği yutmamalı")

		records := logRecords(t, buf)
		require.Len(t, records, 1, "panikleyen istek de erişim logunda görünmeli")
		assert.Equal(t, "request completed", records[0]["msg"])
		assert.Equal(t, "/patlayan", records[0]["path"])
		assert.InDelta(t, float64(http.StatusInternalServerError), records[0]["status"], 0,
			"yanıt hiç başlamadıysa status 500 kaydedilir")
	})

	t.Run("ErrAbortHandler ile bırakılırsa", func(t *testing.T) {
		t.Parallel()

		log, buf := testLogger()
		h := corehttp.RequestLogger(log)(corehttp.Recoverer(log)(
			http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				panic(http.ErrAbortHandler)
			})))

		assert.Panics(t, func() {
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", http.NoBody))
		}, "abort sözleşmesi yeniden fırlatılmalı")

		records := logRecords(t, buf)
		require.Len(t, records, 1, "abort edilen istek erişim logundan düşmemeli")
		assert.Equal(t, "request completed", records[0]["msg"])
		assert.Equal(t, "/abort", records[0]["path"])
	})
}

// deadlineYazici yalnızca SetWriteDeadline sağlayan sahte writer'dır.
// Sarmalayıcı bu metodu tanımlamadığı için http.ResponseController ona ancak
// Unwrap zincirini yürüyerek ulaşabilir.
type deadlineYazici struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

// SetWriteDeadline istenen süreyi kaydeder.
func (d *deadlineYazici) SetWriteDeadline(t time.Time) error {
	d.deadline = t
	return nil
}

func TestResponseWriterUnwrapResponseControllerYolunuAcar(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	istenen := time.Now().Add(42 * time.Second)

	var hata error
	// İki sarmalayıcı katmanı: zincirin sonuna kadar yürünmeli.
	h := corehttp.RequestLogger(log)(corehttp.Recoverer(log)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hata = http.NewResponseController(w).SetWriteDeadline(istenen)
		})))

	alt := &deadlineYazici{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(alt, httptest.NewRequest(http.MethodGet, "/uzun-yanit", http.NoBody))

	require.NoError(t, hata, "Unwrap olmadan ResponseController alttaki writer'ı bulamaz")
	assert.Equal(t, istenen, alt.deadline, "deadline alttaki writer'a ulaşmalı")
}

func TestResponseWriterIkinciWriteHeaderiYutar(t *testing.T) {
	t.Parallel()

	log, buf := testLogger()
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.WriteHeader(http.StatusInternalServerError)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/urunler", http.NoBody))

	assert.Equal(t, http.StatusCreated, rec.Code, "gönderilen status değişmemeli")

	records := logRecords(t, buf)
	require.Len(t, records, 1)
	assert.InDelta(t, float64(http.StatusCreated), records[0]["status"], 0,
		"loglanan status ilk WriteHeader çağrısını yansıtmalı")
}

func TestResponseWriterFlushYanitiBaslatmisSayar(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	var flusherVar bool
	h := corehttp.Recoverer(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, ok := w.(http.Flusher)
		flusherVar = ok
		if ok {
			f.Flush()
		}
		panic("akış ortasında koptu")
	}))

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() {
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/olaylar", http.NoBody))
	})

	assert.True(t, flusherVar, "sarmalayıcı http.Flusher'ı gizlememeli")
	assert.True(t, rec.Flushed, "Flush alttaki writer'a iletilmeli")
	assert.Empty(t, rec.Body.String(), "Flush yanıtı başlatır; üstüne hata gövdesi yazılmamalı")
}

// hijackYazici Hijack sağlayan sahte writer'dır.
type hijackYazici struct {
	*httptest.ResponseRecorder
	cagrildi bool
}

// Hijack çağrıldığını kaydeder; gerçek bir bağlantı üretmez.
func (h *hijackYazici) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.cagrildi = true
	return nil, bufio.NewReadWriter(bufio.NewReader(strings.NewReader("")), bufio.NewWriter(io.Discard)), nil
}

func TestResponseWriterHijackiGizlemez(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	var (
		hijacker bool
		hata     error
	)

	// Bağlantı devralındıktan sonra paniklenirse Recoverer gövde yazmamalı.
	h := corehttp.RequestLogger(log)(corehttp.Recoverer(log)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hj, ok := w.(http.Hijacker)
			hijacker = ok
			if !ok {
				return
			}
			_, _, hata = hj.Hijack()
			panic("upgrade sonrası patladı")
		})))

	alt := &hijackYazici{ResponseRecorder: httptest.NewRecorder()}
	require.NotPanics(t, func() {
		h.ServeHTTP(alt, httptest.NewRequest(http.MethodGet, "/ws", http.NoBody))
	})

	assert.True(t, hijacker, "sarmalayıcı http.Hijacker'ı gizlememeli")
	assert.NoError(t, hata)
	assert.True(t, alt.cagrildi, "Hijack alttaki writer'a iletilmeli")
	assert.Empty(t, alt.Body.String(), "devralınan bağlantıya hata gövdesi yazılmamalı")
}

func TestResponseWriterHijackDesteklenmiyorsaHataDoner(t *testing.T) {
	t.Parallel()

	log, _ := testLogger()
	var hata error
	h := corehttp.RequestLogger(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		require.True(t, ok)
		_, _, hata = hj.Hijack()
	}))

	// httptest.ResponseRecorder Hijack desteklemez.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws", http.NoBody))

	require.Error(t, hata)
	assert.ErrorIs(t, hata, http.ErrNotSupported, "desteklenmeyen hijack açıkça bildirilmeli")
}
