package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// contentTypeJSON JSON yanıtlarının Content-Type değeridir.
const contentTypeJSON = "application/json; charset=utf-8"

// defaultInternalCode sınıflandırılmamış sunucu hatalarının istemciye
// bildirilen kodudur.
const defaultInternalCode = "internal_error"

// genericInternalMessage KindInternal hatalarında istemciye dönen sabit
// mesajdır. Alttaki hatanın metni ASLA istemciye yazılmaz: SQL parçaları,
// bağlantı dizeleri veya dosya yolları içerebilir (plan Bölüm 8).
const genericInternalMessage = "beklenmeyen bir sunucu hatası oluştu"

// fallbackErrorBody gövde kodlanamadığında yazılan sabit yanıttır.
// Yeniden kodlama denenmediği için sonsuz döngü riski yoktur.
const fallbackErrorBody = `{"error":{"code":"internal_error","message":"yanıt üretilemedi"}}` + "\n"

// ErrorResponse hata yanıtlarının dış zarfıdır.
// Tüm hata gövdeleri tek bir "error" anahtarı altında toplanır.
type ErrorResponse struct {
	// Error hatanın ayrıntılarıdır.
	Error ErrorBody `json:"error"`
}

// ErrorBody hata zarfının içeriğidir.
type ErrorBody struct {
	// Code makine tarafından okunabilen sabit hata kodudur (örn. "product_not_found").
	Code string `json:"code"`
	// Message insan tarafından okunabilen açıklamadır.
	Message string `json:"message"`
	// Details isteğe bağlı yapısal bağlamdır (örn. geçersiz alanlar).
	Details map[string]any `json:"details,omitempty"`
	// RequestID isteğin korelasyon kimliğidir; destek kaydını loga bağlar.
	RequestID string `json:"request_id,omitempty"`
}

// WriteJSON verilen değeri JSON olarak yanıta yazar.
//
// Gövde önce belleğe kodlanır; kodlama başarısız olursa status kodu henüz
// gönderilmemiş olduğu için istemciye yarım gövde yerine 500 döner. v nil ise
// yalnızca başlık ve status yazılır.
func WriteJSON(ctx context.Context, w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if v != nil {
		if err := json.NewEncoder(&buf).Encode(v); err != nil {
			LoggerFromContext(ctx).ErrorContext(ctx, "yanıt gövdesi kodlanamadı",
				"error", err,
				"request_id", RequestIDFromContext(ctx),
			)
			w.Header().Set("Content-Type", contentTypeJSON)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(fallbackErrorBody))
			return
		}
	}

	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)

	if buf.Len() == 0 {
		return
	}
	if _, err := buf.WriteTo(w); err != nil {
		// Status kodu gönderildikten sonra yapılabilecek bir şey kalmaz
		// (örn. istemci bağlantıyı kapatmıştır); yalnızca kaydedilir.
		LoggerFromContext(ctx).ErrorContext(ctx, "yanıt gövdesi yazılamadı",
			"error", err,
			"request_id", RequestIDFromContext(ctx),
		)
	}
}

// contentTypeHTML HTML yanıtlarının Content-Type değeridir.
const contentTypeHTML = "text/html; charset=utf-8"

// headerContentTypeOptions ve nosniff, tarayıcının gövdeyi kendi tahminine göre
// yeniden yorumlamasını engeller. JSON yanıtlarında gerekmez (tarayıcı onları
// çalıştırmaz); HTML ve varlık yanıtlarında gerekir ve dosya sunumunda da aynı
// gerekçeyle yazılır.
const (
	headerContentTypeOptions = "X-Content-Type-Options"
	nosniff                  = "nosniff"
)

