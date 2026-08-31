package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// testAzamiBoyut birim testlerde kullanılan boyut sınırıdır; küçük olması
// sınırı aşan gövdeyi tek satırda kurulabilir kılar.
const testAzamiBoyut int64 = 32

// yeniServis sahte depo ve sahte sağlayıcı üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T, depo *sahteDepo, prov coreprovider.FileProvider) *service.Service {
	t.Helper()

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(prov))

	svc, err := service.New(service.Options{
		Store:          depo,
		Providers:      kayit,
		ProviderID:     prov.ID(),
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes: []string{
			coreprovider.ContentTypePNG,
			coreprovider.ContentTypeJPEG,
		},
	})
	require.NoError(t, err)

	return svc
}

// TestYuklemeKaydiTespitEDILENTipiSaklar defterin ne yazdığını sabitler.
func TestYuklemeKaydiTespitEDILENTipiSaklar(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	prov := &sahteSaglayici{id: "sahte"}
	svc := yeniServis(t, depo, prov)

	kayit, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType:  coreprovider.ContentTypePNG,
		Body:         strings.NewReader("gövde"),
		OriginalName: "urun.png",
		UploadedBy:   "user_1",
	})

	require.NoError(t, err)
	assert.Equal(t, coreprovider.ContentTypePNG, kayit.ContentType)
	assert.Equal(t, "sahte", kayit.ProviderID)
	assert.Equal(t, "urun.png", kayit.OriginalName)
	assert.Equal(t, "user_1", kayit.UploadedBy)
	assert.NotEmpty(t, kayit.StorageKey)
	assert.NotEmpty(t, kayit.URL)

	ozet := sha256.Sum256([]byte("gövde"))
	assert.Equal(t, hex.EncodeToString(ozet[:]), kayit.Checksum,
		"özet, sağlayıcıya AKAN baytlar üzerinden hesaplanmalı")
	assert.Equal(t, int64(len("gövde")), kayit.Size)
	assert.Equal(t, []string{"gövde"}, prov.yuklenen, "gövde sağlayıcıya eksiksiz ulaşmalı")
}

// TestIzinListesiDISINDAKITipReddedilir yasak listesi yerine izin listesi
// kullanıldığını doğrular.
//
// Asıl iddia ret DEĞİL, retin ne zaman olduğudur: sağlayıcıya HİÇ
// gidilmemelidir. Denetim yazmadan sonra yapılsaydı, reddedilen her dosya için
// bir silme çağrısı gerekir ve o silme başarısız olduğunda dosya depoda
// kalırdı.
func TestIzinListesiDISINDAKITipReddedilir(t *testing.T) {
	t.Parallel()

	tipler := map[string]string{
		// DetectContentType bir SVG için "text/xml" ya da "text/plain" döner;
		// yine de her iki ad da sınanır — izin listesi ikisini de tanımaz ve
		// SVG'nin geçebileceği hiçbir yol kalmamalıdır.
		"SVG (bildirilen ad)": "image/svg+xml",
		"SVG (tespit edilen)": "text/xml",
		"metin":               "text/plain",
		"HTML":                "text/html",
		"bilinmeyen ikili":    "application/octet-stream",
		"izin listesinde yok": coreprovider.ContentTypeGIF,
		"tespit edilemedi":    "",
		"parametreli png":     "image/png; charset=utf-8",
		"büyük harfli png":    "IMAGE/PNG",
	}

	for ad, tip := range tipler {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			depo := yeniSahteDepo()
			prov := &sahteSaglayici{}
			svc := yeniServis(t, depo, prov)

			_, err := svc.Upload(context.Background(), service.UploadInput{
				ContentType: tip,
				Body:        strings.NewReader("içerik"),
			})

			require.Error(t, err, "%q kabul edilmemeli", tip)
			assert.True(t, coreerrors.IsInvalid(err), "hata: %v", err)
			assert.Empty(t, prov.yuklenen, "sağlayıcıya HİÇ gidilmemeli")
			assert.Zero(t, depo.sayi(), "deftere kayıt yazılmamalı")
		})
	}
}

