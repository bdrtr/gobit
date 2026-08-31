// Package api order modülünün HTTP yüzeyidir.
//
// İki yüzey vardır: yönetim tarafı (/admin/v1/orders …) siparişi okur ve durum
// geçişlerini uygular, müşteri tarafı (/store/v1/orders/{id}) YALNIZCA OKUR.
//
// # Yetki
//
// Yönetim uçları yetki İSTER ve yetki uç uç zorlanır (bkz. [Handler.Routes]):
//
//   - [ScopeRead] ("order:read") — /admin/v1 altındaki GET uçlarını açar:
//     sipariş listesi ve tekil sipariş, iade/değişim/hasar kayıtları.
//   - [ScopeWrite] ("order:write") — /admin/v1 altındaki POST uçlarını açar:
//     iptal, tamamla, arşivle ve satış sonrası kayıt oluşturma.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR; ikisini de tek başına karşılar
// (bkz. corehttp.Principal.HasScope).
//
// Mağaza ucuna yetki EKLENMEZ: /store/v1'in kimliği publishable anahtardır ve
// o anahtar tanımı gereği yetki taşımaz.
//
// # HTTP'ye açılmayan yüzeyler
//
// [service.Service.CreateOrder] BİLİNÇLİ OLARAK route almaz. Sipariş, tutarları
// dışarıdan verilen bir kayıttır: HTTP'ye açılsaydı bir istemci kendi
// belirlediği toplamla — örneğin sıfır tutarla — sipariş yazabilirdi.
// Doğrulama katmanları yalnızca girdinin KENDİ İÇİNDE tutarlı olmasını
// sağlar, tutarların GERÇEK fiyatlara karşılık geldiğini değil; o güvenceyi
// veren tek şey, görüntüyü sepetten ve pricing'den kuran complete_cart
// workflow'udur (ADR 0006). Sipariş bu yüzden yalnızca "order.interop"
// üzerinden açılır.
//
// [service.Service.SetOrderSummaryTotals] de aynı sebeple route almaz: ödenen
// tutarı bilen taraf ödeme akışıdır, istemci değil.
//
// Yönetim tarafından elle sipariş oluşturma (draft order) sonraki fazların
// işidir ve geldiğinde kendi doğrulama zinciriyle gelmelidir.
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
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// maxBodyBytes istek gövdesi için üst sınırdır. Sınır olmadan tek bir istek
// sunucunun belleğini tüketebilirdi.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest gövde/parametre çözümlenemediğinde dönen hata kodudur.
const codeInvalidRequest = "order_invalid_request"

// URL parametre adları.
const (
	// paramOrderID sipariş kimliğinin URL parametre adıdır.
	paramOrderID = "id"
	// paramReturnID iade kaydı kimliğinin URL parametre adıdır.
	paramReturnID = "returnId"
	// paramExchangeID değişim kaydı kimliğinin URL parametre adıdır.
	paramExchangeID = "exchangeId"
	// paramClaimID hasar kaydı kimliğinin URL parametre adıdır.
	paramClaimID = "claimId"
)

// Orders handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç yüz satırlık bir sahte ile doğrulanabilir. Yüzeyde CreateOrder
// ve SetOrderSummaryTotals YOKTUR; ikisi de HTTP'ye açılmayan workflow
// yüzeyidir (bkz. paket belgesi).
type Orders interface {
	// GetOrder siparişi satırları ve özetiyle döner.
	GetOrder(ctx context.Context, orderID string) (models.OrderDetail, error)
	// ListOrders siparişleri sayfalar.
	ListOrders(ctx context.Context, in service.ListOrdersInput) ([]models.Order, int64, error)
	// CancelOrder siparişi iptal eder; idempotenttir.
	CancelOrder(ctx context.Context, orderID, reason string) error
	// CompleteOrder siparişi tamamlar.
	CompleteOrder(ctx context.Context, orderID string) (models.Order, error)
	// ArchiveOrder tamamlanmış siparişi arşivler.
	ArchiveOrder(ctx context.Context, orderID string) (models.Order, error)

	// CreateReturn siparişe iade kaydı açar.
	CreateReturn(ctx context.Context, in service.CreateReturnInput) (models.Return, error)
	// GetReturn iade kaydını kimliğiyle döner.
	GetReturn(ctx context.Context, returnID string) (models.Return, error)
	// ListReturns siparişin iade kayıtlarını sayfalar.
	ListReturns(ctx context.Context, orderID string, page service.Page) ([]models.Return, int64, error)

	// CreateExchange siparişe değişim kaydı açar.
	CreateExchange(ctx context.Context, in service.CreateExchangeInput) (models.Exchange, error)
	// GetExchange değişim kaydını kimliğiyle döner.
	GetExchange(ctx context.Context, exchangeID string) (models.Exchange, error)
	// ListExchanges siparişin değişim kayıtlarını sayfalar.
	ListExchanges(ctx context.Context, orderID string, page service.Page) ([]models.Exchange, int64, error)

	// CreateClaim siparişe hasar kaydı açar.
	CreateClaim(ctx context.Context, in service.CreateClaimInput) (models.Claim, error)
	// GetClaim hasar kaydını kimliğiyle döner.
	GetClaim(ctx context.Context, claimID string) (models.Claim, error)
	// ListClaims siparişin hasar kayıtlarını sayfalar.
	ListClaims(ctx context.Context, orderID string, page service.Page) ([]models.Claim, int64, error)
}

