package http

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// requestIDHeader istek kimliğinin taşındığı HTTP başlığıdır. Hem gelen
// istekten okunur hem de yanıta geri yazılır; böylece istemci gördüğü hatayı
// sunucu loglarıyla eşleştirebilir.
const requestIDHeader = "X-Request-Id"

// maxRequestIDLen dışarıdan gelen istek kimliği için kabul edilen üst sınırdır.
// Daha uzun değerler reddedilir; log satırlarını ve yanıt başlıklarını
// şişirmek için kullanılmalarını engeller.
const maxRequestIDLen = 128

// requestIDPrefix üretilen kimliklerin önekidir (plan Bölüm 8: prefix'li ID).
const requestIDPrefix = "req_"

// redactedPlaceholder loglarda maskelenen değerlerin yerine yazılır.
const redactedPlaceholder = "REDACTED"

// contextKey bu paketin context anahtarları için kullandığı özel tiptir.
// Dışa açılmadığı için başka paketlerin anahtarlarıyla çakışamaz.
type contextKey int

const (
	// requestIDKey context'teki istek kimliğinin anahtarıdır.
	requestIDKey contextKey = iota
	// loggerKey context'teki isteğe özgü logger'ın anahtarıdır.
	loggerKey
)

// sensitiveQueryMarkers sorgu parametresi adının İÇİNDE geçtiğinde değerin
// maskelenmesini gerektiren parçalardır. Bilinçli olarak geniş tutulmuştur:
// yanlışlıkla maskelemek, yanlışlıkla sızdırmaktan ucuzdur (plan Bölüm 8).
//
// Liste yalnızca kimlik bilgisini değil KİŞİSEL VERİYİ de kapsar. Erişim logu
// uzun ömürlüdür ve onu görebilenlerin kümesi uygulamayı çalıştıranlardan
// geniştir; auth modülü e-postayı bilinçli olarak loglamazken aynı değerin
// "GET /admin/v1/users?email=..." süzgecinden erişim loguna düşmesi o kararı
// boşa çıkarırdı.
var sensitiveQueryMarkers = []string{
	// Kimlik bilgisi ve jetonlar.
	"token", "secret", "password", "passwd", "passphrase", "pwd", "key",
	"auth", "signature", "credential", "session", "cookie", "jwt", "otp",
	// Kişisel veri. "mail" parçası hem "email" hem "e-mail" hem de
	// "customer_email" gibi bileşikleri yakalar; ayrıca "email" yazmak
	// listeyi uzatır, kapsamı genişletmez.
	"mail", "posta", "phone", "telefon", "msisdn", "gsm",
	"iban", "tckn", "vkn", "cvv", "cvc",
}

// sensitiveQueryExactKeys yalnızca TAM eşleşmede maskelenen parametre adlarıdır.
//
// Bu adlar alt dize olarak aranamayacak kadar kısadır: "pin" masum "shipping"
// içinde, "tel" ise "telemetry" içinde geçer. sensitiveQueryMarkers'a
// konsalardı erişim logunun en çok işe yarayan süzgeçlerini okunmaz hâle
// getirirlerdi ki maskelemenin amacı logu kullanılamaz kılmak değil. Bedeli
// bilinçlidir: "card_pin" gibi bileşik bir ad buradan yakalanmaz, böyle bir
// parametre doğarsa sensitiveQueryMarkers'a eklenmelidir.
var sensitiveQueryExactKeys = map[string]struct{}{
	"pin": {},
	"tel": {},
}

// RequestID her isteğe bir kimlik atayan middleware'dir.
//
// Gelen "X-Request-Id" başlığı geçerliyse korunur (dağıtık izlemede zincirin
// kopmaması için), aksi hâlde yeni bir kimlik üretilir. Kimlik hem context'e
// konur (RequestIDFromContext ile okunur) hem de aynı adlı yanıt başlığına
// yazılır.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(requestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// WithRequestID verilen istek kimliğini context'e yerleştirir.
// HTTP dışındaki akışlarda (worker, cron) aynı korelasyon kimliğini
// taşımak için kullanılır.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext context'e konmuş istek kimliğini döner.
// Kimlik yoksa boş dize döner; çağıran ayrıca nil kontrolü yapmak zorunda değildir.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// WithLogger isteğe özgü logger'ı context'e yerleştirir.
// WriteError ve WriteJSON, hata durumunu bu logger üzerinden raporlar.
func WithLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, log)
}

// LoggerFromContext context'teki logger'ı döner.
// Context'te logger yoksa slog varsayılanı kullanılır; dönen değer asla nil değildir.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if log, ok := ctx.Value(loggerKey).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}

