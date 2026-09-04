package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/b2b/api"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
	"github.com/bdrtr/gobit/internal/modules/b2b/service"
)

// sabitSaat testlerin belirlenimci zaman kaynağıdır.
var sabitSaat = time.Date(2026, time.March, 17, 12, 0, 0, 0, time.UTC)

// yeniRouter verilen sahte servisle route'ları bağlanmış bir router üretir.
func yeniRouter(svc api.B2B) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// adminKimlik testlerin varsayılan çağıranıdır: tam yetkili yönetim kimliği.
var adminKimlik = corehttp.Principal{
	ID:     "user_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// istek bir HTTP isteğini TAM YETKİLİ bir kimlikle çalıştırıp yanıtı döner.
//
// Kimliğin context'e konması, yönetim uçları corehttp.RequireScope ile
// korunduğu için gereklidir: o middleware kimliği context'ten okur ve kimliği
// oraya koyan corehttp.RequireAdmin bu testte YOKTUR (router doğrudan
// kurulur). Kimlik eklenmeseydi bu dosyadaki her yönetim testi, sınadığı
// davranışa hiç ulaşamadan 401 alırdı.
func istek(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return istekGonder(t, r, &adminKimlik, method, path, body)
}

// istekGonder isteği verilen kimlikle çalıştırır; kimlik nil ise istek
// KİMLİKSİZ gider.
func istekGonder(
	t *testing.T,
	r chi.Router,
	kimlik *corehttp.Principal,
	method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	ctx := context.Background()
	if kimlik != nil {
		ctx = corehttp.WithPrincipal(ctx, *kimlik)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// govde yanıt gövdesini haritaya çözer.
func govde(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "gövde: %s", rec.Body.String())
	return out
}

// veri tekil yanıt zarfının içindeki nesneyi döner.
func veri(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok, "yanıt zarfında nesne olmalı: %s", rec.Body.String())
	return data
}

// ornekSirket testlerin varsayılan şirket kaydıdır.
func ornekSirket() models.Company {
	return models.Company{
		ID:                       "comp_01",
		Name:                     "Acme Sanayi A.Ş.",
		Email:                    "muhasebe@acme.example",
		CurrencyCode:             "TRY",
		CountryCode:              "TR",
		SpendingLimitResetPeriod: models.ResetMonthly,
		CreatedAt:                sabitSaat,
		UpdatedAt:                sabitSaat,
	}
}

// ornekCalisan testlerin varsayılan çalışan kaydıdır.
func ornekCalisan() models.CompanyEmployee {
	limit := int64(150000)
	return models.CompanyEmployee{
		ID:            "compemp_01",
		CompanyID:     "comp_01",
		CustomerID:    "cust_01",
		SpendingLimit: &limit,
		CreatedAt:     sabitSaat,
		UpdatedAt:     sabitSaat,
	}
}

// yolParametreRe chi route desenindeki {param} parçalarını yakalar.
var yolParametreRe = regexp.MustCompile(`\{([^}]*)\}`)

// rotalar router ağacındaki tüm uçları (metot, desen) döner.
func rotalar(t *testing.T, r chi.Router) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	err := chi.Walk(r, func(metot, desen string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		out[desen] = append(out[desen], metot)
		return nil
	})
	require.NoError(t, err, "router ağacı gezilemedi")
	return out
}

// TestVitrinYuzeyindeSirketKimligiYok modülün en önemli vitrin değişmezini
// YAPISAL olarak sabitler.
//
// İddia şudur: vitrin yüzeyinde şirketin (ya da çalışan kaydının) kimliğiyle
// çağrılabilen bir uç YOKTUR; tek anahtar müşterinin kendi kimliğidir.
// "Başkasının şirketini oku" isteği böylece reddedilen bir istek değil, İFADE
// EDİLEMEYEN bir istek olur.
//
// Test bir uç LİSTESİ tutmaz, ağacı GEZER: yarın eklenecek bir vitrin ucu da
// otomatik olarak kapsama girer. Elle yazılmış bir liste, listeye eklenmesi
// unutulan ilk uçta kör kalırdı — ve unutulacak uç, tam da yeni yazılmış
// olandır.
func TestVitrinYuzeyindeSirketKimligiYok(t *testing.T) {
	agac := rotalar(t, yeniRouter(&stubB2B{}))

	var vitrin []string
	for desen := range agac {
		if strings.HasPrefix(desen, "/store/v1") {
			vitrin = append(vitrin, desen)
		}
	}
	require.NotEmpty(t, vitrin, "vitrin uçları bulunamadı; gezme mantığı bozulmuş olabilir")

	for _, desen := range vitrin {
		for _, eslesme := range yolParametreRe.FindAllStringSubmatch(desen, -1) {
			assert.Equal(t, "customer_id", eslesme[1],
				"vitrin ucu %q, customer id DIŞINDA bir parametre alıyor; "+
					"şirket ya da çalışan kimliğiyle adreslenen bir uç, başkasının "+
					"şirketini okumayı mümkün kılardı", desen)
		}
	}
}

