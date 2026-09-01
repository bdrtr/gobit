//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya satış kanalı kapsamının YAZMA yolunda da uygulandığını GERÇEK
// yığınla kanıtlar:
//
//	B kanalının publishable anahtarıyla gelen bir istemci, yalnızca A
//	kanalında satılan bir varyantı sepetine EKLEYEMEZ.
//
// # Neden HTTP'den geçiyor
//
// İddia bir akış iddiası değildir. Doğru çalışması için dört katmanın
// hizalanması gerekir: auth anahtarın kanallarını çözecek, çekirdeğin koruma
// yığını kimliği context'e koyacak, sepet akışı kanalları O kimlikten okuyacak
// ve product'ın Query sağlayıcısı süzgeci gerçekten uygulayacaktır. Akışı
// doğrudan çağıran bir test, zincirin ilk üç halkası kopmuşken bile yeşil
// kalırdı — üstelik açık tam olarak orada, HTTP ucunda sömürülür.
//
// # Neden ikinci bir anahtar
//
// Tek anahtarla "varyant eklenemiyor" gözlemi hiçbir şey kanıtlamaz: varyant
// silinmiş, fiyatsız ya da ürün taslak da olabilir; her şeyi reddeden bir kapı
// da o testi geçerdi. AYNI varyantın KENDİ kanalının anahtarıyla eklenebilmesi,
// reddin sebebinin tam olarak kanal olduğunu söyleyen tek gözlemdir.
//
// # Okuma yüzeyiyle simetri
//
// Kanal kataloğu testleri (kanal_katalogu_test.go) aynı zemini OKUMA yüzeyi
// için kurar. Bu dosya onun aynadaki hâlidir ve fikstürünü paylaşır: iki
// yüzeyin AYNI kurulumda aynı cevabı vermesi, kapsamın tek bir kural olduğunu
// söyleyen şeydir.

// kanalSepeti yazma yolu senaryosunun kurulmuş zeminidir.
type kanalSepeti struct {
	// birinciKanalVaryant YALNIZCA [testKanalID]'ye atanmış ürünün varyantıdır.
	birinciKanalVaryant string
	// ikinciKanalVaryant YALNIZCA ikinci kanala atanmış ürünün varyantıdır.
	ikinciKanalVaryant string
	// atamasizVaryant hiçbir kanala atanmamış ürünün varyantıdır; kural gereği
	// İKİ vitrinde de satılabilmelidir.
	atamasizVaryant string
}

// Fikstürün bir kez kurulması için gereken durum.
var (
	// kanalSepetiBirKez zeminin yalnızca bir kez kurulmasını sağlar.
	kanalSepetiBirKez sync.Once
	// kanalSepetiZemin kurulmuş zemindir.
	kanalSepetiZemin kanalSepeti
	// kanalSepetiHatasi kurulum hatasını testlere taşır.
	kanalSepetiHatasi error
)

// kanalSepetiFiksturu üç fiyatlı varyantı ve kanal atamalarını hazırlar.
//
// Kurulum [kanalKataloguFiksturu] üzerine biner (ikinci kanal ve ikinci
// anahtar oradan gelir) ama KENDİ ürünlerini kurar: oradaki üç ürünün varyantı
// ve fiyatı yoktur, sepete girecek bir varyantın ikisine de ihtiyacı vardır.
// Aynı ürünleri fiyatlandırmak, okuma testlerinin saydığı kümeyi değiştirirdi.
func kanalSepetiFiksturu(t *testing.T) kanalSepeti {
	t.Helper()

	zemin := kanalKataloguFiksturu(t)

	kanalSepetiBirKez.Do(func() {
		// Kurulum context'i t.Context() DEĞİLDİR; gerekçe
		// [kanalKataloguFiksturu] ile aynıdır.
		kanalSepetiZemin, kanalSepetiHatasi = kanalSepetiKur(context.Background(), zemin.ikinciKanalID)
	})
	require.NoError(t, kanalSepetiHatasi, "kanal sepeti fikstürü kurulamadı")

	return kanalSepetiZemin
}

