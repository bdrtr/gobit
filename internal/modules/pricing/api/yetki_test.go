package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/pricing/api"
)

// Bu dosya pricing'in yönetim uçlarındaki YETKİ katmanını sınar.
//
// Kimlik katmanı (corehttp.RequireAdmin) burada taklit edilir: testin
// kanıtlamak istediği şey "kimlik doğru çözülüyor mu" değil, "çözülmüş
// kimliğin YETKİSİ uç bazında zorlanıyor mu" sorusudur. İkisi ayrı
// sınandığında, kimlik doğrulaması kusursuz çalışırken yetkilendirmenin hiç
// bağlanmamış olduğu durum — yani düzeltilen arıza — görünür kalır.
//
// Servis GERÇEKTİR (bellek içi depoyla): reddedilen bir isteğin depoyu
// değiştirmediği ancak gerçek bir yazma yolu varken anlamlı biçimde
// doğrulanabilir.

// yetkiliIstek verilen yetkileri taşıyan bir kimlikle istek yapar.
//
// Hiç yetki verilmemesi geçerli bir durumdur ve "kimliği var ama yetkisi yok"
// çağıranı üretir — arızanın ta kendisi bu kullanıcıydı.
func yetkiliIstek(
	t *testing.T, r chi.Router, method, yol, govde string, scopes ...string,
) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, yol, strings.NewReader(govde))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), corehttp.Principal{
		ID:     "usr_dar",
		Kind:   "user",
		Scopes: scopes,
	}))

	kayit := httptest.NewRecorder()
	r.ServeHTTP(kayit, req)
	return kayit
}

// kimliksizIstek context'e HİÇ kimlik koymadan istek yapar.
func kimliksizIstek(t *testing.T, r chi.Router, method, yol, govde string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, yol, strings.NewReader(govde))
	req.Header.Set("Content-Type", "application/json")

	kayit := httptest.NewRecorder()
	r.ServeHTTP(kayit, req)
	return kayit
}

// yetkiFiksturu tam yetkili bir kimlikle bir fiyat seti, bir fiyat listesi ve
// bir fiyat kaydı oluşturur.
//
// Okuma uçlarının 200 dönebilmesi için gerçek kimlikler gerekir: var olmayan
// bir kaydı okumak 404 dönerdi ve test, yetki katmanının izin verdiğini değil
// kaydın bulunamadığını ölçerdi. Fikstür do() ile kurulur; o yardımcı isteğe
// tam yetkili kimliği ekler.
func yetkiFiksturu(t *testing.T, r chi.Router) (priceSetID, priceID, priceListID string) {
	t.Helper()

	set := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":19900}]}`))
	priceSetID, ok := set["id"].(string)
	require.True(t, ok, "fiyat seti kimliği okunamadı")

	prices, ok := set["prices"].([]any)
	require.True(t, ok)
	require.Len(t, prices, 1)
	first, ok := prices[0].(map[string]any)
	require.True(t, ok)
	priceID, ok = first["id"].(string)
	require.True(t, ok, "fiyat kimliği okunamadı")

	liste := decodeItem(t, do(t, r, http.MethodPost, "/admin/v1/price-lists",
		`{"title":"Yaz kampanyası","type":"sale"}`))
	priceListID, ok = liste["id"].(string)
	require.True(t, ok, "fiyat listesi kimliği okunamadı")

	return priceSetID, priceID, priceListID
}

