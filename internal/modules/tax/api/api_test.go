package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/tax/api"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// testNow testlerin sabit saatidir.
var testNow = time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)

// adminKimlik testlerin varsayılan çağıranıdır: tam yetkili yönetici.
//
// Yönetim uçları corehttp.RequireScope ile korunuyor ve o middleware
// context'te kimlik YOKSA 401 döner. Bu testler router'ı doğrudan kuruyor,
// yani zincirde kimliği yerleştiren corehttp.RequireAdmin yok; kimliği bu
// yüzden testin kendisi koyar. Eklenen tek şey KİMLİKTİR — testlerin
// doğruladığı davranış (durum kodları, zarflar, hata kodları) değişmedi.
var adminKimlik = corehttp.Principal{
	ID:     "usr_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// okumaKimligi yalnızca [api.ScopeRead] taşıyan dar yetkili çağırandır.
var okumaKimligi = corehttp.Principal{
	ID:     "usr_dar",
	Kind:   "user",
	Scopes: []string{api.ScopeRead},
}

// newTestRouter gerçek servis ve bellek içi depoyla bir router kurar.
func newTestRouter(t *testing.T) (chi.Router, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := service.New(repo, service.Options{Now: func() time.Time { return testNow }})

	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r, repo
}

// do bir isteği tam yetkili kimlikle çalıştırır ve yanıtı döner.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	return doAs(t, r, adminKimlik, method, path, body)
}

// doAs bir isteği verilen kimlikle çalıştırır ve yanıtı döner.
func doAs(t *testing.T, r chi.Router, kimlik corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), kimlik))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// decodeItem tekil zarfın data alanını çözer.
func decodeItem(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "gövde: %s", rec.Body.String())
	return envelope.Data
}

// decodeList liste zarfını çözer.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) (data []map[string]any, count, offset, limit int64) {
	t.Helper()

	var envelope struct {
		Data   []map[string]any `json:"data"`
		Count  int64            `json:"count"`
		Offset int64            `json:"offset"`
		Limit  int64            `json:"limit"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "gövde: %s", rec.Body.String())
	return envelope.Data, envelope.Count, envelope.Offset, envelope.Limit
}

// errorCode hata zarfındaki kodu döner.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "gövde: %s", rec.Body.String())
	return envelope.Error.Code
}

// createRegion bir kök vergi bölgesi oluşturur ve kimliğini döner.
func createRegion(t *testing.T, r chi.Router, countryCode string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-regions",
		`{"country_code":"`+countryCode+`"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok)
	return id
}

// createRate bir vergi oranı oluşturur ve kimliğini döner.
func createRate(t *testing.T, r chi.Router, body string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates", body)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok)
	return id
}