// TestSVGReddiKodlaTeshisEdilebilir reddin makine tarafından okunabilir
// olduğunu doğrular.
//
// İstemcinin görebileceği tek yol budur: status kodu 422'dir ve 422 pek çok
// sebepten gelir; "hangi biçimler kabul ediliyor" sorusunun cevabı koda ve
// mesaja yazılmalıdır.
func TestSVGReddiKodlaTeshisEdilebilir(t *testing.T) {
	t.Parallel()

	svc := yeniServis(t, yeniSahteDepo(), &sahteSaglayici{})

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: "image/svg+xml",
		Body:        strings.NewReader("<svg onload=\"alert(1)\"/>"),
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeTypeNotAllowed, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "image/svg+xml", "reddedilen tip yazılmalı")
	assert.Contains(t, err.Error(), coreprovider.ContentTypePNG, "kabul edilenler yazılmalı")
}

// TestBoyutSinirinAsanGovdeReddedilir sınırın gerçekten zorlandığını doğrular.
//
// Sınır AKIŞ ÜZERİNDE uygulanır: gövdenin uzunluğu önceden bilinmez (chunked
// istekte Content-Length yoktur) ve sayılmadan reddedilemez.
func TestBoyutSinirinAsanGovdeReddedilir(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	prov := &sahteSaglayici{}
	svc := yeniServis(t, depo, prov)

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(strings.Repeat("A", int(testAzamiBoyut)+1)),
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "hata: %v", err)
	assert.Equal(t, service.CodeTooLarge, coreerrors.CodeOf(err))
	assert.Zero(t, depo.sayi(), "sınırı aşan yükleme deftere GİRMEMELİ")
}

// TestTamSinirdakiGovdeKabulEdilir sınırın "aşınca" reddettiğini, "gelince"
// değil, doğrular.
//
// Sayaç bir eksik başlatılsaydı tam sınırdaki her dosya reddedilir ve sınır
// belgede yazandan bir bayt küçük olurdu — kimsenin fark etmeyeceği, ama
// belgeyi yalancı çıkaran bir kayma.
func TestTamSinirdakiGovdeKabulEdilir(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	svc := yeniServis(t, depo, &sahteSaglayici{})

	kayit, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(strings.Repeat("A", int(testAzamiBoyut))),
	})

	require.NoError(t, err)
	assert.Equal(t, testAzamiBoyut, kayit.Size)
}

// TestSinirAsimindaGOVDEKESILMEZ sınırın sessizce kırpmadığını doğrular.
//
// io.LimitReader kullanılsaydı sınıra gelindiğinde io.EOF dönerdi: sağlayıcı
// bunu "dosya bitti" diye okur ve YARIM bir görsel başarıyla kaydedilirdi.
// Yani sınırı aşan istek reddedilmek yerine bozuk veri üretirdi. İddia,
// sağlayıcının bir dosya kaydetmemiş olmasıdır.
func TestSinirAsimindaGOVDEKESILMEZ(t *testing.T) {
	t.Parallel()

	prov := &sahteSaglayici{}
	svc := yeniServis(t, yeniSahteDepo(), prov)

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(strings.Repeat("A", int(testAzamiBoyut)*4)),
	})

	require.Error(t, err)
	assert.Empty(t, prov.yuklenen, "kesilmiş bir gövde BAŞARIYLA kaydedilmemeli")
}

// TestUzunDosyaAdiReddedilirVeKIRPILMAZ adın sessizce değiştirilmediğini
// doğrular.
//
// Kırpmak, istemcinin gönderdiği veriyi haber vermeden değiştirmek olurdu;
// alanın tek işi zaten "kullanıcı ne gördüyse onu göstermek".
func TestUzunDosyaAdiReddedilirVeKIRPILMAZ(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	svc := yeniServis(t, depo, &sahteSaglayici{})

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType:  coreprovider.ContentTypePNG,
		Body:         strings.NewReader("g"),
		OriginalName: strings.Repeat("a", 256),
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "hata: %v", err)
	assert.Zero(t, depo.sayi())
}

