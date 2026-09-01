package openapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// sayanRouter kaç kez GEZİLDİĞİNİ sayan bir router sarmalayıcısıdır.
//
// Önbellek dışarıdan görünmez: aynı belgeyi iki kez üretmek de bir kez üretip
// saklamak da AYNI gövdeyi verir. Ayırt edilebilir tek şey, ağacın kaç kez
// gezildiğidir — [openapi.Doc.Handler] her istekte kimlik için bir kez gezer,
// belgeyi ÜRETİRKEN ise bir kez daha.
type sayanRouter struct {
	chi.Router
	gezilme *int
}

// Routes sayacı artırır ve sarmalanan router'ın route'larını döner.
func (s sayanRouter) Routes() []chi.Route {
	*s.gezilme++

	return s.Router.Routes()
}

// istek handler'ı çağırıp durum kodunu ve gövdeyi döner.
func istek(t *testing.T, h http.HandlerFunc) (kod int, govde string) {
	t.Helper()

	kayit := httptest.NewRecorder()
	h(kayit, httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody))

	return kayit.Code, kayit.Body.String()
}

// TestBelgeGirdiDegismedikceYenidenUretilmez önbelleğin gerçekten tuttuğunu
// doğrular.
//
// Uç kimlik ve kota kapılarının dışında mount edilebilir; her istekte tüm
// route ağacını gezip belgeyi kurmak ve kodlamak, küçük bir GET'i sürecin en
// pahalı işine çevirirdi.
func TestBelgeGirdiDegismedikceYenidenUretilmez(t *testing.T) {
	t.Parallel()

	gezilme := 0
	r := sayanRouter{Router: routerKur(t), gezilme: &gezilme}
	h := openapi.New("test", "v1").Handler(r)

	kod, govde := istek(t, h)
	require.Equal(t, http.StatusOK, kod, "gövde: %s", govde)
	require.Equal(t, 2, gezilme, "ilk istek ağacı kimlik için ve üretim için gezer")

	kod, ikinci := istek(t, h)
	require.Equal(t, http.StatusOK, kod)

	assert.Equal(t, 3, gezilme,
		"ikinci istek ağacı yalnızca KİMLİK için gezmeli; belge yeniden üretilirse "+
			"önbellek hiçbir şey kazandırmıyor demektir")
	assert.JSONEq(t, govde, ikinci, "önbellekten dönen gövde ilkiyle aynı olmalı")
}

// TestSonradanEklenenRouteBelgeyeGirer önbelleğin bir VARSAYIMA değil belgenin
// girdilerine bağlandığını doğrular.
//
// Ağacın ne zaman donduğu çekirdeğin bilebileceği bir şey değildir: route'ları
// modüller bootstrap sırasında, eklentiler ondan sonra bağlar ve handler'ın
// hangi sırada kaydedildiğine dair bir garanti yoktur. "Açılışta bir kez üret"
// diyen bir önbellek, handler'dan sonra bağlanan her ucu belgeden SESSİZCE
// düşürürdü — belgenin varlık sebebi tam da bunun olmamasıdır.
func TestSonradanEklenenRouteBelgeyeGirer(t *testing.T) {
	t.Parallel()

	r := routerKur(t)
	h := openapi.New("test", "v1").Handler(r)

	kod, ilk := istek(t, h)
	require.Equal(t, http.StatusOK, kod)
	require.NotContains(t, ilk, "/store/v1/sonradan")

	r.Get("/store/v1/sonradan", func(http.ResponseWriter, *http.Request) {})

	kod, ikinci := istek(t, h)
	require.Equal(t, http.StatusOK, kod)
	assert.Contains(t, ikinci, "/store/v1/sonradan",
		"ağaca sonradan eklenen uç belgede görünmeli")
}

// TestSonradanAnlatilanUcBelgeyeGirer anlatım kayıtlarının da önbelleği
// geçersiz kıldığını doğrular.
//
// Route ağacı DEĞİŞMEDEN de belge değişebilir: gövde şemasını ve özeti
// [openapi.Doc.Describe] taşır. Yalnızca ağacı izleyen bir önbellek, kurulumdan
// sonra anlatılan bir ucu gövdesiz göstermeye devam ederdi.
func TestSonradanAnlatilanUcBelgeyeGirer(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	h := doc.Handler(routerKur(t))

	kod, ilk := istek(t, h)
	require.Equal(t, http.StatusOK, kod)
	require.NotContains(t, ilk, "ürünleri listeler")

	doc.Describe(http.MethodGet, "/store/v1/products", openapi.Operation{
		Summary: "ürünleri listeler",
	})

	kod, ikinci := istek(t, h)
	require.Equal(t, http.StatusOK, kod)
	assert.Contains(t, ikinci, "ürünleri listeler",
		"sonradan anlatılan uç belgede görünmeli")
}

// TestUretilemeyenBelgeCekirdekHataZarfiylaDoner uç hatasının çekirdeğin
// politikasından geçtiğini doğrular.
//
// Üretimi başarısız kılmak için çekirdeğin ortak bileşeniyle çakışan bir tip
// ([Error], schema_test.go) kaydedilir; belge üretilemez ve uç hata döner.
//
// Uç bir JSON API'sidir; düz metin bir hata gövdesi, istemcinin hatayı
// ayrıştıramaması demektir. Daha önemlisi üretim hatasının METNİ çakışan
// tiplerin PAKET YOLLARINI taşır ve bu uç kimliksiz çağrılabilir: iç yapıyı
// olduğu gibi yazmak, sunucunun kaynak ağacını dışarıya anlatmaktır.
func TestUretilemeyenBelgeCekirdekHataZarfiylaDoner(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	doc.SchemaOf(Error{})

	kod, govde := istek(t, doc.Handler(routerKur(t)))
	require.Equal(t, http.StatusInternalServerError, kod, "gövde: %s", govde)

	var zarf struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(govde), &zarf),
		"hata gövdesi ortak JSON zarfı olmalı; gelen: %s", govde)

	assert.Equal(t, "openapi_document_unavailable", zarf.Error.Code,
		"istemci hatayı KODUNDAN tanımalı")
	assert.NotContains(t, govde, "internal/core/openapi",
		"çakışan tiplerin paket yolu istemciye SIZDIRILMAMALI")
	assert.NotContains(t, govde, "Error adı",
		"üretim hatasının ham metni istemciye SIZDIRILMAMALI")
}

// TestBelgeYanitiJSONBasligiTasir yanıtın içerik tipini doğrular.
//
// Gövde çekirdeğin yazıcısından geçmeden yazılıyor (belge zaten kodlanmıştır);
// başlığın da yazıldığını sınayan bir iddia olmadan, o yolun sessizce
// Content-Type'sız yanıt vermesi mümkün olurdu.
func TestBelgeYanitiJSONBasligiTasir(t *testing.T) {
	t.Parallel()

	kayit := httptest.NewRecorder()
	openapi.New("test", "v1").Handler(routerKur(t))(
		kayit, httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody))

	assert.Equal(t, "application/json; charset=utf-8", kayit.Header().Get("Content-Type"))

	var belge map[string]any
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &belge))
	assert.Equal(t, openapi.Version, belge["openapi"])
}
