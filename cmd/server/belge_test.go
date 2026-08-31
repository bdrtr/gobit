package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/module"
	"github.com/bdrtr/gobit/internal/core/openapi"
)

// sessizModul kendini ANLATMAYAN bir modüldür.
//
// [openapi.Describer] opsiyonel olduğu için bu da geçerli bir modeldir;
// belgeye yalnızca yolu ve güvenliğiyle girer.
type sessizModul struct{ ad string }

func (m sessizModul) Name() string                                         { return m.ad }
func (m sessizModul) Register(context.Context, *container.Container) error { return nil }
func (m sessizModul) Migrations() fs.FS                                    { return nil }
func (m sessizModul) Routes(chi.Router)                                    {}

// anlatanModul kendi ucunu anlatan bir modüldür.
type anlatanModul struct {
	sessizModul
	yol string
}

// govde anlatan modülün istek/yanıt gövdesinin şeklidir.
type govde struct {
	ID    string `json:"id"`
	Adres string `json:"adres,omitempty"`
}

// Describe modülün ucunu belgeye işler.
func (m anlatanModul) Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, m.yol, openapi.Operation{
		Summary:     "Sepet oluşturur",
		RequestBody: d.RequestBody(govde{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan sepet", d.Item(govde{})),
		},
	})
}

// Routes modülün ucunu router'a bağlar.
func (m anlatanModul) Routes(r chi.Router) {
	r.Post(m.yol, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
}

// belgeJSON belgeyi üretip JSON'dan geri okunmuş hâlini döner.
//
// Doğrudan [openapi.Doc.Build] çıktısına bakmak yetmezdi: işlemler orada Go
// struct'ıdır ve incelenen davranış tam olarak alanların JSON'a YAZILIP
// yazılmadığıdır.
func belgeJSON(t *testing.T, doc *openapi.Doc, r chi.Routes) map[string]any {
	t.Helper()

	belge, err := doc.Build(r)
	require.NoError(t, err)

	ham, err := json.Marshal(belge)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(ham, &cozulmus))

	return cozulmus
}

// belgeRouter anlatılan ucu taşıyan bir router kurar.
func belgeRouter(t *testing.T, moduller ...module.Module) chi.Router {
	t.Helper()

	r := chi.NewRouter()
	for _, mod := range moduller {
		mod.Routes(r)
	}

	return r
}

// TestBelgeyiAnlatOpsiyonelArayuzuCagirir tip iddiasının çalıştığını doğrular.
//
// Sözleşmeye zorunlu bir metot eklemek TÜM modülleri kırardı; opsiyonel
// arayüzün bedeli, kimin anlattığının derleme zamanında görünmemesidir. Bu
// test o bedeli karşılar: kanca kopsa hiçbir şey derlemede kırılmaz, yalnızca
// belge sessizce boşalırdı.
func TestBelgeyiAnlatOpsiyonelArayuzuCagirir(t *testing.T) {
	t.Parallel()

	anlatan := anlatanModul{sessizModul: sessizModul{ad: "cart"}, yol: "/store/v1/carts"}
	moduller := []module.Module{sessizModul{ad: "region"}, anlatan}

	doc := belgeyiAnlat("test", "v1", moduller)

	belge := belgeJSON(t, doc, belgeRouter(t, moduller...))
	assert.Empty(t, doc.UnmatchedDescriptions(), "açıklama route ile eşleşmeli")

	yollar, ok := belge["paths"].(map[string]any)
	require.True(t, ok)

	yol, ok := yollar["/store/v1/carts"].(map[string]any)
	require.True(t, ok, "anlatılan uç belgede olmalı")

	islem, ok := yol["post"].(map[string]any)
	require.True(t, ok)

	// Bulgunun tam karşılığı: uç artık ne göndereceğini ve ne döneceğini
	// SÖYLÜYOR. İkisi de yoksa istemci üreteci her şeyi 'any' olan, dönüş tipi
	// 'void' olan bir metot üretir.
	require.Contains(t, islem, "requestBody", "istek gövdesi belgelenmeli")

	yanitlar, ok := islem["responses"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, yanitlar, "201", "başarılı yanıt belgelenmeli")
}

// TestBelgeyiAnlatAnlatmayanModuluAtlar Describer uygulamayan modülün belgeyi
// bozmadığını doğrular.
func TestBelgeyiAnlatAnlatmayanModuluAtlar(t *testing.T) {
	t.Parallel()

	doc := belgeyiAnlat("test", "v1", []module.Module{sessizModul{ad: "region"}})

	_, err := doc.Build(chi.NewRouter())
	require.NoError(t, err, "anlatılmamış modül geçerli bir modeldir")
}

// TestSemayiDenetleEslesmeyenAciklamayiUyarir yolu değişmiş bir açıklamanın
// sessizce kaybolmadığını doğrular.
//
// Açılış DURMAZ (ADR 0007): şema belgedir, ürünün doğruluğu değil. Ama uyarı
// olmadan, silinmiş bir ucun açıklaması kimseye görünmeden yok olurdu.
func TestSemayiDenetleEslesmeyenAciklamayiUyarir(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	doc.Describe(http.MethodGet, "/store/v1/silinmis", openapi.Operation{Summary: "eski uç"})

	semayiDenetle(t.Context(), doc, chi.NewRouter(), slogYut())

	assert.Equal(t, []string{"GET /store/v1/silinmis"}, doc.UnmatchedDescriptions())
}