// Handler order modülünün HTTP handler kümesidir.
type Handler struct {
	svc Orders
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc Orders) *Handler {
	return &Handler{svc: svc}
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
	// Count filtreye uyan TÜM kayıtların sayısıdır; sayfadaki satır sayısı değil.
	Count int64 `json:"count"`
	// Offset atlanan kayıt sayısıdır.
	Offset int64 `json:"offset"`
	// Limit istenen sayfa boyutudur.
	Limit int64 `json:"limit"`
}

// orderDTO siparişin dış gösterimidir.
type orderDTO struct {
	ID            string         `json:"id"`
	DisplayID     int64          `json:"display_id"`
	Status        string         `json:"status"`
	RegionID      string         `json:"region_id"`
	CustomerID    string         `json:"customer_id,omitempty"`
	Email         string         `json:"email,omitempty"`
	CurrencyCode  string         `json:"currency_code"`
	CartID        string         `json:"cart_id,omitempty"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	ShippingTotal int64          `json:"shipping_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	PlacedAt      time.Time      `json:"placed_at"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CanceledAt    *time.Time     `json:"canceled_at,omitempty"`
	CancelReason  string         `json:"cancel_reason,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// orderDetailDTO siparişin satırları ve özetiyle dış gösterimidir.
type orderDetailDTO struct {
	orderDTO
	Items   []lineItemDTO `json:"items"`
	Summary summaryDTO    `json:"summary"`
}

// lineItemDTO sipariş satırının dış gösterimidir.
type lineItemDTO struct {
	ID            string         `json:"id"`
	OrderID       string         `json:"order_id"`
	VariantID     string         `json:"variant_id"`
	Title         string         `json:"title"`
	Quantity      int64          `json:"quantity"`
	UnitPrice     int64          `json:"unit_price"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// summaryDTO siparişin ödeme/iade özetinin dış gösterimidir.
//
// Outstanding TÜRETİLMİŞ bir alandır ve tutarlarla BİRLİKTE sunulur: kalan
// tutarı istemciye kendi hesaplattırmak, aynı formülün iki yerde yazılması ve
// birinin yanlış olması demekti. Değer NEGATİF olabilir (fazla tahsilat).
type summaryDTO struct {
	ID            string    `json:"id"`
	OrderID       string    `json:"order_id"`
	PaidTotal     int64     `json:"paid_total"`
	RefundedTotal int64     `json:"refunded_total"`
	Outstanding   int64     `json:"outstanding"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// returnDTO iade kaydının dış gösterimidir.
type returnDTO struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"order_id"`
	Status       string         `json:"status"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason,omitempty"`
	Note         string         `json:"note,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ReceivedAt   *time.Time     `json:"received_at,omitempty"`
	CanceledAt   *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// exchangeDTO değişim kaydının dış gösterimidir.
type exchangeDTO struct {
	ID            string         `json:"id"`
	OrderID       string         `json:"order_id"`
	Status        string         `json:"status"`
	DifferenceDue int64          `json:"difference_due"`
	Note          string         `json:"note,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CanceledAt    *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// claimDTO hasar kaydının dış gösterimidir.
type claimDTO struct {
	ID           string         `json:"id"`
	OrderID      string         `json:"order_id"`
	Type         string         `json:"type"`
	Status       string         `json:"status"`
	RefundAmount int64          `json:"refund_amount"`
	Reason       string         `json:"reason,omitempty"`
	Note         string         `json:"note,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CompletedAt  *time.Time     `json:"completed_at,omitempty"`
	CanceledAt   *time.Time     `json:"canceled_at,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// toOrderDTO modeli dış gösterime çevirir.
func toOrderDTO(order models.Order) orderDTO {
	return orderDTO{
		ID:            order.ID,
		DisplayID:     order.DisplayID,
		Status:        order.Status.String(),
		RegionID:      order.RegionID,
		CustomerID:    order.CustomerID,
		Email:         order.Email,
		CurrencyCode:  order.CurrencyCode,
		CartID:        order.CartID,
		Subtotal:      order.Subtotal,
		DiscountTotal: order.DiscountTotal,
		TaxTotal:      order.TaxTotal,
		ShippingTotal: order.ShippingTotal,
		Total:         order.Total,
		Metadata:      order.Metadata,
		PlacedAt:      order.PlacedAt,
		CompletedAt:   order.CompletedAt,
		CanceledAt:    order.CanceledAt,
		CancelReason:  order.CancelReason,
		CreatedAt:     order.CreatedAt,
		UpdatedAt:     order.UpdatedAt,
	}
}

// toOrderDetailDTO siparişi satırları ve özetiyle dış gösterime çevirir.
func toOrderDetailDTO(detail models.OrderDetail) orderDetailDTO {
	out := orderDetailDTO{
		orderDTO: toOrderDTO(detail.Order),
		Items:    make([]lineItemDTO, 0, len(detail.Items)),
		Summary:  toSummaryDTO(detail.Summary, detail.Total),
	}
	// Döngü indeksle gezilir: satır yapısı büyüktür ve değerle kopyalamak her
	// tur birkaç yüz baytı boşuna taşır.
	for i := range detail.Items {
		out.Items = append(out.Items, toLineItemDTO(detail.Items[i]))
	}
	return out
}

// toLineItemDTO modeli dış gösterime çevirir.
func toLineItemDTO(item models.OrderLineItem) lineItemDTO {
	return lineItemDTO{
		ID:            item.ID,
		OrderID:       item.OrderID,
		VariantID:     item.VariantID,
		Title:         item.Title,
		Quantity:      item.Quantity,
		UnitPrice:     item.UnitPrice,
		Subtotal:      item.Subtotal,
		DiscountTotal: item.DiscountTotal,
		TaxTotal:      item.TaxTotal,
		Total:         item.Total,
		Metadata:      item.Metadata,
		CreatedAt:     item.CreatedAt,
		UpdatedAt:     item.UpdatedAt,
	}
}

// toSummaryDTO özeti dış gösterime çevirir; kalan tutar sipariş toplamından
// hesaplanır.
func toSummaryDTO(summary models.OrderSummary, orderTotal int64) summaryDTO {
	return summaryDTO{
		ID:            summary.ID,
		OrderID:       summary.OrderID,
		PaidTotal:     summary.PaidTotal,
		RefundedTotal: summary.RefundedTotal,
		Outstanding:   summary.Outstanding(orderTotal),
		CreatedAt:     summary.CreatedAt,
		UpdatedAt:     summary.UpdatedAt,
	}
}

// toReturnDTO modeli dış gösterime çevirir.
func toReturnDTO(ret models.Return) returnDTO {
	return returnDTO{
		ID:           ret.ID,
		OrderID:      ret.OrderID,
		Status:       ret.Status.String(),
		RefundAmount: ret.RefundAmount,
		Reason:       ret.Reason,
		Note:         ret.Note,
		Metadata:     ret.Metadata,
		ReceivedAt:   ret.ReceivedAt,
		CanceledAt:   ret.CanceledAt,
		CreatedAt:    ret.CreatedAt,
		UpdatedAt:    ret.UpdatedAt,
	}
}

// toExchangeDTO modeli dış gösterime çevirir.
func toExchangeDTO(exchange models.Exchange) exchangeDTO {
	return exchangeDTO{
		ID:            exchange.ID,
		OrderID:       exchange.OrderID,
		Status:        exchange.Status.String(),
		DifferenceDue: exchange.DifferenceDue,
		Note:          exchange.Note,
		Metadata:      exchange.Metadata,
		CompletedAt:   exchange.CompletedAt,
		CanceledAt:    exchange.CanceledAt,
		CreatedAt:     exchange.CreatedAt,
		UpdatedAt:     exchange.UpdatedAt,
	}
}

// toClaimDTO modeli dış gösterime çevirir.
func toClaimDTO(claim models.Claim) claimDTO {
	return claimDTO{
		ID:           claim.ID,
		OrderID:      claim.OrderID,
		Type:         claim.Type.String(),
		Status:       claim.Status.String(),
		RefundAmount: claim.RefundAmount,
		Reason:       claim.Reason,
		Note:         claim.Note,
		Metadata:     claim.Metadata,
		CompletedAt:  claim.CompletedAt,
		CanceledAt:   claim.CanceledAt,
		CreatedAt:    claim.CreatedAt,
		UpdatedAt:    claim.UpdatedAt,
	}
}

// --- yardımcılar -------------------------------------------------------------

// decodeBody istek gövdesini çözer; gövde ZORUNLUDUR.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSON(w, r, dst, false)
}

// decodeOptionalBody gövdesi BOŞ BIRAKILABİLEN istekleri çözer.
//
// İptal ucunda gövde yalnızca isteğe bağlı bir gerekçe taşır; boş gövdeyi hata
// saymak, gerekçesiz iptali imkânsız kılardı. Gövde gönderilmişse [decodeJSON]
// katılığının tamamı (bilinmeyen alan reddi dâhil) geçerlidir.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSON(w, r, dst, true)
}

// decodeJSON istek gövdesini çözer.
//
// Gövde boyutu sınırlanır ve TANINMAYAN ALANLAR reddedilir: sessizce yutulan
// bir alan, istemcinin gönderdiğini sandığı ama uygulanmayan bir ayar demektir.
//
// allowEmpty true ise hiç gövde göndermemek geçerlidir ve dst sıfır değerinde
// kalır. Boşluk kontrolü Content-Length'e DEĞİL çözümlemenin io.EOF'una
// bakılarak yapılır: chunked bir istekte uzunluk -1'dir ve uzunluğa bakan bir
// kontrol o istekleri yanlış sınıflandırırdı.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, allowEmpty bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			if allowEmpty {
				return nil
			}
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

// orderID istekten sipariş kimliğini okur.
func orderID(r *http.Request) string {
	return chi.URLParam(r, paramOrderID)
}
