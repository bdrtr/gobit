package local_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/local"
)

// pngIcerik geçerli bir PNG imzası taşıyan test içeriğidir.
//
// Gerçek bir imza kullanılır çünkü sağlayıcı anahtarın uzantısını içerik
// tipinden türetir; uydurma bir dize testin uzantı iddiasını anlamsız
// kılardı.
var pngIcerik = append([]byte("\x89PNG\r\n\x1a\n"), []byte("gövde")...)

// yeniSaglayici geçici bir kök dizin üzerinde çalışan sağlayıcı üretir.
//
// [testing.T.TempDir] her test için ayrı bir dizin verir ve testten sonra
// siler; testler birbirinin dosyalarını görmez.
func yeniSaglayici(t *testing.T) (prov *local.Provider, kok string) {
	t.Helper()

	kok = t.TempDir()

	prov, err := local.New(local.Options{Root: kok})
	require.NoError(t, err)

	return prov, kok
}

// kokIcerigi kök dizindeki dosya adlarını döner.
func kokIcerigi(t *testing.T, kok string) []string {
	t.Helper()

	girdiler, err := os.ReadDir(kok)
	require.NoError(t, err)

	adlar := make([]string, 0, len(girdiler))
	for _, g := range girdiler {
		adlar = append(adlar, g.Name())
	}

	return adlar
}

// TestBosKokReddedilirVeGeciciDizineDUSULMEZ kurulumun sessizce geçici dizine
// kaymadığını doğrular.
//
// Geçici dizin, "hiçbir şey yapılandırmadan çalışsın" isteğinin en cazip
// cevabıdır ve tam da bu yüzden test edilir: yazılsaydı, yeniden başlatmada
// tüm görseller kaybolur, ürün kayıtlarındaki adresler yerinde kalır ve
// hiçbir hata görünmezdi.
func TestBosKokReddedilirVeGeciciDizineDUSULMEZ(t *testing.T) {
	t.Parallel()

	prov, err := local.New(local.Options{})

	require.Error(t, err, "kök dizin olmadan sağlayıcı kurulamamalı")
	assert.Nil(t, prov)
	assert.Equal(t, local.CodeNotReady, coreerrors.CodeOf(err))
	assert.NotContains(t, err.Error(), os.TempDir(),
		"geçici dizin bir alternatif olarak bile önerilmemeli")
}

// TestKokDizinAcilistaYaratilir yazılabilir bir kökün kurulum anında
// hazırlandığını doğrular.
//
// İlk yüklemeye ertelenseydi, yanlış yazılmış bir yol ancak müşteri bir
// dosya yüklemeye çalıştığında ortaya çıkardı — oysa o an düzeltilebilecek
// tek şey açılış yapılandırmasıdır.
func TestKokDizinAcilistaYaratilir(t *testing.T) {
	t.Parallel()

	kok := filepath.Join(t.TempDir(), "henuz", "yok")

	prov, err := local.New(local.Options{Root: kok})

	require.NoError(t, err)
	assert.Equal(t, kok, prov.Root())

	bilgi, statErr := os.Stat(kok)
	require.NoError(t, statErr, "kök dizin kurulumda yaratılmalı")
	assert.True(t, bilgi.IsDir())
}

// TestUretilenAnahtarYOLICERMEZ depo anahtarının tek bir dosya adı olduğunu
// doğrular.
//
// İddia, yol geçişine karşı YAPISAL korumanın kendisidir: anahtar tek
// düzlemde bir addır, yol ayıracı taşımaz ve kökle birleştiğinde kökün
// altından çıkamaz. Girdide istemci dosya adı diye bir alan zaten yoktur —
// yani "temizlenmesi" gereken bir değer de yoktur.
func TestUretilenAnahtarYOLICERMEZ(t *testing.T) {
	t.Parallel()

	prov, kok := yeniSaglayici(t)

	dosya, err := prov.Upload(context.Background(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngIcerik)),
	})

	require.NoError(t, err)
	assert.Equal(t, filepath.Base(dosya.Key), dosya.Key, "anahtar bir YOL değil, tek bir addır")
	assert.NotContains(t, dosya.Key, "/")
	assert.NotContains(t, dosya.Key, "..")
	assert.True(t, strings.HasSuffix(dosya.Key, ".png"),
		"uzantı tespit edilen tipten türemeli: %s", dosya.Key)
	assert.Equal(t, local.DefaultURLPrefix+"/"+dosya.Key, dosya.URL)
	assert.Equal(t, int64(len(pngIcerik)), dosya.Size)

	yazilan, readErr := os.ReadFile(filepath.Join(kok, dosya.Key))
	require.NoError(t, readErr)
	assert.Equal(t, pngIcerik, yazilan)
}

