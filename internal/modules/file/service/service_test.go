package service_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// yuklenmisKayit tek bir dosya yükleyip kaydını döner.
func yuklenmisKayit(t *testing.T, svc *service.Service) string {
	t.Helper()

	kayit, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("gövde"),
	})
	require.NoError(t, err)

	return kayit.ID
}

// TestSilmeIDEMPOTENTTIR ikinci silmenin hata vermediğini doğrular.
//
// Silme bir SON DURUM iddiasıdır ("bu yükleme artık yok"). İkinci çağrının
// 404 dönmesi, istenen son durum SAĞLANMIŞKEN yeniden denenen bir temizlik
// akışını hata sayardı — yani temizlemesi gereken şeyi temizlenemez kılardı.
func TestSilmeIDEMPOTENTTIR(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	prov := &sahteSaglayici{}
	svc := yeniServis(t, depo, prov)
	id := yuklenmisKayit(t, svc)
	ctx := context.Background()

	require.NoError(t, svc.DeleteUpload(ctx, id), "ilk silme")
	require.NoError(t, svc.DeleteUpload(ctx, id), "İKİNCİ silme de hata vermemeli")
	require.NoError(t, svc.DeleteUpload(ctx, "upl_HICVAROLMADI"), "hiç var olmamış kimlik")

	assert.Zero(t, depo.sayi())
	assert.Len(t, prov.silinenler(), 1,
		"ikinci turda silinecek dosya kalmadığı için sağlayıcıya gidilmemeli")
}

// TestSilmeONCEDOSYASONRAKAYIT sıranın yakınsayan taraf olduğunu doğrular.
//
// İki taraf ayrı sistemlerdedir ve tek bir işleme alınamaz; geriye yalnızca
// sıra kalır. Depo silmesi patlarsa KAYIT DA SİLİNMEMELİDİR: kayıt gittikten
// sonra dosyanın anahtarını bilen kimse kalmaz ve o dosya erişilemez çöp
// olurdu. Bu sırada ise yeniden deneme her şeyi kapatır.
func TestSilmeONCEDOSYASONRAKAYIT(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	prov := &sahteSaglayici{silmeHatasi: coreerrors.Unavailable("disk_down", "diske ulaşılamadı")}
	svc := yeniServis(t, depo, prov)
	id := yuklenmisKayit(t, svc)

	err := svc.DeleteUpload(context.Background(), id)

	require.Error(t, err)
	assert.Equal(t, 1, depo.sayi(),
		"dosya silinemediyse kayıt DA silinmemeli; aksi hâlde dosya erişilemez çöp olurdu")

	// Depo düzelince aynı çağrı işi bitirir: yakınsama iddiası budur.
	prov.silmeHatasi = nil
	require.NoError(t, svc.DeleteUpload(context.Background(), id))
	assert.Zero(t, depo.sayi())
}

// TestSilmeKAYDINSaglayicisiniKullanir yapılandırma değişse bile eski
// dosyaların silinebildiğini doğrular.
//
// Kurulum bir gün nesne deposuna geçtiğinde eski kayıtlar hâlâ yerel diskte
// durur. O an yapılandırılmış sağlayıcıya sorulsaydı, silme çağrısı yanlış
// depoda var olmayan bir anahtarı siler ve gerçek dosya sonsuza kadar kalırdı
// — üstelik idempotent silme yüzünden hiç hata da vermeden.
func TestSilmeKAYDINSaglayicisiniKullanir(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	eski := &sahteSaglayici{id: "eski"}
	yeni := &sahteSaglayici{id: "yeni"}

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(eski))
	require.NoError(t, kayit.Register(yeni))

	// Servis "yeni" ile yükler ama defterde "eski" ile yazılmış bir kayıt
	// vardır; silme onu bulmalıdır.
	svc, err := service.New(service.Options{
		Store:          depo,
		Providers:      kayit,
		ProviderID:     yeni.ID(),
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	eskiKayit, err := depo.CreateUpload(context.Background(), yeniModelKaydi("eski"))
	require.NoError(t, err)

	require.NoError(t, svc.DeleteUpload(context.Background(), eskiKayit.ID))

	assert.Equal(t, []string{"ESKI_ANAHTAR.png"}, eski.silinenler(),
		"dosya, onu YAZAN sağlayıcıdan silinmeli")
	assert.Empty(t, yeni.silinenler(), "yapılandırılmış sağlayıcıya hiç gidilmemeli")
}

// TestSunumSAKLANANTipiVerir sunum yolunun kayıttan okuduğunu doğrular.
//
// İstemcinin yükleme sırasında bildirdiği tip hiçbir yerde saklanmaz; sunulan
// tip her zaman yükleme anında İÇERİKTEN tespit edilmiş olandır.
func TestSunumSAKLANANTipiVerir(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	prov := &sahteAcilabilirSaglayici{
		sahteSaglayici: &sahteSaglayici{id: "acilabilir"},
		icerik:         "ham baytlar",
	}

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(prov))

	svc, err := service.New(service.Options{
		Store:          depo,
		Providers:      kayit,
		ProviderID:     prov.ID(),
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	yuklendi, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("gövde"),
	})
	require.NoError(t, err)

	acilan, err := svc.OpenByKey(context.Background(), yuklendi.StorageKey)
	require.NoError(t, err)
	defer func() { _ = acilan.Content.Close() }()

	assert.Equal(t, coreprovider.ContentTypePNG, acilan.Upload.ContentType)

	okunan, err := io.ReadAll(acilan.Content)
	require.NoError(t, err)
	assert.Equal(t, "ham baytlar", string(okunan))
}

