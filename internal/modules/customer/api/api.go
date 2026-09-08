// Package api customer modülünün HTTP yüzeyidir.
//
// İki ad alanı vardır (plan Bölüm 8): /admin/v1 yönetim, /store/v1 müşteri.
//
// # The storefront REQUIRES an identity this framework does not issue
//
// The store endpoints manage a customer's OWN profile and addresses, and the
// customer is named in the path. That claim is not taken on trust: every one of
// them resolves a corehttp.Identity from the container under
// corehttp.IdentityName, asks it what the request PROVES, and refuses the
// request unless the two agree ([Handler.storeCustomerID], ADR 0043).
//
// gobit still verifies nothing itself. It holds no proof about the person
// behind a storefront request — ADR 0008 drew that boundary, it STANDS, and the
// embedder is the one who satisfies the contract. What changed on 2026-09-08 is
// the failure mode of an installation that never read ADR 0008: with nothing
// bound these routes REJECT with corehttp.CodeIdentityNotBound instead of
// handing a stranger somebody's street address. Closed rather than open is
// ADR 0007's row for an unconfigured authenticator, and this is the same row.
//
// POST /store/v1/customers is the one store route that asks nothing, and the
// reason is that there is nobody to ask about yet: it MINTS a guest record, so
// the customer the request would have to prove does not exist until it answers.
//
// # One word in the old warning was wrong, and it is worth not repeating
//
// This surface was called "unauthenticated" here and in the defect ledger. It
// is not: the six address routes and the two profile routes sit under the store
// prefix of the guard stack, and a request without a publishable key never
// reaches a handler — corehttp.RequireStore answers it with a 401. What the
// address book lacked was AUTHORIZATION: nothing asked whether the caller was
// the customer the path named. Naming the gap wrongly cost a reader the search
// for a missing 401 that was never missing.
//
// # Yetki
//
// /admin/v1 altındaki uçlar kimlikten AYRI olarak yetki ister:
//
//   - [ScopeRead] ("customer:read") — GET uçlarını açar.
//   - [ScopeWrite] ("customer:write") — POST, PUT ve DELETE uçlarını açar.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR ve ikisini de karşılar; tam
// yetkili bir kimliğe ayrıca verilmesi gerekmez.
//
// /store/v1 uçları yetki İSTEMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki taşımaz.
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

	coreerrors "github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	corepage "github.com/bdrtr/gobit/internal/core/page"
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
	// identity proves which customer a storefront request belongs to. It is
	// NIL when the installation bound none, and that state is CLOSED rather
	// than open; see [Handler.storeCustomerID].
	identity corehttp.Identity
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
//
// identity may be nil and the zero value is the SAFE one: with no identity the
// store routes that name a customer refuse every request. A constructor that
// defaulted a missing identity to "the path is the truth" would restore the
// hole ADR 0043 closed, and it would do it silently — which is this
// repository's most expensive class of fault.
//
// The module hands in a wrapper that resolves the embedder's implementation
// from the container ON FIRST USE (see the customer module's Register), so a
// nil arriving here means the caller built the handler by hand: a test, or an
// embedder driving the package directly.
func New(svc Customer, identity corehttp.Identity) *Handler {
	return &Handler{svc: svc, identity: identity}
}

// Yetki sözlüğü: customer'ın yönetim uçlarının istediği yetkiler.
//
// Adlar TÜM modüllerde aynı kalıptadır ("<modül>:read" / "<modül>:write").
// Her modülün kendi sözcüğünü uydurması, yetki dağıtan kişinin modül başına
// ayrı bir sözlük ezberlemesi demek olurdu; ezberlenmeyen sözlükte yapılan
// hata da her zaman aynı yöne düşer — fazla yetki verilir.
const (
	// ScopeRead customer yönetim yüzeyindeki OKUMA uçlarının istediği
	// yetkidir.
	//
	// Müşteri kayıtlarını, adreslerini ve gruplarını okumaya yeter; hiçbir
	// yazma ucunu açmaz. Tam yetkili kimliklere ayrıca verilmesi gerekmez:
	// corehttp.ScopeAdmin taşıyan bir çağıran bunu da karşılar (bkz.
	// corehttp.Principal.HasScope).
	ScopeRead = "customer:read"

	// ScopeWrite customer yönetim yüzeyindeki YAZMA uçlarının istediği
	// yetkidir.
	//
	// Müşteri oluşturma, güncelleme, silme, misafiri hesaba çevirme, adres
	// yazma ve grup üyeliği değiştirme uçlarını açar. Bu uçların bazıları
	// kişisel veriyi KALICI olarak değiştirir; okuma yetkisiyle karışmaması
	// bu yüzden önemlidir.
	ScopeWrite = "customer:write"
)

