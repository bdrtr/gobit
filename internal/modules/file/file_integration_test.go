//go:build integration

// Bu dosyadaki testler gerçek bir PostgreSQL örneği (dolayısıyla Docker)
// gerektirir; `make test` hızlı kalsın diye `integration` etiketiyle
// ayrılmıştır. Çalıştırmak için: make test-integration
//
// Birim testleri servisin ve handler'ın KARARLARINI sahtelerle kanıtlar.
// Buradaki testler kararların dayandığı ZEMİNİ kanıtlar: dosyanın GERÇEKTEN
// diske yazıldığını, adresin GERÇEKTEN çalıştığını, sunulan Content-Type'ın
// veritabanındaki satırdan geldiğini, silmenin hem satırı hem dosyayı
// götürdüğünü ve depo anahtarının benzersizliğinin sahte bir haritada değil
// gerçek bir indekste durduğunu.
package file_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file"
	"github.com/bdrtr/gobit/internal/modules/file/local"
	"github.com/bdrtr/gobit/internal/modules/file/models"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

const postgresImage = "postgres:16-alpine"

// testAzamiBoyut entegrasyon testlerinde kullanılan boyut sınırıdır.
const testAzamiBoyut int64 = 1 << 20

// pngIcerik geçerli bir PNG imzası taşıyan test içeriğidir.
//
// İmza GERÇEK olmalıdır: tespit içerikten yapılır ve uydurma bir dize,
// yüklemenin izin listesinden geçmesini engellerdi.
const pngIcerik = "\x89PNG\r\n\x1a\n" + "gerçek olmayan ama imzalı gövde"

var (
	// testPool tüm testlerin paylaştığı havuzdur.
	testPool *db.Pool
	// testDSN migration çağrıları için bağlantı adresidir.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres tek bir Postgres konteyneri kaldırıp tüm testleri onun
// üzerinde çalıştırır. os.Exit defer'ları atladığı için ayrı fonksiyondadır.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "postgres konteyneri durdurulamadı: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postgres konteyneri başlatılamadı: %v\n", err)

		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı adresi alınamadı: %v\n", err)

		return 1
	}

	testPool, err = db.New(ctx, db.DefaultConfig(testDSN), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bağlantı havuzu açılamadı: %v\n", err)

		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN,
		file.New(file.Options{}).Migrations(), file.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "migration uygulanamadı: %v\n", err)

		return 1
	}

	return m.Run()
}

// kurulanModul GERÇEK veritabanı ve GERÇEK bir kök dizin üzerinde çalışan bir
// modül kurar; router'ı da bağlar.
//
// Modülün kendi Register'ı kullanılır: kurulum yolunun (sağlayıcı kaydı, kök
// dizinin yaratılması, container'a yazma) gerçekten çalıştığını ancak bu
// gösterir.
func kurulanModul(t *testing.T) (*file.Module, chi.Router, string) {
	t.Helper()

	kok := t.TempDir()

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := file.New(file.Options{
		Root:           kok,
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes: []string{
			coreprovider.ContentTypeJPEG,
			coreprovider.ContentTypePNG,
			coreprovider.ContentTypeGIF,
			coreprovider.ContentTypeWebP,
		},
	})
	require.NoError(t, mod.Register(context.Background(), c))

	r := chi.NewRouter()
	mod.Routes(r)

	return mod, r, kok
}

// yonetici testlerin varsayılan kimliğidir: tam yetkili bir yönetim
// kullanıcısı.
func yonetici() corehttp.Principal {
	return corehttp.Principal{ID: "user_entegrasyon", Kind: "user", Scopes: []string{corehttp.ScopeAdmin}}
}

// yukle multipart bir yükleme isteği yapar.
func yukle(t *testing.T, r chi.Router, dosyaAdi, bildirilenTip, icerik string) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	yazici := multipart.NewWriter(&buf)

	basliklar := make(textproto.MIMEHeader)
	basliklar.Set("Content-Disposition", `form-data; name="file"; filename="`+dosyaAdi+`"`)
	basliklar.Set("Content-Type", bildirilenTip)

	parca, err := yazici.CreatePart(basliklar)
	require.NoError(t, err)
	_, err = parca.Write([]byte(icerik))
	require.NoError(t, err)
	require.NoError(t, yazici.Close())

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/uploads", &buf)
	req.Header.Set("Content-Type", yazici.FormDataContentType())
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), yonetici()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// yuklemeYaniti başarılı bir yükleme yanıtının alanlarıdır.
type yuklemeYaniti struct {
	Data struct {
		ID          string `json:"id"`
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		Size        int64  `json:"size"`
		Checksum    string `json:"checksum"`
		ProviderID  string `json:"provider_id"`
	} `json:"data"`
}

