// Package api b2b modülünün HTTP yüzeyidir.
//
// İki ad alanı vardır (plan Bölüm 8): /admin/v1 yönetim, /store/v1 müşteri.
// Modülün tüm uçları "b2b" segmentinin altındadır; bir sonraki B2B kavramı
// (teklif, onay akışı) eklendiğinde de aynı ağaca girer.
//
// # Vitrin: başkasının şirketi OKUNAMAZ
//
// Vitrin yüzeyinde şirket kimliğiyle çağrılan bir uç YOKTUR. Şirkete ulaşan tek
// yol müşterinin KENDİ çalışan kaydından geçer:
//
//	GET /store/v1/b2b/customers/{customer_id}/company
//	GET /store/v1/b2b/customers/{customer_id}/employee
//
// İkisi de yalnızca o müşterinin üyeliğini çözer (bkz.
// service.Service.MembershipOfCustomer). "Başkasının şirketini oku" isteği bu
// yüzden reddedilen bir istek değil, İFADE EDİLEMEYEN bir istektir: istemcinin
// yazabileceği bir şirket kimliği parametresi hiçbir uçta bulunmaz.
//
// # Vitrin uçları müşteriyi YOL PARAMETRESİNDEN okur, ama artık İNANMAZ
//
// Bu depoda vitrin isteklerinin kimliği publishable API anahtarıdır ve o anahtar
// bir SATIŞ KANALINI temsil eder, bir müşteriyi değil (bkz. corehttp.RequireStore).
// Uçlar customer idni yoldan alır; o değer bir İDDİADIR ve karşılığı aranır.
//
// Until 2026-09-08 nothing looked. What that cost is worth keeping, because it
// is what the change bought: a caller holding somebody's customer id read that
// person's employer — the company's name, contact address and billing address
// — and their spending limit, its reset interval and the start of the current
// window. Nothing about the identifier is a secret; it travels in cart and
// order response bodies. Closing the company's own name as a parameter was
// never the hole. The hole was believing the customer id.
//
// Since ADR 0057 both routes resolve a corehttp.Identity from the container
// under corehttp.IdentityName, ask it what the request PROVES and refuse when
// the two DISAGREE ([Handler.storeCustomerID]). With nothing bound they answer
// as they always have: an installation that never bound a verifier keeps its
// working B2B storefront, and the oracle above stays open there until it binds
// one. That residue is stated rather than paid for by an upgrade — the address
// book could be closed outright in ADR 0043 because no anonymous caller has a
// correct use for somebody's street address, and these two routes are the same
// class of leak reached through a surface that is in service.
//
// gobit still verifies nothing itself: ADR 0008 stands and the embedder is the
// one who satisfies the contract. The comparison is corehttp.ProvenCustomer's
// and not this package's, which is what keeps this module's copy from becoming
// a second, silently diverging answer to one authorization question.
//
// # Yetki
//
// /admin/v1 altındaki uçlar kimlikten AYRI olarak yetki ister:
//
//   - [ScopeRead] ("b2b:read") — GET uçlarını açar.
//   - [ScopeWrite] ("b2b:write") — POST, PUT ve DELETE uçlarını açar.
//
// corehttp.ScopeAdmin ("admin") ÜST YETKİDİR ve ikisini de karşılar.
//
// Handler'lar status kodu SEÇMEZ: servis tipli hata döner, corehttp.WriteError
// onu status koduna çevirir (plan Bölüm 2.7).
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
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// maxBodyBytes tek bir istek gövdesinin azami boyutudur. Sınırsız bir gövde,
// tek istekle belleği tüketmenin en ucuz yoludur.
const maxBodyBytes int64 = 1 << 20 // 1 MiB

// codeInvalidBody istek gövdesi ya da parametresi çözümlenemediğinde dönen
// hata kodudur.
const codeInvalidBody = "b2b_invalid_body"

// Yol parametrelerinin adları.
const (
	paramID         = "id"
	paramCustomerID = "customer_id"
)

// Yetki sözlüğü: b2b'nin yönetim uçlarının istediği yetkiler.
//
// Adlar TÜM modüllerde aynı kalıptadır ("<modül>:read" / "<modül>:write").
// Her modülün kendi sözcüğünü uydurması, yetki dağıtan kişinin modül başına
// ayrı bir sözlük ezberlemesi demek olurdu; ezberlenmeyen sözlükte yapılan hata
// da her zaman aynı yöne düşer — fazla yetki verilir.
const (
	// ScopeRead b2b yönetim yüzeyindeki OKUMA uçlarının istediği yetkidir.
	//
	// Şirketleri ve çalışan kayıtlarını okumaya yeter; hiçbir yazma ucunu
	// açmaz. Tam yetkili kimliklere ayrıca verilmesi gerekmez: corehttp.ScopeAdmin
	// taşıyan bir çağıran bunu da karşılar.
	ScopeRead = "b2b:read"

	// ScopeWrite b2b yönetim yüzeyindeki YAZMA uçlarının istediği yetkidir.
	//
	// Şirket açma/kapatma ve çalışanların HARCAMA YETKİSİNİ değiştirme uçlarını
	// açar. İkincisi bu modülde okuma yetkisinden ayrılmasının asıl sebebidir:
	// bir çalışanın limitini yükseltmek, şirketin parasını harcama iznini
	// genişletmektir.
	ScopeWrite = "b2b:write"
)