// TestIkiYuklemeAyriAnahtarAlir anahtarların tekrar kullanılmadığını doğrular.
//
// Tekrar kullanılan bir anahtar iki şeyi birden bozardı: defterdeki benzersizlik
// kısıtı ihlal edilir ve — çok daha kötüsü — yayımlanmış bir adres bir gün
// BAŞKA bir görseli göstermeye başlardı. Sunum yolundaki "immutable" önbellek
// başlığı da tam olarak bu iddiaya dayanır.
func TestIkiYuklemeAyriAnahtarAlir(t *testing.T) {
	t.Parallel()

	prov, _ := yeniSaglayici(t)
	ctx := context.Background()

	ilk, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngIcerik)),
	})
	require.NoError(t, err)

	ikinci, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngIcerik)),
	})
	require.NoError(t, err)

	assert.NotEqual(t, ilk.Key, ikinci.Key)
}

// kesikOkuyucu birkaç bayt verdikten SONRA hata döndüren okuyucudur.
//
// Boyut sınırının aşıldığı an tam olarak böyle görünür: gövde okunmaya
// başlanmıştır ve okuma ortada kesilir.
type kesikOkuyucu struct {
	veri  []byte
	hata  error
	okudu bool
}

// Read io.Reader'ı karşılar.
func (k *kesikOkuyucu) Read(p []byte) (int, error) {
	if k.okudu {
		return 0, k.hata
	}
	k.okudu = true

	n := copy(p, k.veri)

	return n, nil
}

// TestOkumaYarideKesilirseYARIMDOSYAKALMAZ sınırı aşan bir gövdenin diskte iz
// bırakmadığını doğrular.
//
// Çekirdek sözleşmesinin şartıdır ve gerekçesi somuttur: yarım nesne, hiçbir
// kaydın işaret etmediği ve hiçbir silme yolunun anahtarını bilmediği bir
// dosyadır. Temizlenmeseydi, boyut sınırını aşan istekler REDDEDİLDİKLERİ
// hâlde diski doldurabilirdi.
func TestOkumaYarideKesilirseYARIMDOSYAKALMAZ(t *testing.T) {
	t.Parallel()

	prov, kok := yeniSaglayici(t)
	sinirHatasi := errors.New("boyut sınırı aşıldı")

	_, err := prov.Upload(context.Background(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        &kesikOkuyucu{veri: pngIcerik, hata: sinirHatasi},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, sinirHatasi,
		"asıl hata KORUNMALI; çağıran onu errors.Is ile tanıyıp sınıflandırır")
	assert.Empty(t, kokIcerigi(t, kok),
		"ne yarım dosya ne de geçici dosya kalmalı")
}

// TestYarimYazilmisDosyaSUNULAMAZ atomik yazmanın gözlemlenebilir sonucunu
// sabitler.
//
// Yazma sırasında kök dizinde bir dosya belirip sonra tamamlansaydı, o aralıkta
// gelen bir sunum isteği bozuk bir görsel döndürürdü. Geçici adın nokta ile
// başlaması ve anahtar biçiminin nokta ile başlayan bir gövdeyi reddetmesi bu
// aralığı yapısal olarak kapatır — test, geçici adın gerçekten sunulamaz
// olduğunu doğrular.
func TestYarimYazilmisDosyaSUNULAMAZ(t *testing.T) {
	t.Parallel()

	prov, kok := yeniSaglayici(t)

	gecici, err := os.CreateTemp(kok, ".yukleniyor-*")
	require.NoError(t, err)
	require.NoError(t, gecici.Close())

	_, _, err = prov.Open(context.Background(), filepath.Base(gecici.Name()))

	require.Error(t, err, "geçici ad sunulabilir bir anahtar OLMAMALI")
	assert.True(t, coreerrors.IsInvalid(err), "hata: %v", err)
	assert.Equal(t, local.CodeInvalidKey, coreerrors.CodeOf(err))
}

// TestSilmeIDEMPOTENTTIR olmayan bir anahtarın hata vermediğini doğrular.
//
// Silme, kaydı kaldıran akışın temizlik adımıdır ve o akış yeniden
// denenebilir. İkinci çağrının patlaması, kaydı çoktan silinmiş bir dosyayı
// temizlenemez hâle getirirdi — yani tam olarak temizlemesi gereken çöpü
// kalıcı kılardı.
func TestSilmeIDEMPOTENTTIR(t *testing.T) {
	t.Parallel()

	prov, kok := yeniSaglayici(t)
	ctx := context.Background()

	dosya, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngIcerik)),
	})
	require.NoError(t, err)

	require.NoError(t, prov.Delete(ctx, dosya.Key), "ilk silme")
	require.NoError(t, prov.Delete(ctx, dosya.Key), "İKİNCİ silme de hata vermemeli")
	assert.Empty(t, kokIcerigi(t, kok))
}

