package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya paketin İÇİNDEN test eder ve bunun tek bir sebebi vardır:
// buradaki iddialar handler'ın DIŞINDAN gözlenemez. "Reddedilen belge
// önbelleğe girmemeli" cümlesinin yanıtta hiçbir izi yoktur — sorgu iki kez
// gönderilse bile ikinci yanıt aynı görünür — ve yanıt sarmalayıcısının
// akış hâlindeki davranışı, tek Write yapan bugünkü taşımayla hiç
// tetiklenmez. Dışarıdan sınanamayan şeyi sınamamak, sınamak için üretimi
// eğip bükmekten iyidir; ama burada üretim eğilmeden erişilebiliyor.

// sessizVitrin hiçbir veri döndürmeyen vitrindir.
//
// Önbellek iddialarının verisi yoktur: ölçülen şey yanıtın içeriği değil,
// belgenin saklanıp saklanmadığıdır.
type sessizVitrin struct{}

// ListStoreProducts boş bir liste döner.
func (sessizVitrin) ListStoreProducts(
	_ context.Context,
	_ service.StoreListOptions,
) (service.ListResult[service.StoreProduct], error) {
	return service.ListResult[service.StoreProduct]{}, nil
}

// GetStoreProduct boş bir ürün döner.
func (sessizVitrin) GetStoreProduct(
	_ context.Context,
	_ string,
	_ []string,
) (service.StoreProduct, error) {
	return service.StoreProduct{}, nil
}

// sunucuyaGonder belgeyi gqlgen sunucusuna POST eder ve yanıt gövdesini döner.
func sunucuyaGonder(t *testing.T, srv http.Handler, belge string) string {
	t.Helper()

	govde, err := json.Marshal(map[string]any{"query": belge})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(govde))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	return rec.Body.String()
}

// TestGecenBelgeOnbelleklenir önbelleğin GERÇEKTEN çalıştığını doğrular.
//
// Sıra bilinçli olarak önce bu testtedir: aşağıdaki iki test "belge
// önbellekte olmamalı" der ve hiçbir şeyi saklamayan bozuk bir önbellek de
// onları geçerdi. Olumlu iddia olmadan olumsuz iddialar hiçbir şey ölçmez.
func TestGecenBelgeOnbelleklenir(t *testing.T) {
	t.Parallel()

	srv, onbellek := yeniSunucu(sessizVitrin{}, Options{})

	const belge = `{ products { count } }`

	yanit := sunucuyaGonder(t, srv, belge)
	require.NotContains(t, yanit, `"errors"`, "meşru belge geçmeli: %s", yanit)

	_, saklandi := onbellek.Get(t.Context(), belge)
	assert.True(t, saklandi, "sınırlardan geçen belge önbelleğe girmeli")
}

// TestReddedilenBelgeOnbellegeGirmez sınıra takılan belgenin yer tutmadığını
// doğrular.
//
// gqlgen belgeyi ayrıştırıp doğruladıktan HEMEN SONRA önbelleğe ekler; sınır
// eklentileri ise ondan SONRA koşar. Yani düzeltmeden önce, servise hiç
// ulaşmayan bir belge de önbellekte yer tutuyordu. Ölçüldü: 65 KB'lık 100
// reddedilmiş belge, runtime.GC sonrası 171,8 MiB kalıcı yığın — 6,5 MB'lık
// yüklemenin 26 katı.
//
// Bedeli yalnızca bellek değildi: LRU dolduğu için vitrinin GERÇEK belgeleri
// önbellekten atılıyordu, yani saldırgan tek bir kotayla herkesin sorgusunu
// yeniden ayrıştırtabiliyordu.
func TestReddedilenBelgeOnbellegeGirmez(t *testing.T) {
	t.Parallel()

	srv, onbellek := yeniSunucu(sessizVitrin{}, Options{})

	belge := `{ products(limit: 100) { items {` + icTekrarliSecim(489, "description") + `} } }`

	yanit := sunucuyaGonder(t, srv, belge)
	require.Contains(t, yanit, "FIELD_REPETITION_LIMIT_EXCEEDED", "belge reddedilmeliydi: %s", yanit)

	_, saklandi := onbellek.Get(t.Context(), belge)
	assert.False(t, saklandi, "sınıra takılan belge önbellekte yer tutmamalı")
}

// TestBuyukBelgeOnbelleklenmez sınırlardan GEÇEN ama fazla büyük olan belgenin
// saklanmadığını doğrular.
//
// İki kural birbirinin yerine geçmez: kabul kapısı "geçti mi" sorar, bayt
// sınırı "saklamaya değer mi". İkincisi olmasaydı, sınırlardan rahatça geçen
// 60 KB'lık yüz belge yine önbelleği şişirirdi — hiçbiri reddedilmeden.
//
// Aşağıdaki belge kusursuzdur: tek bir alan, tek bir argüman; büyüklüğü
// yalnızca argümanın uzunluğundan gelir.
func TestBuyukBelgeOnbelleklenmez(t *testing.T) {
	t.Parallel()

	srv, onbellek := yeniSunucu(sessizVitrin{}, Options{})

	belge := `{ product(handle: "` + strings.Repeat("x", maxOnbellekBelgeBayt) + `") { id } }`
	require.Less(t, len(belge), maxSorguBayt, "belge gövde kapısından geçecek kadar küçük olmalı")

	yanit := sunucuyaGonder(t, srv, belge)
	require.NotContains(t, yanit, `"errors"`, "belge sınırlardan geçmeliydi: %s", yanit)

	_, saklandi := onbellek.Get(t.Context(), belge)
	assert.False(t, saklandi, "bayt sınırının üstündeki belge saklanmamalı")
}