// B2B handler'ların servisten ihtiyaç duyduğu yüzeydir.
//
// Dar tutulması testleri sadeleştirir: HTTP davranışı, gerçek bir veritabanı
// olmadan birkaç satırlık bir sahte ile doğrulanabilir.
type B2B interface {
	// CreateCompany yeni bir şirket oluşturur.
	CreateCompany(ctx context.Context, in service.CompanyInput) (models.Company, error)
	// GetCompany şirketi kimliğiyle döner.
	GetCompany(ctx context.Context, id string) (models.Company, error)
	// ListCompanies şirketleri süzer ve sayfalar.
	ListCompanies(ctx context.Context, in service.ListCompaniesInput) (service.Page[models.Company], error)
	// UpdateCompany şirketin verilen alanlarını günceller.
	UpdateCompany(ctx context.Context, id string, in service.UpdateCompanyInput) (models.Company, error)
	// DeleteCompany şirketi ve çalışanlarını yumuşak siler.
	DeleteCompany(ctx context.Context, id string) error

	// CreateEmployee şirkete yeni bir çalışan ekler.
	CreateEmployee(ctx context.Context, in service.EmployeeInput) (models.CompanyEmployee, error)
	// GetEmployee çalışanı kimliğiyle döner.
	GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error)
	// ListEmployees çalışanları süzer ve sayfalar.
	ListEmployees(ctx context.Context, in service.ListEmployeesInput) (service.Page[models.CompanyEmployee], error)
	// UpdateEmployee çalışanın verilen alanlarını günceller.
	UpdateEmployee(ctx context.Context, id string, in service.UpdateEmployeeInput) (models.CompanyEmployee, error)
	// DeleteEmployee çalışanı yumuşak siler ve müşteri bağını kaldırır.
	DeleteEmployee(ctx context.Context, id string) error

	// MembershipOfCustomer müşterinin KENDİ üyeliğini döner.
	MembershipOfCustomer(ctx context.Context, customerID string) (service.Membership, error)
}

// IdentityLookup hands back the customer identity the installation bound, a NIL
// identity when it bound none, or an error when the binding itself is broken.
//
// It is a lookup rather than the corehttp.Identity itself because "this
// installation bound no verifier" is an answer these two routes act on, and
// that contract cannot carry it: CustomerID returns an identifier or an error,
// and an absent binding is neither.
//
// What these routes DO with that answer changed under this type. ADR 0057 served
// the path's claim rather than turning every b2b storefront read in an unprepared
// installation into a 401; ADR 0125 makes it exactly that 401 and gives such an
// installation one setting to keep the old answer. The distinction this lookup
// carries is what let either record be written.
type IdentityLookup func(ctx context.Context) (corehttp.Identity, error)