// kanalSepetiKur üç varyantı kurar ve ikisini birer kanala bağlar.
func kanalSepetiKur(ctx context.Context, ikinciKanalID string) (kanalSepeti, error) {
	var zemin kanalSepeti
	var err error

	if zemin.birinciKanalVaryant, err = kanalliVaryantKur(ctx, "birinci", testKanalID); err != nil {
		return zemin, err
	}
	if zemin.ikinciKanalVaryant, err = kanalliVaryantKur(ctx, "ikinci", ikinciKanalID); err != nil {
		return zemin, err
	}
	if zemin.atamasizVaryant, err = kanalliVaryantKur(ctx, "atamasiz", ""); err != nil {
		return zemin, err
	}

	return zemin, nil
}

// kanalliVaryantKur yayında bir ürün, fiyatlı bir varyant ve (istenirse) kanal
// bağı kurar; VARYANT kimliğini döner.
//
// Ürün YAYINDADIR ve varyant FİYATLIDIR: ikisi de eksik olsaydı satır ekleme
// zaten reddedilirdi ve test, ölçmek istediği şeyi (kanal) değil başka bir
// kapıyı ölçerdi.
func kanalliVaryantKur(ctx context.Context, ad, kanalID string) (string, error) {
	handle := "e2e-kanal-sepeti-" + ad

	urun, err := urunSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: handle,
		Title:  "E2E Kanal Sepeti " + ad,
		Status: productmodels.StatusPublished,
	})
	if err != nil {
		return "", fmt.Errorf("%q ürünü kurulamadı: %w", handle, err)
	}

	varyant, err := urunSvc.CreateVariant(ctx, urun.ID, productsvc.CreateVariantInput{
		Title: "Tek beden",
	})
	if err != nil {
		return "", fmt.Errorf("%q varyantı kurulamadı: %w", handle, err)
	}

	kume, err := fiyatSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{{
		CurrencyCode: vergiliParaBirimi,
		Amount:       1000,
		MinQuantity:  1,
	}})
	if err != nil {
		return "", fmt.Errorf("%q fiyat kümesi kurulamadı: %w", handle, err)
	}
	if err := urunSvc.SetVariantPriceSet(ctx, varyant.ID, kume.ID); err != nil {
		return "", fmt.Errorf("%q fiyat bağı kurulamadı: %w", handle, err)
	}

	if kanalID != "" {
		if err := kanalBagla(urun.ID, kanalID); err != nil {
			return "", err
		}
	}

	return varyant.ID, nil
}

