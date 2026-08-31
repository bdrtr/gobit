package api

import (
	"encoding/json"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Test DAHİLİ pakettedir çünkü anlatılan gövde ([uploadDTO]) dışa kapalıdır.
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

// islem verilen yolun belirtilen metodunu döner.
func islem(t *testing.T, yollar map[string]any, yol, metot string) map[string]any {
	t.Helper()

	kayit, ok := yollar[yol].(map[string]any)
	require.True(t, ok, "%q belgede olmalı", yol)

	op, ok := kayit[metot].(map[string]any)
	require.True(t, ok, "%q için %s anlatılmalı", yol, metot)

	return op
}

// TestYuklemeUcuMULTIPARTAnlatir şemanın en kritik iddiasıdır.
//
// Gövde "application/json" yazılsaydı, üretilen istemci dosyayı JSON gövdesinde
// göndermeye çalışır ve HER istek reddedilirdi — üstelik şemaya bakan
// geliştirici, hatanın kendi kodunda olduğunu sanardı. Yanlış bir şema burada
// eksik bir şemadan kötüdür.
func TestYuklemeUcuMULTIPARTAnlatir(t *testing.T) {
	t.Parallel()

	post := islem(t, belge(t), pathAdminUploads, "post")

	govde, ok := post["requestBody"].(map[string]any)
	require.True(t, ok, "yükleme ucu istek gövdesi anlatmalı")

	icerik, ok := govde["content"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, icerik, icerikMultipart)
	assert.NotContains(t, icerik, "application/json",
		"bu uç JSON OKUMAZ; şemada JSON yazmak doğrudan yalan olurdu")

	sema, ok := icerik[icerikMultipart].(map[string]any)
	require.True(t, ok)

	alanlar, ok := sema["schema"].(map[string]any)
	require.True(t, ok)

	ozellikler, ok := alanlar[semaOzellikler].(map[string]any)
	require.True(t, ok)
	require.Contains(t, ozellikler, fieldFile, "handler'ın okuduğu alan anlatılmalı")

	alan, ok := ozellikler[fieldFile].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, bicimIkili, alan[semaBicim],
		"ikili içerik format: binary ile anlatılır; aksi hâlde üreteç metin gönderir")
}

// TestYuklemeUcu201veGovdeAnlatir başarı yanıtının şeklini sabitler.
func TestYuklemeUcu201veGovdeAnlatir(t *testing.T) {
	t.Parallel()

	post := islem(t, belge(t), pathAdminUploads, "post")

	yanitlar, ok := post["responses"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, yanitlar, "201")

	yanit, ok := yanitlar["201"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, yanit, "content", "201 bir gövde döner ve gövdesi anlatılmalı")
}

// TestSilmeUcu204UGovdesizAnlatir 204'ün gövdesiz olduğunu sabitler.
//
// İçerik şeması yazılsaydı istemci üreteci okunacak bir gövde vaat eder ve
// üretilen metot boş yanıtı çözmeye çalışırdı.
func TestSilmeUcu204UGovdesizAnlatir(t *testing.T) {
	t.Parallel()

	del := islem(t, belge(t), pathAdminUpload, "delete")

	yanitlar, ok := del["responses"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, yanitlar, "204")

	yanit, ok := yanitlar["204"].(map[string]any)
	require.True(t, ok)
	assert.NotContains(t, yanit, "content", "204 gövdesizdir")
	assert.NotEmpty(t, yanit[semaAciklama])
}

// TestListeUcuOkunanTumParametreleriAnlatir belgedeki parametre kümesinin
// handler'ın GERÇEKTEN okuduğu kümeyle aynı olduğunu doğrular.
//
// İki taraf ayrıştığında iki ayrı sessiz arıza doğar: anlatılmayan bir
// parametre istemci üretecinde hiç görünmez, anlatılan ama okunmayan bir
// parametre ise çalışmayan bir vaattir.
func TestListeUcuOkunanTumParametreleriAnlatir(t *testing.T) {
	t.Parallel()

	get := islem(t, belge(t), pathAdminUploads, "get")

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

	assert.ElementsMatch(t, []string{queryLimit, queryOffset}, adlar,
		"belgedeki parametreler handler'ın okuduklarıyla birebir aynı olmalı")
}

// TestSunumUcuBelgeyeGIRMEZ kapsam sınırının bilinçli olduğunu sabitler.
//
// Çekirdek belgeye yalnızca /admin/v1 ve /store/v1 öneklerini alır; /files bir
// API çağrısı değil, bir <img> etiketinin hedefidir. Eksikliğin test edilmesi,
// bir gün "unutulmuş" diye eklenmesini engeller — eklenseydi, istemci üreteci
// kimliksiz çağrılan bir metot üretir ve o metot şemadaki güvenlik
// varsayılanını miras alarak yanlış anlatılırdı.
func TestSunumUcuBelgeyeGIRMEZ(t *testing.T) {
	t.Parallel()

	yollar := belge(t)

	for yol := range yollar {
		assert.NotContains(t, yol, "/files/",
			"sunum ucu belgeye girmemeli; çekirdek zaten yalnızca API öneklerini alır")
	}
}

// TestDescribeGovdeDEPOANAHTARIAnlatmaz şemanın, yayımlanmayan bir alanı vaat
// etmediğini doğrular.
//
// Anahtar ile adres AYRI şeylerdir: bugün adres anahtardan türüyor ama bir
// nesne deposunda adres imzalıdır ve anahtarla ilgisi yoktur. İkisini birden
// yayımlayan bir şema o gün sessizce yalan söylemeye başlardı.
func TestDescribeGovdeDEPOANAHTARIAnlatmaz(t *testing.T) {
	t.Parallel()

	doc := openapi.New("test", "v1")
	sema := doc.SchemaOf(uploadDTO{})
	require.NotEmpty(t, sema)

	kodlanmis, err := json.Marshal(doc.Schemas())
	require.NoError(t, err)

	metin := string(kodlanmis)
	assert.NotContains(t, metin, `"storage_key"`, "depo anahtarı yayımlanmaz")
	assert.Contains(t, metin, `"url"`, "istemcinin ihtiyacı olan alan adrestir")
	assert.Contains(t, metin, `"content_type"`)
	assert.Contains(t, metin, `"size"`)
}