// yuklemeyiCoz yanıt gövdesini çözer.
func yuklemeyiCoz(t *testing.T, rec *httptest.ResponseRecorder) yuklemeYaniti {
	t.Helper()

	var yanit yuklemeYaniti
	require.NoError(t, jsonCoz(rec.Body.Bytes(), &yanit), "gövde: %s", rec.Body.String())

	return yanit
}

// TestYuklemeSunmaSilmeUCTANUCA gerçek bir tüketici yolunu baştan sona
// yürütür.
//
// Zincir tam olarak ürün görselinin izleyeceği yoldur: yükle → dönen adresi
// bir <img> gibi çağır → sil. Her adım GERÇEK bileşenlerle çalışır; sahte
// hiçbir şey yoktur.
func TestYuklemeSunmaSilmeUCTANUCA(t *testing.T) {
	_, r, kok := kurulanModul(t)

	// 1) Yükleme.
	rec := yukle(t, r, "urun-onden.png", coreprovider.ContentTypePNG, pngIcerik)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	yanit := yuklemeyiCoz(t, rec)
	assert.Equal(t, coreprovider.ContentTypePNG, yanit.Data.ContentType)
	assert.Equal(t, int64(len(pngIcerik)), yanit.Data.Size)
	assert.Equal(t, local.ID, yanit.Data.ProviderID)
	assert.NotEmpty(t, yanit.Data.Checksum)

	// Dosya GERÇEKTEN diskte ve kök dizinin İÇİNDE olmalı.
	anahtar := filepath.Base(yanit.Data.URL)
	diskYolu := filepath.Join(kok, anahtar)
	diskteki, err := os.ReadFile(diskYolu)
	require.NoError(t, err, "yüklenen dosya kök dizinde olmalı")
	assert.Equal(t, pngIcerik, string(diskteki))

	// 2) Sunma — adres GERÇEKTEN çalışmalı ve kimlik İSTEMEMELİ.
	sunum := httptest.NewRecorder()
	r.ServeHTTP(sunum, httptest.NewRequest(http.MethodGet, yanit.Data.URL, http.NoBody))

	require.Equal(t, http.StatusOK, sunum.Code, "yüklemenin ürettiği adres çalışmalı")
	assert.Equal(t, coreprovider.ContentTypePNG, sunum.Header().Get("Content-Type"),
		"Content-Type SAKLANAN tipten yazılmalı")
	assert.Equal(t, "nosniff", sunum.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, pngIcerik, sunum.Body.String())

	// 3) Silme — hem satır hem dosya gitmeli.
	sil := silIstegi(t, r, yanit.Data.ID)
	require.Equal(t, http.StatusNoContent, sil.Code)

	_, err = os.Stat(diskYolu)
	assert.True(t, os.IsNotExist(err), "silme dosyayı da götürmeli: %v", err)

	sonrasi := httptest.NewRecorder()
	r.ServeHTTP(sonrasi, httptest.NewRequest(http.MethodGet, yanit.Data.URL, http.NoBody))
	assert.Equal(t, http.StatusNotFound, sonrasi.Code, "silinmiş dosya artık sunulmamalı")
	assert.Equal(t, "nosniff", sonrasi.Header().Get("X-Content-Type-Options"),
		"nosniff HER yanıtta, hata yanıtında da bulunmalı")
}

// TestSilmeIDEMPOTENTTIR ikinci silmenin gerçek veritabanında da 204
// döndüğünü doğrular.
func TestSilmeIDEMPOTENTTIR(t *testing.T) {
	_, r, _ := kurulanModul(t)

	rec := yukle(t, r, "a.png", coreprovider.ContentTypePNG, pngIcerik)
	require.Equal(t, http.StatusCreated, rec.Code)
	id := yuklemeyiCoz(t, rec).Data.ID

	assert.Equal(t, http.StatusNoContent, silIstegi(t, r, id).Code, "ilk silme")
	assert.Equal(t, http.StatusNoContent, silIstegi(t, r, id).Code, "İKİNCİ silme")
	assert.Equal(t, http.StatusNoContent, silIstegi(t, r, "upl_HICVAROLMADI").Code,
		"hiç var olmamış kimlik de son durumu sağlar")
}

// TestYalanIcerikTipiREDDEDILIRveDiskeYAZILMAZ istemcinin iddiasının
// denetlenmediği bir kurulumun neye benzeyeceğini gösterir.
//
// Gönderilen şey "image/png" başlıklı bir HTML dosyasıdır. Ret, GERÇEK disk
// üzerinde sınanır: kök dizin BOŞ kalmalıdır — yani denetim yazmadan önce
// yapılmalıdır. Sonra yapılsaydı, reddedilen her dosya için bir silme çağrısı
// gerekir ve o silme başarısız olduğunda dosya depoda kalırdı.
func TestYalanIcerikTipiREDDEDILIRveDiskeYAZILMAZ(t *testing.T) {
	_, r, kok := kurulanModul(t)

	rec := yukle(t, r, "sahte.png", coreprovider.ContentTypePNG,
		"<html><body><script>alert(document.cookie)</script></body></html>")

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Empty(t, kokIcerigi(t, kok), "reddedilen dosya diske HİÇ yazılmamalı")
}