// anahtarliVitrinIstegi VERİLEN publishable anahtarla bir mağaza isteği yapar.
//
// [vitrinIstegi] bunun paylaşılan anahtara sabitlenmiş hâlidir; iki anahtarlı
// senaryolar için ayrıştırıldı. Üçüncü bir kopya çıkarmak, koruma yığınından
// geçmeyen bir istek kurmanın en kolay yolu olurdu.
func anahtarliVitrinIstegi(t *testing.T, anahtar, metod, yol, govde string) *httptest.ResponseRecorder {
	t.Helper()

	istek := httptest.NewRequest(metod, yol, bytes.NewReader([]byte(govde)))
	istek.Header.Set("Content-Type", "application/json")
	istek.Header.Set(corehttp.PublishableKeyHeader, anahtar)

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// kanalliSepetAc verilen anahtarla bir sepet açar ve kimliğini döner.
func kanalliSepetAc(t *testing.T, anahtar string) string {
	t.Helper()

	kayit := anahtarliVitrinIstegi(t, anahtar, http.MethodPost, "/store/v1/carts",
		fmt.Sprintf(`{"country_code":%q}`, vergiliUlke))
	require.Equal(t, http.StatusCreated, kayit.Code,
		"sepet açılmalı; gövde: %s", kayit.Body.String())

	id, ok := vitrinVeri(t, kayit)["id"].(string)
	require.True(t, ok, "açılan sepet kimlik taşımalı; gövde: %s", kayit.Body.String())

	return id
}

// satirEklemeDene verilen anahtarla sepete satır eklemeyi dener.
func satirEklemeDene(t *testing.T, anahtar, sepetID, varyantID string) *httptest.ResponseRecorder {
	t.Helper()

	return anahtarliVitrinIstegi(t, anahtar, http.MethodPost,
		"/store/v1/carts/"+sepetID+"/line-items",
		fmt.Sprintf(`{"variant_id":%q,"quantity":1}`, varyantID))
}

// sepetSatirSayisi sepetin satır sayısını vitrin ucundan okur.
//
// Sayı, akışın döndürdüğü bir değerden değil sepetin KENDİ kaydından okunur:
// sınanan iddia, reddedilen isteğin sepete hiçbir şey YAZMAMASIDIR.
func sepetSatirSayisi(t *testing.T, anahtar, sepetID string) int {
	t.Helper()

	kayit := anahtarliVitrinIstegi(t, anahtar, http.MethodGet, "/store/v1/carts/"+sepetID, "")
	require.Equal(t, http.StatusOK, kayit.Code, "sepet okunmalı; gövde: %s", kayit.Body.String())

	var zarf struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"sepet yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return len(zarf.Data.Items)
}

// TestYabanciKanalinVaryantiSepeteEklenemez açığın kendisini kapatan iddiadır.
//
// Eskiden bu istek 201 dönerdi: sepet akışı varyantı YALNIZCA kimlikle okuyor,
// isteğin satış kanallarını hiç sormuyordu. Vitrinde gizlenen ürün sepette
// satılabiliyordu, yani kapsam kuralı bir yetkilendirme değil bir görüntüleme
// tercihiydi.
func TestYabanciKanalinVaryantiSepeteEklenemez(t *testing.T) {
	zemin := kanalSepetiFiksturu(t)
	kanal := kanalKataloguFiksturu(t)

	sepetID := kanalliSepetAc(t, publishableAnahtar)
	red := satirEklemeDene(t, publishableAnahtar, sepetID, zemin.ikinciKanalVaryant)

	assert.Equal(t, http.StatusNotFound, red.Code,
		"başka bir kanalın varyantı sepete GİREMEMELİ; gövde: %s", red.Body.String())
	assert.Zero(t, sepetSatirSayisi(t, publishableAnahtar, sepetID),
		"reddedilen istek sepete satır YAZMAMALI")

	// Ters yön de geçerlidir: kural tek yönlü bir engel değil, iki vitrinin
	// birbirinin kataloğuna kapalı olmasıdır.
	ikinciSepet := kanalliSepetAc(t, kanal.ikinciAnahtar)
	tersRed := satirEklemeDene(t, kanal.ikinciAnahtar, ikinciSepet, zemin.birinciKanalVaryant)

	assert.Equal(t, http.StatusNotFound, tersRed.Code,
		"birinci kanalın varyantı ikinci vitrinde de eklenememeli; gövde: %s", tersRed.Body.String())
	assert.Zero(t, sepetSatirSayisi(t, kanal.ikinciAnahtar, ikinciSepet))
}

// TestKendiKanalinVaryantiSepeteEklenir kapının HER ŞEYİ reddetmediğini
// kanıtlar.
//
// Bu iddia olmadan yukarıdaki test değersizdir: katalog okumasını tümüyle
// bozan ya da satır eklemeyi hepten kapatan bir değişiklik de onu geçirirdi.
// Aynı varyantın bir anahtarla reddedilip diğeriyle kabul edilmesi, reddin
// sebebinin tam olarak KANAL olduğunu söyleyen tek gözlemdir.
func TestKendiKanalinVaryantiSepeteEklenir(t *testing.T) {
	zemin := kanalSepetiFiksturu(t)
	kanal := kanalKataloguFiksturu(t)

	sepetID := kanalliSepetAc(t, publishableAnahtar)
	kabul := satirEklemeDene(t, publishableAnahtar, sepetID, zemin.birinciKanalVaryant)
	require.Equal(t, http.StatusCreated, kabul.Code,
		"kendi kanalının varyantı sepete girmeli; gövde: %s", kabul.Body.String())
	assert.Equal(t, 1, sepetSatirSayisi(t, publishableAnahtar, sepetID))

	// Birinci vitrinde reddedilen varyant, KENDİ vitrininde kabul edilmeli.
	ikinciSepet := kanalliSepetAc(t, kanal.ikinciAnahtar)
	kendiVitrini := satirEklemeDene(t, kanal.ikinciAnahtar, ikinciSepet, zemin.ikinciKanalVaryant)
	assert.Equal(t, http.StatusCreated, kendiVitrini.Code,
		"gizlenen varyant ait olduğu vitrinde satılabilmeli; gövde: %s", kendiVitrini.Body.String())
	assert.Equal(t, 1, sepetSatirSayisi(t, kanal.ikinciAnahtar, ikinciSepet))
}

// TestAtamasizVaryantHerVitrindeSepeteGirer kuralın GERİYE UYUMLU yarısının
// yazma yolunda da geçerli olduğunu doğrular.
//
// Ataması olmayan ürün her kanalda görünür; görünmeseydi bu değişiklik, kanal
// ataması hiç kullanmayan mevcut kurulumların TÜM sepetlerini bir gecede
// bozardı. Okuma yüzeyinde aynı iddia vardır
// (bkz. TestVitrinKataloguIsteginSatisKanalinaGoreSuzulur).
func TestAtamasizVaryantHerVitrindeSepeteGirer(t *testing.T) {
	zemin := kanalSepetiFiksturu(t)
	kanal := kanalKataloguFiksturu(t)

	for ad, anahtar := range map[string]string{
		"birinci vitrin": publishableAnahtar,
		"ikinci vitrin":  kanal.ikinciAnahtar,
	} {
		t.Run(ad, func(t *testing.T) {
			sepetID := kanalliSepetAc(t, anahtar)
			kayit := satirEklemeDene(t, anahtar, sepetID, zemin.atamasizVaryant)

			assert.Equal(t, http.StatusCreated, kayit.Code,
				"atamasız ürünün varyantı her vitrinde satılabilmeli; gövde: %s", kayit.Body.String())
			assert.Equal(t, 1, sepetSatirSayisi(t, anahtar, sepetID))
		})
	}
}

// TestKapsamDisiVaryantVarliginiEleVermez yabancı kanalın varyantının
// reddinin, HİÇ VAR OLMAYAN bir varyantın reddinden ayırt edilemediğini
// doğrular.
//
// Ayırt edilebilseydi gizleme delinirdi: elindeki publishable anahtarla gelen
// bir rakip, varyant kimliklerini deneyerek hangilerinin BAŞKA bir kanalda
// satıldığını öğrenirdi. Okuma yüzeyinde aynı iddia vardır
// (bkz. TestGizlenenUrunVarliginiHataKoduylaEleVermez) ve karşılaştırma yine
// yalnızca hata KODU üzerindendir; mesaj istenen kimliği yankıladığı için iki
// istekte zaten farklıdır.
func TestKapsamDisiVaryantVarliginiEleVermez(t *testing.T) {
	zemin := kanalSepetiFiksturu(t)

	sepetID := kanalliSepetAc(t, publishableAnahtar)
	gizlenen := satirEklemeDene(t, publishableAnahtar, sepetID, zemin.ikinciKanalVaryant)
	olmayan := satirEklemeDene(t, publishableAnahtar, sepetID, "variant_e2e_hic_yok")

	require.Equal(t, http.StatusNotFound, gizlenen.Code, "gövde: %s", gizlenen.Body.String())
	require.Equal(t, http.StatusNotFound, olmayan.Code, "gövde: %s", olmayan.Body.String())

	assert.Equal(t, hataOzu(t, olmayan)[0], hataOzu(t, gizlenen)[0],
		"kapsam dışı varyant ile olmayan varyant AYNI hata kodunu dönmeli")
}