// TestYonetimUclariYetkiIster her /admin/v1 ucunun kapsam istediğini ağacı
// gezerek doğrular.
//
// Bu, e2e/yetki_test.go'nun modül düzeyindeki karşılığıdır: modül henüz
// bileşim köküne bağlanmadığı için o test bu uçları görmez ve yetki zorlamasını
// unutmak sessiz kalırdı.
func TestYonetimUclariYetkiIster(t *testing.T) {
	r := yeniRouter(&stubB2B{})

	// Kimlik GEÇERLİ ama yetkisi YOK: beklenen 401 değil 403'tür. Ayrım
	// corehttp.RequireScope'un sözleşmesidir — 401 "kim olduğunu söyle" demek
	// olurdu ve istemci aynı kimlikle sonsuza dek tekrar denerdi.
	yetkisiz := corehttp.Principal{ID: "user_yetkisiz", Kind: "user", Scopes: []string{}}

	sayac := 0
	for desen, metotlar := range rotalar(t, r) {
		if !strings.HasPrefix(desen, "/admin/v1") {
			continue
		}
		yol := yolParametreRe.ReplaceAllString(desen, "sahte_kimlik")
		for _, metot := range metotlar {
			sayac++
			t.Run(metot+" "+desen, func(t *testing.T) {
				rec := istekGonder(t, r, &yetkisiz, metot, yol, "{}")
				assert.Equal(t, http.StatusForbidden, rec.Code,
					"yetkisiz kimlik bu uçta 403 almalı; gövde: %s", rec.Body.String())
			})
		}
	}
	require.Equal(t, 10, sayac, "yönetim yüzeyi beklenenden farklı; gezme mantığı bozulmuş olabilir")
}

// TestOkumaYetkisiYazmaUcunuAcmaz iki yetkinin gerçekten ayrıldığını doğrular.
//
// Ayrılmasaydı raporlama için verilmiş bir okuma yetkisi, çalışanların harcama
// limitini değiştirmeye de yeterdi.
func TestOkumaYetkisiYazmaUcunuAcmaz(t *testing.T) {
	stub := &stubB2B{
		listCompaniesFn: func(context.Context, service.ListCompaniesInput) (service.Page[models.Company], error) {
			return service.Page[models.Company]{Items: []models.Company{ornekSirket()}, Count: 1}, nil
		},
	}
	r := yeniRouter(stub)
	okuyucu := corehttp.Principal{ID: "user_okuma", Kind: "user", Scopes: []string{api.ScopeRead}}

	rec := istekGonder(t, r, &okuyucu, http.MethodGet, "/admin/v1/b2b/companies", "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = istekGonder(t, r, &okuyucu, http.MethodPut, "/admin/v1/b2b/employees/compemp_01",
		`{"spending_limit":999999}`)
	assert.Equal(t, http.StatusForbidden, rec.Code,
		"okuma yetkisi harcama limitini değiştirmeye yetmemeli")
}

