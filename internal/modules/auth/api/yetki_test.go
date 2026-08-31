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

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/api"
)

// Bu dosya auth'un yönetim uçlarındaki YETKİ katmanını sınar.
//
// Kimlik katmanı (corehttp.RequireAdmin) burada taklit edilir: testin
// kanıtlamak istediği şey "kimlik doğru çözülüyor mu" değil, "çözülmüş kimliğin
// YETKİSİ uç bazında zorlanıyor mu" sorusudur. İkisi ayrı sınandığında, kimlik
// doğrulaması kusursuz çalışırken yetkilendirmenin hiç bağlanmamış olduğu
// durum — yani düzeltilen arıza — görünür kalır.

// Testlerin paylaştığı kimlik sabitleri.
const (
	// kimlikTestID sahte middleware'in context'e koyduğu kimliktir.
	kimlikTestID = "usr_test"
	// kimlikTuruKullanici yönetim kullanıcısı kimlik türüdür.
	kimlikTuruKullanici = "user"
	// kimlikTuruAnahtar API anahtarı kimlik türüdür.
	kimlikTuruAnahtar = "api_key"
	// cikisYolu çıkış ucunun tam yoludur.
	cikisYolu = "/admin/v1/auth/logout"
)

// yetkiliRouter verilen yetkileri taşıyan DOĞRULANMIŞ bir kimlikle router
// kurar.
//
// Hiç yetki verilmemesi geçerli bir durumdur ve "kimliği var ama yetkisi yok"
// çağıranı üretir; kimliğin hiç olmadığı durum için [kimliksizRouter] vardır.
func yetkiliRouter(t *testing.T, scopes ...string) (chi.Router, *sahteAuth) {
	t.Helper()

	svc := &sahteAuth{}
	r := chi.NewRouter()
	r.Use(kimlikVer(scopes...))
	api.New(svc).Routes(r)

	return r, svc
}

// kimliksizRouter context'e HİÇ kimlik konmayan bir router kurar.
func kimliksizRouter(t *testing.T) (chi.Router, *sahteAuth) {
	t.Helper()

	svc := &sahteAuth{}
	r := chi.NewRouter()
	api.New(svc).Routes(r)

	return r, svc
}

// kimlikVer doğrulanmış bir KULLANICI kimliğini context'e koyan middleware
// döner.
//
// Üretimde bunu corehttp.RequireAdmin yapar; testte kimliği elle koymak,
// yetki katmanını jeton üretimi ve veritabanı olmadan sınamayı sağlar.
func kimlikVer(scopes ...string) func(http.Handler) http.Handler {
	return kimlikVerTur(kimlikTuruKullanici, scopes...)
}

// kimlikVerTur verilen TÜRDE doğrulanmış bir kimliği context'e koyar.
//
// Tür ayrı verilebilmelidir: çıkış ucu kararını kimliğin türüne göre verir
// (bir API anahtarının kapatılacak oturumu yoktur) ve handler'ın türü servise
// geçirdiği ancak böyle sınanabilir.
func kimlikVerTur(kind string, scopes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal := corehttp.Principal{
				ID:     kimlikTestID,
				Kind:   kind,
				Scopes: scopes,
			}
			next.ServeHTTP(w, r.WithContext(corehttp.WithPrincipal(r.Context(), principal)))
		})
	}
}

// istek bir yönetim isteği çalıştırır ve yanıt kaydını döner.
func istek(t *testing.T, r chi.Router, method, yol, govde string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, yol, strings.NewReader(govde))
	req.Header.Set("Content-Type", "application/json")

	kayit := httptest.NewRecorder()
	r.ServeHTTP(kayit, req)
	return kayit
}

// hataKodu hata zarfındaki kodu döner.
func hataKodu(t *testing.T, kayit *httptest.ResponseRecorder) string {
	t.Helper()

	var zarf struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf), "gövde: %s", kayit.Body.String())
	return zarf.Error.Code
}