// Routes customer'ın admin ve store route'larını router'a bağlar.
//
// Route'lar chi'nin Route/Mount yardımcılarıyla DEĞİL, tam yollarla kaydedilir:
// /admin/v1 önekini birden çok modül paylaşır ve aynı öneki iki kez Mount etmek
// chi'de panik üretirdi. Tam yol kaydı aynı ağaca yan yana yazar.
//
// # KORUMA
//
// Yönetim uçlarının iki katmanı vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — corehttp.RequireAdmin. Bu modülde DEĞİL, router'ı kuran
//     tarafta takılır (bkz. corehttp.APIGuards).
//  2. YETKİ — BURADA, uç uç corehttp.RequireScope ile: okuma uçları
//     [ScopeRead], yazma uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri bilinçli olarak boşaltılmış bir yönetim kullanıcısı da geçerli bir
// kimliktir ve DELETE /admin/v1/customers/{id} ile müşteri kayıtlarını
// silebilirdi.
//
// Store uçlarına yetki EKLENMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ. The authorization the
// store routes DO carry is a different question with a different answer: the
// customer named in the path has to be the customer the request proves, and
// that check is inside the handler rather than in front of it — see
// [Handler.storeCustomerID]. It cannot be middleware here for the same reason
// the core's is not: the parameter it compares against belongs to the route.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	// --- yönetim ---
	yazma.Post("/admin/v1/customers", h.adminCreateCustomer)
	okuma.Get("/admin/v1/customers", h.adminListCustomers)
	okuma.Get("/admin/v1/customers/{id}", h.adminGetCustomer)
	yazma.Put("/admin/v1/customers/{id}", h.adminUpdateCustomer)
	yazma.Delete("/admin/v1/customers/{id}", h.adminDeleteCustomer)
	yazma.Post("/admin/v1/customers/{id}/convert-to-account", h.adminConvertGuest)

	okuma.Get("/admin/v1/customers/{id}/groups", h.adminListGroupsOfCustomer)
	okuma.Get("/admin/v1/customers/{id}/addresses", h.adminListAddresses)
	yazma.Post("/admin/v1/customers/{id}/addresses", h.adminCreateAddress)
	yazma.Put("/admin/v1/customers/{id}/addresses/{address_id}", h.adminUpdateAddress)
	yazma.Delete("/admin/v1/customers/{id}/addresses/{address_id}", h.adminDeleteAddress)
	yazma.Post("/admin/v1/customers/{id}/addresses/{address_id}/default-shipping", h.adminSetDefaultShipping)
	yazma.Post("/admin/v1/customers/{id}/addresses/{address_id}/default-billing", h.adminSetDefaultBilling)

	yazma.Post("/admin/v1/customer-groups", h.adminCreateGroup)
	okuma.Get("/admin/v1/customer-groups", h.adminListGroups)
	okuma.Get("/admin/v1/customer-groups/{id}", h.adminGetGroup)
	yazma.Put("/admin/v1/customer-groups/{id}", h.adminUpdateGroup)
	yazma.Delete("/admin/v1/customer-groups/{id}", h.adminDeleteGroup)
	yazma.Post("/admin/v1/customer-groups/{id}/customers", h.adminAddToGroup)
	yazma.Delete("/admin/v1/customer-groups/{id}/customers/{customer_id}", h.adminRemoveFromGroup)

	// --- vitrin ---
	//
	// The eight routes carrying {id} require the claim to be BACKED; the guest
	// registration below is the one that cannot, because it is what creates the
	// customer (see the package doc).
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
	// NextCursor is the opaque position to send back as "after" for the next
	// page; it is ABSENT when this page is the last one.
	//
	// Its absence is the end-of-listing signal, which is what a client walking
	// forward needs and what offset alone cannot give without a count.
	NextCursor string `json:"next_cursor,omitempty"`
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
		Data:       items,
		Count:      page.Count,
		Offset:     page.Offset,
		Limit:      page.Limit,
		NextCursor: page.NextCursor,
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

// storeCustomerID returns the customer a storefront request is allowed to act
// on, or an error that ends the request.
//
// # What it checks
//
// The path names a customer; that is the CLAIM. The bound identity says which
// customer the request PROVES. The two must be the same string, and every other
// outcome is a refusal: nothing bound is a 401 naming the missing binding, the
// identity's own error passes through unwrapped so the embedder picks the
// status, an identity that proves nothing is a 500, and a disagreement is a 403
// rather than a 404 — the caller supplied the identifier, so there is no
// existence to hide.
//
// # Where the comparison lives
//
// In corehttp.ProvenCustomer, and it was written out here until ADR 0057. It
// moved when the second and third surfaces needed it — the b2b storefront and
// the cart — and the b2b package doc had already refused to take the contract
// in passing, because a second copy of an authorization rule keeps answering
// after it drifts. The four refusals above are summarized here and DECIDED
// there: why each is the kind it is, and why the proven identifier rather than
// the claimed one comes back, are on that function.
//
// What stays here is the CLAIM. Which part of this module's requests names a
// customer is this package's knowledge, and the path parameter is the one thing
// a shared comparison could not know.
func (h *Handler) storeCustomerID(r *http.Request) (string, error) {
	return corehttp.ProvenCustomer(h.identity, r, pathParam(r, paramID))
}

// afterParam reads the cursor of the page being asked for.
//
// An offset alongside it is REFUSED: a cursor and an offset each name a
// position, and honoring both would serve the page N rows past the cursor,
// which neither of them asked for.
func afterParam(r *http.Request, listing string, offset int64) (corepage.Cursor, error) {
	raw := r.URL.Query().Get("after")
	if raw == "" {
		return corepage.Cursor{}, nil
	}
	if offset != 0 {
		return corepage.Cursor{}, coreerrors.Invalid(codeInvalidBody,
			`"after" and "offset" name two different positions; send one of them`)
	}

	return corepage.Decode(listing, raw)
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
