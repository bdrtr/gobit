// Package api customer modülünün HTTP yüzeyidir.
//
// İki ad alanı vardır (plan Bölüm 8): /admin/v1 yönetim, /store/v1 müşteri.
//
// # UYARI: store uçları Faz 8'e kadar KORUMASIZDIR
//
// Store tarafındaki uçlar müşterinin KENDİ profilini ve adreslerini yönetir ve
// müşteriyi yol parametresindeki kimlikle tanır. Kimlik doğrulama Faz 8'de
// gelecektir (plan Faz 8: "store route'ları publishable key ile"); o zamana
// kadar bu uçlara ulaşan HERKES, kimliğini bildiği bir müşterinin adını,
// e-postasını ve adreslerini okuyabilir ve değiştirebilir.
//
// Bu bilinçli bir ara durumdur, gözden kaçmış bir açık değil: uçlar Faz 5'in
// sepet akışı için şimdi yazılır, koruma Faz 8'de auth middleware ile eklenir.
// Faz 8'de yapılacak iş: müşteri kimliğini yol parametresinden DEĞİL, oturum
// belirtecinden almak ve [storeCustomerID] yardımcısını o kaynağa bağlamak.
// Üretime bu hâliyle çıkılmamalıdır.
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7). Bu, hata sınıflandırmasının tek
// bir yerde kalmasını sağlar.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur. Sınırsız bir gövde,
// tek istekle belleği tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody istek gövdesi ya da parametresi çözümlenemediğinde dönen
// hata kodudur.
const codeInvalidBody = "customer_invalid_body"

// Yol parametrelerinin adları.
const (
	paramID         = "id"
	paramAddressID  = "address_id"
	paramCustomerID = "customer_id"
)

// Customer handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç satırlık bir sahte ile doğrulanabilir.
type Customer interface {
	// CreateCustomer kayıtlı bir müşteri hesabı oluşturur.
	CreateCustomer(ctx context.Context, in service.CustomerInput) (models.Customer, error)
	// RegisterGuest misafir müşteri kaydı oluşturur.
	RegisterGuest(ctx context.Context, in service.CustomerInput) (models.Customer, error)
	// GetCustomer müşteriyi kimliğiyle döner.
	GetCustomer(ctx context.Context, id string) (models.Customer, error)
	// ListCustomers müşterileri süzer ve sayfalar.
	ListCustomers(ctx context.Context, in service.ListCustomersInput) (service.Page[models.Customer], error)
	// UpdateCustomer müşterinin verilen alanlarını günceller.
	UpdateCustomer(ctx context.Context, id string, in service.UpdateCustomerInput) (models.Customer, error)
	// DeleteCustomer müşteriyi yumuşak siler.
	DeleteCustomer(ctx context.Context, id string) error
	// ConvertGuestToAccount misafiri kayıtlı hesaba çevirir.
	ConvertGuestToAccount(ctx context.Context, customerID string) error

	// CreateGroup yeni bir müşteri grubu oluşturur.
	CreateGroup(ctx context.Context, in service.GroupInput) (models.CustomerGroup, error)
	// GetGroup grubu kimliğiyle döner.
	GetGroup(ctx context.Context, id string) (models.CustomerGroup, error)
	// ListGroups grupları sayfalar.
	ListGroups(ctx context.Context, limit, offset int64) (service.Page[models.CustomerGroup], error)
	// UpdateGroup grubun verilen alanlarını günceller.
	UpdateGroup(ctx context.Context, id string, in service.UpdateGroupInput) (models.CustomerGroup, error)
	// DeleteGroup grubu yumuşak siler.
	DeleteGroup(ctx context.Context, id string) error
	// AddToGroup müşteriyi gruba ekler.
	AddToGroup(ctx context.Context, customerID, groupID string) error
	// RemoveFromGroup müşteriyi gruptan çıkarır.
	RemoveFromGroup(ctx context.Context, customerID, groupID string) error
	// ListGroupsOf müşterinin gruplarını döner.
	ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error)

	// CreateAddress müşterinin yeni adresini ekler.
	CreateAddress(ctx context.Context, customerID string, in service.AddressInput) (models.CustomerAddress, error)
	// ListAddresses müşterinin adreslerini döner.
	ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error)
	// UpdateAddress adresin verilen alanlarını günceller.
	UpdateAddress(ctx context.Context, customerID, addressID string, in service.UpdateAddressInput) (models.CustomerAddress, error)
	// DeleteAddress adresi yumuşak siler.
	DeleteAddress(ctx context.Context, customerID, addressID string) error
	// SetDefaultShippingAddress adresi varsayılan kargo adresi yapar.
	SetDefaultShippingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)
	// SetDefaultBillingAddress adresi varsayılan fatura adresi yapar.
	SetDefaultBillingAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)
}