// Handler b2b modülünün HTTP handler kümesidir.
type Handler struct {
	svc B2B
	// identity finds the verifier that proves which customer a storefront
	// request belongs to. Only the DISAGREEMENT it can then see is refused;
	// see [Handler.storeCustomerID].
	identity IdentityLookup
	// trustUnverified says whether the claim may be served when NO verifier is
	// bound. It is false by default and by zero value (ADR 0125), and it is the
	// SAME installation-wide choice the cart module takes — one field in the
	// composition root feeds both, because two answers to one question is the
	// divergence ADR 0057 built a shared comparison to prevent.
	trustUnverified bool
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
//
// identity may be nil, and a nil one means what a lookup finding nothing means:
// this handler holds no verifier, does not pretend to, and answers the path
// claim as this module answered it before ADR 0057. The module hands in a
// wrapper that resolves the embedder's implementation from the container ON
// FIRST USE, so a nil arriving here means the caller built the handler by hand:
// a test, or an embedder driving the package directly.
func New(svc B2B, identity IdentityLookup, trustUnverified bool) *Handler {
	return &Handler{svc: svc, identity: identity, trustUnverified: trustUnverified}
}

// Routes b2b'nin admin ve store route'larını router'a bağlar.
//
// Route'lar chi'nin Route/Mount yardımcılarıyla DEĞİL, tam yollarla kaydedilir:
// /admin/v1 önekini birden çok modül paylaşır ve aynı öneki iki kez Mount etmek
// chi'de panik üretirdi. Tam yol kaydı aynı ağaca yan yana yazar.
//
// # KORUMA
//
// Yönetim uçlarının iki katmanı vardır ve ikisi de gereklidir:
//
//  1. KİMLİK — corehttp.RequireAdmin. Bu modülde DEĞİL, router'ı kuran tarafta
//     takılır (bkz. corehttp.APIGuards).
//  2. YETKİ — BURADA, uç uç corehttp.RequireScope ile: okuma uçları
//     [ScopeRead], yazma uçları [ScopeWrite] ister.
//
// İkinci katman olmasaydı kimlik doğrulama yetkilendirmenin yerine geçerdi:
// yetkileri bilinçli olarak boşaltılmış bir yönetim kullanıcısı da geçerli bir
// kimliktir ve PUT /admin/v1/b2b/employees/{id} ile kendi harcama limitini
// yükseltebilirdi.
//
// Store uçlarına yetki EKLENMEZ: mağaza yüzeyinin kimliği publishable
// anahtardır ve o anahtar tanımı gereği yetki TAŞIMAZ. Vitrin uçlarının hangi
// anlamda korunduğu (ve hangi anlamda korunmadığı) için bkz. paket belgesi.
func (h *Handler) Routes(r chi.Router) {
	okuma := r.With(corehttp.RequireScope(ScopeRead))
	yazma := r.With(corehttp.RequireScope(ScopeWrite))

	// --- yönetim: şirketler ---
	yazma.Post("/admin/v1/b2b/companies", h.adminCreateCompany)
	okuma.Get("/admin/v1/b2b/companies", h.adminListCompanies)
	okuma.Get("/admin/v1/b2b/companies/{id}", h.adminGetCompany)
	yazma.Put("/admin/v1/b2b/companies/{id}", h.adminUpdateCompany)
	yazma.Delete("/admin/v1/b2b/companies/{id}", h.adminDeleteCompany)

	// --- yönetim: çalışanlar ---
	yazma.Post("/admin/v1/b2b/employees", h.adminCreateEmployee)
	okuma.Get("/admin/v1/b2b/employees", h.adminListEmployees)
	okuma.Get("/admin/v1/b2b/employees/{id}", h.adminGetEmployee)
	yazma.Put("/admin/v1/b2b/employees/{id}", h.adminUpdateEmployee)
	yazma.Delete("/admin/v1/b2b/employees/{id}", h.adminDeleteEmployee)

	// --- vitrin ---
	//
	// Yol MÜŞTERİYLE başlar, şirketle değil: kaynağın anahtarı müşterinin
	// kendi kimliğidir ve şirket ondan TÜRETİLİR. Bir "/store/v1/b2b/companies/{id}"
	// ucu bu modülde bilinçli olarak yoktur.
	r.Get("/store/v1/b2b/customers/{customer_id}/company", h.storeGetCompany)
	r.Get("/store/v1/b2b/customers/{customer_id}/employee", h.storeGetEmployee)
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

// decodeBody istek gövdesini hedefe çözer.
//
// Bilinmeyen alanlar REDDEDİLİR: sessizce yok sayılan bir alan, istemcinin
// gönderdiğini sandığı bir değerin hiç yazılmaması demektir. Bu modülde o değer
// bir harcama limiti olabilir. Gövde boyutu da sınırlıdır; aşılırsa çözümleme
// hatası olarak döner.
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

// storeCustomerID returns the customer a storefront request may act on, or an
// error that ends the request.
//
// The path names a customer; that is the CLAIM. The bound identity says which
// customer the request PROVES. corehttp.ProvenCustomer compares them and every
// disagreement is a refusal — a mismatch is a 403, an implementation that
// proves nothing is a 500, and the embedder's own error passes through with the
// status its kind picks. All of them are written out on that function.
//
// # Why an installation with no verifier is still served
//
// Because nothing here can contradict the claim, and refusing an unchecked
// claim would withdraw a working surface from an embedder who did nothing
// wrong. Until ADR 0125 the claim was handed back as it arrived, which is what
// this module did before ADR 0057; that record named the residue instead of
// charging an upgrade for it. It is REFUSED now, and the old answer is one
// setting away (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM) — so an operator
// who wants it still has it, and nobody gets it without deciding.
//
// # Why the comparison is not written here
//
// It is corehttp.ProvenCustomer's, shared with the address book (ADR 0057).
// This package doc used to say the work was "wait for a session to exist"; when
// ADR 0043 published the contract it became "bind it", and it warned in the
// same paragraph that doing it in passing would put a second, silently
// diverging copy of an authorization rule in the tree. What stays here is the
// CLAIM: this module spells it "customer_id" in the path and the address book
// spells it "id", which is the one thing a shared comparison cannot know.
func (h *Handler) storeCustomerID(r *http.Request) (string, error) {
	claimed := pathParam(r, paramCustomerID)

	var identity corehttp.Identity
	if h.identity != nil {
		var err error
		if identity, err = h.identity(r.Context()); err != nil {
			return "", err
		}
	}
	if identity == nil && h.trustUnverified {
		return claimed, nil
	}

	return corehttp.ProvenCustomer(identity, r, claimed)
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
// nil ile false arasındaki fark burada anlamlıdır: "is_company_admin=false"
// yönetici olmayanları süzer, parametrenin hiç verilmemesi ise süzmez.
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
