package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Test DAHİLİ pakettedir çünkü anlatılan gövde ([deliveryDTO]) dışa kapalıdır.
// Dışarıdan sınamanın tek yolu tipi dışa açmak olurdu; belgeyi sınamak uğruna
// modülün yüzeyini genişletmek, sınanan şeyin kendisini bozardı.

// belge Describe'ın çıktısını GERÇEK route ağacına karşı üretip JSON'dan geri
// okunmuş hâlini döner.
//
// Router da gerçek olmalıdır: açıklama ile route'un yolu ayrışırsa hata BURADA
// görünsün, üretimde /openapi.json'a bakan birinde değil.
func belge(t *testing.T) map[string]any {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	ham, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"anlatılan her uç bir route ile eşleşmeli; eşleşmeyen kayıt belgeye hiç girmez")

	kodlanmis, err := json.Marshal(ham)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(kodlanmis, &cozulmus))

	yollar, ok := cozulmus["paths"].(map[string]any)
	require.True(t, ok, "belgede paths olmalı")

	return yollar
}

// islem verilen yolun GET işlemini döner.
func islem(t *testing.T, yollar map[string]any, yol string) map[string]any {
	t.Helper()

	kayit, ok := yollar[yol].(map[string]any)
	require.True(t, ok, "%q belgede olmalı", yol)

	get, ok := kayit["get"].(map[string]any)
	require.True(t, ok, "%q için GET anlatılmalı", yol)

	return get
}

// TestDescribeTeslimGunluguUcunuAnlatir ucun belgede göründüğünü doğrular.
//
// Anlatılmayan bir uç istemci üretecinde HİÇ görünmez: şema router'dan
// üretildiği için yol ve metot yine yazılır, ama gövdesi olmayan bir işlem
// istemciye "yanıtın şekli bilinmiyor" der.
func TestDescribeTeslimGunluguUcunuAnlatir(t *testing.T) {
	get := islem(t, belge(t), pathAdminDeliveries)

	assert.NotEmpty(t, get["summary"])
	yanitlar, ok := get["responses"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, yanitlar, "200")
}

// TestDescribeOkunanTumParametreleriAnlatir belgedeki parametre kümesinin
// handler'ın GERÇEKTEN okuduğu kümeyle aynı olduğunu doğrular.
//
// İki taraf ayrıştığında iki ayrı sessiz arıza doğar: anlatılmayan bir süzgeç
// istemci üretecinde hiç görünmez (kimse kullanamaz), anlatılan ama okunmayan
// bir parametre ise çalışmayan bir vaattir — istemci onu gönderir ve süzgeçsiz
// bir liste alır.
func TestDescribeOkunanTumParametreleriAnlatir(t *testing.T) {
	get := islem(t, belge(t), pathAdminDeliveries)

	ham, ok := get["parameters"].([]any)
	require.True(t, ok, "uç sorgu parametresi anlatmalı")

	adlar := make([]string, 0, len(ham))
	for _, p := range ham {
		param, castOK := p.(map[string]any)
		require.True(t, castOK)

		ad, adOK := param["name"].(string)
		require.True(t, adOK, "parametre adı dize olmalı: %#v", param)
		adlar = append(adlar, ad)
	}

	assert.ElementsMatch(t,
		[]string{queryReference, queryStatus, queryLimit, queryOffset}, adlar,
		"belgedeki parametreler handler'ın okuduklarıyla birebir aynı olmalı")
}

// TestDescribeGovdeAliciAdresiAnlatmaz şemanın, kayıtta bulunmayan bir alanı
// vaat etmediğini doğrular.
func TestDescribeGovdeAliciAdresiAnlatmaz(t *testing.T) {
	doc := openapi.New("test", "v1")
	sema := doc.SchemaOf(deliveryDTO{})

	kodlanmis, err := json.Marshal(doc.Schemas())
	require.NoError(t, err)
	require.NotEmpty(t, sema)

	metin := string(kodlanmis)
	assert.NotContains(t, metin, `"to"`, "şema alıcı adresi vaat etmemeli")
	assert.Contains(t, metin, `"reference"`, "kaydı siparişe bağlayan alan anlatılmalı")
}

// TestRouteYolununMetoduYalnizcaOKUMADIR modülün yazma ucu açmadığını
// doğrular.
//
// Bir "bildirim gönder" ucu, aynı işi ikinci bir yoldan yapılır kılar ve
// idempotency anahtarını dışarıdan seçilebilir hâle getirirdi.
func TestRouteYolununMetoduYalnizcaOKUMADIR(t *testing.T) {
	r := chi.NewRouter()
	New(nil).Routes(r)

	metotlar := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		metotlar[method+" "+route] = true

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, map[string]bool{http.MethodGet + " " + pathAdminDeliveries: true}, metotlar)
}
