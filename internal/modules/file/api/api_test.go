package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// pngIcerik geçerli bir PNG imzası taşıyan test içeriğidir.
//
// İmzanın gerçek olması ŞART: tespit içerikten yapılır ve uydurma bir dize,
// tespit iddiasını sınanamaz kılardı.
const pngIcerik = "\x89PNG\r\n\x1a\n" + "gövde baytları"

// jsonCoz gövdeyi hedefe çözer.
func jsonCoz(ham []byte, hedef any) error { return json.Unmarshal(ham, hedef) }

// TestYuklemeIstemciDosyaAdiniYOLOLARAKKULLANMAZ görevin ilk güvenlik
// iddiasıdır.
//
// İstemci "../../etc/passwd" adını gönderir. Ad deftere GÖSTERİM verisi olarak
// girer ama depo anahtarı ve adres onunla hiç ilgilenmez: anahtarı sağlayıcı
// üretir. Yani yol geçişi "temizlenerek" değil, adın hiçbir yol ifadesine
// girmemesiyle — YAPISAL olarak — engellenir.
//
// # Kaydedilen ad neden "passwd"
//
// [mime/multipart.Part.FileName] adı RFC 7578 §4.2 gereği filepath.Base'den
// geçirir; dizin bileşenleri daha bize ulaşmadan düşer. Test bunu OLDUĞU GİBİ
// kaydeder ama iddiasını buna DAYANDIRMAZ: asıl iddialar anahtarın ve adresin
// istemci adından türemediğidir. Stdlib'in bu davranışına yaslanan bir
// tasarım, adı başka bir kanaldan (örn. bir JSON alanından) alan ilk
// değişiklikte sessizce çökerdi.
func TestYuklemeIstemciDosyaAdiniYOLOLARAKKULLANMAZ(t *testing.T) {
	t.Parallel()

	const kotuAd = "../../etc/passwd"

	svc := &sahteYuklemeler{}
	govde, tip := multipartGovde(t, "file", kotuAd, coreprovider.ContentTypePNG, pngIcerik)

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	var yanit struct {
		Data struct {
			URL          string `json:"url"`
			OriginalName string `json:"original_name"`
			ContentType  string `json:"content_type"`
			Size         int64  `json:"size"`
			ID           string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, jsonCoz(rec.Body.Bytes(), &yanit))

	assert.Equal(t, "passwd", yanit.Data.OriginalName,
		"ad GÖSTERİM için saklanır; sakınılan şey adın saklanması değil, YOL OLARAK kullanılmasıdır")
	assert.NotContains(t, yanit.Data.OriginalName, "..",
		"multipart katmanı dizin bileşenlerini zaten düşürür (RFC 7578)")
	assert.NotContains(t, yanit.Data.URL, "..", "adres istemci adından türemez")
	assert.NotContains(t, yanit.Data.URL, "passwd")
	assert.Equal(t, "/files/URETILENANAHTAR0123456789.png", yanit.Data.URL)

	// Görevin istediği yanıt alanları eksiksiz olmalı.
	assert.NotEmpty(t, yanit.Data.ID)
	assert.Equal(t, coreprovider.ContentTypePNG, yanit.Data.ContentType)
	assert.Equal(t, int64(len(pngIcerik)), yanit.Data.Size)

	assert.Equal(t, []string{"passwd"}, svc.adlar(),
		"ad servise VERİ olarak geçer; sağlayıcının sözleşmesinde ad alanı zaten yoktur")
	assert.Contains(t, kotuAd, "..", "test gerçekten yol geçişi denemesi göndermeli")
}

// TestIcerikTipiISTEMCIYESORULMAZ ikinci güvenlik iddiasıdır.
//
// İstemci "image/png" diye YALAN söyleyen bir metin dosyası gönderir. Tespit
// içerikten yapıldığı için servise giden tip "text/plain"dir ve izin listesi
// onu reddeder. İstemcinin başlığına güvenen bir liste hiçbir şey elemezdi:
// aynı numarayla bir HTML dosyası depoya girer ve sunulduğunda tarayıcıda
// çalışırdı.
func TestIcerikTipiISTEMCIYESORULMAZ(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{
		yuklemeHatasi: coreerrors.Invalid(service.CodeTypeNotAllowed,
			"%q içerik tipi kabul edilmiyor", "text/plain"),
	}
	govde, tip := multipartGovde(t, "file", "sahte.png", coreprovider.ContentTypePNG,
		"<html><script>alert(1)</script></html>")

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, service.CodeTypeNotAllowed, hataKodu(t, rec))

	require.Len(t, svc.tipler(), 1)
	assert.NotEqual(t, coreprovider.ContentTypePNG, svc.tipler()[0],
		"istemcinin başlığı bir İDDİADIR; servise geçen tip içerikten gelmeli")
	assert.True(t, strings.HasPrefix(svc.tipler()[0], "text/"),
		"tespit edilen tip: %s", svc.tipler()[0])
}

