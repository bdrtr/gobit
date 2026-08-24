// Package api payment modülünün HTTP yüzeyidir.
//
// İki yüzey vardır ve yetkileri farklıdır:
//
//   - /admin/v1 — ödemenin TÜM aşamalarını yönetir: koleksiyon açma, oturum
//     açma, yetkilendirme, tahsilat, iptal ve iade.
//   - /store/v1 — müşterinin ödeme akışı için gereken EN AZ yüzey: koleksiyonu
//     okumak ve bir sağlayıcıda oturum açmak. Yetkilendirme ve tahsilat mağaza
//     tarafından TETİKLENMEZ; onları sipariş tamamlama workflow'u yürütür
//     (plan Faz 6). Müşterinin kendi tarayıcısından tahsilat tetikleyebilmesi,
//     siparişi hiç oluşmamış bir sepetten para çekilmesi demek olurdu.
//
// Aynı gerekçe mağaza yüzeyindeki oturum açma ucunun GÖVDESİNİ de daraltır:
// tutar istemciden alınmaz (her zaman koleksiyonun kalanının tamamı) ve
// sağlayıcının davranış anahtarları reddedilir. Tahsilatı tetikleyemeyen ama
// ödemenin tutarını ya da sonucunu yazabilen bir uç, aynı kapıyı arka taraftan
// açardı.
//
// Kimlik doğrulama Faz 8'de gelir; şimdilik iki yüzey de açıktır.
//
// Handler'lar status kodu SEÇMEZ: servis core/errors tipli hatasını döner,
// corehttp.WriteError sınıfına uygun kodu yazar (plan Bölüm 8).
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/models"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// Route yolları. Modül route'ları TAM YOL ile kaydedilir; "/admin/v1" gibi bir
// ön ek MOUNT EDİLMEZ, çünkü mount eden ilk modül o alt ağacın tamamını
// sahiplenir ve aynı ön eki kullanan diğer modüllerle çakışırdı.
const (
	pathAdminProviders        = "/admin/v1/payment-providers"
	pathAdminCollections      = "/admin/v1/payment-collections"
	pathAdminCollection       = "/admin/v1/payment-collections/{id}"
	pathAdminCollectionSess   = "/admin/v1/payment-collections/{id}/payment-sessions"
	pathAdminCollectionPays   = "/admin/v1/payment-collections/{id}/payments"
	pathAdminSessionAuthorize = "/admin/v1/payment-sessions/{id}/authorize"
	pathAdminSessionCapture   = "/admin/v1/payment-sessions/{id}/capture"
	pathAdminSessionCancel    = "/admin/v1/payment-sessions/{id}/cancel"
	pathAdminSession          = "/admin/v1/payment-sessions/{id}"
	pathAdminPayment          = "/admin/v1/payments/{id}"
	pathAdminPaymentRefund    = "/admin/v1/payments/{id}/refunds"

	pathStoreProviders   = "/store/v1/payment-providers"
	pathStoreCollection  = "/store/v1/payment-collections/{id}"
	pathStoreCollectSess = "/store/v1/payment-collections/{id}/payment-sessions"
	// pathStoreSessionCancel müşterinin KENDİ açtığı oturumu bırakmasıdır.
	//
	// Rezervasyon koleksiyon düzeyinde tutulur (açık bir oturum, koleksiyonun
	// kalan tutarını kapatır) — çift tahsilatı bu engelliyor. Bırakma yolu
	// olmasaydı vitrinde "kredi kartı" seçip sonra "havale"ye dönmek isteyen
	// müşteri, bir YÖNETİCİ oturumu elle iptal edene kadar kilitli kalırdı.
	pathStoreSessionCancel = "/store/v1/payment-sessions/{id}/cancel"
)

// maxBodyBytes istek gövdesi için üst sınırdır. Sınır olmadan tek bir istek
// sunucunun belleğini tüketebilirdi.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest gövde/parametre çözümlenemediğinde dönen hata kodudur.
const codeInvalidRequest = "payment_invalid_request"

