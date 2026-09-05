package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/inventory/api"
)

// Bu dosya inventory'nin yönetim uçlarındaki YETKİ katmanını sınar.
//
// Kimlik katmanı (corehttp.RequireAdmin) burada taklit edilir: testin
// kanıtlamak istediği şey "kimlik doğru çözülüyor mu" değil, "çözülmüş
// kimliğin YETKİSİ uç bazında zorlanıyor mu" sorusudur. İkisi ayrı
// sınandığında, kimlik doğrulaması kusursuz çalışırken yetkilendirmenin hiç
// bağlanmamış olduğu durum — yani düzeltilen arıza — görünür kalır.

// yetkiliIstek verilen yetkileri taşıyan bir kimlikle istek yapar.
//
// Hiç yetki verilmemesi geçerli bir durumdur ve "kimliği var ama yetkisi yok"
// çağıranı üretir — arızanın ta kendisi bu kullanıcıydı.
func yetkiliIstek(
	t *testing.T, router chi.Router, method, yol, govde string, scopes ...string,
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
	router.ServeHTTP(kayit, req)
	return kayit
}

// kimliksizIstek context'e HİÇ kimlik koymadan istek yapar.
func kimliksizIstek(t *testing.T, router chi.Router, method, yol string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, yol, strings.NewReader(""))
	kayit := httptest.NewRecorder()
	router.ServeHTTP(kayit, req)
	return kayit
}

// yetkiHataKodu hata zarfındaki kodu döner.
func yetkiHataKodu(t *testing.T, kayit *httptest.ResponseRecorder) string {
	t.Helper()

	var zarf struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf), "gövde: %s", kayit.Body.String())
	return zarf.Error.Code
}

// servisDokunulmadi sahtenin HİÇBİR çağrı kaydı taşımadığını doğrular.
//
// Status kodu tek başına yetmez: 403'ü stoğu yazdıktan SONRA döndüren bir
// handler da aynı kodu yazardı. Sahtenin kaydettiği tek şey çağrı
// parametreleridir, bu yüzden "hiç kayıt yok" ile "servise hiç inilmedi" aynı
// şeydir. Tek istisna lokasyon oluşturmadır: sahte o çağrının girdisini
// kaydetmez, orada kanıt yalnızca status kodudur.
func servisDokunulmadi(t *testing.T, svc *fakeInventory) {
	t.Helper()

	assert.Empty(t, svc.gorulenID, "reddedilen istek servise HİÇ ulaşmamalı")
	assert.Empty(t, svc.gorulenLocationID, "reddedilen istek servise HİÇ ulaşmamalı")
	assert.Zero(t, svc.gorulenItemInput, "reddedilen istek servise HİÇ ulaşmamalı")
	assert.Zero(t, svc.gorulenStocked, "reddedilen istek servise HİÇ ulaşmamalı")
	assert.Zero(t, svc.gorulenDelta, "reddedilen istek servise HİÇ ulaşmamalı")
}

// yazmaUclari [api.ScopeWrite] isteyen tüm yönetim uçlarıdır.
//
// Liste [api.Handler.Routes] ile birlikte büyümelidir: eklenen ama buraya
// yazılmayan bir yazma ucu, sessizce yetkisiz kalabilecek tek yerdir.
var yazmaUclari = map[string]struct {
	method string
	yol    string
	govde  string
}{
	"lokasyon oluşturma": {
		http.MethodPost, "/admin/v1/stock-locations", `{"name":"Merkez","country_code":"TR"}`,
	},
	"kalem oluşturma": {http.MethodPost, "/admin/v1/inventory-items", `{"sku":"SKU-1"}`},
	"kalem silme":     {http.MethodDelete, "/admin/v1/inventory-items/iitem_1", ""},
	"seviye yazma": {
		http.MethodPost, "/admin/v1/inventory-items/iitem_1/levels",
		`{"location_id":"sloc_1","stocked_quantity":5}`,
	},
	"seviye düzeltme": {
		http.MethodPost, "/admin/v1/inventory-items/iitem_1/levels/sloc_1/adjust", `{"delta":3}`,
	},
}

// okumaUclari [api.ScopeRead] isteyen tüm yönetim uçlarıdır.
var okumaUclari = map[string]string{
	"lokasyon listesi": "/admin/v1/stock-locations",
	"tekil lokasyon":   "/admin/v1/stock-locations/sloc_1",
	"kalem listesi":    "/admin/v1/inventory-items",
	"tekil kalem":      "/admin/v1/inventory-items/iitem_1",
	"seviye listesi":   "/admin/v1/inventory-items/iitem_1/levels",
}

