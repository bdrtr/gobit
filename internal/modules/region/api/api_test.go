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

	"github.com/bdrtr/gobit/internal/modules/region/api"
	"github.com/bdrtr/gobit/internal/modules/region/service"
)

// testNow testlerin sabit saatidir.
var testNow = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

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
func do(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

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

// createRegion test için bir bölge oluşturur ve kimliğini döner.
func createRegion(t *testing.T, r chi.Router, name, currency string) string {
	t.Helper()

	rec := do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"`+name+`","currency_code":"`+currency+`","automatic_taxes":true,"tax_rate_bps":2000}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	id, ok := decodeItem(t, rec)["id"].(string)
	require.True(t, ok, "kimlik dönmeli")
	return id
}

// TestCreateRegionReturnsCreatedEnvelope oluşturma yanıtının status kodunu ve
// zarfını kanıtlar (plan Bölüm 8).
func TestCreateRegionReturnsCreatedEnvelope(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"Türkiye","currency_code":"try","automatic_taxes":true,"tax_rate_bps":2000}`)

	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))

	data := decodeItem(t, rec)
	assert.Equal(t, "Türkiye", data["name"])
	assert.Equal(t, "TRY", data["currency_code"], "kod BÜYÜK harfe normalleştirilmeli")
	assert.Equal(t, true, data["automatic_taxes"])
	assert.InDelta(t, 2000, data["tax_rate_bps"], 0, "oran baz puan olarak dönmeli")
	assert.Contains(t, data["id"], "reg_")
}

// TestCreateRegionRejectsInvalidInput geçersiz girdinin 422 ile döndüğünü
// kanıtlar.
//
// Handler status SEÇMEZ: servis errors.Invalid döner, corehttp onu 422'ye
// çevirir (plan Bölüm 2.7).
func TestCreateRegionRejectsInvalidInput(t *testing.T) {
	r, _ := newTestRouter(t)

	cases := map[string]string{
		"geçersiz para birimi biçimi": `{"name":"X","currency_code":"TRYX"}`,
		"tanımsız para birimi":        `{"name":"X","currency_code":"XYZ"}`,
		"boş ad":                      `{"name":"  ","currency_code":"TRY"}`,
		"aralık dışı vergi oranı":     `{"name":"X","currency_code":"TRY","tax_rate_bps":10001}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := do(t, r, http.MethodPost, "/admin/v1/regions", body)

			assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
			assert.NotEmpty(t, errorCode(t, rec))
		})
	}
}

// TestCreateRegionRejectsUnknownField bilinmeyen alanın sessizce yok
// sayılmadığını kanıtlar.
//
// Sessizce yok sayılsaydı, alan adını yanlış yazan bir istemci vergi oranının
// yazıldığını sanırdı.
func TestCreateRegionRejectsUnknownField(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"X","currency_code":"TRY","tax_rate":2000}`)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "region_invalid_body", errorCode(t, rec))
}

// TestCreateRegionRejectsEmptyAndDoubleBody boş ve çift JSON belgeli gövdenin
// reddedildiğini kanıtlar.
func TestCreateRegionRejectsEmptyAndDoubleBody(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodPost, "/admin/v1/regions", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "region_invalid_body", errorCode(t, rec))

	rec = do(t, r, http.MethodPost, "/admin/v1/regions",
		`{"name":"A","currency_code":"TRY"}{"name":"B","currency_code":"USD"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "region_invalid_body", errorCode(t, rec))
}