// Recoverer handler paniklerini yakalayan middleware üretir.
//
// Panik hâlinde yığın izi (stack trace) loglanır ve istemciye 500 JSON yanıtı
// döner; süreç ayakta kalır. http.ErrAbortHandler paniği stdlib'in "bu isteği
// sessizce bırak" sözleşmesidir ve olduğu gibi yeniden fırlatılır.
//
// Yanıt çoktan başlamışsa üstüne ikinci bir gövde yazılmaz. Bunu güvenilir
// kılmak için Recoverer handler'a kendi sarmalayıcısını verir: "yazıldı mı"
// bilgisi başka bir middleware'in ürettiği tipe bakılarak tahmin edilmez,
// dolayısıyla middleware sırasından ve araya giren sarmalayıcılardan
// bağımsızdır.
func Recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	log = loggerOrDefault(log)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := WithLogger(r.Context(), log)
			r = r.WithContext(ctx)
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}

			// ctx parametre olarak geçirilir; kapanış (closure) içinde
			// yeniden türetilmez, böylece iptal/değer zinciri korunur.
			defer func(ctx context.Context) {
				rec := recover()
				if rec == nil {
					return
				}

				// stdlib sözleşmesi: ErrAbortHandler yakalanmaz, yanıt yazılmaz.
				if err, ok := rec.(error); ok && coreerrors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				log.ErrorContext(ctx, "handler panikledi",
					"panic", fmt.Sprint(rec),
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", RequestIDFromContext(ctx),
				)

				// Yanıt çoktan başlamışsa üstüne ikinci bir gövde yazmak
				// istemciye bozuk JSON gönderir; sadece loglayıp bırakılır.
				if rw.wroteHeader {
					return
				}

				WriteJSON(ctx, rw, http.StatusInternalServerError,
					newErrorResponse(ctx, defaultInternalCode, genericInternalMessage, nil))
			}(ctx)

			next.ServeHTTP(rw, r)
		})
	}
}

// RequestLogger her isteği yapısal olarak loglayan middleware üretir.
//
// Kaydedilen alanlar: method, path, (maskelenmiş) query, status, bytes,
// duration_ms ve request_id. Hassas veri loglanmaz (plan Bölüm 8): istek
// başlıkları — özellikle Authorization ve Cookie — hiç okunmaz, sorgu
// parametrelerinden jeton ya da kişisel veri taşıyan adların değerleri
// maskelenir (bkz. redactQuery), istek ve yanıt gövdeleri log'a girmez.
//
// path maskelenmez: rota şablonu erişim logunun temel eksenidir ve maskelenirse
// geriye izlenebilir hiçbir şey kalmaz. Karşılığı, rotaların kişisel veriyi yol
// parçasına koymaması ve kaynakları kimlikle adreslemesidir.
//
// Satır defer ile yazılır: handler paniklese (veya http.ErrAbortHandler ile
// isteği bıraksa) bile erişim kaydı kaybolmaz — izlenmesi en kritik istekler
// tam olarak bunlardır. Yanıt hiç başlamadan zincirden bir panik geçtiyse
// istemciye bir şey ulaşmadığı için status 500 olarak kaydedilir.
func RequestLogger(log *slog.Logger) func(http.Handler) http.Handler {
	log = loggerOrDefault(log)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			ctx := WithLogger(r.Context(), log)
			finished := false

			defer func() {
				status := rw.status
				if !finished && !rw.wroteHeader {
					status = http.StatusInternalServerError
				}

				elapsed := time.Since(start)
				log.Log(ctx, levelForStatus(status), "istek tamamlandı",
					"method", r.Method,
					"path", r.URL.Path,
					"query", redactQuery(r.URL.RawQuery),
					"status", status,
					"bytes", rw.bytes,
					"duration_ms", float64(elapsed.Microseconds())/1000.0,
					"request_id", RequestIDFromContext(ctx),
				)
			}()

			next.ServeHTTP(rw, r.WithContext(ctx))
			finished = true
		})
	}
}

// responseWriter yanıtın status kodunu ve yazılan bayt sayısını sayan
// http.ResponseWriter sarmalayıcısıdır.
//
// Handler'lara olabildiğince şeffaf kalır: Unwrap ile http.ResponseController
// yolunu, Flush ile streaming'i, Hijack ile protokol yükseltmeyi aktarır.
type responseWriter struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

// WriteHeader status kodunu kaydeder ve alttaki writer'a iletir.
// İlk çağrıdan sonrakiler yok sayılır; stdlib'in "superfluous WriteHeader"
// uyarısı bu sayede tetiklenmez.
func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