// TestYolBenzeriDosyaAdiSAGLAYICIYAGITMEZ istemci adının depo anahtarını
// etkilemediğini doğrular.
//
// İddia yapısaldır: çekirdek sözleşmesindeki UploadInput'ta dosya adı ALANI
// YOKTUR, dolayısıyla adın sağlayıcıya ulaşabileceği bir kanal da yoktur.
// Test bunu gözlemlenebilir kılar — ad deftere yazılır ama anahtarda ve
// adreste hiç görünmez.
func TestYolBenzeriDosyaAdiSAGLAYICIYAGITMEZ(t *testing.T) {
	t.Parallel()

	const kotuAd = "../../etc/passwd"

	depo := yeniSahteDepo()
	prov := &sahteSaglayici{}
	svc := yeniServis(t, depo, prov)

	kayit, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType:  coreprovider.ContentTypePNG,
		Body:         strings.NewReader("gövde"),
		OriginalName: kotuAd,
	})

	require.NoError(t, err, "ad bir yol olmadığı için yükleme REDDEDİLMEZ, ad sadece veridir")
	assert.Equal(t, kotuAd, kayit.OriginalName, "ad gösterim için AYNEN saklanır")
	assert.NotContains(t, kayit.StorageKey, "..", "anahtar istemci adından TÜREMEZ")
	assert.NotContains(t, kayit.StorageKey, "/")
	assert.NotContains(t, kayit.URL, "passwd")
}

// TestKayitYazilamazsaDosyaTEMIZLENIR erişilemez nesne bırakılmadığını
// doğrular.
//
// Dosya yazıldıktan sonra kayıt patlarsa, geride anahtarını kimsenin bilmediği
// bir nesne kalır: hiçbir listede görünmez, hiçbir silme ucu ona ulaşamaz ve
// sonsuza kadar yer kaplar.
func TestKayitYazilamazsaDosyaTEMIZLENIR(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	depo.yazmaHatasi = coreerrors.Unavailable("db_down", "veritabanına ulaşılamadı")
	prov := &sahteSaglayici{}
	svc := yeniServis(t, depo, prov)

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("gövde"),
	})

	require.Error(t, err)
	assert.Len(t, prov.silinenler(), 1, "yazılan dosya geri alınmalı")
	assert.Equal(t, prov.yuklenen[0], "gövde")
}

// TestBosGovdeReddedilir sıfır baytlık yüklemenin kabul edilmediğini
// doğrular; gövdesi olmayan bir dosyanın ne tipi tespit edilebilir ne de
// sunulacak içeriği vardır.
func TestBosGovdeReddedilir(t *testing.T) {
	t.Parallel()

	svc := yeniServis(t, yeniSahteDepo(), &sahteSaglayici{})

	_, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        nil,
	})

	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "hata: %v", err)
}

// TestBosIzinListesiyleServisKURULAMAZ en tehlikeli varsayılanın kapalı
// olduğunu doğrular.
//
// "Liste boşsa her şeyi kabul et" yazılsaydı, yapılandırmadaki tek bir yazım
// hatası denetimi sessizce KALDIRIRDI.
func TestBosIzinListesiyleServisKURULAMAZ(t *testing.T) {
	t.Parallel()

	_, err := service.New(service.Options{
		Store:          yeniSahteDepo(),
		Providers:      service.NewProviderRegistry(),
		ProviderID:     "sahte",
		MaxUploadBytes: testAzamiBoyut,
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeNotReady, coreerrors.CodeOf(err))
}

// TestIzinListesiKOPYALANIR çağıranın dilimini değiştirmesinin listeyi
// genişletemediğini doğrular.
//
// Dilim olduğu gibi tutulsaydı, config'ten gelen değeri elinde tutan bir kod
// çalışırken izin listesini büyütebilirdi — denetimin dışarıdan
// değiştirilebilir olması, denetimin olmaması demektir.
func TestIzinListesiKOPYALANIR(t *testing.T) {
	t.Parallel()

	tipler := []string{coreprovider.ContentTypePNG}

	svc, err := service.New(service.Options{
		Store:          yeniSahteDepo(),
		Providers:      service.NewProviderRegistry(),
		ProviderID:     "sahte",
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes:   tipler,
	})
	require.NoError(t, err)

	tipler[0] = "text/html"

	assert.Equal(t, []string{coreprovider.ContentTypePNG}, svc.AllowedTypes())
}