// yazmaUclari yetki isteyen tüm yazma uçlarıdır.
//
// Liste [api.Handler.Routes] ile birlikte büyümelidir: eklenen ama buraya
// yazılmayan bir yazma ucu, sessizce yetkisiz kalabilecek tek yerdir.
var yazmaUclari = map[string]struct {
	method string
	yol    string
	govde  string
	beklem int
}{
	"kullanıcı oluşturma":  {http.MethodPost, "/admin/v1/users", `{"email":"a@b.co"}`, http.StatusCreated},
	"kullanıcı güncelleme": {http.MethodPut, "/admin/v1/users/usr_1", `{}`, http.StatusOK},
	"kullanıcı silme":      {http.MethodDelete, "/admin/v1/users/usr_1", "", http.StatusNoContent},
	"parola atama": {
		http.MethodPost, "/admin/v1/users/usr_1/password", `{"password":"cok-uzun-parola"}`, http.StatusNoContent,
	},
	"anahtar oluşturma": {
		http.MethodPost, "/admin/v1/api-keys", `{"type":"secret","title":"t","scopes":["admin"]}`, http.StatusCreated,
	},
	"anahtar silme":  {http.MethodDelete, "/admin/v1/api-keys/apk_1", "", http.StatusNoContent},
	"anahtar iptali": {http.MethodPost, "/admin/v1/api-keys/apk_1/revoke", "", http.StatusOK},
	"kanal bağlama": {
		http.MethodPost, "/admin/v1/api-keys/apk_1/sales-channels", `{"sales_channel_id":"sc_1"}`, http.StatusOK,
	},
	"kanal bağını kaldırma": {
		http.MethodDelete, "/admin/v1/api-keys/apk_1/sales-channels/sc_1", "", http.StatusNoContent,
	},
	"kanal oluşturma":  {http.MethodPost, "/admin/v1/sales-channels", `{"name":"web"}`, http.StatusCreated},
	"kanal güncelleme": {http.MethodPut, "/admin/v1/sales-channels/sc_1", `{}`, http.StatusOK},
	"kanal silme":      {http.MethodDelete, "/admin/v1/sales-channels/sc_1", "", http.StatusNoContent},
}

// okumaUclari yetki isteyen tüm okuma uçlarıdır.
var okumaUclari = map[string]string{
	"kullanıcı listesi":    "/admin/v1/users",
	"tekil kullanıcı":      "/admin/v1/users/usr_1",
	"anahtar listesi":      "/admin/v1/api-keys",
	"tekil anahtar":        "/admin/v1/api-keys/apk_1",
	"anahtarın kanalları":  "/admin/v1/api-keys/apk_1/sales-channels",
	"satış kanalı listesi": "/admin/v1/sales-channels",
	"tekil satış kanalı":   "/admin/v1/sales-channels/sc_1",
}

// TestYazmaUcuDarYetkiliCagiraniReddeder yazma uçlarının [api.ScopeWrite]
// istediğini kanıtlar.
//
// Çağıran GERÇEK bir kimliktir ve okuma yetkisi vardır; eksik olan tek şey
// yazma yetkisidir. Arızanın kendisi tam buydu: kimliği doğrulanmış her
// çağıran, yetkisine bakılmadan tüm yönetim uçlarını çağırabiliyordu.
func TestYazmaUcuDarYetkiliCagiraniReddeder(t *testing.T) {
	for ad, tt := range yazmaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t, api.ScopeRead)

			kayit := istek(t, r, tt.method, tt.yol, tt.govde)

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"okuma yetkili çağıran yazma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Equal(t, corehttp.CodeForbidden, hataKodu(t, kayit))
			assert.Zero(t, svc.cagriSayisi,
				"reddedilen istek servise HİÇ ulaşmamalı; yazma reddedilmeden önce yapılmış olurdu")
		})
	}
}

// TestYazmaUcuAdminCagiraniKabulEder yetki katmanının yalnızca reddetmediğini,
// doğru kimliği de GEÇİRDİĞİNİ kanıtlar.
//
// Ayrı bir test olması bilinçlidir: her isteği reddeden bir middleware,
// yukarıdaki tabloyu kusursuz geçer ama yönetim yüzeyini tümüyle kilitlerdi.
func TestYazmaUcuAdminCagiraniKabulEder(t *testing.T) {
	for ad, tt := range yazmaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t, corehttp.ScopeAdmin)

			kayit := istek(t, r, tt.method, tt.yol, tt.govde)

			assert.Equal(t, tt.beklem, kayit.Code,
				"admin yazma ucunu çağırabilmeli; gövde: %s", kayit.Body.String())
			assert.Positive(t, svc.cagriSayisi, "istek servise ulaşmalı")
		})
	}
}

