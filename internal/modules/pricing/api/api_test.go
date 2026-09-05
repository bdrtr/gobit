package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// testNow testlerin sabit saatidir.
var testNow = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// newTestRouter gerçek servis ve bellek içi depoyla bir router kurar.
func newTestRouter(t *testing.T) (chi.Router, *memRepo) {
	t.Helper()

	repo := newMemRepo()
	svc := service.New(repo, service.Options{Now: func() time.Time { return testNow }})

	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r, repo
}

// do bir istek çalıştırır ve yanıtı döner.
//
// İstek TAM YETKİLİ bir kimlik taşır. Üretimde kimliği corehttp.RequireAdmin
// context'e koyar; bu testler router'ı doğrudan kurduğu için o middleware
// devrede değildir ve kimlik elle konur. Gerekçesi, yönetim uçlarına
// corehttp.RequireScope eklenmesidir: kimliksiz bir istek artık handler'a hiç
// ulaşmadan 401 alır ve buradaki testler fiyat davranışı yerine yetki
// katmanını sınamış olurdu. Yetkinin KENDİSİ ayrı bir dosyada sınanır
// (yetki_test.go); bu dosyanın iddiaları değişmedi.
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_test",
		Kind:   "user",
		Scopes: []string{corehttp.ScopeAdmin},
	}))

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

// TestCreatePriceSetWithPrices oluşturma akışını ve tekil zarfı kanıtlar.
func TestCreatePriceSetWithPrices(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"try","amount":19900},{"currency_code":"usd","amount":599}]}`)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	data := decodeItem(t, rec)

	id, ok := data["id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(id, "pset_"))

	prices, ok := data["prices"].([]any)
	require.True(t, ok, "fiyatlar yanıtta olmalı")
	assert.Len(t, prices, 2)

	first, ok := prices[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "TRY", first["currency_code"], "para birimi büyük harfe normalleştirilmeli")
	assert.InDelta(t, 19900, first["amount"], 0)
}

// TestGetPriceSetReturnsPrices tekil okumanın fiyatları taşıdığını kanıtlar.
func TestGetPriceSetReturnsPrices(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)

	data := decodeItem(t, rec)
	assert.Equal(t, id, data["id"])
	prices, ok := data["prices"].([]any)
	require.True(t, ok)
	assert.Len(t, prices, 1)
}

// TestStoreGetPriceSet store uç noktasının aynı gövdeyi döndürdüğünü kanıtlar.
func TestStoreGetPriceSet(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet, "/store/v1/price-sets/"+id, "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, id, decodeItem(t, rec)["id"])
}

// TestListPriceSetsEnvelope liste zarfının plan Bölüm 8'deki alanları
// taşıdığını kanıtlar.
func TestListPriceSetsEnvelope(t *testing.T) {
	r, _ := newTestRouter(t)
	for range 3 {
		require.Equal(t, http.StatusCreated,
			do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`).Code)
	}

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets?limit=2&offset=1", "")
	require.Equal(t, http.StatusOK, rec.Code)

	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, 2)
	assert.Equal(t, int64(3), count, "count TOPLAM kayıt sayısı olmalı")
	assert.Equal(t, int64(1), offset)
	assert.Equal(t, int64(2), limit)
	assert.NotContains(t, data[0], "prices", "liste yanıtında fiyat taşınmaz")
}

// TestListPriceSetsEmptyIsArray boş listenin JSON'da null değil [] olduğunu
// kanıtlar.
func TestListPriceSetsEmptyIsArray(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/price-sets", "")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`)
}