// TestSVGREDDEDILIRveDiskeYAZILMAZ SVG'nin gerçek akışta da geçemediğini
// doğrular.
//
// SVG bir görsel gibi görünür ama BELGEDİR: <script> taşıyabilir ve aynı
// kökenden sunulduğunda depolanmış XSS olur — yükleyen kullanıcı, görseli
// açan herkesin oturumunda kod çalıştırır.
func TestSVGREDDEDILIRveDiskeYAZILMAZ(t *testing.T) {
	_, r, kok := kurulanModul(t)

	const svg = `<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg">` +
		`<script>alert(document.cookie)</script></svg>`

	rec := yukle(t, r, "logo.svg", "image/svg+xml", svg)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Empty(t, kokIcerigi(t, kok))
}

// TestYolGecisiDENEMESIKOKDISINAYAZMAZ istemci dosya adının hiçbir aşamada yol
// olmadığını GERÇEK dosya sistemi üzerinde doğrular.
//
// İddia iki yönlüdür: kök dizinin dışında hiçbir şey oluşmaz VE kök dizinde
// oluşan tek şey üretilmiş anahtarlı bir dosyadır.
func TestYolGecisiDENEMESIKOKDISINAYAZMAZ(t *testing.T) {
	_, r, kok := kurulanModul(t)

	ust := filepath.Dir(kok)
	oncekiUst := dizinIcerigi(t, ust)

	rec := yukle(t, r, "../../etc/passwd", coreprovider.ContentTypePNG, pngIcerik)
	require.Equal(t, http.StatusCreated, rec.Code, "gövde: %s", rec.Body.String())

	icerik := kokIcerigi(t, kok)
	require.Len(t, icerik, 1, "kök dizinde tam olarak bir dosya olmalı")
	assert.NotContains(t, icerik[0], "passwd", "anahtar istemci adından türemez")
	assert.Equal(t, filepath.Base(icerik[0]), icerik[0], "anahtar bir YOL değil")

	assert.ElementsMatch(t, oncekiUst, dizinIcerigi(t, ust),
		"kök dizinin DIŞINDA hiçbir şey oluşmamalı")
}

// TestBoyutSinirinAsanGovdeReddedilir sınırın gerçek akışta zorlandığını ve
// diske iz bırakmadığını doğrular.
//
// Yarım nesne bırakılsaydı, sınırı aşan istekler REDDEDİLDİKLERİ hâlde diski
// doldurabilirdi: o dosyaya hiçbir kayıt işaret etmez ve hiçbir silme yolu
// anahtarını bilmez.
func TestBoyutSinirinAsanGovdeReddedilir(t *testing.T) {
	kok := t.TempDir()

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	// Sınır bilerek küçük tutulur; 1 MiB'lık bir gövde üretmek testi
	// yavaşlatırdı.
	mod := file.New(file.Options{
		Root:           kok,
		MaxUploadBytes: 64,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, mod.Register(context.Background(), c))

	r := chi.NewRouter()
	mod.Routes(r)

	buyuk := pngIcerik + string(bytes.Repeat([]byte("A"), 256))
	rec := yukle(t, r, "buyuk.png", coreprovider.ContentTypePNG, buyuk)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "gövde: %s", rec.Body.String())
	assert.Equal(t, service.CodeTooLarge, hataKodu(t, rec))
	assert.Empty(t, kokIcerigi(t, kok),
		"sınırı aşan gövde ne yarım dosya ne geçici dosya bırakmalı")
}

// TestDepoAnahtariBENZERSIZ kısıtın sahte bir haritada değil GERÇEK indekste
// durduğunu doğrular.
//
// Benzersizlik iki şeyi birden korur: silme başka bir kaydın dosyasını
// götüremez ve sunum yolu anahtardan kayda TEK bir satırla ulaşır — sunulan
// Content-Type o satırdan yazıldığı için "hangi satır" sorusunun tek bir
// cevabı olmalıdır.
func TestDepoAnahtariBENZERSIZ(t *testing.T) {
	_, r, _ := kurulanModul(t)

	rec := yukle(t, r, "a.png", coreprovider.ContentTypePNG, pngIcerik)
	require.Equal(t, http.StatusCreated, rec.Code)
	anahtar := filepath.Base(yuklemeyiCoz(t, rec).Data.URL)

	// Aynı anahtarla ikinci bir kayıt açmak, servisi atlayıp doğrudan depoya
	// yazmayı gerektirir: anahtarı sağlayıcı ürettiği için normal akışta
	// çakışma oluşamaz. Sınanan şey de zaten SON SAVUNMA hattıdır.
	_, err := testPool.Pool().Exec(context.Background(),
		`INSERT INTO file_uploads (id, storage_key, provider_id, content_type, size, checksum, url)
		 VALUES ($1, $2, 'local', 'image/png', 1, 'x', '/files/x')`,
		models.NewUploadID(time.Now()), anahtar)

	require.Error(t, err, "aynı depo anahtarıyla ikinci kayıt açılamamalı")
}