// Payments handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç satırlık bir sahte ile doğrulanabilir.
type Payments interface {
	// ProviderIDs kayıtlı sağlayıcı kimliklerini döner.
	ProviderIDs(ctx context.Context) []string

	// CreatePaymentCollection yeni bir ödeme koleksiyonu oluşturur.
	CreatePaymentCollection(ctx context.Context, in service.CreateCollectionInput) (models.PaymentCollection, error)
	// GetPaymentCollection koleksiyonu kimliğiyle döner.
	GetPaymentCollection(ctx context.Context, id string) (models.PaymentCollection, error)
	// ListPaymentCollections koleksiyonları sayfalar.
	ListPaymentCollections(ctx context.Context, in service.ListCollectionsInput) ([]models.PaymentCollection, int64, error)

	// CreateSession bir sağlayıcıda ödeme oturumu açar.
	CreateSession(ctx context.Context, collectionID, providerID string, in service.CreateSessionInput) (models.PaymentSession, error)
	// GetPaymentSession oturumu kimliğiyle döner.
	GetPaymentSession(ctx context.Context, id string) (models.PaymentSession, error)
	// ListPaymentSessions koleksiyonun oturumlarını döner.
	ListPaymentSessions(ctx context.Context, collectionID string) ([]models.PaymentSession, error)
	// AuthorizePayment oturumu yetkilendirir.
	AuthorizePayment(ctx context.Context, sessionID string) (models.PaymentSession, error)
	// CapturePayment bloke tutarı tahsil eder.
	CapturePayment(ctx context.Context, sessionID string, amount int64) (models.Payment, error)
	// CancelPayment oturumu iptal eder (saga telafisi).
	CancelPayment(ctx context.Context, sessionID string) error

	// GetPayment tahsilatı kimliğiyle döner.
	GetPayment(ctx context.Context, id string) (models.Payment, error)
	// ListPayments koleksiyonun tahsilatlarını döner.
	ListPayments(ctx context.Context, collectionID string) ([]models.Payment, error)
	// RefundPayment tahsilatı iade eder.
	RefundPayment(ctx context.Context, paymentID string, amount int64, reason string) (models.Refund, error)
	// ListRefunds tahsilatın iadelerini döner.
	ListRefunds(ctx context.Context, paymentID string) ([]models.Refund, error)
}

// Handler payment modülünün HTTP handler kümesidir.
type Handler struct {
	svc Payments
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc Payments) *Handler { return &Handler{svc: svc} }

// Routes modülün admin ve store route'larını router'a bağlar.
func (h *Handler) Routes(r chi.Router) {
	r.Get(pathAdminProviders, h.listProviders)

	r.Post(pathAdminCollections, h.createCollection)
	r.Get(pathAdminCollections, h.listCollections)
	r.Get(pathAdminCollection, h.getCollection)
	r.Get(pathAdminCollectionSess, h.listSessions)
	r.Post(pathAdminCollectionSess, h.createSession)
	r.Get(pathAdminCollectionPays, h.listPayments)

	r.Get(pathAdminSession, h.getSession)
	r.Post(pathAdminSessionAuthorize, h.authorizeSession)
	r.Post(pathAdminSessionCapture, h.captureSession)
	r.Post(pathAdminSessionCancel, h.cancelSession)

	r.Get(pathAdminPayment, h.getPayment)
	r.Get(pathAdminPaymentRefund, h.listRefunds)
	r.Post(pathAdminPaymentRefund, h.refundPayment)

	r.Get(pathStoreProviders, h.listProviders)
	r.Get(pathStoreCollection, h.getCollection)
	// Oturum açma iki yüzeyde de vardır ama AYNI handler değildir: mağaza ucu
	// tutarı ve sağlayıcı davranışını istemciye bırakmaz.
	r.Post(pathStoreCollectSess, h.createStoreSession)
	r.Post(pathStoreSessionCancel, h.cancelStoreSession)
}

// --- zarflar ve DTO'lar ------------------------------------------------------

// singleEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type singleEnvelope struct {
	// Data yanıtın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count süzgece uyan TÜM kayıtların sayısıdır; sayfadaki satır sayısı değil.
	Count int64 `json:"count"`
	// Offset atlanan kayıt sayısıdır.
	Offset int64 `json:"offset"`
	// Limit istenen sayfa boyutudur.
	Limit int64 `json:"limit"`
}