// TestSetPricesReplaces toplu yazmanın YERİNE KOYMA olduğunu kanıtlar.
func TestSetPricesReplaces(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100},{"currency_code":"USD","amount":5}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets/"+id+"/prices",
		`{"prices":[{"currency_code":"EUR","amount":9}]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 1)
	assert.Equal(t, "EUR", data[0]["currency_code"])
	assert.Equal(t, int64(1), count)

	after, _, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/price-sets/"+id+"/prices", ""))
	require.Len(t, after, 1, "verilmeyen fiyatlar silinmeli")
}

// TestDeletePriceSet silme akışını ve 204'ü kanıtlar.
func TestDeletePriceSet(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodDelete, "/admin/v1/price-sets/"+id, "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String(), "204 gövdesiz olmalı")

	assert.Equal(t, http.StatusNotFound,
		do(t, r, http.MethodGet, "/admin/v1/price-sets/"+id, "").Code)
}

// TestCalculateEndpoint hesaplama uç noktasının seçim sonucunu döndüğünü
// kanıtlar.
func TestCalculateEndpoint(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{"prices":[
		{"currency_code":"TRY","amount":1000},
		{"currency_code":"TRY","amount":800,"min_quantity":10,"max_quantity":20}
	]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+id+"/calculate?currency_code=TRY&quantity=10", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data := decodeItem(t, rec)
	assert.InDelta(t, 800, data["amount"], 0, "dar aralıklı kademe seçilmeli")
	assert.InDelta(t, 8000, data["total"], 0)
	assert.InDelta(t, 10, data["quantity"], 0)
	assert.Nil(t, data["price_list_type"], "taban fiyatta liste türü null olmalı")
}

// TestCalculateEndpointNotCalculable geçerli fiyat yokken 404 ve ayırt edici
// kod döndüğünü kanıtlar.
func TestCalculateEndpointNotCalculable(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":1000}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+id+"/calculate?currency_code=EUR", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "price_not_calculable", errorCode(t, rec))
}

// TestEskiHesaplamaPostuKaldirildi hesaplama ucunun POST karşılığının artık
// OLMADIĞINI kanıtlar.
//
// Kırıcı bir değişikliktir ve bilinçlidir: POST yolu uyumluluk için bırakılsa
// düzeltilen arıza olduğu yerde dururdu — yazma yetkisi isteyen bir hesaplama
// ucu ayakta kalır ve entegrasyonlar ona yaslanmayı sürdürürdü.
func TestEskiHesaplamaPostuKaldirildi(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":1000}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets/"+id+"/calculate",
		`{"currency_code":"TRY"}`)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code, rec.Body.String())
}

// TestHesaplamaKuralBaglaminiSorgudanOkur kural bağlamının sorgu dizesinden
// EKSİKSİZ taşındığını kanıtlar.
//
// Ucun gövdeden sorguya taşınmasının tek gerçek riski buydu: bağlam yolda
// düşerse hesap hata vermez, sessizce BAŞKA bir fiyata düşer. Bu yüzden her
// iki yön de sınanır — bağlam verilmeyince kurala bağlı fiyat elenmeli,
// verilince kazanmalıdır.
func TestHesaplamaKuralBaglaminiSorgudanOkur(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{"prices":[
		{"currency_code":"TRY","amount":1000},
		{"currency_code":"TRY","amount":800,"rules":[
			{"attribute":"region_id","operator":"eq","values":["reg_tr"]}
		]}
	]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)
	temel := "/admin/v1/price-sets/" + id + "/calculate?currency_code=TRY"

	rec := do(t, r, http.MethodGet, temel, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 1000, decodeItem(t, rec)["amount"], 0,
		"bağlam verilmediğinde kurala bağlı fiyat elenmeli")

	rec = do(t, r, http.MethodGet, temel+"&attr_region_id=reg_tr", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data := decodeItem(t, rec)
	assert.InDelta(t, 800, data["amount"], 0, "kural eşleşince bölgeye özel fiyat seçilmeli")
	assert.InDelta(t, 1, data["matched_rules"], 0)
}

// TestHesaplamaAniniSorgudanOkur "at" parametresinin hesaba GERÇEKTEN
// geçtiğini kanıtlar.
//
// Zaman damgası, sorgu dizesindeki tek yapılandırılmış değerdir; çözülüp
// servise verilmezse kampanya penceresi her zaman "şimdi"ye göre
// değerlendirilir ve geçmişe/geleceğe dönük her hesap sessizce yanlış olur.
func TestHesaplamaAniniSorgudanOkur(t *testing.T) {
	r, _ := newTestRouter(t)

	liste := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-lists",
		`{"title":"Temmuz kampanyası","type":"sale","status":"active",`+
			`"starts_at":"2026-07-01T00:00:00Z","ends_at":"2026-08-01T00:00:00Z"}`))
	listID, ok := liste["id"].(string)
	require.True(t, ok)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	require.Equal(t, http.StatusOK, do(t, r, http.MethodPost,
		"/admin/v1/price-sets/"+id+"/prices",
		`{"prices":[{"currency_code":"TRY","amount":1000},`+
			`{"currency_code":"TRY","amount":700,"price_list_id":"`+listID+`"}]}`).Code)
	temel := "/admin/v1/price-sets/" + id + "/calculate?currency_code=TRY"

	rec := do(t, r, http.MethodGet, temel, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 1000, decodeItem(t, rec)["amount"], 0,
		"testin saati kampanya penceresinin dışında; taban fiyat kazanmalı")

	rec = do(t, r, http.MethodGet, temel+"&at=2026-07-10T00:00:00Z", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.InDelta(t, 700, decodeItem(t, rec)["amount"], 0,
		"verilen an pencerenin içinde; kampanya fiyatı kazanmalı")
}

// TestHesaplamaSorgusuSessizceYokSaymaz bozuk sorgunun REDDEDİLDİĞİNİ
// kanıtlar.
//
// Uç POST'ken gövde decodeBody ile katı çözülüyordu: bilinmeyen alan hataydı.
// Sorgu dizesi doğası gereği hoşgörülüdür ve aynı katılık elle kurulmazsa
// taşıma sessiz bir gerileme olurdu — "?qty=10" yazan istemci 10 adet için
// sorduğunu sanırken tek adetlik fiyatı okurdu.
func TestHesaplamaSorgusuSessizceYokSaymaz(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":1000}]}`))
	id, ok := created["id"].(string)
	require.True(t, ok)
	temel := "/admin/v1/price-sets/" + id + "/calculate"

	for ad, sorgu := range map[string]string{
		"tanınmayan parametre":    "?currency_code=TRY&qty=10",
		"sayı olmayan adet":       "?currency_code=TRY&quantity=abc",
		"bozuk zaman damgası":     "?currency_code=TRY&at=2026-06-15",
		"tekrarlanan parametre":   "?currency_code=TRY&currency_code=USD",
		"tekrarlanan kural alanı": "?currency_code=TRY&attr_region_id=reg_1&attr_region_id=reg_2",
	} {
		t.Run(ad, func(t *testing.T) {
			rec := do(t, r, http.MethodGet, temel+sorgu, "")

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "hata zarfında kod bulunmalı")
		})
	}
}