// Write gövdeyi yazar ve yazılan bayt sayısını biriktirir.
func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// Unwrap alttaki ResponseWriter'ı döner.
// http.ResponseController bu metot sayesinde Hijack/SetDeadline gibi
// yetenekleri sarmalayıcının ardından bulabilir.
func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Hijack alttaki TCP bağlantısını handler'a devreder.
//
// websocket, protokol yükseltme ve bazı SSE kütüphaneleri http.ResponseController
// yerine doğrudan w.(http.Hijacker) tip iddiası yapar; bu metot olmadan
// sarmalayıcı o yeteneği gizler ve söz konusu handler'lar kırılırdı. Alttaki
// writer hijack desteklemiyorsa http.ErrNotSupported döner.
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}

	conn, buf, err := h.Hijack()
	if err == nil {
		// Bağlantı devralındı: artık normal yolla yanıt yazılamaz.
		w.wroteHeader = true
	}
	return conn, buf, err
}

// Flush alttaki writer'ı boşaltır; streaming (SSE) handler'ları için
// sarmalayıcının şeffaf kalmasını sağlar.
func (w *responseWriter) Flush() {
	f, ok := w.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	// Flush, başlıkların gönderilmesine yol açar; durum kaydı buna göre güncellenir.
	w.wroteHeader = true
	f.Flush()
}

// newRequestID kriptografik olarak rastgele yeni bir istek kimliği üretir.
func newRequestID() string {
	return requestIDPrefix + rand.Text()
}

// sanitizeRequestID dışarıdan gelen kimliği doğrular.
//
// Yalnızca yazdırılabilir ASCII ve en fazla maxRequestIDLen karakter kabul
// edilir; kural dışı her değer boş dizeye indirgenir (fail-closed) ve çağıran
// yerine yeni kimlik üretir. Böylece istemci ne log satırlarını ne de yanıt
// başlığını kontrol edebilir.
func sanitizeRequestID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > maxRequestIDLen {
		return ""
	}
	if strings.IndexFunc(v, func(r rune) bool { return r < 0x20 || r > 0x7e }) >= 0 {
		return ""
	}
	return v
}

// redactQuery sorgu dizesini loglanabilir hâle getirir: hassas adlı
// parametrelerin değerleri maskelenir, geri kalanı (sayfalama, filtre)
// olduğu gibi kalır. Ayrıştırılamayan sorgu tamamen maskelenir.
//
// Maskeleme bir DENY-LIST'tir ve bedeli açıkça kabul edilir: listede olmayan
// yeni bir hassas parametre — yarın eklenen "?recovery_hint=..." gibi —
// maskesiz loglanır, koruma ancak adın listeye eklenmesiyle gelir. Ters
// seçenek olan allow-list bu katmanda mümkün değildi: çekirdek modülleri
// tanımaz (Prensip 2.4), dolayısıyla core/http hiçbir modülün süzgeç adını
// önceden bilemez. Allow-list ya sürekli eksik kalıp her yeni uç noktanın
// sorgusunu tümüyle maskeleyerek erişim logunu körleştirirdi ya da modülleri
// çekirdeğe ad kaydetmeye zorlayarak bağımlılık yönünü tersine çevirirdi.
func redactQuery(raw string) string {
	if raw == "" {
		return ""
	}

	values, err := url.ParseQuery(raw)
	if err != nil {
		return redactedPlaceholder
	}

	for key, vals := range values {
		if !isSensitiveKey(key) {
			continue
		}
		for i := range vals {
			vals[i] = redactedPlaceholder
		}
	}

	return values.Encode()
}

// isSensitiveKey parametre adının hassas bir değeri taşıyıp taşımadığını bildirir.
//
// Karşılaştırma küçük harfe indirgenerek yapılır: anahtar adını istemci
// belirler ve "?Email=" ile "?EMAIL=" yazmak maskelemeyi atlatmaya yetmemeli.
func isSensitiveKey(key string) bool {
	lower := strings.ToLower(key)
	if _, ok := sensitiveQueryExactKeys[lower]; ok {
		return true
	}
	for _, marker := range sensitiveQueryMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// levelForStatus status koduna uygun log seviyesini seçer:
// 5xx hata, 4xx uyarı, geri kalanı bilgi seviyesindedir.
func levelForStatus(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// loggerOrDefault nil logger yerine slog varsayılanını koyar.
func loggerOrDefault(log *slog.Logger) *slog.Logger {
	if log == nil {
		return slog.Default()
	}
	return log
}