// TestSVGReddedilir SVG'nin izin listesinden geçemediğini uçtan uca gösterir.
//
// İki katman birden sınanır: tespit, SVG için hiçbir zaman "image/svg+xml"
// dönmez (DetectContentType onu XML ya da düz metin görür) ve izin listesi de
// o tipleri tanımaz. SVG bir görsel gibi görünür ama BELGEDİR: <script>
// taşıyabilir ve aynı kökenden sunulduğunda depolanmış XSS olur.
func TestSVGReddedilir(t *testing.T) {
	t.Parallel()

	const svg = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert(document.cookie)</script></svg>`

	svc := &sahteYuklemeler{
		yuklemeHatasi: coreerrors.Invalid(service.CodeTypeNotAllowed,
			"içerik tipi kabul edilmiyor"),
	}
	govde, tip := multipartGovde(t, "file", "logo.svg", "image/svg+xml", svg)

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, service.CodeTypeNotAllowed, hataKodu(t, rec))

	require.Len(t, svc.tipler(), 1)
	assert.NotEqual(t, "image/svg+xml", svc.tipler()[0],
		"DetectContentType SVG için image/svg+xml DÖNMEZ; istemcinin adı taşınmamalı")
}

// TestBoyutSinirinAsanGovdeReddedilir sınırın HTTP katmanında da zorlandığını
// doğrular.
//
// Gövdeyi saran MaxBytesReader okuma zincirini ortadan keser ve hata,
// multipart ayrıştırıcısının ya da servisin içinden sarmalanmış olarak döner;
// handler onu TİPİYLE tanır.
//
// Yanıt 422'dir, 413 değil: status kodunu handler seçmez (plan Bölüm 2.7),
// hatanın sınıfından türer ve çekirdeğin sınıf kümesinde 413'ün karşılığı
// yoktur. İstemcinin dallanacağı şey zaten makine kodudur.
func TestBoyutSinirinAsanGovdeReddedilir(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{}
	// Gövde, MaxBytesReader'ın sınırını (dosya sınırı + zarf payı) AŞMALIDIR;
	// aşmasaydı sınanan şey handler değil, sahte servisin davranışı olurdu.
	buyuk := pngIcerik + strings.Repeat("A", 16<<10)
	govde, tip := multipartGovde(t, "file", "buyuk.png", coreprovider.ContentTypePNG, buyuk)

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, service.CodeTooLarge, hataKodu(t, rec))
}

// TestIlk512BaytAkisaGERIKONUR tespit için okunan baytların kaybolmadığını
// doğrular.
//
// Handler içerik tipini bulmak için gövdenin başını okur. io.MultiReader ile
// geri konmasaydı, 512 bayttan büyük her dosya BAŞI EKSİK kaydedilirdi — ve
// bu, dosya "başarıyla yüklendi" dendikten sonra ancak görsel açılmayınca
// fark edilirdi.
func TestIlk512BaytAkisaGERIKONUR(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{}
	// 512'den UZUN bir gövde şart: sniff sınırının altında kalan bir dosyada
	// "geri koyma" adımının hiç çalışmadığı da fark edilmezdi.
	icerik := pngIcerik + strings.Repeat("B", 600)
	govde, tip := multipartGovde(t, "file", "uzun.png", coreprovider.ContentTypePNG, icerik)

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())
	require.Len(t, svc.govdeler(), 1)
	assert.Equal(t, icerik, svc.govdeler()[0],
		"servise akan baytlar, gönderilenle BİREBİR aynı olmalı")
	assert.Equal(t, coreprovider.ContentTypePNG, svc.tipler()[0],
		"tespit, geri konan baytların önündeki imzadan yapılmalı")
}

// TestBeklenmeyenAlanReddedilir tek dosya sözleşmesinin zorlandığını doğrular.
func TestBeklenmeyenAlanReddedilir(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{}
	govde, tip := multipartGovde(t, "belge", "a.png", coreprovider.ContentTypePNG, pngIcerik)

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.tipler(), "servise hiç gidilmemeli")
}

// TestJSONGovdeReddedilir ucun multipart beklediğini doğrular.
//
// Şemada da "application/json" YAZILMAZ (bkz. describe.go): yazılsaydı,
// üretilen istemci dosyayı JSON gövdesinde göndermeye çalışır ve her istek
// buradaki hataya düşerdi.
func TestJSONGovdeReddedilir(t *testing.T) {
	t.Parallel()

	rec := yukle(t, yeniRouter(&sahteYuklemeler{}),
		strings.NewReader(`{"url":"https://ornek/a.png"}`), "application/json")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestBosDosyaReddedilir sıfır baytlık yüklemenin kabul edilmediğini doğrular.
func TestBosDosyaReddedilir(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{}
	govde, tip := multipartGovde(t, "file", "bos.png", coreprovider.ContentTypePNG, "")

	rec := yukle(t, yeniRouter(svc), govde, tip)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Empty(t, svc.tipler(), "servise hiç gidilmemeli")
}

// TestSunumNOSNIFFVeSAKLANANTipiYazar görevin sunum iddiasıdır.
//
// İki başlık birlikte sınanır çünkü biri olmadan diğeri yetmez: Content-Type
// saklanan tipten yazılır, ama nosniff olmadan tarayıcı içeriğe bakıp kendi
// tahminini yapar ve "image/png" olarak saklanmış ama HTML'e benzeyen bir
// dosya HTML gibi çalıştırılabilirdi. İçeriğin bilerek HTML seçilmesinin
// sebebi budur.
func TestSunumNOSNIFFVeSAKLANANTipiYazar(t *testing.T) {
	t.Parallel()

	const htmlBenzeri = "<html><body><script>alert(1)</script></body></html>"

	svc := &sahteYuklemeler{acilan: acilanDosya(coreprovider.ContentTypePNG, htmlBenzeri)}

	rec := istek(t, yeniRouter(svc), http.MethodGet, "/files/URETILENANAHTAR0123456789.png")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, coreprovider.ContentTypePNG, rec.Header().Get("Content-Type"),
		"Content-Type SAKLANAN tipten yazılmalı")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, htmlBenzeri, rec.Body.String())
	assert.Empty(t, rec.Header().Get("Content-Disposition"),
		"istemcinin dosya adı hiçbir BAŞLIĞA yazılmamalı")
}

// TestSunumHataYanitindaDaNOSNIFFTasir başlığın HER yanıtta olduğunu doğrular.
//
// "Her yanıtta" kuralı yalnızca başarılı yanıtta uygulansaydı, 404 gövdesi
// (JSON hata zarfı) tarayıcı tahminine açık kalırdı. Ucuz ve mutlak olan bir
// kuralın istisnası olmamalıdır.
func TestSunumHataYanitindaDaNOSNIFFTasir(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{acmaHatasi: bulunamadi()}

	rec := istek(t, yeniRouter(svc), http.MethodGet, "/files/YOKYOKYOKYOKYOKYOKYOKYOK00.png")

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestSunumUcuYETKIISTEMEZ korumasız önekin bilinçli olduğunu sabitler.
//
// Vitrindeki <img> etiketi ne Authorization ne publishable anahtar
// gönderebilir; uç korumalı bir önek altına konsaydı yüklenen her görsel
// vitrinde 401 dönerdi. Test, kimliksiz bir isteğin GERÇEKTEN geçtiğini
// doğrular — bu, karar bir gün sessizce değiştirilirse patlar.
func TestSunumUcuYETKIISTEMEZ(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{acilan: acilanDosya(coreprovider.ContentTypePNG, "baytlar")}
	r := yeniRouter(svc)

	// Kimlik context'e KONMAZ: tarayıcının bir <img> isteğinde yaptığı tam
	// olarak budur.
	req := httptest.NewRequest(http.MethodGet, "/files/URETILENANAHTAR0123456789.png", http.NoBody)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code,
		"sunum ucu kimlik istemez; istese vitrin görselleri 401 dönerdi")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestYonetimUclariYETKIISTER korumanın gerçekten takılı olduğunu doğrular.
//
// Kimlik doğrulama (corehttp.RequireAdmin) router'ı kuran tarafta takılır;
// buradaki iddia YETKİ katmanıdır: yetkileri boşaltılmış bir yönetim
// kullanıcısı da geçerli bir kimliktir ve bu katman olmasaydı dosya
// yükleyebilir ve silebilirdi.
func TestYonetimUclariYETKIISTER(t *testing.T) {
	t.Parallel()

	r := yeniRouter(&sahteYuklemeler{})
	yetkisiz := corehttp.Principal{ID: "user_x", Kind: "user", Scopes: []string{}}

	uclar := map[string]struct{ metot, yol string }{
		"listeleme": {http.MethodGet, "/admin/v1/uploads"},
		"silme":     {http.MethodDelete, "/admin/v1/uploads/upl_1"},
	}

	for ad, uc := range uclar {
		t.Run(ad, func(t *testing.T) {
			req := httptest.NewRequest(uc.metot, uc.yol, http.NoBody)
			req = req.WithContext(corehttp.WithPrincipal(req.Context(), yetkisiz))

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code)
		})
	}
}

// TestSilmeIDEMPOTENTTIR ikinci silmenin de 204 döndüğünü doğrular.
//
// Servis idempotenttir ve handler onu olduğu gibi yansıtır: silme bir SON
// DURUM iddiasıdır ve yeniden denenen bir temizlik akışı ikinci turunda hata
// almamalıdır.
func TestSilmeIDEMPOTENTTIR(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{}
	r := yeniRouter(svc)

	ilk := istek(t, r, http.MethodDelete, "/admin/v1/uploads/upl_TEST")
	ikinci := istek(t, r, http.MethodDelete, "/admin/v1/uploads/upl_TEST")

	assert.Equal(t, http.StatusNoContent, ilk.Code)
	assert.Equal(t, http.StatusNoContent, ikinci.Code, "İKİNCİ silme de 204 dönmeli")
	assert.Empty(t, ilk.Body.String(), "204 gövdesizdir")
	assert.Equal(t, []string{"upl_TEST", "upl_TEST"}, svc.silinenler)
}

// TestListeZarfIcindeDoner liste yanıtının biçimini sabitler.
func TestListeZarfIcindeDoner(t *testing.T) {
	t.Parallel()

	svc := &sahteYuklemeler{}
	yuklendi, err := svc.Upload(t.Context(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(pngIcerik),
	})
	require.NoError(t, err)
	svc.kayitlar = append(svc.kayitlar, yuklendi)

	rec := istek(t, yeniRouter(svc), http.MethodGet, "/admin/v1/uploads")

	require.Equal(t, http.StatusOK, rec.Code)

	var govde struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
		Limit int64            `json:"limit"`
	}
	require.NoError(t, jsonCoz(rec.Body.Bytes(), &govde))

	require.Len(t, govde.Data, 1)
	assert.Equal(t, int64(1), govde.Count)
	assert.Equal(t, service.DefaultLimit, govde.Limit,
		"zarf, servisin UYGULADIĞI limiti bildirmeli")
	assert.NotContains(t, govde.Data[0], "storage_key",
		"depo anahtarı yayımlanmaz; istemcinin ihtiyacı olan tek şey adrestir")
	assert.Contains(t, govde.Data[0], "url")
}