// TestPriceListLifecycle fiyat listesi CRUD'unu kanıtlar.
func TestPriceListLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-lists",
		`{"title":"Yaz kampanyası","type":"sale"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	created := decodeItem(t, rec)
	id, ok := created["id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(id, "plist_"))
	assert.Equal(t, "draft", created["status"], "durum verilmediyse taslak olmalı")

	rec = do(t, r, http.MethodPut, "/admin/v1/price-lists/"+id,
		`{"title":"Yaz kampanyası","type":"sale","status":"active"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "active", decodeItem(t, rec)["status"])

	rec = do(t, r, http.MethodGet, "/admin/v1/price-lists", "")
	data, count, _, _ := decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)

	assert.Equal(t, http.StatusNoContent,
		do(t, r, http.MethodDelete, "/admin/v1/price-lists/"+id, "").Code)
	assert.Equal(t, http.StatusNotFound,
		do(t, r, http.MethodGet, "/admin/v1/price-lists/"+id, "").Code)
}

// TestPriceRuleEndpoints kural ekleme/listeleme/silme akışını kanıtlar.
func TestPriceRuleEndpoints(t *testing.T) {
	r, _ := newTestRouter(t)

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`))
	prices, ok := created["prices"].([]any)
	require.True(t, ok)
	first, ok := prices[0].(map[string]any)
	require.True(t, ok)
	priceID, ok := first["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/prices/"+priceID+"/rules",
		`{"attribute":"region_id","operator":"eq","values":["reg_1"]}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	rule := decodeItem(t, rec)
	ruleID, ok := rule["id"].(string)
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(ruleID, "prule_"))
	assert.Equal(t, priceID, rule["price_id"])

	data, _, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/prices/"+priceID+"/rules", ""))
	assert.Len(t, data, 1)

	assert.Equal(t, http.StatusNoContent,
		do(t, r, http.MethodDelete, "/admin/v1/price-rules/"+ruleID, "").Code)
}