// TestBolgeYasamDongusu bölge uçlarının uçtan uca akışını doğrular.
func TestBolgeYasamDongusu(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-regions",
		`{"country_code":"tr","metadata":{"kaynak":"test"}}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	created := decodeItem(t, rec)
	assert.Equal(t, "TR", created["country_code"])
	assert.Nil(t, created["province_code"], "kök bölgede eyalet kodu null olmalı")
	assert.Nil(t, created["parent_id"])
	id, _ := created["id"].(string)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "TR", decodeItem(t, rec)["country_code"])

	rec = do(t, r, http.MethodDelete, "/admin/v1/tax-regions/"+id, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String(), "204 gövdesiz olmalı")

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-regions/"+id, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestBolgeListesiZarfi liste zarfının plan Bölüm 8 biçimini doğrular.
func TestBolgeListesiZarfi(t *testing.T) {
	r, _ := newTestRouter(t)
	createRegion(t, r, "TR")
	createRegion(t, r, "US")

	rec := do(t, r, http.MethodGet, "/admin/v1/tax-regions", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, 2)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, int64(service.DefaultLimit), limit, "uygulanan limit zarfta bildirilmeli")

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-regions?country_code=tr", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ = decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
}

// TestBolgeHataDurumKodlari handler'ın status kodu SEÇMEDİĞİNİ, servis hata
// sınıfının kodu belirlediğini doğrular.
func TestBolgeHataDurumKodlari(t *testing.T) {
	r, _ := newTestRouter(t)
	createRegion(t, r, "TR")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"geçersiz ülke kodu", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"TUR"}`, http.StatusUnprocessableEntity},
		{"ikinci kök çakışma", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"TR"}`, http.StatusConflict},
		{"bilinmeyen alan", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"DE","tax_rate":20}`, http.StatusUnprocessableEntity},
		{"boş gövde", http.MethodPost, "/admin/v1/tax-regions", ``, http.StatusUnprocessableEntity},
		{"iki JSON belgesi", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"DE"}{"country_code":"FR"}`, http.StatusUnprocessableEntity},
		{"olmayan bölge", http.MethodGet, "/admin/v1/tax-regions/taxreg_YOK", ``, http.StatusNotFound},
		{"yanlış türde kimlik", http.MethodGet, "/admin/v1/tax-regions/taxrate_ABC", ``, http.StatusUnprocessableEntity},
		{"sayfalama sayı değil", http.MethodGet, "/admin/v1/tax-regions?limit=abc", ``, http.StatusUnprocessableEntity},
		{"negatif offset", http.MethodGet, "/admin/v1/tax-regions?offset=-1", ``, http.StatusUnprocessableEntity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "gövde: %s", rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "hata zarfı kod taşımalı")
		})
	}
}

// TestOranYasamDongusu oran uçlarının uçtan uca akışını doğrular.
func TestOranYasamDongusu(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")

	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","code":"KDV20","rate_bps":2000,"is_default":true}`)

	rec := do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decodeItem(t, rec)
	assert.Equal(t, "KDV", got["name"])
	assert.Equal(t, "KDV20", got["code"])
	assert.Equal(t, float64(2000), got["rate_bps"], "oran BAZ PUAN olarak dönmeli")
	assert.Equal(t, true, got["is_default"])

	rec = do(t, r, http.MethodPut, "/admin/v1/tax-rates/"+rateID, `{"rate_bps":1800}`)
	require.Equal(t, http.StatusOK, rec.Code)
	got = decodeItem(t, rec)
	assert.Equal(t, float64(1800), got["rate_bps"])
	assert.Equal(t, "KDV", got["name"], "verilmeyen alan DEĞİŞMEMELİ")

	rec = do(t, r, http.MethodPut, "/admin/v1/tax-rates/"+rateID, `{"code":""}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, decodeItem(t, rec)["code"], "boş dize kodu kaldırmalı")

	rec = do(t, r, http.MethodDelete, "/admin/v1/tax-rates/"+rateID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestOranListesiIkiYoldanOkunur hem bölge alt kaynağının hem sorgu
// parametreli ucun aynı listeyi döndürdüğünü doğrular.
func TestOranListesiIkiYoldanOkunur(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","rate_bps":2000,"is_default":true}`)
	createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"İndirimli","rate_bps":100}`)

	altKaynak := do(t, r, http.MethodGet, "/admin/v1/tax-regions/"+regionID+"/tax-rates", "")
	require.Equal(t, http.StatusOK, altKaynak.Code)
	data, count, _, limit := decodeList(t, altKaynak)
	assert.Len(t, data, 2)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, int64(2), limit, "sayfalanmayan listede limit dönen kayıt sayısıdır")
	assert.Equal(t, true, data[0]["is_default"], "varsayılan oran başta olmalı")

	sorgu := do(t, r, http.MethodGet, "/admin/v1/tax-rates?tax_region_id="+regionID, "")
	require.Equal(t, http.StatusOK, sorgu.Code)
	assert.JSONEq(t, altKaynak.Body.String(), sorgu.Body.String(),
		"iki yol aynı gövdeyi döndürmeli")
}

// TestOranListesiBolgesizReddedilir zorunlu sorgu parametresini doğrular.
func TestOranListesiBolgesizReddedilir(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/tax-rates", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.NotEmpty(t, errorCode(t, rec))
}

// TestOranHataDurumKodlari oran uçlarının hata eşlemesini doğrular.
func TestOranHataDurumKodlari(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","rate_bps":2000,"is_default":true}`)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"oran yüzde yüzü aşar", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"` + regionID + `","name":"Fazla","rate_bps":10001}`, http.StatusUnprocessableEntity},
		{"ad boş", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"` + regionID + `","name":"","rate_bps":100}`, http.StatusUnprocessableEntity},
		{"ikinci varsayılan", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"` + regionID + `","name":"İkinci","rate_bps":100,"is_default":true}`, http.StatusConflict},
		{"olmayan bölge", http.MethodPost, "/admin/v1/tax-rates",
			`{"tax_region_id":"taxreg_YOK","name":"KDV","rate_bps":100}`, http.StatusNotFound},
		{"boş yama", http.MethodPut, "/admin/v1/tax-rates/taxrate_X", `{}`, http.StatusUnprocessableEntity},
		{"olmayan oran", http.MethodDelete, "/admin/v1/tax-rates/taxrate_YOK", ``, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "gövde: %s", rec.Body.String())
		})
	}
}

// TestKuralYasamDongusu kural uçlarını doğrular.
func TestKuralYasamDongusu(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"İndirimli","rate_bps":100}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+rateID+"/rules",
		`{"reference":"product","reference_id":"prod_1"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	rule := decodeItem(t, rec)
	assert.Equal(t, "product", rule["reference"])
	assert.Equal(t, "prod_1", rule["reference_id"])
	assert.Equal(t, rateID, rule["tax_rate_id"], "oran kimliği YOLDAN alınmalı")
	ruleID, _ := rule["id"].(string)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID+"/rules", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)

	rec = do(t, r, http.MethodDelete, "/admin/v1/tax-rates/"+rateID+"/rules/"+ruleID, "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/tax-rates/"+rateID+"/rules", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _, _, _ = decodeList(t, rec)
	assert.Empty(t, data)
}