// TestModulVarsayilanSaglayiciylaKaydolur kutudan çıkan kurulumun eksiksiz
// olduğunu doğrular: hiçbir eklenti yokken de bir sağlayıcı vardır ve seçili
// kimlik onu bulur.
func TestModulVarsayilanSaglayiciylaKaydolur(t *testing.T) {
	mod, _, kok := kurulanModul(t)

	assert.Equal(t, []string{local.ID}, mod.Providers().IDs())
	assert.Equal(t, file.DefaultProviderID, mod.Service().ProviderID())

	bilgi, err := os.Stat(kok)
	require.NoError(t, err, "kök dizin Register sırasında hazırlanmalı")
	assert.True(t, bilgi.IsDir())
}

// TestKOKVERILMEZSEYerelSaglayiciKAYDEDILMEZ kök dizini olmayan bir kurulumun
// GEÇİCİ DİZİNE düşmediğini doğrular.
//
// Düşseydi kurulum "çalışıyor" görünür, ilk yeniden başlatmada tüm görseller
// kaybolur ve ürün kayıtlarındaki adresler yerinde kalırdı. Bunun yerine
// sağlayıcı hiç kaydedilmez; seçili sağlayıcı oysa açılış kompozisyon
// kökünde durur (bkz. cmd/server dosyaSaglayicisiniDogrula).
func TestKOKVERILMEZSEYerelSaglayiciKAYDEDILMEZ(t *testing.T) {
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))

	mod := file.New(file.Options{
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, mod.Register(context.Background(), c),
		"kök verilmemesi kurulum HATASI değildir; dosya yüklemeyen kurulum meşrudur")

	assert.Empty(t, mod.Providers().IDs(), "geçici dizinle bir sağlayıcı UYDURULMAMALI")

	_, err := mod.Providers().Get(local.ID)
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "hata: %v", err)
}

// TestListeYuklemeleriDoner defterin yönetim listesinde göründüğünü doğrular.
func TestListeYuklemeleriDoner(t *testing.T) {
	_, r, _ := kurulanModul(t)

	require.Equal(t, http.StatusCreated,
		yukle(t, r, "a.png", coreprovider.ContentTypePNG, pngIcerik).Code)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/uploads", http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), yonetici()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var govde struct {
		Data  []map[string]any `json:"data"`
		Count int64            `json:"count"`
	}
	require.NoError(t, jsonCoz(rec.Body.Bytes(), &govde))

	assert.GreaterOrEqual(t, govde.Count, int64(1))
	require.NotEmpty(t, govde.Data)
	assert.Equal(t, "user_entegrasyon", govde.Data[0]["uploaded_by"],
		"yükleyenin kimliği kayda yazılmalı")
}

// silIstegi kimlikli bir silme isteği yapar.
func silIstegi(t *testing.T, r chi.Router, id string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodDelete, "/admin/v1/uploads/"+id, http.NoBody)
	req = req.WithContext(corehttp.WithPrincipal(req.Context(), yonetici()))

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	return rec
}

// kokIcerigi kök dizindeki dosya adlarını döner.
func kokIcerigi(t *testing.T, kok string) []string {
	t.Helper()

	return dizinIcerigi(t, kok)
}

// dizinIcerigi verilen dizindeki girdi adlarını döner.
func dizinIcerigi(t *testing.T, dizin string) []string {
	t.Helper()

	girdiler, err := os.ReadDir(dizin)
	require.NoError(t, err)

	adlar := make([]string, 0, len(girdiler))
	for _, g := range girdiler {
		adlar = append(adlar, g.Name())
	}

	return adlar
}

// jsonCoz gövdeyi hedefe çözer.
func jsonCoz(ham []byte, hedef any) error { return json.Unmarshal(ham, hedef) }

// hataKodu yanıt gövdesindeki makine kodunu döner.
func hataKodu(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var govde struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, jsonCoz(rec.Body.Bytes(), &govde), "gövde: %s", rec.Body.String())

	return govde.Error.Code
}