// TestYazmaUcuDarYetkiliCagiraniReddeder yazma uçlarının [api.ScopeWrite]
// istediğini kanıtlar.
//
// Çağıran GERÇEK bir kimliktir ve okuma yetkisi vardır; eksik olan tek şey
// yazma yetkisidir. Arızanın kendisi tam buydu: kimliği doğrulanmış her
// çağıran, yetkisine bakılmadan bütün fiyatları değiştirebiliyordu.
func TestYazmaUcuDarYetkiliCagiraniReddeder(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, priceID, listID := yetkiFiksturu(t, r)

	uclar := map[string]struct {
		method string
		yol    string
		govde  string
	}{
		"fiyat seti oluşturma": {http.MethodPost, "/admin/v1/price-sets", `{"prices":[]}`},
		"fiyat seti silme":     {http.MethodDelete, "/admin/v1/price-sets/" + setID, ""},
		"fiyat yazma": {
			http.MethodPost, "/admin/v1/price-sets/" + setID + "/prices",
			`{"prices":[{"currency_code":"TRY","amount":1}]}`,
		},
		"fiyat listesi oluşturma": {
			http.MethodPost, "/admin/v1/price-lists", `{"title":"x","type":"sale"}`,
		},
		"fiyat listesi güncelleme": {
			http.MethodPut, "/admin/v1/price-lists/" + listID, `{"title":"x","type":"sale"}`,
		},
		"fiyat listesi silme": {http.MethodDelete, "/admin/v1/price-lists/" + listID, ""},
		"kural oluşturma": {
			http.MethodPost, "/admin/v1/prices/" + priceID + "/rules",
			`{"attribute":"region_id","operator":"eq","values":["reg_1"]}`,
		},
		"kural silme": {http.MethodDelete, "/admin/v1/price-rules/prule_1", ""},
	}

	for ad, tt := range uclar {
		t.Run(ad, func(t *testing.T) {
			kayit := yetkiliIstek(t, r, tt.method, tt.yol, tt.govde, api.ScopeRead)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"okuma yetkili çağıran yazma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, errorCode(t, kayit))
		})
	}

	// Reddedilen istek servise HİÇ ulaşmamalıdır. Status kodu tek başına bunu
	// kanıtlamaz: 403'ü yazmadan ÖNCE silmiş bir handler da aynı kodu dönerdi.
	assert.Equal(t, http.StatusOK, do(t, r, http.MethodGet, "/admin/v1/price-sets/"+setID, "").Code,
		"reddedilen silme isteği fiyat setini silmemeli")
	assert.Equal(t, http.StatusOK, do(t, r, http.MethodGet, "/admin/v1/price-lists/"+listID, "").Code,
		"reddedilen silme isteği fiyat listesini silmemeli")

	fiyatlar, _, _, _ := decodeList(t, do(t, r, http.MethodGet, "/admin/v1/price-sets/"+setID+"/prices", ""))
	require.Len(t, fiyatlar, 1, "reddedilen fiyat yazma isteği fiyatları değiştirmemeli")
	assert.InDelta(t, 19900, fiyatlar[0]["amount"], 0)
}

// TestOkumaUcuDarYetkiyleCalisir okuma uçlarının aynı dar kimliği GEÇİRDİĞİNİ
// kanıtlar.
//
// Ayrı bir test olması bilinçlidir: her isteği reddeden bir middleware
// yukarıdaki tabloyu kusursuz geçer ama yönetim yüzeyini tümüyle kilitlerdi.
// [api.ScopeRead] yalnızca yazmayı kapalı tutmak için vardır; okumayı da
// admin'e bağlamak, fiyatı raporlayan dar yetkili bir entegrasyonun fiyat
// yazabilen bir kimlikle çalışmasını zorunlu kılardı.
func TestOkumaUcuDarYetkiyleCalisir(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, priceID, listID := yetkiFiksturu(t, r)

	uclar := map[string]string{
		"fiyat seti listesi":    "/admin/v1/price-sets",
		"tekil fiyat seti":      "/admin/v1/price-sets/" + setID,
		"fiyat listesi":         "/admin/v1/price-sets/" + setID + "/prices",
		"fiyat listeleri":       "/admin/v1/price-lists",
		"tekil fiyat listesi":   "/admin/v1/price-lists/" + listID,
		"fiyatın kural listesi": "/admin/v1/prices/" + priceID + "/rules",
		"fiyat hesaplama":       "/admin/v1/price-sets/" + setID + "/calculate?currency_code=TRY",
	}

	for ad, yol := range uclar {
		t.Run(ad, func(t *testing.T) {
			kayit := yetkiliIstek(t, r, http.MethodGet, yol, "", api.ScopeRead)

			assert.Equal(t, http.StatusOK, kayit.Code,
				"okuma yetkisi okuma ucuna yetmeli; gövde: %s", kayit.Body.String())
		})
	}
}