// collectionDTO ödeme koleksiyonunun dış gösterimidir.
type collectionDTO struct {
	ID               string         `json:"id"`
	Reference        string         `json:"reference"`
	Amount           int64          `json:"amount"`
	CurrencyCode     string         `json:"currency_code"`
	Status           string         `json:"status"`
	AuthorizedAmount int64          `json:"authorized_amount"`
	CapturedAmount   int64          `json:"captured_amount"`
	RefundedAmount   int64          `json:"refunded_amount"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// sessionDTO ödeme oturumunun dış gösterimidir.
//
// DeclineReason yalnızca ret hâlinde doludur ve TEŞHİS içindir; müşteriye
// gösterilecek bir metin değildir. Mağaza yüzeyinde de görünür olması
// bilinçlidir: alanı gizlemek, entegrasyonu yazan geliştiricinin reddin
// sebebini hiç görememesi demek olurdu.
type sessionDTO struct {
	ID                  string          `json:"id"`
	PaymentCollectionID string          `json:"payment_collection_id"`
	ProviderID          string          `json:"provider_id"`
	ExternalID          string          `json:"external_id"`
	Status              string          `json:"status"`
	Amount              int64           `json:"amount"`
	AuthorizedAmount    int64           `json:"authorized_amount"`
	CurrencyCode        string          `json:"currency_code"`
	Data                json.RawMessage `json:"data,omitempty"`
	DeclineReason       string          `json:"decline_reason,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// paymentDTO tahsilatın dış gösterimidir.
type paymentDTO struct {
	ID                  string    `json:"id"`
	PaymentSessionID    string    `json:"payment_session_id"`
	PaymentCollectionID string    `json:"payment_collection_id"`
	Amount              int64     `json:"amount"`
	CurrencyCode        string    `json:"currency_code"`
	RefundedAmount      int64     `json:"refunded_amount"`
	CapturedAt          time.Time `json:"captured_at"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// refundDTO iadenin dış gösterimidir.
type refundDTO struct {
	ID        string    `json:"id"`
	PaymentID string    `json:"payment_id"`
	Amount    int64     `json:"amount"`
	Reason    string    `json:"reason,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// toCollectionDTO modeli dış gösterime çevirir.
func toCollectionDTO(col models.PaymentCollection) collectionDTO {
	return collectionDTO{
		ID:               col.ID,
		Reference:        col.Reference,
		Amount:           col.Amount,
		CurrencyCode:     col.CurrencyCode,
		Status:           col.Status.String(),
		AuthorizedAmount: col.AuthorizedAmount,
		CapturedAmount:   col.CapturedAmount,
		RefundedAmount:   col.RefundedAmount,
		Metadata:         col.Metadata,
		CreatedAt:        col.CreatedAt,
		UpdatedAt:        col.UpdatedAt,
	}
}

// toSessionDTO modeli dış gösterime çevirir.
func toSessionDTO(ses models.PaymentSession) sessionDTO {
	return sessionDTO{
		ID:                  ses.ID,
		PaymentCollectionID: ses.PaymentCollectionID,
		ProviderID:          ses.ProviderID,
		ExternalID:          ses.ExternalID,
		Status:              ses.Status.String(),
		Amount:              ses.Amount,
		AuthorizedAmount:    ses.AuthorizedAmount,
		CurrencyCode:        ses.CurrencyCode,
		Data:                ses.Data,
		DeclineReason:       ses.DeclineReason,
		CreatedAt:           ses.CreatedAt,
		UpdatedAt:           ses.UpdatedAt,
	}
}

// toPaymentDTO modeli dış gösterime çevirir.
func toPaymentDTO(pay models.Payment) paymentDTO {
	return paymentDTO{
		ID:                  pay.ID,
		PaymentSessionID:    pay.PaymentSessionID,
		PaymentCollectionID: pay.PaymentCollectionID,
		Amount:              pay.Amount,
		CurrencyCode:        pay.CurrencyCode,
		RefundedAmount:      pay.RefundedAmount,
		CapturedAt:          pay.CapturedAt,
		CreatedAt:           pay.CreatedAt,
		UpdatedAt:           pay.UpdatedAt,
	}
}

// toRefundDTO modeli dış gösterime çevirir.
func toRefundDTO(ref models.Refund) refundDTO {
	return refundDTO{
		ID:        ref.ID,
		PaymentID: ref.PaymentID,
		Amount:    ref.Amount,
		Reason:    ref.Reason,
		CreatedAt: ref.CreatedAt,
		UpdatedAt: ref.UpdatedAt,
	}
}

// --- yardımcılar -------------------------------------------------------------

// decodeBody istek gövdesini çözer.
//
// Gövde boyutu sınırlanır ve TANINMAYAN ALANLAR reddedilir: sessizce yutulan
// bir alan, istemcinin gönderdiğini sandığı ama uygulanmayan bir ayar demektir.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidRequest, "istek gövdesi boş olamaz")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"istek gövdesi çözümlenemedi")
	}
	// Tek bir JSON değerinden fazlası gönderilmişse bu da bir istemci hatasıdır.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidRequest,
			"istek gövdesi tek bir JSON nesnesi olmalı")
	}
	return nil
}

// parsePage limit/offset sorgu parametrelerini çözer.
func parsePage(r *http.Request) (service.Page, error) {
	limit, err := parseInt64Param(r, "limit")
	if err != nil {
		return service.Page{}, err
	}
	offset, err := parseInt64Param(r, "offset")
	if err != nil {
		return service.Page{}, err
	}
	page := service.Page{Limit: limit, Offset: offset}
	if page.Limit == 0 {
		// Yanıttaki limit alanının gerçekten uygulanan sınırı göstermesi için
		// varsayılan burada da görünür kılınır.
		page.Limit = service.DefaultLimit
	}
	return page, nil
}

// parseInt64Param bir sorgu parametresini tam sayıya çevirir; yoksa 0 döner.
func parseInt64Param(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"%s tam sayı olmalı: %q", name, raw)
	}
	return value, nil
}

// writeList bir dilimi liste zarfıyla yazar.
//
// Sayfalanmayan uçlarda (bir koleksiyonun oturumları gibi) count satır
// sayısıdır ve limit ile aynıdır: zarf her yerde aynı şekle sahiptir, istemci
// iki farklı yanıt biçimi öğrenmek zorunda kalmaz.
func writeList[T any](ctx context.Context, w http.ResponseWriter, items []T) {
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  int64(len(items)),
		Offset: 0,
		Limit:  int64(len(items)),
	})
}