// TestRegionLifecycle bölge okuma, listeleme, kısmi güncelleme ve silmeyi
// uçtan uca kanıtlar.
func TestRegionLifecycle(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Türkiye", "TRY")

	rec := do(t, r, http.MethodGet, "/admin/v1/regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "Türkiye", decodeItem(t, rec)["name"])

	rec = do(t, r, http.MethodGet, "/admin/v1/regions", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, offset, limit := decodeList(t, rec)
	assert.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, int64(0), offset)
	assert.Equal(t, int64(service.DefaultLimit), limit, "uygulanan limit zarfta dönmeli")

	// Yalnızca ad gönderilir; para birimi ve oran DEĞİŞMEMELİDİR.
	rec = do(t, r, http.MethodPut, "/admin/v1/regions/"+id, `{"name":"Türkiye Bölgesi"}`)
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	updated := decodeItem(t, rec)
	assert.Equal(t, "Türkiye Bölgesi", updated["name"])
	assert.Equal(t, "TRY", updated["currency_code"], "verilmeyen alan değişmemeli")
	assert.InDelta(t, 2000, updated["tax_rate_bps"], 0, "verilmeyen alan değişmemeli")

	rec = do(t, r, http.MethodPut, "/admin/v1/regions/"+id, `{}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "boş yama reddedilmeli")

	rec = do(t, r, http.MethodDelete, "/admin/v1/regions/"+id, "")
	require.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String(), "204 gövdesiz olmalı")

	rec = do(t, r, http.MethodGet, "/admin/v1/regions/"+id, "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "silinen bölge okunamamalı")
}

// TestGetRegionWrongPrefixIsUnprocessable yanlış türde bir kimliğin 404 değil
// 422 döndüğünü kanıtlar.
func TestGetRegionWrongPrefixIsUnprocessable(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/regions/prod_01", "")

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Equal(t, "region_invalid_input", errorCode(t, rec))
}

// TestCountryUniquenessIsConflict aynı ülkeyi ikinci bir bölgeye eklemenin
// 409 döndüğünü kanıtlar.
func TestCountryUniquenessIsConflict(t *testing.T) {
	r, _ := newTestRouter(t)
	first := createRegion(t, r, "Türkiye", "TRY")
	second := createRegion(t, r, "Avrupa", "USD")

	rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+first+"/countries", `{"country_code":"tr"}`)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())
	country := decodeItem(t, rec)
	assert.Equal(t, "TR", country["code"], "ülke kodu BÜYÜK harfe normalleştirilmeli")
	assert.Equal(t, first, country["region_id"])

	rec = do(t, r, http.MethodPost, "/admin/v1/regions/"+second+"/countries", `{"country_code":"TR"}`)
	assert.Equal(t, http.StatusConflict, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, "country_already_in_region", errorCode(t, rec))
}

// TestRegionCountryListAndRemoval bölgenin ülke listesini ve çıkarmayı
// kanıtlar.
func TestRegionCountryListAndRemoval(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Avrupa", "USD")

	for _, code := range []string{"DE", "TR"} {
		rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+id+"/countries",
			`{"country_code":"`+code+`"}`)
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	rec := do(t, r, http.MethodGet, "/admin/v1/regions/"+id+"/countries", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 2)
	assert.Equal(t, int64(2), count)
	assert.Equal(t, "DE", data[0]["code"])

	rec = do(t, r, http.MethodDelete, "/admin/v1/regions/"+id+"/countries/de", "")
	require.Equal(t, http.StatusNoContent, rec.Code)

	rec = do(t, r, http.MethodDelete, "/admin/v1/regions/"+id+"/countries/DE", "")
	assert.Equal(t, http.StatusNotFound, rec.Code, "ikinci çıkarma bulunamadı dönmeli")

	rec = do(t, r, http.MethodGet, "/admin/v1/regions/"+id+"/countries", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, _, _, _ = decodeList(t, rec)
	require.Len(t, data, 1)
	assert.Equal(t, "TR", data[0]["code"])
}

// TestListCountriesFilter ülke listesinin bölge süzgecini kanıtlar.
func TestListCountriesFilter(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Türkiye", "TRY")
	rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+id+"/countries", `{"country_code":"TR"}`)
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = do(t, r, http.MethodGet, "/admin/v1/countries", "")
	require.Equal(t, http.StatusOK, rec.Code)
	_, count, _, _ := decodeList(t, rec)
	assert.Equal(t, int64(3), count, "süzgeçsiz istek tüm ülkeleri saymalı")

	rec = do(t, r, http.MethodGet, "/admin/v1/countries?region_id="+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 1)
	assert.Equal(t, int64(1), count)
	assert.Equal(t, "TR", data[0]["code"])

	// Boş bir region_id "süzme yok" DEĞİL, istemci hatasıdır.
	rec = do(t, r, http.MethodGet, "/admin/v1/countries?region_id=", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestCurrencyEndpointsAreReadOnly para birimi uç noktalarının okuma
// yüzeyini ve ondalık basamak alanını kanıtlar.
func TestCurrencyEndpointsAreReadOnly(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/admin/v1/currencies", "")
	require.Equal(t, http.StatusOK, rec.Code)
	data, count, _, _ := decodeList(t, rec)
	assert.Equal(t, int64(3), count)
	require.NotEmpty(t, data)

	rec = do(t, r, http.MethodGet, "/admin/v1/currencies/jpy", "")
	require.Equal(t, http.StatusOK, rec.Code)
	currency := decodeItem(t, rec)
	assert.Equal(t, "JPY", currency["code"])
	assert.InDelta(t, 0, currency["decimal_digits"], 0, "JPY ondalıksızdır")

	rec = do(t, r, http.MethodGet, "/admin/v1/currencies/XYZ", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// Referans veriye yazma yüzeyi YOKTUR; route hiç kayıtlı değildir.
	rec = do(t, r, http.MethodPost, "/admin/v1/currencies", `{"code":"XYZ"}`)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"para birimi yazma yüzeyi bilinçli olarak yoktur")
}

// TestStoreRegionsExposeCurrencyScale vitrin uç noktasının para biriminin
// ONDALIK BASAMAĞINI döndürdüğünü kanıtlar.
//
// Tutarlar minor unit tam sayıdır; istemci bölme çarpanını aynı yanıttan
// öğrenmezse sabit 100 varsayar ve yen tutarlarını yüz kat küçük gösterir.
func TestStoreRegionsExposeCurrencyScale(t *testing.T) {
	r, _ := newTestRouter(t)
	jpID := createRegion(t, r, "Japonya", "JPY")
	rec := do(t, r, http.MethodPost, "/admin/v1/regions/"+jpID+"/countries", `{"country_code":"JP"}`)
	require.Equal(t, http.StatusCreated, rec.Code)
	createRegion(t, r, "Türkiye", "TRY")

	rec = do(t, r, http.MethodGet, "/store/v1/regions", "")
	require.Equal(t, http.StatusOK, rec.Code, "gövde: %s", rec.Body.String())
	data, count, _, _ := decodeList(t, rec)
	require.Len(t, data, 2)
	assert.Equal(t, int64(2), count)

	byID := map[string]map[string]any{}
	for _, item := range data {
		id, ok := item["id"].(string)
		require.True(t, ok)
		byID[id] = item
	}

	jp := byID[jpID]
	currency, ok := jp["currency"].(map[string]any)
	require.True(t, ok, "para birimi gövdesi olmalı")
	assert.Equal(t, "JPY", currency["code"])
	assert.Equal(t, "¥", currency["symbol"])
	assert.InDelta(t, 0, currency["decimal_digits"], 0)

	countries, ok := jp["countries"].([]any)
	require.True(t, ok, "ülkeler gövdede olmalı")
	require.Len(t, countries, 1)

	// Vergi yapılandırması MÜŞTERİYE gitmez.
	assert.NotContains(t, jp, "tax_rate_bps")
	assert.NotContains(t, jp, "automatic_taxes")
}

// TestStoreRegionEmptyCountriesIsArray ülkesi olmayan bölgenin null değil boş
// dizi döndürdüğünü kanıtlar.
func TestStoreRegionEmptyCountriesIsArray(t *testing.T) {
	r, _ := newTestRouter(t)
	id := createRegion(t, r, "Türkiye", "TRY")

	rec := do(t, r, http.MethodGet, "/store/v1/regions/"+id, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"countries":[]`,
		"boş liste null değil [] olmalı")
}

// TestStoreRegionNotFound olmayan bölge için 404 döndüğünü kanıtlar.
func TestStoreRegionNotFound(t *testing.T) {
	r, _ := newTestRouter(t)

	rec := do(t, r, http.MethodGet, "/store/v1/regions/reg_YOK", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "region_not_found", errorCode(t, rec))
}

// TestPagingParamsMustBeIntegers sayıya çevrilemeyen sayfalama parametresinin
// sessizce ilk sayfaya düşmediğini kanıtlar.
func TestPagingParamsMustBeIntegers(t *testing.T) {
	r, _ := newTestRouter(t)

	for _, path := range []string{
		"/admin/v1/regions?limit=abc",
		"/admin/v1/regions?offset=abc",
		"/store/v1/regions?limit=x",
	} {
		rec := do(t, r, http.MethodGet, path, "")

		assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "yol: %s", path)
		assert.Equal(t, "region_invalid_body", errorCode(t, rec), "yol: %s", path)
	}
}