// WriteHTML hazır bir HTML gövdesini yanıta yazar.
//
// # Gövde ÖNCEDEN üretilmiş olmalıdır
//
// İmza bilinçli olarak bir şablon değil, []byte alır: çağıran şablonu ÖNCE
// belleğe üretir, hata olursa [WriteError] çağırır, ancak başarılıysa buraya
// gelir. Şablonu doğrudan yazıcıya akıtan bir tasarımda, şablonun ORTASINDA
// doğan bir hata 200 durum kodlu YARIM bir sayfa bırakır: başlık gönderilmiş
// olduğu için panik yakalayıcı da hata yazıcısı da artık hiçbir şey yapamaz ve
// arıza istemcide sessizleşir. Aynı gerekçe [WriteJSON]'ın gövdeyi önce
// belleğe kodlamasının da sebebidir.
//
// # Durum kodu SERBESTTİR
//
// [WriteJSON]'ın aksine burada 2xx zorunluluğu yoktur ve bu bilinçlidir:
// kimliksiz bir tarayıcıya giriş sayfasını 401 ile döndürmek, onu 303 ile başka
// bir yere yollamaktan daha dürüsttür — yönlendirme, isteğin başarısız
// olduğunu durum kodundan siler.
//
// Bu yüzeyi kullanan tek yer bugün yönetim panelidir (ADR 0011). Çerçevenin
// JSON hata zarfı DEĞİŞMEZ: bir API ucu hata yazarken hâlâ [WriteError]
// kullanır ve panelin varlığı o politikayı hiçbir yerde ikinci bir politikaya
// bölmez.
func WriteHTML(ctx context.Context, w http.ResponseWriter, status int, govde []byte) {
	w.Header().Set("Content-Type", contentTypeHTML)
	w.Header().Set(headerContentTypeOptions, nosniff)
	w.WriteHeader(status)

	if len(govde) == 0 {
		return
	}
	if _, err := w.Write(govde); err != nil {
		// Status kodu gönderildikten sonra yapılabilecek bir şey kalmaz.
		LoggerFromContext(ctx).ErrorContext(ctx, "HTML gövdesi yazılamadı",
			"error", err,
			"request_id", RequestIDFromContext(ctx),
		)
	}
}

// WriteRedirect tarayıcıyı başka bir yola yollar.
//
// net/http'nin kendi yönlendiricisi KULLANILMAZ: o, gövdeyi çekirdeğin
// dışında yazar ve depo genelinde yanıt gövdesinin tek kapıdan geçmesi kuralı
// onu yapısal olarak reddeder. Buradaki uygulama gövdesizdir; yönlendirmenin
// gövdesi zaten hiçbir tarayıcı tarafından gösterilmez.
//
// Varsayılan durum kodu 303'tür ve çağıranın seçimine bırakılmaz: bir formun
// POST'undan sonra 303, tarayıcıya "hedefe GET ile git" der ve yenilemede
// formun yeniden gönderilmesini engeller. Panelde yönlendirmenin tek meşru
// sebebi budur.
func WriteRedirect(_ context.Context, w http.ResponseWriter, hedef string) {
	w.Header().Set("Location", hedef)
	w.WriteHeader(http.StatusSeeOther)
}