// TestHesaplamaUcuOkumaYetkisiyleCalisir düzeltilen arızanın kendisini
// kanıtlar: fiyat HESAPLATMAK için fiyat YAZABİLEN bir kimlik gerekmez.
//
// Uç eskiden POST'tu; yetki sözlüğü metoda baktığı için (bkz. api.API.Routes)
// [api.ScopeWrite] istiyordu ve fiyatı yalnızca raporlayan bir entegrasyon —
// fiyat karşılaştırma, dışa aktarma — tek istekte bütün kataloğu
// değiştirebilen bir kimlikle çalışmak zorunda kalıyordu.
//
// İkinci iddia aynı testte durur ve bilinçlidir: hesaplama okumaya açılırken
// YAZMA yüzeyi kapalı kalmalıdır. Yalnızca ilk iddia sınansaydı, dar kimliğe
// yazma yetkisi de veren bir gerileme testi geçerdi ve düzeltme arızayı
// büyütmüş olurdu.
func TestHesaplamaUcuOkumaYetkisiyleCalisir(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, _, _ := yetkiFiksturu(t, r)

	kayit := yetkiliIstek(t, r, http.MethodGet,
		"/admin/v1/price-sets/"+setID+"/calculate?currency_code=TRY&quantity=2", "",
		api.ScopeRead)

	require.Equal(t, http.StatusOK, kayit.Code,
		"okuma yetkisi fiyat hesaplamaya yetmeli; gövde: %s", kayit.Body.String())
	hesap := decodeItem(t, kayit)
	assert.InDelta(t, 19900, hesap["amount"], 0)
	assert.InDelta(t, 39800, hesap["total"], 0, "hesap gerçekten yapılmalı, boş zarf dönmemeli")

	yazma := yetkiliIstek(t, r, http.MethodPost, "/admin/v1/price-sets/"+setID+"/prices",
		`{"prices":[{"currency_code":"TRY","amount":1}]}`, api.ScopeRead)

	assert.Equal(t, http.StatusForbidden, yazma.Code,
		"aynı dar kimlik fiyat yazamamalı; gövde: %s", yazma.Body.String())
}

// TestAdminUstYetkidir corehttp.ScopeAdmin'in "pricing:write" ayrıca
// verilmeden de yazmaya yettiğini kanıtlar.
func TestAdminUstYetkidir(t *testing.T) {
	r, _ := newTestRouter(t)

	kayit := yetkiliIstek(t, r, http.MethodPost, "/admin/v1/price-sets",
		`{"prices":[{"currency_code":"TRY","amount":100}]}`, corehttp.ScopeAdmin)

	assert.Equal(t, http.StatusCreated, kayit.Code,
		"admin yetkisi tek başına yazmaya yetmeli; gövde: %s", kayit.Body.String())
}

// TestYetkisizKullaniciFiyatlaraErisemez yetkisi hiç olmayan bir yönetim
// kullanıcısının okuma ucuna da erişemediğini kanıtlar.
//
// auth service.CreateUserInput.Scopes godoc'u boş yetki listesinin "giriş
// yapabilir ama hiçbir yönetim ucuna erişemez" bir kullanıcı ürettiğini
// söylüyor; bu test o cümlenin pricing tarafındaki karşılığıdır.
func TestYetkisizKullaniciFiyatlaraErisemez(t *testing.T) {
	r, _ := newTestRouter(t)

	kayit := yetkiliIstek(t, r, http.MethodGet, "/admin/v1/price-sets", "")

	assert.Equal(t, http.StatusForbidden, kayit.Code,
		"yetkisiz kullanıcı okuma ucunda 403 almalı; gövde: %s", kayit.Body.String())
	assert.Equal(t, corehttp.CodeForbidden, errorCode(t, kayit))
}

// TestMagazaUcuYetkiIstemez /store/v1 ucunun yetki SORMADIĞINI kanıtlar.
//
// Mağaza yüzeyinin kimliği publishable anahtardır ve o anahtar tanımı gereği
// yetki TAŞIMAZ. Bu uca bir scope eklenseydi, hiçbir mağaza istemcisi fiyat
// okuyamazdı.
func TestMagazaUcuYetkiIstemez(t *testing.T) {
	r, _ := newTestRouter(t)
	setID, _, _ := yetkiFiksturu(t, r)

	kayit := kimliksizIstek(t, r, http.MethodGet, "/store/v1/price-sets/"+setID, "")

	assert.Equal(t, http.StatusOK, kayit.Code,
		"mağaza ucu yetki istememeli; gövde: %s", kayit.Body.String())
}

// TestKimliksizYonetimIstegi401Dondurur kimliğin hiç olmadığı durumda yetki
// katmanının 403 DEĞİL 401 döndüğünü kanıtlar.
//
// Ayrım istemci için anlamlıdır: 401 "kim olduğunu söyle", 403 "kim olduğunu
// biliyorum ama yetkin yok" demektir. 403 dönseydi, kimlik başlığını unutan
// bir istemci jetonunu yenilemek yerine yetki istemeye giderdi.
func TestKimliksizYonetimIstegi401Dondurur(t *testing.T) {
	r, _ := newTestRouter(t)

	kayit := kimliksizIstek(t, r, http.MethodGet, "/admin/v1/price-sets", "")

	assert.Equal(t, http.StatusUnauthorized, kayit.Code, "gövde: %s", kayit.Body.String())
	assert.Equal(t, "Bearer", kayit.Header().Get("WWW-Authenticate"),
		"RFC 9110: 401 hangi şemanın beklendiğini bildirmeli")
}
