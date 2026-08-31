package openapi_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// loginYolu giriş ucunun tam yoludur (auth modülündeki LoginPath ile aynı).
//
// Değer burada elle yazılır çünkü çekirdek testleri de modülleri import
// EDEMEZ (Prensip 2.4); uyum, üretilen şemanın giriş ucunu tanımasıyla
// dolaylı olarak sınanır.
const loginYolu = "/admin/v1/auth/login"

// routerKur belgelenecek uçları taşıyan bir router kurar.
func routerKur(t *testing.T) chi.Router {
	t.Helper()

	bos := func(http.ResponseWriter, *http.Request) {}

	r := chi.NewRouter()
	r.Post(loginYolu, bos)
	r.Get("/admin/v1/auth/me", bos)
	r.Get("/store/v1/products", bos)

	return r
}

// semaUret belgeyi üretip JSON'dan geri okunmuş hâlini döner.
//
// Doğrudan [openapi.Doc.Build] çıktısına bakmak yetmezdi: incelenen davranış
// tam olarak alanların JSON'a YAZILIP yazılmadığıdır ve struct'a bakan bir
// test omitempty'yi hiç görmez.
func semaUret(t *testing.T, d *openapi.Doc, r chi.Router) map[string]any {
	t.Helper()

	belge, err := d.Build(r)
	require.NoError(t, err)

	ham, err := json.Marshal(belge)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	return cozulmus
}

// harita bir şema düğümünü map olarak döner.
func harita(t *testing.T, deger any, ad string) map[string]any {
	t.Helper()

	m, ok := deger.(map[string]any)
	require.True(t, ok, "%s bir nesne olmalı, gelen: %T", ad, deger)

	return m
}

// islemAl şemadan tek bir yol+metod işlemini çıkarır.
func islemAl(t *testing.T, sema map[string]any, yol, metod string) map[string]any {
	t.Helper()

	yollar := harita(t, sema["paths"], "paths")
	require.Contains(t, yollar, yol)

	yolDugumu := harita(t, yollar[yol], yol)
	require.Contains(t, yolDugumu, metod)

	return harita(t, yolDugumu[metod], yol+" "+metod)
}

// yanitlarAl işlemin yanıt kümesini döner.
func yanitlarAl(t *testing.T, islem map[string]any) map[string]any {
	t.Helper()

	return harita(t, islem["responses"], "responses")
}

// TestGirisUcuKorumasizIsaretiSemayaYazilir boş "security" dizisinin JSON'a
// GERÇEKTEN yazıldığını doğrular.
//
// omitempty ile boş dizi hiç yazılmaz; alanı olmayan bir işlem OpenAPI'de
// "belirtilmemiş" sayılıp kök seviyedeki güvenliği miras alır. Yani şema,
// jetonu veren ucun jeton istediğini söyler ve istemci üreteçleri hiç
// çağrılamayan bir login metodu üretir.
func TestGirisUcuKorumasizIsaretiSemayaYazilir(t *testing.T) {
	t.Parallel()

	sema := semaUret(t, openapi.New("test", "v1"), routerKur(t))
	islem := islemAl(t, sema, loginYolu, "post")

	guvenlik, yazildi := islem["security"]
	require.True(t, yazildi,
		"\"security\" alanı yazılmalı; yazılmazsa uç korumalı sanılır")
	assert.Equal(t, []any{}, guvenlik, "boş dizi = bu uç açıkça korumasız")
}

// TestKorumaliUclarinGuvenligiYazilir korumalı uçların şemasının değişmediğini
// doğrular.
func TestKorumaliUclarinGuvenligiYazilir(t *testing.T) {
	t.Parallel()

	sema := semaUret(t, openapi.New("test", "v1"), routerKur(t))

	assert.Equal(t,
		[]any{map[string]any{"bearerAuth": []any{}}},
		islemAl(t, sema, "/admin/v1/auth/me", "get")["security"])
	assert.Equal(t,
		[]any{map[string]any{"publishableKey": []any{}}},
		islemAl(t, sema, "/store/v1/products", "get")["security"])
}

// TestGirisUcu401YanitiniBelgeler giriş ucunun 401'ini şemaya yazdığını
// doğrular.
//
// Uç korumasızdır ama işi kimlik bilgisi doğrulamaktır: hatalı parola 401
// döner. 401 belgelenmezse istemci üreteci giriş hatasını hiç ele almayan bir
// metod üretir ve yanlış parola beklenmeyen bir arıza gibi görünür.
func TestGirisUcu401YanitiniBelgeler(t *testing.T) {
	t.Parallel()

	sema := semaUret(t, openapi.New("test", "v1"), routerKur(t))
	yanitlar := yanitlarAl(t, islemAl(t, sema, loginYolu, "post"))

	require.Contains(t, yanitlar, "401", "giriş ucu hatalı parolada 401 döner")
	assert.Contains(t,
		harita(t, yanitlar["401"], "401")["description"], "parola",
		"girişte 401 \"jeton eksik\" değil \"kimlik bilgisi hatalı\" demektir")

	// 403 yalnızca yetkilendirme adımı olan uçlarda anlamlıdır; girişte henüz
	// bir kimlik yoktur.
	assert.NotContains(t, yanitlar, "403")
}

// TestKorumaliAdminUcu403Belgeler yetki yanıtının korumalı uçlarda durduğunu
// doğrular.
func TestKorumaliAdminUcu403Belgeler(t *testing.T) {
	t.Parallel()

	sema := semaUret(t, openapi.New("test", "v1"), routerKur(t))
	yanitlar := yanitlarAl(t, islemAl(t, sema, "/admin/v1/auth/me", "get"))

	assert.Contains(t, yanitlar, "401")
	assert.Contains(t, yanitlar, "403")
}

// TestElleVerilenBosGuvenlikKorunur [openapi.Doc.Describe] ile "açıkça
// korumasız" işaretlenen bir ucun ezilmediğini doğrular.
//
// Ezilseydi, korumasız olduğu bilinen tek uç giriş ucuyla sınırlı kalır ve
// eklentilerin getirdiği webhook uçları şemada yanlış anlatılırdı.
func TestElleVerilenBosGuvenlikKorunur(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	doc.Describe(http.MethodGet, "/store/v1/products", openapi.Operation{
		Security: []map[string][]string{},
	})

	sema := semaUret(t, doc, routerKur(t))

	assert.Equal(t, []any{}, islemAl(t, sema, "/store/v1/products", "get")["security"])
}