// TestVitrinUclariYetkiIstemez mağaza yüzeyinin publishable anahtarla
// çalıştığını doğrular: o anahtar tanımı gereği yetki taşımaz.
func TestVitrinUclariYetkiIstemez(t *testing.T) {
	stub := &stubB2B{
		membershipFn: func(context.Context, string) (service.Membership, error) {
			return service.Membership{Company: ornekSirket(), Employee: ornekCalisan()}, nil
		},
	}
	r := yeniRouter(stub)

	rec := istekGonder(t, r, nil, http.MethodGet,
		"/store/v1/b2b/customers/cust_01/company", "")
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestVitrinMusteriyiYoldanOkur handler'ın servise DOĞRU müşteriyi ilettiğini
// doğrular.
//
// Kimliğin tek bir yerden (storeCustomerID) okunması, müşteri oturumu
// geldiğinde değişikliğin tek dosyada yapılabilmesi içindir.
func TestVitrinMusteriyiYoldanOkur(t *testing.T) {
	stub := &stubB2B{
		membershipFn: func(_ context.Context, customerID string) (service.Membership, error) {
			if customerID != "cust_42" {
				return service.Membership{}, errors.NotFound("b2b_employee_not_found", "yok")
			}
			pencere := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
			return service.Membership{
				Company:             ornekSirket(),
				Employee:            ornekCalisan(),
				SpendingWindowStart: &pencere,
			}, nil
		},
	}
	r := yeniRouter(stub)

	rec := istekGonder(t, r, nil, http.MethodGet,
		"/store/v1/b2b/customers/cust_42/employee", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "cust_42", stub.sonCustomerID)

	data := veri(t, rec)
	assert.Equal(t, "compemp_01", data["id"])
	assert.InEpsilon(t, float64(150000), data["spending_limit"], 0)
	assert.Equal(t, string(models.ResetMonthly), data["spending_limit_reset_period"])
	assert.Equal(t, "2026-03-01T00:00:00Z", data["spending_window_start"])

	// KALAN hak alanı BİLİNÇLİ olarak yoktur; uydurulmuş bir sayı istemciye
	// yanlış bilgi verirdi (bkz. service.Membership).
	assert.NotContains(t, data, "spending_remaining")
}

// TestVitrinUyeOlmayanMusteriyeBulunamadiDoner şirketi olmayan bir müşterinin
// boş bir kayıt değil 404 aldığını doğrular.
func TestVitrinUyeOlmayanMusteriyeBulunamadiDoner(t *testing.T) {
	stub := &stubB2B{
		membershipFn: func(context.Context, string) (service.Membership, error) {
			return service.Membership{}, errors.NotFound("b2b_employee_not_found",
				"müşteri hiçbir şirketin çalışanı değil")
		},
	}
	r := yeniRouter(stub)

	for _, yol := range []string{
		"/store/v1/b2b/customers/cust_01/company",
		"/store/v1/b2b/customers/cust_01/employee",
	} {
		rec := istekGonder(t, r, nil, http.MethodGet, yol, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s: %s", yol, rec.Body.String())
	}
}

// TestHataSinifiStatusKodunaCevrilir handler'ın status kodu SEÇMEDİĞİNİ
// doğrular: sınıflandırma servisten gelir ve çekirdek onu koda çevirir.
func TestHataSinifiStatusKodunaCevrilir(t *testing.T) {
	durumlar := map[string]struct {
		err    error
		bekler int
	}{
		"bulunamadı": {errors.NotFound("b2b_company_not_found", "yok"), http.StatusNotFound},
		"geçersiz":   {errors.Invalid("b2b_invalid_input", "kötü"), http.StatusUnprocessableEntity},
		"çakışma": {
			errors.Conflict("b2b_link_failed", "müşteri zaten başka bir şirkette"),
			http.StatusConflict,
		},
	}

	for ad, durum := range durumlar {
		t.Run(ad, func(t *testing.T) {
			stub := &stubB2B{
				getCompanyFn: func(context.Context, string) (models.Company, error) {
					return models.Company{}, durum.err
				},
			}
			rec := istek(t, yeniRouter(stub), http.MethodGet, "/admin/v1/b2b/companies/comp_01", "")
			assert.Equal(t, durum.bekler, rec.Code, rec.Body.String())
		})
	}
}

// TestSirketOlusturmaGovdesiServiseGecer alanların doğru eşlendiğini doğrular.
func TestSirketOlusturmaGovdesiServiseGecer(t *testing.T) {
	stub := &stubB2B{
		createCompanyFn: func(_ context.Context, in service.CompanyInput) (models.Company, error) {
			company := ornekSirket()
			company.Name = in.Name
			return company, nil
		},
	}
	r := yeniRouter(stub)

	rec := istek(t, r, http.MethodPost, "/admin/v1/b2b/companies", `{
        "name": "Acme Sanayi A.Ş.",
        "email": "muhasebe@acme.example",
        "phone": "",
        "address": "",
        "city": "",
        "postal_code": "",
        "country_code": "TR",
        "currency_code": "TRY",
        "spending_limit_reset_period": "monthly"
    }`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	assert.Equal(t, "TRY", stub.sonCompanyInput.CurrencyCode)
	assert.Equal(t, "monthly", stub.sonCompanyInput.SpendingLimitResetPeriod)
	assert.Equal(t, "comp_01", veri(t, rec)["id"])
}

// TestBilinmeyenAlanReddedilir sessizce yok sayılan bir alanın istemciye
// "yazıldı" izlenimi vermesini engeller.
//
// Bu modülde o alan bir harcama limiti olabilir: yanlış yazılmış bir anahtar
// sessizce düşseydi, sınırsız kalan bir çalışan sınırlandırıldı sanılırdı.
func TestBilinmeyenAlanReddedilir(t *testing.T) {
	r := yeniRouter(&stubB2B{})

	rec := istek(t, r, http.MethodPut, "/admin/v1/b2b/employees/compemp_01",
		`{"spendingLimit": 100}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestLimitKaldirmaBayragiServiseGecer JSON'un null/eksik ayrımı yapamaması
// yüzünden eklenen bayrağın gerçekten taşındığını doğrular.
func TestLimitKaldirmaBayragiServiseGecer(t *testing.T) {
	stub := &stubB2B{
		updateEmployeeFn: func(context.Context, string, service.UpdateEmployeeInput) (models.CompanyEmployee, error) {
			calisan := ornekCalisan()
			calisan.SpendingLimit = nil
			return calisan, nil
		},
	}
	r := yeniRouter(stub)

	rec := istek(t, r, http.MethodPut, "/admin/v1/b2b/employees/compemp_01",
		`{"clear_spending_limit": true}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	assert.True(t, stub.sonEmployeeUpdate.ClearSpendingLimit)
	assert.Nil(t, stub.sonEmployeeUpdate.SpendingLimit)
	assert.Nil(t, veri(t, rec)["spending_limit"], "sınırsız çalışanın limiti null dönmeli")
}

// TestCalisanSuzgecleriSorguDizesindenOkunur handler'ın gerçekten okuduğu
// parametreleri sabitler; okunmayan bir süzgeç belgede duran ama işlemeyen bir
// vaat olurdu.
func TestCalisanSuzgecleriSorguDizesindenOkunur(t *testing.T) {
	stub := &stubB2B{
		listEmployeesFn: func(context.Context, service.ListEmployeesInput) (service.Page[models.CompanyEmployee], error) {
			return service.Page[models.CompanyEmployee]{
				Items: []models.CompanyEmployee{ornekCalisan()}, Count: 1, Limit: 10, Offset: 0,
			}, nil
		},
	}
	r := yeniRouter(stub)

	rec := istek(t, r, http.MethodGet,
		"/admin/v1/b2b/employees?company_id=comp_01&is_company_admin=true&limit=10&offset=0", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.NotNil(t, stub.sonEmployeeList.CompanyID)
	assert.Equal(t, "comp_01", *stub.sonEmployeeList.CompanyID)
	require.NotNil(t, stub.sonEmployeeList.IsCompanyAdmin)
	assert.True(t, *stub.sonEmployeeList.IsCompanyAdmin)
	assert.Equal(t, int64(10), stub.sonEmployeeList.Limit)

	zarf := govde(t, rec)
	assert.InEpsilon(t, float64(1), zarf["count"], 0)
	assert.Contains(t, zarf, "limit")
	assert.Contains(t, zarf, "offset")
}

// TestSayisalOlmayanSayfalamaReddedilir sessizce ilk sayfaya düşmeyi engeller.
func TestSayisalOlmayanSayfalamaReddedilir(t *testing.T) {
	r := yeniRouter(&stubB2B{})

	rec := istek(t, r, http.MethodGet, "/admin/v1/b2b/companies?limit=abc", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = istek(t, r, http.MethodGet, "/admin/v1/b2b/employees?is_company_admin=belki", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestSilmeGovdesizYanitDoner 204'ün gerçekten gövdesiz olduğunu doğrular.
func TestSilmeGovdesizYanitDoner(t *testing.T) {
	stub := &stubB2B{
		deleteCompanyFn:  func(context.Context, string) error { return nil },
		deleteEmployeeFn: func(context.Context, string) error { return nil },
	}
	r := yeniRouter(stub)

	rec := istek(t, r, http.MethodDelete, "/admin/v1/b2b/companies/comp_01", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())

	rec = istek(t, r, http.MethodDelete, "/admin/v1/b2b/employees/compemp_01", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}