// icTekrarliSecim aynı alanı n kez takma adla seçen listeyi üretir.
//
// limits_test.go'daki eşinin kopyasıdır ve olması gerekir: o dosya paketin
// DIŞINDAN (graph_test) test eder, buradan görünmez. Alternatif, üretim
// paketine yalnızca testin kullandığı bir yardımcı koymaktı.
func icTekrarliSecim(n int, alan string) string {
	var secimler strings.Builder

	for i := range n {
		secimler.WriteString(" a" + strconv.Itoa(i) + ": " + alan)
	}

	return secimler.String()
}

// TestYanitSayaciTekParcadaTamZarfYazar hiçbir bayt gitmemişken sınıra
// çarpıldığında ne olduğunu sınar.
//
// Bugünkü taşımanın davranışı budur: gqlgen yanıtı önce belleğe kodlar ve tek
// bir Write ile yazar, yani sarmalayıcı gövdeyi istemciye hiç göndermeden
// reddedebilir. O hâlde yarım bir belge yerine TAM bir hata zarfı yazılır —
// istemci kırık bir gövde değil, sebebini söyleyen bir yanıt alır.
func TestYanitSayaciTekParcadaTamZarfYazar(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sayac := &yanitSayaci{ResponseWriter: rec, sinir: 64, kalan: 64}

	n, err := sayac.Write(bytes.Repeat([]byte("v"), 4096))

	assert.Zero(t, n, "aşan gövdenin hiçbir baytı yazılmamalı")
	require.ErrorIs(t, err, errYanitCokBuyuk)
	assert.False(t, sayac.kesildi, "hiç bayt gitmediyse bağlantı bırakılmamalı")

	govde := rec.Body.String()
	assert.NotContains(t, govde, "vvv", "kırpılmış gövde sızmamalı")

	var zarf struct {
		Errors []struct {
			Message    string         `json:"message"`
			Extensions map[string]any `json:"extensions"`
		} `json:"errors"`
	}

	require.NoError(t, json.Unmarshal([]byte(govde), &zarf), "zarf çözülebilir olmalı: %s", govde)
	require.NotEmpty(t, zarf.Errors)
	assert.Equal(t, kodYanitAsimi, zarf.Errors[0].Extensions["code"])
}

// TestYanitSayaciYarimGovdedeBaglantiyiBirakir gövdenin bir kısmı çoktan
// gitmişken sınıra çarpıldığında ne olduğunu sınar.
//
// Bu dal bugün ULAŞILMAZDIR: tek taşıma (POST) yanıtı tek Write ile yazar.
// Yine de karar burada verilmiştir ve testi vardır, çünkü
// http.ResponseWriter sözleşmesi parçalı yazmaya izin verir ve uca bir gün
// akış yapan bir taşıma (SSE, @defer) eklendiğinde bu satırlar sessizce
// devreye girecektir.
//
// Karar: yarım JSON GÖNDERİLMEZ. Kırpılmış bir gövde istemciyi ya ayrıştırma
// hatasıyla sebebini bilemeyeceği bir yere düşürür, ya da — daha kötüsü —
// kısa bir sonuç sanılır. Bağlantıyı bırakmak dürüsttür: istemci bir aktarım
// hatası görür ve olan tam olarak budur. Panik değeri http.ErrAbortHandler'dır
// çünkü stdlib'in "bu isteği sessizce bırak" sözleşmesi odur; çekirdeğin
// Recoverer'ı da onu yeniden fırlatır (bkz. corehttp).
func TestYanitSayaciYarimGovdedeBaglantiyiBirakir(t *testing.T) {
	t.Parallel()

	akan := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := w.Write([]byte(`{"data":`))
		assert.NoError(t, err, "ilk parça sınırın altında, geçmeli")

		_, err = w.Write(bytes.Repeat([]byte("v"), 4096))
		assert.ErrorIs(t, err, errYanitCokBuyuk, "aşan parça reddedilmeli")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, Path, http.NoBody)

	assert.PanicsWithValue(t, http.ErrAbortHandler, func() {
		yanitSiniri(akan, 64).ServeHTTP(rec, req)
	})

	assert.Equal(t, `{"data":`, rec.Body.String(),
		"giden parçanın üstüne ne kırpılmış gövde ne de ikinci bir zarf yazılmalı")
}

// TestYanitSayaciSinirinAltindakiniGecirir sarmalayıcının sıradan yanıta
// dokunmadığını doğrular.
//
// Her yazmayı reddeden bir sarmalayıcı da yukarıdaki iki testi geçerdi.
func TestYanitSayaciSinirinAltindakiniGecirir(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sayac := &yanitSayaci{ResponseWriter: rec, sinir: 64, kalan: 64}

	n, err := sayac.Write([]byte(`{"data":{"products":{"count":0}}}`))

	require.NoError(t, err)
	assert.Equal(t, 33, n)
	assert.Equal(t, `{"data":{"products":{"count":0}}}`, rec.Body.String())
}