// TestYazmaUcuDarYetkiliCagiraniReddeder yazma uçlarının [api.ScopeWrite]
// istediğini kanıtlar.
//
// Çağıran GERÇEK bir kimliktir ve okuma yetkisi vardır; eksik olan tek şey
// yazma yetkisidir. Arızanın kendisi tam buydu: kimliği doğrulanmış her
// çağıran, yetkisine bakılmadan stok seviyelerini yazabiliyordu.
func TestYazmaUcuDarYetkiliCagiraniReddeder(t *testing.T) {
	for ad, tt := range yazmaUclari {
		t.Run(ad, func(t *testing.T) {
			router, svc := yeniSunucu(t)

			kayit := yetkiliIstek(t, router, tt.method, tt.yol, tt.govde, api.ScopeRead)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"okuma yetkili çağıran yazma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, yetkiHataKodu(t, kayit))
			servisDokunulmadi(t, svc)
		})
	}
}

// TestOkumaUcuDarYetkiyleCalisir okuma uçlarının aynı dar kimliği GEÇİRDİĞİNİ
// kanıtlar.
//
// Ayrı bir test olması bilinçlidir: her isteği reddeden bir middleware
// yukarıdaki tabloyu kusursuz geçer ama yönetim yüzeyini tümüyle kilitlerdi.
// [api.ScopeRead] yalnızca yazmayı kapalı tutmak için vardır; okumayı da
// admin'e bağlamak, stoğu raporlayan dar yetkili bir entegrasyonun (depo
// panosu, satış tahmini) gerçek stoğu bozabilen bir kimlikle çalışmasını
// zorunlu kılardı.
func TestOkumaUcuDarYetkiyleCalisir(t *testing.T) {
	for ad, yol := range okumaUclari {
		t.Run(ad, func(t *testing.T) {
			router, _ := yeniSunucu(t)

			kayit := yetkiliIstek(t, router, http.MethodGet, yol, "", api.ScopeRead)

			assert.Equal(t, http.StatusOK, kayit.Code,
				"okuma yetkisi okuma ucuna yetmeli; gövde: %s", kayit.Body.String())
		})
	}
}

// TestYazmaUcuAdminCagiraniKabulEder corehttp.ScopeAdmin'in ÜST YETKİ
// olduğunu, yani "inventory:write" ayrıca verilmeden de yazmaya yettiğini
// kanıtlar.
func TestYazmaUcuAdminCagiraniKabulEder(t *testing.T) {
	for ad, tt := range yazmaUclari {
		t.Run(ad, func(t *testing.T) {
			router, _ := yeniSunucu(t)

			kayit := yetkiliIstek(t, router, tt.method, tt.yol, tt.govde, corehttp.ScopeAdmin)

			assert.NotEqual(t, http.StatusForbidden, kayit.Code,
				"admin yazma ucunda 403 ALMAMALI; gövde: %s", kayit.Body.String())
			assert.NotEqual(t, http.StatusUnauthorized, kayit.Code,
				"admin kimliği kabul edilmeli; gövde: %s", kayit.Body.String())
		})
	}
}

// TestYetkisizKullaniciStogaErisemez yetkisi hiç olmayan bir yönetim
// kullanıcısının hiçbir stok ucunu çağıramadığını kanıtlar.
//
// auth service.CreateUserInput.Scopes godoc'u boş yetki listesinin "giriş
// yapabilir ama hiçbir yönetim ucuna erişemez" bir kullanıcı ürettiğini
// söylüyor; bu test o cümlenin inventory tarafındaki karşılığıdır.
func TestYetkisizKullaniciStogaErisemez(t *testing.T) {
	for ad, yol := range okumaUclari {
		t.Run("okuma/"+ad, func(t *testing.T) {
			router, svc := yeniSunucu(t)

			kayit := yetkiliIstek(t, router, http.MethodGet, yol, "")

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"yetkisiz kullanıcı okuma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			servisDokunulmadi(t, svc)
		})
	}

	for ad, tt := range yazmaUclari {
		t.Run("yazma/"+ad, func(t *testing.T) {
			router, svc := yeniSunucu(t)

			kayit := yetkiliIstek(t, router, tt.method, tt.yol, tt.govde)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"yetkisiz kullanıcı yazma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			servisDokunulmadi(t, svc)
		})
	}
}

// TestKimliksizYonetimIstegi401Dondurur kimliğin hiç olmadığı durumda yetki
// katmanının 403 DEĞİL 401 döndüğünü kanıtlar.
//
// Ayrım istemci için anlamlıdır: 401 "kim olduğunu söyle", 403 "kim olduğunu
// biliyorum ama yetkin yok" demektir. 403 dönseydi, kimlik başlığını unutan
// bir istemci jetonunu yenilemek yerine yetki istemeye giderdi.
func TestKimliksizYonetimIstegi401Dondurur(t *testing.T) {
	router, svc := yeniSunucu(t)

	kayit := kimliksizIstek(t, router, http.MethodGet, "/admin/v1/inventory-items")

	assert.Equal(t, http.StatusUnauthorized, kayit.Code, "gövde: %s", kayit.Body.String())
	assert.Equal(t, "Bearer", kayit.Header().Get("WWW-Authenticate"),
		"RFC 9110: 401 hangi şemanın beklendiğini bildirmeli")
	servisDokunulmadi(t, svc)
}