// WriteAsset gömülü bir statik varlığı yazar.
//
// [WriteHTML]'den ayrı durmasının sebebi önbelleklemedir: varlıklar ikiliye
// gömülüdür, yani sürüm boyunca DEĞİŞMEZLER ve tarayıcının onları her sayfada
// yeniden indirmesi gereksizdir. HTML sayfaları ise oturuma ve veriye bağlıdır
// ve önbelleklenemez; ikisini tek yüzeyde toplamak, birine doğru olan başlığı
// diğerine de yazmak olurdu.
//
// etag çağıranın verdiği sürüm damgasıdır; boşsa önbellek başlığı yazılmaz.
func WriteAsset(ctx context.Context, w http.ResponseWriter, contentType, etag string, govde []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set(headerContentTypeOptions, nosniff)
	if etag != "" {
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.WriteHeader(http.StatusOK)

	if len(govde) == 0 {
		return
	}
	if _, err := w.Write(govde); err != nil {
		LoggerFromContext(ctx).ErrorContext(ctx, "varlık gövdesi yazılamadı",
			"error", err,
			"request_id", RequestIDFromContext(ctx),
		)
	}
}

// WriteError hatayı sınıfına uygun status kodu ve tutarlı JSON zarfıyla yazar.
//
// KindInternal hatalarında alttaki hata metni istemciye sızdırılmaz: gövdeye
// genel bir mesaj konur, gerçek hata (sarmalanan zincir dâhil) context'teki
// logger ile kaydedilir. Diğer sınıflarda Message ve Details güvenli kabul
// edilir; servis yazarı bu alanlara hassas veri koymaz (plan Bölüm 8).
//
// nil ya da tipli-nil hata da güvenle işlenir: 500 yazılır, panik üretilmez.
func WriteError(ctx context.Context, w http.ResponseWriter, err error) {
	typed, ok := typedError(err)

	kind := coreerrors.KindInternal
	var code string
	if ok {
		kind = typed.Kind
		code = typed.Code
	}
	policy := policyForKind(kind)
	status := policy.status

	if !policy.clientSafe {
		LoggerFromContext(ctx).ErrorContext(ctx, "istek sunucu hatasıyla sonuçlandı",
			"error", err,
			"code", code,
			"status", status,
			"request_id", RequestIDFromContext(ctx),
		)
		if code == "" {
			code = defaultInternalCode
		}
		WriteJSON(ctx, w, status, newErrorResponse(ctx, code, genericInternalMessage, nil))
		return
	}

	message := kind.String()
	var details map[string]any
	if ok {
		if typed.Message != "" {
			message = typed.Message
		}
		details = typed.Details
	}
	if code == "" {
		code = kind.String()
	}

	WriteJSON(ctx, w, status, newErrorResponse(ctx, code, message, details))
}

// StatusFor hatanın sınıfına karşılık gelen HTTP status kodunu döner.
//
// Eşleme plan Bölüm 8'de tanımlıdır. Tipli olmayan (veya nil) hatalar
// KindInternal sayılır ve 500 döner; sınıflandırılmamış bir hata kazara
// istemci hatası gibi raporlanmaz. Zincirde tipli-nil bir *errors.Error
// bulunması da (bkz. typedError) aynı şekilde 500 ile sonuçlanır.
func StatusFor(err error) int {
	kind := coreerrors.KindInternal
	if typed, ok := typedError(err); ok {
		kind = typed.Kind
	}
	return policyForKind(kind).status
}

// typedError zincirdeki ilk *errors.Error'ı döner.
//
// Bulunan işaretçi nil ise ikinci dönüş değeri false olur. Bu tuzak gerçektir:
// errors.Wrap sarmalanacak hata nil iken (*Error)(nil) döner, bu değer error
// arayüzüne konduğunda "err != nil" doğru çıkar ve alanlarına erişmek panik
// üretirdi. HTTP katmanı böyle bir hatayı sınıflandırılmamış sayar.
func typedError(err error) (*coreerrors.Error, bool) {
	var typed *coreerrors.Error
	if coreerrors.As(err, &typed) && typed != nil {
		return typed, true
	}
	return nil, false
}

// kindPolicy bir hata sınıfının HTTP karşılığını ve gövdesinin istemciye
// olduğu gibi verilip verilemeyeceğini belirler.
type kindPolicy struct {
	status int
	// clientSafe true ise servisin yazdığı Message istemciye aynen gider.
	// false ise mesaj maskelenir ve gerçek hata yalnızca loglanır.
	clientSafe bool
}

// policyForKind sınıfa karşılık gelen politikayı döner.
//
// coreerrors.Kind bir uint8'dir ve Error.Kind alanı dışa açıktır; yani çağıran
// enum dışında bir değer kurabilir. Böyle bir değer GÜVENLİ TARAFA düşer:
// 500 ve maskeleme. Aksi hâlde tanınmayan bir sınıf, sunucu içi ayrıntıyı
// (DSN, sorgu, dosya yolu) istemciye sızdırırdı.
func policyForKind(kind coreerrors.Kind) kindPolicy {
	switch kind {
	case coreerrors.KindNotFound:
		return kindPolicy{status: http.StatusNotFound, clientSafe: true}
	case coreerrors.KindInvalid:
		return kindPolicy{status: http.StatusUnprocessableEntity, clientSafe: true}
	case coreerrors.KindConflict:
		return kindPolicy{status: http.StatusConflict, clientSafe: true}
	case coreerrors.KindUnauthorized:
		return kindPolicy{status: http.StatusUnauthorized, clientSafe: true}
	case coreerrors.KindForbidden:
		return kindPolicy{status: http.StatusForbidden, clientSafe: true}
	case coreerrors.KindUnavailable:
		return kindPolicy{status: http.StatusServiceUnavailable, clientSafe: true}
	case coreerrors.KindTooManyRequests:
		return kindPolicy{status: http.StatusTooManyRequests, clientSafe: true}
	case coreerrors.KindInternal:
		return kindPolicy{status: http.StatusInternalServerError, clientSafe: false}
	default:
		return kindPolicy{status: http.StatusInternalServerError, clientSafe: false}
	}
}

// newErrorResponse hata zarfını kurar ve context'teki istek kimliğini ekler.
func newErrorResponse(ctx context.Context, code, message string, details map[string]any) ErrorResponse {
	return ErrorResponse{
		Error: ErrorBody{
			Code:      code,
			Message:   message,
			Details:   details,
			RequestID: RequestIDFromContext(ctx),
		},
	}
}