// Handler customer modülünün HTTP handler kümesidir.
type Handler struct {
	svc Customer
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc Customer) *Handler {
	return &Handler{svc: svc}
}

// Routes customer'ın admin ve store route'larını router'a bağlar.
//
// Route'lar chi'nin Route/Mount yardımcılarıyla DEĞİL, tam yollarla kaydedilir:
// /admin/v1 önekini birden çok modül paylaşır ve aynı öneki iki kez Mount etmek
// chi'de panik üretirdi. Tam yol kaydı aynı ağaca yan yana yazar.
func (h *Handler) Routes(r chi.Router) {
	// --- yönetim ---
	r.Post("/admin/v1/customers", h.adminCreateCustomer)
	r.Get("/admin/v1/customers", h.adminListCustomers)
	r.Get("/admin/v1/customers/{id}", h.adminGetCustomer)
	r.Put("/admin/v1/customers/{id}", h.adminUpdateCustomer)
	r.Delete("/admin/v1/customers/{id}", h.adminDeleteCustomer)
	r.Post("/admin/v1/customers/{id}/convert-to-account", h.adminConvertGuest)

	r.Get("/admin/v1/customers/{id}/groups", h.adminListGroupsOfCustomer)
	r.Get("/admin/v1/customers/{id}/addresses", h.adminListAddresses)
	r.Post("/admin/v1/customers/{id}/addresses", h.adminCreateAddress)
	r.Put("/admin/v1/customers/{id}/addresses/{address_id}", h.adminUpdateAddress)
	r.Delete("/admin/v1/customers/{id}/addresses/{address_id}", h.adminDeleteAddress)
	r.Post("/admin/v1/customers/{id}/addresses/{address_id}/default-shipping", h.adminSetDefaultShipping)
	r.Post("/admin/v1/customers/{id}/addresses/{address_id}/default-billing", h.adminSetDefaultBilling)

	r.Post("/admin/v1/customer-groups", h.adminCreateGroup)
	r.Get("/admin/v1/customer-groups", h.adminListGroups)
	r.Get("/admin/v1/customer-groups/{id}", h.adminGetGroup)
	r.Put("/admin/v1/customer-groups/{id}", h.adminUpdateGroup)
	r.Delete("/admin/v1/customer-groups/{id}", h.adminDeleteGroup)
	r.Post("/admin/v1/customer-groups/{id}/customers", h.adminAddToGroup)
	r.Delete("/admin/v1/customer-groups/{id}/customers/{customer_id}", h.adminRemoveFromGroup)

	// --- vitrin (Faz 8'e kadar KORUMASIZ, bkz. paket belgesi) ---
	r.Post("/store/v1/customers", h.storeRegisterGuest)
	r.Get("/store/v1/customers/{id}", h.storeGetCustomer)
	r.Put("/store/v1/customers/{id}", h.storeUpdateCustomer)
	r.Get("/store/v1/customers/{id}/addresses", h.storeListAddresses)
	r.Post("/store/v1/customers/{id}/addresses", h.storeCreateAddress)
	r.Put("/store/v1/customers/{id}/addresses/{address_id}", h.storeUpdateAddress)
	r.Delete("/store/v1/customers/{id}/addresses/{address_id}", h.storeDeleteAddress)
	r.Post("/store/v1/customers/{id}/addresses/{address_id}/default-shipping", h.storeSetDefaultShipping)
	r.Post("/store/v1/customers/{id}/addresses/{address_id}/default-billing", h.storeSetDefaultBilling)
}

// itemEnvelope tekil yanıtların zarfıdır (plan Bölüm 8).
type itemEnvelope struct {
	// Data tek kaydın gövdesidir.
	Data any `json:"data"`
}

// listEnvelope liste yanıtlarının zarfıdır (plan Bölüm 8).
type listEnvelope struct {
	// Data geçerli sayfadaki kayıtlardır.
	Data any `json:"data"`
	// Count filtreye uyan TOPLAM kayıt sayısıdır.
	Count int64 `json:"count"`
	// Offset uygulanan atlama sayısıdır.
	Offset int64 `json:"offset"`
	// Limit uygulanan sayfa boyudur.
	Limit int64 `json:"limit"`
}

// writeItem tekil yanıtı zarfıyla yazar.
func writeItem(w http.ResponseWriter, r *http.Request, status int, data any) {
	corehttp.WriteJSON(r.Context(), w, status, itemEnvelope{Data: data})
}