// TestErrorStatusMapping servis hata sınıflarının status koduna doğru
// eşlendiğini kanıtlar. Handler'lar status SEÇMEZ; eşleme core/http'dedir.
func TestErrorStatusMapping(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{"olmayan kap", http.MethodGet, "/admin/v1/price-sets/pset_yok", "", http.StatusNotFound},
		{"yanlış kimlik öneki", http.MethodGet, "/admin/v1/price-sets/variant_1", "",
			http.StatusUnprocessableEntity},
		{"negatif tutar", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[{"currency_code":"TRY","amount":-1}]}`, http.StatusUnprocessableEntity},
		{"geçersiz para birimi", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[{"currency_code":"TRYX","amount":1}]}`, http.StatusUnprocessableEntity},
		{"aşırı büyük tutar", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[{"currency_code":"TRY","amount":9223372036854775807}]}`,
			http.StatusUnprocessableEntity},
		{"bozuk gövde", http.MethodPost, "/admin/v1/price-sets", `{`,
			http.StatusUnprocessableEntity},
		{"boş gövde", http.MethodPost, "/admin/v1/price-sets", "",
			http.StatusUnprocessableEntity},
		{"bilinmeyen alan", http.MethodPost, "/admin/v1/price-sets",
			`{"prices":[],"margin":5}`, http.StatusUnprocessableEntity},
		{"ikinci JSON belgesi", http.MethodPost, "/admin/v1/price-sets", `{} {}`,
			http.StatusUnprocessableEntity},
		{"sayı olmayan limit", http.MethodGet, "/admin/v1/price-sets?limit=abc", "",
			http.StatusUnprocessableEntity},
		{"negatif offset", http.MethodGet, "/admin/v1/price-sets?offset=-1", "",
			http.StatusUnprocessableEntity},
		{"geçersiz liste türü", http.MethodPost, "/admin/v1/price-lists",
			`{"title":"K","type":"bogus"}`, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, r, tc.method, tc.path, tc.body)
			assert.Equal(t, tc.status, rec.Code, rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec), "hata zarfında kod bulunmalı")
		})
	}
}

// TestBodySizeLimit aşırı büyük gövdenin reddedildiğini kanıtlar.
func TestBodySizeLimit(t *testing.T) {
	r, _ := newTestRouter(t)

	var sb strings.Builder
	sb.WriteString(`{"prices":[`)
	for i := range 40000 {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"currency_code":"TRY","amount":1}`)
	}
	sb.WriteString(`]}`)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets", sb.String())

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestUnpagedListReportsRealLimit sayfalanmamış liste zarfının limit alanının
// GERÇEĞİ yansıttığını kanıtlar.
//
// Limit service.MaxLimit'e kırpılsaydı 150 kayıtlı bir yanıt "count=150,
// limit=100" derdi; istemci sayfa boyunu 100 sanıp sayfalama döngüsüne girer ve
// aynı kayıtları tekrar okurdu. Kayıt sayısı bu yüzden MaxLimit'in ÜSTÜNDE
// seçilir — altında kalsaydı kırpma zaten görünmezdi.
func TestUnpagedListReportsRealLimit(t *testing.T) {
	r, _ := newTestRouter(t)

	const priceCount = int64(service.MaxLimit) + 50
	items := make([]string, 0, priceCount)
	for i := range priceCount {
		items = append(items,
			`{"currency_code":"TRY","amount":`+strconv.FormatInt(100+i, 10)+
				`,"min_quantity":`+strconv.FormatInt(i+1, 10)+`}`)
	}
	body := `{"prices":[` + strings.Join(items, ",") + `]}`

	created := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets", `{}`))
	id, ok := created["id"].(string)
	require.True(t, ok)

	rec := do(t, r, http.MethodPost, "/admin/v1/price-sets/"+id+"/prices", body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, int(priceCount))
	assert.Equal(t, priceCount, count)
	assert.Zero(t, offset)
	assert.Equal(t, priceCount, limit, "sayfalanmamış yanıtta limit kayıt sayısına eşit olmalı")

	data, count, _, limit = decodeList(t, do(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+id+"/prices", ""))
	assert.Len(t, data, int(priceCount))
	assert.Equal(t, priceCount, count)
	assert.Equal(t, priceCount, limit, "okuma yolunda da kırpılmamalı")
}