// TestGecersizAnahtarinSilinmesiHataDegildir biçimi bozuk bir anahtarın da
// idempotent davrandığını doğrular.
//
// Böyle bir anahtarla yazılmış dosya hiç var olamaz, dolayısıyla "silinmiş
// olma" son durumu zaten sağlanmıştır. Hata dönmek, silme akışını
// düzeltilemeyecek bir şey yüzünden sonsuza kadar tekrar ettirirdi.
func TestGecersizAnahtarinSilinmesiHataDegildir(t *testing.T) {
	t.Parallel()

	prov, _ := yeniSaglayici(t)

	assert.NoError(t, prov.Delete(context.Background(), "../../etc/passwd"))
}

// TestAcmaYolGecisiniREDDEDER anahtar denetiminin sunum yolunu koruduğunu
// doğrular.
//
// Normal akışta anahtar veritabanındaki kayıttan gelir, yani zaten bu
// sağlayıcının ürettiği bir değerdir. Denetim yine de vardır ve bir
// "temizleme" DEĞİLDİR: bozuk anahtar düzeltilmez, reddedilir. Böylece
// çağıranı ne olursa olsun kök dizinin dışına çıkan bir yol ifadesi hiç
// kurulamaz.
func TestAcmaYolGecisiniREDDEDER(t *testing.T) {
	t.Parallel()

	prov, kok := yeniSaglayici(t)

	// Kökün DIŞINDA, gerçekten var olan bir dosya: reddin "dosya yok"tan
	// değil, anahtarın biçiminden geldiğini ancak böyle kanıtlayabiliriz.
	disari := filepath.Join(filepath.Dir(kok), "sir.txt")
	require.NoError(t, os.WriteFile(disari, []byte("gizli"), 0o600))

	anahtarlar := map[string]string{
		"üst dizine çıkış":    "../" + filepath.Base(disari),
		"iki üst dizin":       "../../etc/passwd",
		"mutlak yol":          "/etc/passwd",
		"gömülü ayıraç":       "ABC/../../etc/passwd",
		"uzantısız":           strings.Repeat("A", 26),
		"iki nokta":           strings.Repeat("A", 26) + ".png.png",
		"küçük harfli gövde":  strings.Repeat("a", 26) + ".png",
		"kısa gövde":          "ABC.png",
		"büyük harfli uzantı": strings.Repeat("A", 26) + ".PNG",
	}

	for ad, anahtar := range anahtarlar {
		t.Run(ad, func(t *testing.T) {
			_, _, err := prov.Open(context.Background(), anahtar)

			require.Error(t, err, "anahtar %q kabul edilmemeli", anahtar)
			assert.Equal(t, local.CodeInvalidKey, coreerrors.CodeOf(err),
				"ret, dosyanın yokluğundan değil anahtarın BİÇİMİNDEN gelmeli")
		})
	}
}

// TestAcmaYazilaniAynenDoner sunum yolunun okuduğu içeriği doğrular.
func TestAcmaYazilaniAynenDoner(t *testing.T) {
	t.Parallel()

	prov, _ := yeniSaglayici(t)
	ctx := context.Background()

	dosya, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngIcerik)),
	})
	require.NoError(t, err)

	icerik, modTime, err := prov.Open(ctx, dosya.Key)
	require.NoError(t, err)
	defer func() { _ = icerik.Close() }()

	okunan, err := io.ReadAll(icerik)
	require.NoError(t, err)
	assert.Equal(t, pngIcerik, okunan)
	assert.False(t, modTime.IsZero(), "koşullu istekler için değişim zamanı gerekir")
}

// TestSilinmisDosyaninAcilmasiBulunamadiDoner silmenin sunumu gerçekten
// kapattığını doğrular.
func TestSilinmisDosyaninAcilmasiBulunamadiDoner(t *testing.T) {
	t.Parallel()

	prov, _ := yeniSaglayici(t)
	ctx := context.Background()

	dosya, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngIcerik)),
	})
	require.NoError(t, err)
	require.NoError(t, prov.Delete(ctx, dosya.Key))

	_, _, err = prov.Open(ctx, dosya.Key)

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "hata: %v", err)
}

// TestTaninmayanTipVarsayilanUzantiAlir eşlemede olmayan bir tipin yüklemeyi
// engellemediğini doğrular.
//
// Uzantı yalnızca insan kolaylığıdır; sunum kararı ona bakmaz, Content-Type
// kayıttaki tespit edilmiş tipten yazılır. İzin listesi de burada değil servis
// katmanında uygulanır — sağlayıcı, kendisine verilen tipi sorgulamaz.
func TestTaninmayanTipVarsayilanUzantiAlir(t *testing.T) {
	t.Parallel()

	prov, _ := yeniSaglayici(t)

	dosya, err := prov.Upload(context.Background(), coreprovider.UploadInput{
		ContentType: "application/octet-stream",
		Body:        strings.NewReader("ham"),
	})

	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(dosya.Key, ".bin"), "anahtar: %s", dosya.Key)
}
