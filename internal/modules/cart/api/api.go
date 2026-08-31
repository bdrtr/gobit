// Package api cart modülünün HTTP yüzeyidir.
//
// İki yüzey vardır: müşteri tarafı (/store/v1/carts …) sepeti kurar ve
// değiştirir, yönetim tarafı (/admin/v1/carts) YALNIZCA OKUR. Sepet yönetim
// panelinden değiştirilmez; sepeti değiştiren tek taraf müşteridir ve sipariş
// düzeltmeleri Faz 6'daki order modülünün işidir.
//
// # HTTP'ye açılmayan yüzeyler
//
// [service.Service.SetTotals] ve [service.Service.MarkCompleted] BİLİNÇLİ
// OLARAK route almaz. İkisi de workflow yüzeyidir: toplamları hesaplayan
// calculate_totals ve sepeti kapatan complete_cart onları container'dan çözerek
// çağırır (ADR 0006). HTTP'ye açılsalardı bir istemci sepetin tutarını kendi
// yazabilir ya da ödeme yapmadan sepeti kapatabilirdi.
//
// # Yetki
//
// /admin/v1 altındaki uçlar kimlikten AYRI olarak yetki ister:
//
//   - [ScopeRead] ("cart:read") — GET uçlarını açar.
//   - [ScopeWrite] ("cart:write") — yazma uçlarını açardı; cart'ın yönetim
//     yüzeyi yalnızca okuma olduğu için bugün hiçbir route'a bağlı değildir.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR ve ikisini de karşılar; tam
// yetkili bir kimliğe ayrıca verilmesi gerekmez.
//
// /store/v1 uçları yetki İSTEMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki taşımaz.
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
	"github.com/bdrtr/gobit/internal/modules/cart/models"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
)

// maxBodyBytes istek gövdesi için üst sınırdır. Sınır olmadan tek bir istek
// sunucunun belleğini tüketebilirdi.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidRequest gövde/parametre çözümlenemediğinde dönen hata kodudur.
const codeInvalidRequest = "cart_invalid_request"

// URL parametre adları.
const (
	paramCartID     = "id"
	paramLineItemID = "line_item_id"
	paramMethodID   = "shipping_method_id"
)

// Carts handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç yüz satırlık bir sahte ile doğrulanabilir. Yüzeyde SetTotals
// ve MarkCompleted YOKTUR; ikisi de HTTP'ye açılmayan workflow yüzeyidir.
type Carts interface {
	// CreateCart yeni bir sepet oluşturur.
	CreateCart(ctx context.Context, in service.CreateCartInput) (models.Cart, error)
	// GetCart sepeti çocuklarıyla döner.
	GetCart(ctx context.Context, cartID string) (models.CartDetail, error)
	// UpdateCart sepetin e-posta ve müşteri alanlarını günceller.
	UpdateCart(ctx context.Context, cartID string, in service.UpdateCartInput) (models.Cart, error)
	// ListCarts sepetleri sayfalar.
	ListCarts(ctx context.Context, in service.ListCartsInput) ([]models.Cart, int64, error)
	// DeleteCart sepeti yumuşak siler.
	DeleteCart(ctx context.Context, cartID string) error

	// AddLineItem sepete satır ekler.
	AddLineItem(ctx context.Context, cartID string, in service.AddLineItemInput) (models.LineItem, error)
	// UpdateLineItemQuantity satırın adedini yazar.
	UpdateLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error)
	// RemoveLineItem satırı kaldırır.
	RemoveLineItem(ctx context.Context, cartID, lineID string) error

	// SetShippingAddress sepetin kargo adresini yazar.
	SetShippingAddress(ctx context.Context, cartID string, in service.AddressInput) (models.CartAddress, error)
	// SetBillingAddress sepetin fatura adresini yazar.
	SetBillingAddress(ctx context.Context, cartID string, in service.AddressInput) (models.CartAddress, error)

	// AddShippingMethod sepete kargo yöntemi ekler.
	AddShippingMethod(ctx context.Context, cartID string, in service.AddShippingMethodInput) (models.ShippingMethod, error)
	// RemoveShippingMethod kargo yöntemini kaldırır.
	RemoveShippingMethod(ctx context.Context, cartID, methodID string) error
}