// TestSunumONCEDEFTEREBakar kaydı olmayan bir anahtar için depoya hiç
// gidilmediğini doğrular.
//
// "Sunulan tek şey yüklenmiş dosyalardır" iddiasını taşıyan yapı budur: uç bir
// "dosya oku" ucu değil, "bu kaydı sun" ucudur ve deftere yazılmamış bir
// anahtar depoya ulaşamaz.
func TestSunumONCEDEFTEREBakar(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	prov := &sahteAcilabilirSaglayici{
		sahteSaglayici: &sahteSaglayici{id: "acilabilir"},
		icerik:         "gizli",
	}

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(prov))

	svc, err := service.New(service.Options{
		Store:          depo,
		Providers:      kayit,
		ProviderID:     prov.ID(),
		MaxUploadBytes: testAzamiBoyut,
		AllowedTypes:   []string{coreprovider.ContentTypePNG},
	})
	require.NoError(t, err)

	_, err = svc.OpenByKey(context.Background(), "../../etc/passwd")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "hata: %v", err)
}

// TestSunulamayanSaglayiciBULUNAMADIDoner nesne deposuna yazan bir kurulumda
// sunum yolunun ne dediğini sabitler.
//
// "Uygulanmadı" (500) demek yanlış olurdu: istemci açısından o adreste
// gerçekten bir şey yoktur — dosyanın gerçek adresi CDN'dedir.
func TestSunulamayanSaglayiciBULUNAMADIDoner(t *testing.T) {
	t.Parallel()

	depo := yeniSahteDepo()
	svc := yeniServis(t, depo, &sahteSaglayici{id: "sahte"})

	yuklendi, err := svc.Upload(context.Background(), service.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("gövde"),
	})
	require.NoError(t, err)

	_, err = svc.OpenByKey(context.Background(), yuklendi.StorageKey)

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "hata: %v", err)
	assert.Equal(t, service.CodeNotServable, coreerrors.CodeOf(err))
}

// TestListeSayfalamaSinirlariZorlanir sayfalama doğrulamasını sabitler.
func TestListeSayfalamaSinirlariZorlanir(t *testing.T) {
	t.Parallel()

	svc := yeniServis(t, yeniSahteDepo(), &sahteSaglayici{})
	ctx := context.Background()

	tests := map[string]service.Page{
		"negatif limit":  {Limit: -1},
		"negatif offset": {Offset: -1},
		"aşırı limit":    {Limit: service.MaxLimit + 1},
	}

	for ad, sayfa := range tests {
		t.Run(ad, func(t *testing.T) {
			_, _, err := svc.ListUploads(ctx, sayfa)

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "hata: %v", err)
		})
	}

	_, _, err := svc.ListUploads(ctx, service.Page{})
	require.NoError(t, err, "boş sayfa varsayılana düşmeli")
}

// TestRegistryAyniKimlikleIkinciKayitCakisirVeMevcutuKorur sessizce üzerine
// yazmanın reddedildiğini doğrular.
//
// Dosyada bunun bedeli somuttur: sağlayıcı kimliği KAYITLARA yazılır ve bir
// dosyayı okuyabilecek tek şey onu yazan sağlayıcıdır. Kayıt sırası
// değişebilseydi, dün yazılan dosyalar bugün okunamaz hâle gelirdi.
func TestRegistryAyniKimlikleIkinciKayitCakisirVeMevcutuKorur(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()
	ilk := &sahteSaglayici{id: "local"}
	ikinci := &sahteSaglayici{id: "local"}

	require.NoError(t, kayit.Register(ilk))
	err := kayit.Register(ikinci)

	require.Error(t, err)
	assert.True(t, coreerrors.IsConflict(err), "hata: %v", err)
	assert.Equal(t, service.CodeProviderExists, coreerrors.CodeOf(err))

	cozulen, getErr := kayit.Get("local")
	require.NoError(t, getErr)
	assert.Same(t, ilk, cozulen, "mevcut sağlayıcı KORUNMALI")
}

// TestRegistryBilinmeyenKimlikTeshisEdilebilirHataVerir kurulum hatasının
// okunabilir olduğunu doğrular (ADR 0002).
func TestRegistryBilinmeyenKimlikTeshisEdilebilirHataVerir(t *testing.T) {
	t.Parallel()

	kayit := service.NewProviderRegistry()
	require.NoError(t, kayit.Register(&sahteSaglayici{id: "local"}))

	_, err := kayit.Get("s3")

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "hata: %v", err)
	assert.Contains(t, err.Error(), "s3", "aranan kimlik yazılmalı")
	assert.Contains(t, err.Error(), "local", "kayıtlı kimlikler yazılmalı")
}
