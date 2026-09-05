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
	"github.com/bdrtr/gobit/internal/modules/promotion/api"
	"github.com/bdrtr/gobit/internal/modules/promotion/models"
	"github.com/bdrtr/gobit/internal/modules/promotion/service"
)

// testNow testlerin sabit saatidir.
var testNow = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

// adminKimlik testlerin varsayılan çağıranıdır: tam yetkili yönetici.
//
// Yönetim uçları corehttp.RequireScope ile korunuyor ve o middleware
// context'te kimlik YOKSA 401 döner. Bu testler router'ı doğrudan kuruyor,
// yani zincirde kimliği yerleştiren corehttp.RequireAdmin yok; kimliği bu
// yüzden testin kendisi koyar. Eklenen tek şey KİMLİKTİR — testlerin
// doğruladığı davranış (durum kodları, zarflar, sızıntı sınamaları) değişmedi.
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

// promosyonOlustur bir promosyon yaratır ve kimliğini döner.
func promosyonOlustur(t *testing.T, r chi.Router, govde string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/promotions", govde)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "yanıt bir kimlik taşımalı")
	return id
}

func TestAdminKampanyaYasamDongusu(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/campaigns", `{
	  "name": "Yaz İndirimi",
	  "campaign_identifier": "YAZ-2026",
	  "budget_type": "spend",
	  "budget_limit": 100000,
	  "budget_currency_code": "TRY"
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	created := decodeItem(t, rec)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)
	assert.Equal(t, "TRY", created["budget_currency_code"])
	assert.InDelta(t, 0, created["budget_used"], 0)

	rec = do(t, r, http.MethodGet, "/admin/v1/campaigns/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPut, "/admin/v1/campaigns/"+id, `{
	  "name": "Yaz İndirimi 2",
	  "campaign_identifier": "YAZ-2026",
	  "budget_type": "none"
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "Yaz İndirimi 2", decodeItem(t, rec)["name"])

	_, count, offset, limit := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/campaigns", ""))
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, int64(service.DefaultLimit), limit, "zarf UYGULANAN limiti bildirir")

	rec = do(t, r, http.MethodDelete, "/admin/v1/campaigns/"+id, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/campaigns/"+id, "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAdminPromosyonYasamDongusu(t *testing.T) {
	r, _ := newTestRouter(t)

	id := promosyonOlustur(t, r, `{"code": "yaz20", "status": "active", "is_automatic": true}`)

	rec := do(t, r, http.MethodGet, "/admin/v1/promotions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "YAZ20", decodeItem(t, rec)["code"])

	rec = do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "allocation": "each", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	method := decodeItem(t, rec)
	assert.Equal(t, "percentage", method["type"])
	assert.Nil(t, method["currency_code"], "yüzde indirimde para birimi null olmalı")

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/rules", `{
	  "rule_type": "context", "attribute": "region_id", "operator": "eq", "values": ["reg_1"]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())
	ruleID, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "kural yanıtı bir kimlik taşımalı")

	rules, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions/"+id+"/rules", ""))
	require.Len(t, rules, 1)
	assert.Equal(t, int64(1), count)

	rec = do(t, r, http.MethodDelete, "/admin/v1/promotion-rules/"+ruleID, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodDelete, "/admin/v1/promotions/"+id+"/application-method", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodDelete, "/admin/v1/promotions/"+id, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestAdminPromosyonListesiSuzulebilir(t *testing.T) {
	r, _ := newTestRouter(t)
	promosyonOlustur(t, r, `{"code": "AKTIF", "status": "active"}`)
	promosyonOlustur(t, r, `{"code": "TASLAK", "status": "draft"}`)

	data, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions?status=active", ""))
	require.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, "AKTIF", data[0]["code"])

	rec := do(t, r, http.MethodGet, "/admin/v1/promotions?status=olmayan", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "tanımsız durum süzgeci reddedilir")
}

func TestHataSiniflandirmasiStatusKodunaCevrilir(t *testing.T) {
	r, _ := newTestRouter(t)
	id := promosyonOlustur(t, r, `{"code": "YAZ20"}`)

	testler := []struct {
		ad     string
		method string
		path   string
		body   string
		status int
	}{
		{
			ad: "bulunamayan promosyon", method: http.MethodGet,
			path: "/admin/v1/promotions/promo_YOKYOKYOKYOKYOKYOKYOKYOKYO", status: http.StatusNotFound,
		},
		{
			ad: "yanlış önekli kimlik", method: http.MethodGet,
			path: "/admin/v1/promotions/camp_1", status: http.StatusUnprocessableEntity,
		},
		{
			ad: "geçersiz gövde", method: http.MethodPost,
			path: "/admin/v1/promotions", body: `{"code": "AB"}`, status: http.StatusUnprocessableEntity,
		},
		{
			ad: "bilinmeyen alan", method: http.MethodPost,
			path: "/admin/v1/promotions", body: `{"code": "YENI", "bilinmeyen": 1}`,
			status: http.StatusUnprocessableEntity,
		},
		{
			ad: "boş gövde", method: http.MethodPost,
			path: "/admin/v1/promotions", body: "", status: http.StatusUnprocessableEntity,
		},
		{
			ad: "tekrarlanan kod", method: http.MethodPost,
			path: "/admin/v1/promotions", body: `{"code": "yaz20"}`, status: http.StatusConflict,
		},
		{
			ad: "sayısal olmayan sayfa parametresi", method: http.MethodGet,
			path: "/admin/v1/promotions?limit=abc", status: http.StatusUnprocessableEntity,
		},
		{
			ad: "olmayan promosyona yöntem", method: http.MethodPut,
			path:   "/admin/v1/promotions/promo_YOKYOKYOKYOKYOKYOKYOKYOKYO/application-method",
			body:   `{"type": "percentage", "target_type": "items", "value": 1000}`,
			status: http.StatusNotFound,
		},
	}

	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			rec := do(t, r, tt.method, tt.path, tt.body)
			assert.Equal(t, tt.status, rec.Code, "gövde: %s", rec.Body.String())
		})
	}

	// Var olan promosyonun kimliği yukarıdaki testlerde kullanılmadıysa da
	// yaşam döngüsü bozulmamalıdır.
	rec := do(t, r, http.MethodGet, "/admin/v1/promotions/"+id, "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAdminHesapUcNoktasi(t *testing.T) {
	r, _ := newTestRouter(t)
	id := promosyonOlustur(t, r, `{"code": "YAZ20", "status": "active", "is_automatic": true}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "allocation": "each", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/compute", `{
	  "currency_code": "TRY",
	  "items": [{"id": "li_1", "amount": 10000, "quantity": 1}],
	  "codes": ["HICYOK"]
	}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	result := decodeItem(t, rec)
	assert.InDelta(t, 2000, result["discount_total"], 0)
	assert.InDelta(t, 2000, result["items_discount_total"], 0)
	assert.InDelta(t, 0, result["shipping_discount_total"], 0)
	assert.Equal(t, []any{"HICYOK"}, result["unmatched_codes"])

	items, ok := result["items"].([]any)
	require.True(t, ok)
	require.Len(t, items, 1)
	ilkKalem, ok := items[0].(map[string]any)
	require.True(t, ok)
	assert.InDelta(t, 2000, ilkKalem["amount"], 0)
}

func TestAdminKullanimVeGeriAlma(t *testing.T) {
	r, repo := newTestRouter(t)
	id := promosyonOlustur(t, r, `{"code": "YAZ20", "status": "active"}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/redeem",
		`{"reference": "order_1", "amount": 2500, "currency_code": "TRY"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	ilkKullanim := decodeItem(t, rec)["id"]

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/redeem",
		`{"reference": "order_1", "amount": 2500, "currency_code": "TRY"}`)
	require.Equal(t, http.StatusOK, rec.Code, "idempotent istek hata vermez")
	assert.Equal(t, ilkKullanim, decodeItem(t, rec)["id"])
	assert.Equal(t, int64(1), repo.promotions[id].UsageCount)

	kayitlar, count, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/promotions/"+id+"/redemptions", ""))
	require.Len(t, kayitlar, 1)
	assert.Equal(t, int64(1), count)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/release", `{"reference": "order_1"}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, true, decodeItem(t, rec)["released"])

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/release", `{"reference": "order_1"}`)
	require.Equal(t, http.StatusOK, rec.Code, "telafi tekrar çalıştırılabilir")
	assert.Equal(t, false, decodeItem(t, rec)["released"])
	assert.Zero(t, repo.promotions[id].UsageCount)
}

func TestStoreKuponDogrulamaSizdirmaz(t *testing.T) {
	r, _ := newTestRouter(t)
	id := promosyonOlustur(t, r, `{
	  "code": "YAZ20", "status": "active", "usage_limit": 5,
	  "metadata": {"ic_not": "gizli"}
	}`)

	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "fixed", "target_type": "items", "allocation": "each",
	  "value": 5000, "currency_code": "TRY"
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = do(t, r, http.MethodPost, "/admin/v1/promotions/"+id+"/rules", `{
	  "rule_type": "context", "attribute": "customer_group_id", "operator": "eq", "values": ["vip"]
	}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = do(t, r, http.MethodGet, "/store/v1/promotions/yaz20", "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())

	coupon := decodeItem(t, rec)
	assert.Equal(t, "YAZ20", coupon["code"])
	assert.Equal(t, "fixed", coupon["type"])
	assert.Equal(t, "items", coupon["target_type"])
	assert.InDelta(t, 5000, coupon["value"], 0)
	assert.Equal(t, "TRY", coupon["currency_code"])

	for _, alan := range []string{
		"status", "usage_limit", "usage_count", "campaign_id", "metadata",
		"rules", "is_automatic", "id",
	} {
		assert.NotContains(t, coupon, alan,
			"%q müşteri gövdesinde BULUNMAMALI; pricing modülünde tam bu sınıf bir bulgu çıkmıştı", alan)
	}

	// Kural koşulunun değeri gövdenin HİÇBİR yerinde geçmemeli.
	assert.NotContains(t, rec.Body.String(), "vip", "kural koşulu müşteriye sızmamalı")
	assert.NotContains(t, rec.Body.String(), "gizli", "üstveri müşteriye sızmamalı")
}

// hataKodu yanıt gövdesindeki hata kodunu ve mesajını çözer.
func hataKodu(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope), "gövde: %s", rec.Body.String())
	return envelope.Error.Code, envelope.Error.Message
}

func TestStoreKuponDurumaGoreAyrimYapmaz(t *testing.T) {
	r, repo := newTestRouter(t)

	// Taslak promosyon; uygulama yöntemi de var, yani tek eksiği DURUMU.
	id := promosyonOlustur(t, r, `{"code": "TASLAK", "status": "draft"}`)
	rec := do(t, r, http.MethodPut, "/admin/v1/promotions/"+id+"/application-method", `{
	  "type": "percentage", "target_type": "items", "value": 2000
	}`)
	require.Equal(t, http.StatusOK, rec.Code)

	yokRec := do(t, r, http.MethodGet, "/store/v1/promotions/HICBOYLEBIRKODYOK", "")
	yokKod, yokMesaj := hataKodu(t, yokRec)
	require.Equal(t, http.StatusNotFound, yokRec.Code)

	// Sızıntı sınaması: her durum AYNI status ve AYNI hata kodunu döner, ve
	// mesaj yalnızca istemcinin ZATEN bildiği kodu tekrar eder — sebebi değil.
	durumlar := []struct {
		ad      string
		hazirla func()
		gerekce string
	}{
		{
			ad:      "taslak",
			hazirla: func() {},
			gerekce: "taslak kupon, var olmayan koddan ayırt edilememeli",
		},
		{
			ad: "pasif",
			hazirla: func() {
				promo := repo.promotions[id]
				promo.Status = models.PromotionInactive
				repo.promotions[id] = promo
			},
			gerekce: "pasif kupon, var olmayan koddan ayırt edilememeli",
		},
		{
			ad: "kullanım hakkı bitmiş",
			hazirla: func() {
				promo := repo.promotions[id]
				promo.Status = models.PromotionActive
				promo.UsageLimit = new(int64)
				repo.promotions[id] = promo
			},
			gerekce: "kullanım sayacı ele verilmemeli",
		},
	}

	for _, tt := range durumlar {
		t.Run(tt.ad, func(t *testing.T) {
			tt.hazirla()

			rec := do(t, r, http.MethodGet, "/store/v1/promotions/TASLAK", "")
			kod, mesaj := hataKodu(t, rec)

			assert.Equal(t, yokRec.Code, rec.Code, tt.gerekce)
			assert.Equal(t, yokKod, kod, tt.gerekce)
			assert.Equal(t,
				strings.Replace(yokMesaj, "HICBOYLEBIRKODYOK", "TASLAK", 1), mesaj,
				"mesaj yalnızca istemcinin zaten bildiği kodu tekrar eder; sebep söylemez")
		})
	}
}

func TestStoreYazmaYuzeyiYoktur(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := do(t, r, method, "/store/v1/promotions/YAZ20", `{}`)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
			"%s: kupon yazmak yönetim işidir", method)
	}
}

// TestDarYetkiYazmaUcunuAcmaz yalnızca okuma yetkisi taşıyan bir kimliğin
// yönetim yazma uçlarında 403 aldığını doğrular.
//
// Kimlik doğrulama tek başına yetmez: yetkileri boşaltılmış ya da yalnızca
// okumaya yetkili bir yönetim kullanıcısı, yetki zorlaması olmadan kendine
// %100 indirimli bir promosyon yazabilir ya da kampanya bütçesini
// sıfırlayabilirdi. 401 değil 403 beklenir — kimlik bilinmektedir, eksik olan
// yetkidir.
func TestDarYetkiYazmaUcunuAcmaz(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, durum := range []struct {
		ad     string
		method string
		path   string
		body   string
	}{
		{"kampanya oluştur", http.MethodPost, "/admin/v1/campaigns", `{"name":"X","campaign_identifier":"X","budget_type":"none"}`},
		{"kampanya güncelle", http.MethodPut, "/admin/v1/campaigns/promocamp_1", `{"name":"X","campaign_identifier":"X","budget_type":"none"}`},
		{"kampanya sil", http.MethodDelete, "/admin/v1/campaigns/promocamp_1", ``},
		{"promosyon oluştur", http.MethodPost, "/admin/v1/promotions", `{"code":"BEDAVA"}`},
		{"promosyon güncelle", http.MethodPut, "/admin/v1/promotions/promo_1", `{"code":"BEDAVA"}`},
		{"promosyon sil", http.MethodDelete, "/admin/v1/promotions/promo_1", ``},
		{"uygulama yöntemi yaz", http.MethodPut, "/admin/v1/promotions/promo_1/application-method",
			`{"type":"percentage","target_type":"items","value":10000}`},
		{"uygulama yöntemi sil", http.MethodDelete, "/admin/v1/promotions/promo_1/application-method", ``},
		{"kural ekle", http.MethodPost, "/admin/v1/promotions/promo_1/rules", `{"attribute":"x","operator":"eq","values":["y"]}`},
		{"kural sil", http.MethodDelete, "/admin/v1/promotion-rules/promorule_1", ``},
		{"kullan", http.MethodPost, "/admin/v1/promotions/promo_1/redeem", `{}`},
		{"geri al", http.MethodPost, "/admin/v1/promotions/promo_1/release", `{}`},
		// Hesap ucu hiçbir şey YAZMAZ ama sözlük yöntem üzerinden tanımlıdır:
		// POST → yazma. İstisnası olsaydı sözlük uç uç tartışılan bir şey olurdu.
		{"indirim hesapla", http.MethodPost, "/admin/v1/promotions/compute", `{"items":[]}`},
	} {
		rec := doAs(t, r, okumaKimligi, durum.method, durum.path, durum.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, "durum: %s", durum.ad)
		kod, _ := hataKodu(t, rec)
		assert.Equal(t, corehttp.CodeForbidden, kod, "durum: %s", durum.ad)
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
	id := promosyonOlustur(t, r, `{"code": "YAZ20", "status": "active"}`)

	for _, path := range []string{
		"/admin/v1/campaigns",
		"/admin/v1/promotions",
		"/admin/v1/promotions/" + id,
		"/admin/v1/promotions/" + id + "/rules",
		"/admin/v1/promotions/" + id + "/redemptions",
	} {
		rec := doAs(t, r, okumaKimligi, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "yol: %s — gövde: %s", path, rec.Body.String())
	}
}

// TestStoreUcuYetkiIstemez mağaza ucunun yetkisiz bir kimlikle de çalıştığını
// doğrular.
//
// Vitrinin kimliği publishable anahtardır ve o anahtar tanımı gereği yetki
// TAŞIMAZ. Yönetim uçlarına yetki eklerken store ucuna da eklemek, ilk
// dağıtımda bütün vitrini kapatmanın en sessiz yoludur; bu test o hatayı
// derleme değil TEST zamanında yakalar. Beklenen 404'tür: kod yoktur — ama
// 403 ya da 401 DEĞİL.
func TestStoreUcuYetkiIstemez(t *testing.T) {
	r, _ := newTestRouter(t)

	yetkisiz := corehttp.Principal{ID: "pk_1", Kind: "api_key"}
	rec := doAs(t, r, yetkisiz, http.MethodGet, "/store/v1/promotions/HICBOYLEBIRKODYOK", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "gövde: %s", rec.Body.String())
}