// Handler cart modülünün HTTP handler kümesidir.
type Handler struct {
	svc Carts
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc Carts) *Handler {
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

// cartDTO sepetin dış gösterimidir.
//
// TotalsStale türetilmiş bir alandır ve toplamlarla BİRLİKTE sunulur: bayat bir
// tutarın doğru sanılması, bu API'nin üretebileceği en pahalı hata olurdu.
type cartDTO struct {
	ID            string         `json:"id"`
	RegionID      string         `json:"region_id"`
	CustomerID    string         `json:"customer_id,omitempty"`
	Email         string         `json:"email,omitempty"`
	CurrencyCode  string         `json:"currency_code"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	ShippingTotal int64          `json:"shipping_total"`
	Total         int64          `json:"total"`
	TotalsStale   bool           `json:"totals_stale"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CompletedAt   *time.Time     `json:"completed_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// cartDetailDTO sepetin çocuklarıyla birlikte dış gösterimidir.
type cartDetailDTO struct {
	cartDTO
	Items           []lineItemDTO       `json:"items"`
	ShippingAddress *addressDTO         `json:"shipping_address,omitempty"`
	BillingAddress  *addressDTO         `json:"billing_address,omitempty"`
	ShippingMethods []shippingMethodDTO `json:"shipping_methods"`
}

// lineItemDTO sepet satırının dış gösterimidir.
type lineItemDTO struct {
	ID            string         `json:"id"`
	CartID        string         `json:"cart_id"`
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

// addressDTO sepet adresinin dış gösterimidir.
type addressDTO struct {
	ID              string         `json:"id"`
	CartID          string         `json:"cart_id"`
	Type            string         `json:"type"`
	SourceAddressID string         `json:"source_address_id,omitempty"`
	FirstName       string         `json:"first_name,omitempty"`
	LastName        string         `json:"last_name,omitempty"`
	Company         string         `json:"company,omitempty"`
	Address1        string         `json:"address_1,omitempty"`
	Address2        string         `json:"address_2,omitempty"`
	City            string         `json:"city,omitempty"`
	Province        string         `json:"province,omitempty"`
	PostalCode      string         `json:"postal_code,omitempty"`
	CountryCode     string         `json:"country_code,omitempty"`
	Phone           string         `json:"phone,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

// shippingMethodDTO kargo yönteminin dış gösterimidir.
type shippingMethodDTO struct {
	ID               string         `json:"id"`
	CartID           string         `json:"cart_id"`
	Name             string         `json:"name"`
	ShippingOptionID string         `json:"shipping_option_id,omitempty"`
	Amount           int64          `json:"amount"`
	Data             map[string]any `json:"data,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

// toCartDTO modeli dış gösterime çevirir.
func toCartDTO(cart models.Cart) cartDTO {
	return cartDTO{
		ID:            cart.ID,
		RegionID:      cart.RegionID,
		CustomerID:    cart.CustomerID,
		Email:         cart.Email,
		CurrencyCode:  cart.CurrencyCode,
		Subtotal:      cart.Subtotal,
		DiscountTotal: cart.DiscountTotal,
		TaxTotal:      cart.TaxTotal,
		ShippingTotal: cart.ShippingTotal,
		Total:         cart.Total,
		TotalsStale:   cart.TotalsStale(),
		Metadata:      cart.Metadata,
		CompletedAt:   cart.CompletedAt,
		CreatedAt:     cart.CreatedAt,
		UpdatedAt:     cart.UpdatedAt,
	}
}

// toCartDetailDTO sepeti çocuklarıyla dış gösterime çevirir.
func toCartDetailDTO(detail models.CartDetail) cartDetailDTO {
	out := cartDetailDTO{
		cartDTO:         toCartDTO(detail.Cart),
		Items:           make([]lineItemDTO, 0, len(detail.Items)),
		ShippingMethods: make([]shippingMethodDTO, 0, len(detail.ShippingMethods)),
	}
	// Döngüler indeksle gezilir: satır ve yöntem yapıları büyüktür ve değerle
	// kopyalamak her tur birkaç yüz baytı boşuna taşır.
	for i := range detail.Items {
		out.Items = append(out.Items, toLineItemDTO(detail.Items[i]))
	}
	for i := range detail.ShippingMethods {
		out.ShippingMethods = append(out.ShippingMethods, toShippingMethodDTO(detail.ShippingMethods[i]))
	}
	if detail.ShippingAddress != nil {
		addr := toAddressDTO(*detail.ShippingAddress)
		out.ShippingAddress = &addr
	}
	if detail.BillingAddress != nil {
		addr := toAddressDTO(*detail.BillingAddress)
		out.BillingAddress = &addr
	}
	return out
}

// toLineItemDTO modeli dış gösterime çevirir.
func toLineItemDTO(item models.LineItem) lineItemDTO {
	return lineItemDTO{
		ID:            item.ID,
		CartID:        item.CartID,
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

// toAddressDTO modeli dış gösterime çevirir.
func toAddressDTO(addr models.CartAddress) addressDTO {
	return addressDTO{
		ID:              addr.ID,
		CartID:          addr.CartID,
		Type:            addr.Type.String(),
		SourceAddressID: addr.SourceAddressID,
		FirstName:       addr.FirstName,
		LastName:        addr.LastName,
		Company:         addr.Company,
		Address1:        addr.Address1,
		Address2:        addr.Address2,
		City:            addr.City,
		Province:        addr.Province,
		PostalCode:      addr.PostalCode,
		CountryCode:     addr.CountryCode,
		Phone:           addr.Phone,
		Metadata:        addr.Metadata,
		CreatedAt:       addr.CreatedAt,
		UpdatedAt:       addr.UpdatedAt,
	}
}

// toShippingMethodDTO modeli dış gösterime çevirir.
func toShippingMethodDTO(method models.ShippingMethod) shippingMethodDTO {
	return shippingMethodDTO{
		ID:               method.ID,
		CartID:           method.CartID,
		Name:             method.Name,
		ShippingOptionID: method.ShippingOptionID,
		Amount:           method.Amount,
		Data:             method.Data,
		CreatedAt:        method.CreatedAt,
		UpdatedAt:        method.UpdatedAt,
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

// addressRequest kargo ve fatura uçlarının ortak gövdesidir.
type addressRequest struct {
	SourceAddressID string         `json:"source_address_id"`
	FirstName       string         `json:"first_name"`
	LastName        string         `json:"last_name"`
	Company         string         `json:"company"`
	Address1        string         `json:"address_1"`
	Address2        string         `json:"address_2"`
	City            string         `json:"city"`
	Province        string         `json:"province"`
	PostalCode      string         `json:"postal_code"`
	CountryCode     string         `json:"country_code"`
	Phone           string         `json:"phone"`
	Metadata        map[string]any `json:"metadata"`
}

// toInput gövdeyi servis girdisine çevirir.
func (b addressRequest) toInput() service.AddressInput {
	return service.AddressInput{
		SourceAddressID: b.SourceAddressID,
		FirstName:       b.FirstName,
		LastName:        b.LastName,
		Company:         b.Company,
		Address1:        b.Address1,
		Address2:        b.Address2,
		City:            b.City,
		Province:        b.Province,
		PostalCode:      b.PostalCode,
		CountryCode:     b.CountryCode,
		Phone:           b.Phone,
		Metadata:        b.Metadata,
	}
}

// cartID istekten sepet kimliğini okur.
func cartID(r *http.Request) string {
	return chi.URLParam(r, paramCartID)
}