// TestOkumaUcuDarYetkiyleCalisir okuma uçlarının admin İSTEMEDİĞİNİ kanıtlar.
//
// [api.ScopeRead] yalnızca yazma uçlarını kapalı tutmak için vardır; okumayı
// da admin'e bağlamak, sözlüğü tek yetkiye indirger ve dar yetkili bir
// entegrasyonun (örneğin kullanıcı listesini raporlayan bir iş) tam yetki
// istemesine yol açardı.
func TestOkumaUcuDarYetkiyleCalisir(t *testing.T) {
	for ad, yol := range okumaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t, api.ScopeRead)

			kayit := istek(t, r, http.MethodGet, yol, "")

			assert.Equal(t, http.StatusOK, kayit.Code,
				"okuma yetkisi okuma ucuna yetmeli; gövde: %s", kayit.Body.String())
			assert.Equal(t, 1, svc.cagriSayisi)
		})
	}
}

// TestOkumaUcuYetkisizCagiraniReddeder yetkisi hiç olmayan bir kullanıcının
// okuma uçlarına da erişemediğini kanıtlar.
//
// service.CreateUserInput.Scopes godoc'u boş yetki listesinin "giriş yapabilir
// ama hiçbir yönetim ucuna erişemez" bir kullanıcı ürettiğini söylüyor; bu
// test o cümlenin karşılığıdır.
func TestOkumaUcuYetkisizCagiraniReddeder(t *testing.T) {
	for ad, yol := range okumaUclari {
		t.Run(ad, func(t *testing.T) {
			r, svc := yetkiliRouter(t)

			kayit := istek(t, r, http.MethodGet, yol, "")

			assert.Equal(t, http.StatusForbidden, kayit.Code,
				"yetkisiz kullanıcı okuma ucunda 403 almalı; gövde: %s", kayit.Body.String())
			assert.Zero(t, svc.cagriSayisi)
		})
	}
}

// TestKimlikUclariYetkiIstemez giriş, /auth/me ve /auth/logout uçlarının yetki
// sormadığını kanıtlar.
//
// Giriş ucu kimliği daha yeni kuracaktır; yetki isteseydi hiç kimse giriş
// yapamazdı. Kimlik ucu ise çağıranın ZATEN sahip olduğu yetkileri geri okur;
// yetki isteseydi yetkisiz bir kullanıcı 403'ünün nedenini göremezdi. Çıkış
// ucu da yetki istemez: kendi oturumunu kapatmak bir ayrıcalık değildir ve
// yetki isteseydi, yetkisi geri alınmış bir yöneticinin jetonu süresi dolana
// kadar kapatılamazdı.
func TestKimlikUclariYetkiIstemez(t *testing.T) {
	r, svc := yetkiliRouter(t)

	giris := istek(t, r, http.MethodPost, api.LoginPath, `{"email":"a@b.co","password":"parola"}`)
	assert.Equal(t, http.StatusOK, giris.Code,
		"giriş ucu yetki istememeli; gövde: %s", giris.Body.String())

	kimlik := istek(t, r, http.MethodGet, "/admin/v1/auth/me", "")
	assert.Equal(t, http.StatusOK, kimlik.Code,
		"kimlik ucu yetki istememeli; gövde: %s", kimlik.Body.String())

	cikis := istek(t, r, http.MethodPost, cikisYolu, "")
	assert.Equal(t, http.StatusOK, cikis.Code,
		"çıkış ucu yetki istememeli; gövde: %s", cikis.Body.String())

	assert.Equal(t, 2, svc.cagriSayisi,
		"giriş ve çıkış servise iner; /auth/me context'ten okur")
}

// TestKimliksizIstekYetkiKatmanindaDa401Dondurur kimliğin olmadığı durumda
// yetki katmanının 403 DEĞİL 401 döndüğünü kanıtlar.
//
// Ayrım istemci için anlamlıdır: 401 "kim olduğunu söyle", 403 "kim olduğunu
// biliyorum ama yetkin yok" demektir. 403 dönseydi, kimlik başlığını unutan
// bir istemci jetonunu yenilemek yerine yetki istemeye giderdi.
func TestKimliksizIstekYetkiKatmanindaDa401Dondurur(t *testing.T) {
	r, svc := kimliksizRouter(t)

	kayit := istek(t, r, http.MethodGet, "/admin/v1/users", "")

	assert.Equal(t, http.StatusUnauthorized, kayit.Code, "gövde: %s", kayit.Body.String())
	assert.Equal(t, "Bearer", kayit.Header().Get("WWW-Authenticate"),
		"RFC 9110: 401 hangi şemanın beklendiğini bildirmeli")
	assert.Zero(t, svc.cagriSayisi)
}
