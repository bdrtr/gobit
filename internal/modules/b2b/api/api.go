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
// # UYARI: vitrin uçları müşteriyi YOL PARAMETRESİNDEN tanır
//
// Bu depoda vitrin isteklerinin kimliği publishable API anahtarıdır ve o anahtar
// bir SATIŞ KANALINI temsil eder, bir müşteriyi değil (bkz. corehttp.RequireStore).
// Yani "giriş yapmış müşteri" diye okunabilecek bir oturum kimliği çekirdekte
// HENÜZ YOKTUR. Uçlar bu yüzden customer idni yoldan alır — tıpkı
// /store/v1/customers/{id} gibi — ve kimliğin doğruluğu DOĞRULANMAZ.
//
// Sonuç açıkça yazılmalıdır: başka bir müşterinin kimliğini BİLEN bir çağıran,
// o müşterinin şirketini okuyabilir. Kapatılan şey, şirketin adıyla
// istenebilmesidir; kapatılmayan şey, customer idnin taklit edilmesidir.
// Müşteri oturumu geldiğinde yapılacak iş tek bir yerdedir: [storeCustomerID]
// kimliği yol parametresi yerine oturumdan okumalı ve uyuşmazlıkta
// errors.Forbidden dönmelidir.
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

// Handler b2b modülünün HTTP handler kümesidir.
type Handler struct {
	svc B2B
}

// New verilen servis üzerinde çalışan handler kümesini üretir.
func New(svc B2B) *Handler {
	return &Handler{svc: svc}
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

// storeCustomerID vitrin isteğinin hangi müşteriye ait olduğunu döner.
//
// MÜŞTERİ OTURUMU BAĞLAMA NOKTASI: kimlik ŞİMDİLİK yol parametresinden okunur,
// yani istemcinin kendi bildirdiği değerdir ve doğrulanmaz (gerekçe ve sınırlar
// için bkz. paket belgesi). Oturum geldiğinde bu fonksiyon kimliği belirteçten
// almalı, yol parametresiyle karşılaştırıp uyuşmazlıkta errors.Forbidden
// dönmelidir. Tek bir yerde durması, o değişikliğin tek dosyada yapılabilmesi
// içindir.
func storeCustomerID(r *http.Request) string {
	return pathParam(r, paramCustomerID)
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