// TestKuralVarsayilanOranaEklenemez kapsam kuralının HTTP'de 409 olarak
// göründüğünü doğrular.
func TestKuralVarsayilanOranaEklenemez(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"KDV","rate_bps":2000,"is_default":true}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+rateID+"/rules",
		`{"reference":"product","reference_id":"prod_1"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
}

// TestKuralGecersizReferans tanımsız referans türünü doğrular.
func TestKuralGecersizReferans(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")
	rateID := createRate(t, r, `{"tax_region_id":"`+regionID+`","name":"İndirimli","rate_bps":100}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/tax-rates/"+rateID+"/rules",
		`{"reference":"variant","reference_id":"var_1"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestStoreUcuYok vergi uçlarının müşteriye AÇILMADIĞINI doğrular.
//
// İddia bir eksikliğin değil bir KARARIN kanıtıdır: vergi vitrine yalnızca
// sepet toplamının içinde görünür (bkz. api paket yorumu). Kazara eklenen bir
// /store/v1 ucu burada yakalanır.
func TestStoreUcuYok(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, path := range []string{
		"/store/v1/tax-regions",
		"/store/v1/tax-rates",
		"/store/v1/taxes",
	} {
		rec := do(t, r, http.MethodGet, path, "")
		assert.Equal(t, http.StatusNotFound, rec.Code, "yol: %s", path)
	}
}

// TestGovdeBoyutuSinirli aşırı büyük gövdenin reddedildiğini doğrular.
func TestGovdeBoyutuSinirli(t *testing.T) {
	r, _ := newTestRouter(t)

	buyuk := `{"country_code":"TR","metadata":{"x":"` + strings.Repeat("a", 128<<10) + `"}}`
	rec := do(t, r, http.MethodPost, "/admin/v1/tax-regions", buyuk)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestDarYetkiYazmaUcunuAcmaz yalnızca okuma yetkisi taşıyan bir kimliğin
// yönetim yazma uçlarında 403 aldığını doğrular.
//
// Kimlik doğrulama tek başına yetmez: yetkileri boşaltılmış ya da yalnızca
// okumaya yetkili bir yönetim kullanıcısı, yetki zorlaması olmadan vergi
// kataloğunu değiştirebilir veya silebilirdi. 401 değil 403 beklenir — kimlik
// bilinmektedir, eksik olan yetkidir.
func TestDarYetkiYazmaUcunuAcmaz(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, durum := range []struct {
		ad     string
		method string
		path   string
		body   string
	}{
		{"bölge oluştur", http.MethodPost, "/admin/v1/tax-regions", `{"country_code":"TR"}`},
		{"bölge sil", http.MethodDelete, "/admin/v1/tax-regions/taxreg_1", ``},
		{"oran oluştur", http.MethodPost, "/admin/v1/tax-rates", `{"tax_region_id":"taxreg_1","name":"KDV","rate_bps":2000}`},
		{"oran güncelle", http.MethodPut, "/admin/v1/tax-rates/taxrate_1", `{"rate_bps":1800}`},
		{"oran sil", http.MethodDelete, "/admin/v1/tax-rates/taxrate_1", ``},
		{"kural ekle", http.MethodPost, "/admin/v1/tax-rates/taxrate_1/rules", `{"reference":"product","reference_id":"prod_1"}`},
		{"kural sil", http.MethodDelete, "/admin/v1/tax-rates/taxrate_1/rules/taxrule_1", ``},
	} {
		rec := doAs(t, r, okumaKimligi, durum.method, durum.path, durum.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "durum: %s", durum.ad)
		assert.Equal(t, corehttp.CodeForbidden, errorCode(t, rec), "durum: %s", durum.ad)
	}
}

// TestDarYetkiOkumaUcundaGecer aynı dar kimliğin okuma uçlarından geçtiğini
// doğrular.
//
// Bu testin çifti [TestDarYetkiYazmaUcunuAcmaz]'dır: yetki haritası her yazma
// ucunu kapatırken okuma uçlarını da kapatsaydı, 403 sonuçları haritanın
// doğruluğunu değil yalnızca aşırı kısıtlayıcılığını kanıtlardı.
func TestDarYetkiOkumaUcundaGecer(t *testing.T) {
	r, _ := newTestRouter(t)
	regionID := createRegion(t, r, "TR")

	for _, path := range []string{
		"/admin/v1/tax-regions",
		"/admin/v1/tax-regions/" + regionID,
		"/admin/v1/tax-regions/" + regionID + "/tax-rates",
		"/admin/v1/tax-rates?tax_region_id=" + regionID,
	} {
		rec := doAs(t, r, okumaKimligi, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "yol: %s — gövde: %s", path, rec.Body.String())
	}
}