// writeItems sayfalanmamış bir listeyi zarfıyla yazar.
//
// Sayfalanmayan uç noktalarda (bir müşterinin adresleri, grupları) zarfın
// sayısal alanları kayıt sayısıyla doldurulur: istemcinin zarf şekli uç noktaya
// göre değişmez.
//
// Limit, dönen kayıt sayısına EŞİTTİR ve [service.MaxLimit] ile KIRPILMAZ.
// Kırpılsaydı 250 adresli bir müşteri için yanıt "count=250, limit=100" derdi;
// istemci sayfa boyunu 100 sanıp sayfalama döngüsüne girer ve aynı kayıtları
// tekrar okurdu. Burada sayfa yoktur — tek sayfa tüm kayıtlardır.
func writeItems[T any](w http.ResponseWriter, r *http.Request, items []T) {
	if items == nil {
		items = []T{}
	}
	count := int64(len(items))
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  count,
		Offset: 0,
		Limit:  count,
	})
}

// writePage servis sayfasını liste zarfıyla yazar.
func writePage[S any, T any](w http.ResponseWriter, r *http.Request, page service.Page[S], convert func(S) T) {
	items := make([]T, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, convert(item))
	}
	corehttp.WriteJSON(r.Context(), w, http.StatusOK, listEnvelope{
		Data:   items,
		Count:  page.Count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// convertAll bir dilimi DTO dilimine çevirir; nil dilim boş dilime döner.
func convertAll[S any, T any](items []S, convert func(S) T) []T {
	out := make([]T, 0, len(items))
	for _, item := range items {
		out = append(out, convert(item))
	}
	return out
}

// decodeBody istek gövdesini hedefe çözer.
//
// Bilinmeyen alanlar REDDEDİLİR: sessizce yok sayılan bir alan, istemcinin
// gönderdiğini sandığı bir değerin hiç yazılmaması demektir. Gövde boyutu da
// sınırlıdır; aşılırsa çözümleme hatası olarak döner.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return coreerrors.Invalid(codeInvalidBody, "istek gövdesi boş olamaz")
		}
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidBody,
			"istek gövdesi çözümlenemedi")
	}

	// Tek bir JSON belgesi beklenir; arkasından gelen ikinci belge sessizce
	// yok sayılırsa istemci gönderdiğinin işlendiğini sanırdı.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return coreerrors.Invalid(codeInvalidBody, "istek gövdesi tek bir JSON belgesi olmalı")
	}
	return nil
}

// pathParam yol parametresini okur.
func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

// storeCustomerID vitrin isteğinin hangi müşteriye ait olduğunu döner.
//
// FAZ 8 BAĞLAMA NOKTASI: kimlik ŞİMDİLİK yol parametresinden okunur, yani
// istemcinin kendi bildirdiği değerdir ve doğrulanmaz. Auth geldiğinde bu
// fonksiyon kimliği oturum belirtecinden almalı, yol parametresiyle
// karşılaştırıp uyuşmazlıkta errors.Forbidden dönmelidir. Tek bir yerde
// durması, o değişikliğin tek dosyada yapılabilmesi içindir.
func storeCustomerID(r *http.Request) string {
	return pathParam(r, paramID)
}

// pageParams sorgu dizesinden sayfalama parametrelerini okur.
//
// Eksik parametre sıfır döner ve servis varsayılanı uygular; SAYIYA
// ÇEVRİLEMEYEN bir değer ise hata döner — sessizce sıfıra düşmek, istemcinin
// istediği sayfa yerine ilk sayfayı almasına yol açardı.
func pageParams(r *http.Request) (limit, offset int64, err error) {
	limit, err = intParam(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	offset, err = intParam(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	return limit, offset, nil
}

// intParam tek bir sayısal sorgu parametresini okur; yoksa sıfır döner.
func intParam(r *http.Request, name string) (int64, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, coreerrors.Invalid(codeInvalidBody,
			"%q parametresi tam sayı olmalı, %q verildi", name, raw)
	}
	return value, nil
}

// boolParam bir mantıksal sorgu parametresini okur; yoksa nil döner.
//
// nil ile false arasındaki fark burada anlamlıdır: "has_account=false"
// misafirleri süzer, parametrenin hiç verilmemesi ise süzmez.
func boolParam(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, coreerrors.Invalid(codeInvalidBody,
			"%q parametresi mantıksal (true/false) olmalı, %q verildi", name, raw)
	}
	return &value, nil
}

// stringParam bir metin sorgu parametresini okur; yoksa nil döner.
func stringParam(r *http.Request, name string) *string {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil
	}
	return &raw
}
