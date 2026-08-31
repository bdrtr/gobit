//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya ürün ↔ satış kanalı bağının vitrine yansımasını GERÇEK yığınla
// kanıtlar:
//
//	İki ayrı publishable anahtarla çağrılan aynı mağaza ucu, iki FARKLI
//	katalog döner.
//
// # Neden HTTP'den geçiyor
//
// İddianın kendisi bir servis iddiası değildir. Süzgecin doğru çalışması için
// üç ayrı katmanın hizalanması gerekir: anahtarın kanalını auth çözecek,
// çekirdeğin koruma yığını onu context'e koyacak ve product handler'ı kanalı
// SORGU DİZESİNDEN değil o kimlikten okuyacaktır. Servisi doğrudan çağıran bir
// test, handler kanalı hiç okumasa bile yeşil kalırdı — süzgeç o durumda
// üretimde HİÇ çalışmazdı ama modül testleri bunu göremezdi.
//
// # Neden ikinci bir anahtar
//
// Tek anahtarla "ürün görünmüyor" gözlemi tek başına hiçbir şey kanıtlamaz:
// ürün silinmiş, taslak kalmış ya da sorgu bozulmuş da olabilir. İkinci
// anahtarın AYNI ürünü GÖRMESİ, gizlemenin sebebinin tam olarak kanal olduğunu
// söyleyen tek gözlemdir.
//
// # Yeni ürünler paylaşılan zemini bozar mı
//
// Bozmaz: paketteki mevcut testlerin hiçbiri ürün SAYISINA dayanan bir iddia
// içermez (durum kodlarına ve kendi kurdukları kayıtlara bakarlar). Ters yön —
// paylaşılan zeminin BU testi bozması — gerçek bir risktir ve koleksiyon
// yalıtımıyla kapatılır (bkz. [kanalKataloguFiksturu]).

// İkinci vitrinin fikstür sabitleri.
//
// Adlar ve handle'lar SABİTTİR (fikstür sayacından üretilmez): kurulum süreç
// başına bir kez koşar ve sabit adlar, bir hata mesajında hangi kaydın
// kastedildiğini tek bakışta okunur kılar.
const (
	// ikinciKanalAdi paylaşılan fikstür kanalından ([testKanalAdi]) ayrı ikinci
	// satış kanalının adıdır.
	ikinciKanalAdi = "e2e-ikinci-vitrin"
	// kanalKataloguKoleksiyonHandle üç fikstür ürününü paylaşılan katalogdan
	// ayıran koleksiyonun handle'ıdır.
	kanalKataloguKoleksiyonHandle = "e2e-kanal-katalogu"
)

// kanalKataloguUrunu fikstür ürününün testin ihtiyaç duyduğu alanlarıdır.
//
// Handle da taşınır çünkü tekil vitrin ucu iki adresle de çağrılabilir ve
// süzgecin YALNIZCA kimlikle çalışması, vitrin adreslerinin (handle) süzgeçsiz
// kalması demek olurdu.
type kanalKataloguUrunu struct {
	id     string
	handle string
}

// kanalKatalogu iki vitrinli senaryonun kurulmuş zeminidir.
type kanalKatalogu struct {
	// ikinciKanalID [testKanalID]'den ayrı ikinci satış kanalıdır.
	ikinciKanalID string
	// ikinciAnahtar YALNIZCA ikinci kanala bağlı publishable anahtardır.
	ikinciAnahtar string
	// koleksiyonID üç ürünün de bağlı olduğu koleksiyondur.
	koleksiyonID string
	// birinciKanalUrunu yalnızca paylaşılan fikstür kanalına atanmıştır.
	birinciKanalUrunu kanalKataloguUrunu
	// ikinciKanalUrunu yalnızca [kanalKatalogu.ikinciKanalID]'ye atanmıştır.
	ikinciKanalUrunu kanalKataloguUrunu
	// atamasizUrun hiçbir kanala atanmamıştır ve kural gereği İKİSİNDE de
	// görünmelidir.
	atamasizUrun kanalKataloguUrunu
}

// Fikstürün bir kez kurulması için gereken durum.
var (
	// kanalKataloguBirKez zeminin yalnızca bir kez kurulmasını sağlar.
	kanalKataloguBirKez sync.Once
	// kanalKataloguZemin kurulmuş zemindir.
	kanalKataloguZemin kanalKatalogu
	// kanalKataloguHatasi kurulum hatasını testlere taşır.
	kanalKataloguHatasi error
)

// kanalKataloguFiksturu iki vitrinli zemini kurar ve döner.
//
// # Neden bir kez
//
// Satış kanalı adı ve ürün handle'ı BENZERSİZDİR; her testte yeniden kurmak
// ikinci çağrıda çakışırdı. Kurulum bu yüzden [sync.Once] içindedir ve hata
// dışarı taşınır (bkz. [yetkisizYoneticiJetonu], aynı örüntü).
//
// # Neden koleksiyonla yalıtılıyor
//
// Paylaşılan zeminde onlarca fikstür ürünü vardır ve hiçbirinin kanal ataması
// YOKTUR; kural gereği hepsi HER vitrinde görünür. Yani "kendi kanalımla
// süzmek" bu testi yalıtmaz — üç ürün yabancı ürünlerin arasında kaybolur ve
// sayaç iddiası ("count" süzülmüş kümeyi yansıtıyor mu) sabit bir sayı
// üzerinden hiç kurulamazdı. Üç ürün bu yüzden yalnızca bu dosyanın kurduğu bir
// KOLEKSİYONA konur ve her istek collection_id ile daraltılır.
//
// Yalıtımın bedeli yoktur, yan faydası vardır: koleksiyon süzgeci ile kanal
// süzgeci aynı sorguda VE'lenir, dolayısıyla test aynı zamanda ikisinin
// birbirini ezmediğini de kanıtlar. Sayaç iddiasından tümüyle vazgeçmek
// (alternatif) görevin asıl sorusunu yanıtsız bırakırdı.
func kanalKataloguFiksturu(t *testing.T) kanalKatalogu {
	t.Helper()

	kanalKataloguBirKez.Do(func() {
		// Kurulum context'i t.Context() DEĞİLDİR: zemin testler arasında
		// paylaşılır ve ilk testin context'i o test bittiğinde iptal edilir.
		// Kurulum burada tamamlansa bile o context'i saklamak, sonradan
		// eklenecek bir adımın iptal edilmiş bir context'le koşmasına açık kapı
		// bırakırdı.
		kanalKataloguZemin, kanalKataloguHatasi = kanalKataloguKur(context.Background())
	})
	require.NoError(t, kanalKataloguHatasi, "kanal kataloğu fikstürü kurulamadı")

	return kanalKataloguZemin
}

// kanalKataloguKur ikinci kanalı, ikinci anahtarı ve üç ürünü hazırlar.
//
// Kanal ve anahtar SERVİSTEN, kanal ATAMALARI ise YÖNETİM UCUNDAN kurulur ve
// bu ayrım bilinçlidir: ilk ikisi bu testin konusu değildir (kimlik zemini
// zaten kurulmuştur, bkz. [kimlikFiksturunuKur]), üçüncüsü ise tam olarak
// sınanan şeydir. Atamayı servisten yapmak, yönetim ucunun vitrinin okuduğu
// bağın TA KENDİSİNİ yazdığını kanıtlamazdı — uç başka bir link adına yazsa
// test yine yeşil kalırdı.
func kanalKataloguKur(ctx context.Context) (kanalKatalogu, error) {
	var zemin kanalKatalogu

	kanal, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        ikinciKanalAdi,
		Description: "uçtan uca testin ikinci vitrini",
	})
	if err != nil {
		return zemin, fmt.Errorf("ikinci satış kanalı kurulamadı: %w", err)
	}
	zemin.ikinciKanalID = kanal.ID

	if _, zemin.ikinciAnahtar, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeyPublishable,
		Title:     "e2e ikinci publishable anahtar",
		CreatedBy: yoneticiID,
		// Anahtar YALNIZCA ikinci kanala bağlanır; iki kanal birden taşısaydı
		// testin tamamı anlamsızlaşırdı.
		SalesChannelIDs: []string{kanal.ID},
	}); err != nil {
		return zemin, fmt.Errorf("ikinci publishable anahtar kurulamadı: %w", err)
	}

	koleksiyon, err := urunSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E Kanal Kataloğu",
		Handle: kanalKataloguKoleksiyonHandle,
	})
	if err != nil {
		return zemin, fmt.Errorf("yalıtım koleksiyonu kurulamadı: %w", err)
	}
	zemin.koleksiyonID = koleksiyon.ID

	if zemin.birinciKanalUrunu, err = kanalKataloguUrunuKur(ctx, koleksiyon.ID, "birinci"); err != nil {
		return zemin, err
	}
	if zemin.ikinciKanalUrunu, err = kanalKataloguUrunuKur(ctx, koleksiyon.ID, "ikinci"); err != nil {
		return zemin, err
	}
	if zemin.atamasizUrun, err = kanalKataloguUrunuKur(ctx, koleksiyon.ID, "atamasiz"); err != nil {
		return zemin, err
	}

	if err := kanalBagla(zemin.birinciKanalUrunu.id, testKanalID); err != nil {
		return zemin, err
	}
	if err := kanalBagla(zemin.ikinciKanalUrunu.id, kanal.ID); err != nil {
		return zemin, err
	}

	return zemin, nil
}

// kanalKataloguUrunuKur koleksiyona bağlı YAYINDA bir ürün oluşturur.
//
// Durum [productmodels.StatusPublished]'tır: taslak ürün vitrinde zaten
// görünmez ve o durumda testin ölçtüğü şey kanal süzgeci değil yayın süzgeci
// olurdu.
func kanalKataloguUrunuKur(ctx context.Context, koleksiyonID, ad string) (kanalKataloguUrunu, error) {
	handle := "e2e-kanal-katalogu-" + ad

	urun, err := urunSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:       handle,
		Title:        "E2E Kanal Kataloğu " + ad,
		Status:       productmodels.StatusPublished,
		CollectionID: &koleksiyonID,
	})
	if err != nil {
		return kanalKataloguUrunu{}, fmt.Errorf("%q ürünü kurulamadı: %w", handle, err)
	}

	return kanalKataloguUrunu{id: urun.ID, handle: urun.Handle}, nil
}

// yonetimGovdeliIstek gizli anahtarla bir yönetim yazma isteği yapar.
//
// [yonetimIstegi]'nden ayrı durur çünkü ikisi farklı yerlerden çağrılır: o
// yardımcı *testing.T ister, bu ise fikstürün [sync.Once] gövdesinden — yani
// hiçbir testin içinde olmadığımız bir yerden — çağrılır ve hatayı döndürmek
// zorundadır.
//
// govde nil verilirse istek GÖVDESİZ gider ("null" gövdeyle değil): kanal
// bağını kaldıran uç kimliği YOLDA taşır ve gövdesini hiç okumaz, oraya bir
// JSON değeri koymak okuyana o gövdenin bir anlamı varmış izlenimi verirdi.
func yonetimGovdeliIstek(metot, yol string, govde any) (*httptest.ResponseRecorder, error) {
	icerik := io.Reader(http.NoBody)
	if govde != nil {
		ham, err := json.Marshal(govde)
		if err != nil {
			return nil, fmt.Errorf("istek gövdesi kodlanamadı: %w", err)
		}
		icerik = bytes.NewReader(ham)
	}

	istek := httptest.NewRequest(metot, yol, icerik)
	istek.Header.Set("Authorization", "Bearer "+gizliAnahtar)
	istek.Header.Set("Content-Type", "application/json")

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit, nil
}

// kanalBagla ürünü satış kanalına YÖNETİM UCUNDAN bağlar.
//
// Ucun döndürdüğü güncel liste burada da denetlenir: yazma isteği 200 dönüp
// bağı hiç kurmasaydı, arıza fikstürde değil onu kullanan her testte ve yanlış
// yerde görünürdü.
func kanalBagla(urunID, kanalID string) error {
	kayit, err := yonetimGovdeliIstek(http.MethodPost,
		"/admin/v1/products/"+urunID+"/sales-channels",
		map[string]string{"sales_channel_id": kanalID})
	if err != nil {
		return err
	}
	if kayit.Code != http.StatusOK {
		return fmt.Errorf("kanal bağı kurulamadı (%d): %s", kayit.Code, kayit.Body.String())
	}

	ids, err := kanalListesiniCoz(kayit)
	if err != nil {
		return err
	}
	if !slices.Contains(ids, kanalID) {
		return fmt.Errorf("yönetim ucu %q bağını bildirmedi; dönen liste: %v", kanalID, ids)
	}

	return nil
}

// kanalListesiniCoz satış kanalı yanıtındaki kanal kimliklerini çıkarır.
func kanalListesiniCoz(kayit *httptest.ResponseRecorder) ([]string, error) {
	var zarf struct {
		Data struct {
			SalesChannelIDs []string `json:"sales_channel_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(kayit.Body.Bytes(), &zarf); err != nil {
		return nil, fmt.Errorf("satış kanalı yanıtı çözülemedi: %w (gövde: %s)", err, kayit.Body.String())
	}

	return zarf.Data.SalesChannelIDs, nil
}

// vitrinZarfi mağaza liste yanıtının test tarafındaki karşılığıdır.
//
// Zarfın "offset" ve "limit" alanları BİLİNÇLİ olarak çözülmez: bu testin
// iddiası hangi ürünlerin döndüğü ve kaç tane sayıldığıdır, sayfalama
// parametrelerinin yankılanması değil. Ürünün de yalnızca kimliği ile handle'ı
// okunur; alanlarının doğruluğu başka testlerin işidir.
type vitrinZarfi struct {
	Data []struct {
		ID     string `json:"id"`
		Handle string `json:"handle"`
	} `json:"data"`
	Count int `json:"count"`
}

// kimlikler zarftaki ürün kimliklerini döner.
func (z vitrinZarfi) kimlikler() []string {
	out := make([]string, 0, len(z.Data))
	for _, urun := range z.Data {
		out = append(out, urun.ID)
	}

	return out
}

// vitrinKatalogu verilen publishable anahtarla mağaza listesini çağırır.
func vitrinKatalogu(t *testing.T, anahtar string, sorgu url.Values) vitrinZarfi {
	t.Helper()

	kayit := magazaIstegi(t, "/store/v1/products?"+sorgu.Encode(), anahtar)
	require.Equal(t, http.StatusOK, kayit.Code,
		"mağaza listesi 200 dönmeli; gövde: %s", kayit.Body.String())

	var zarf vitrinZarfi
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"mağaza listesi çözülemedi; gövde: %s", kayit.Body.String())

	return zarf
}

// koleksiyonSorgusu bir koleksiyona daraltan sorgu dizesini üretir.
func koleksiyonSorgusu(koleksiyonID string) url.Values {
	return url.Values{"collection_id": {koleksiyonID}}
}

// TestVitrinKataloguIsteginSatisKanalinaGoreSuzulur iki anahtarın AYNI uçtan
// iki farklı katalog aldığını doğrular.
//
// Üç ürünün üçü de yayındadır ve aynı koleksiyondadır; aralarındaki TEK fark
// kanal atamasıdır. Dolayısıyla listelerin ayrışmasının başka bir açıklaması
// yoktur.
func TestVitrinKataloguIsteginSatisKanalinaGoreSuzulur(t *testing.T) {
	zemin := kanalKataloguFiksturu(t)
	sorgu := koleksiyonSorgusu(zemin.koleksiyonID)

	birinci := vitrinKatalogu(t, publishableAnahtar, sorgu).kimlikler()
	assert.ElementsMatch(t,
		[]string{zemin.birinciKanalUrunu.id, zemin.atamasizUrun.id}, birinci,
		"birinci vitrin kendi ürününü ve ATAMASIZ ürünü görmeli")
	assert.NotContains(t, birinci, zemin.ikinciKanalUrunu.id,
		"başka bir kanala atanmış ürün bu vitrinde GÖRÜNMEMELİ; "+
			"göründüyse süzgeç isteğin kimliğine hiç bakmıyor demektir")

	ikinci := vitrinKatalogu(t, zemin.ikinciAnahtar, sorgu).kimlikler()
	assert.ElementsMatch(t,
		[]string{zemin.ikinciKanalUrunu.id, zemin.atamasizUrun.id}, ikinci,
		"ikinci vitrin kendi ürününü ve ATAMASIZ ürünü görmeli")
	assert.NotContains(t, ikinci, zemin.birinciKanalUrunu.id,
		"birinci kanalın ürünü ikinci vitrinde GÖRÜNMEMELİ")

	// Aynı ürünün bir vitrinde görünüp diğerinde görünmemesi, gizlemenin
	// sebebinin kanal olduğunu söyleyen tek gözlemdir: ürün silinmiş ya da
	// taslak olsaydı İKİSİNDE de görünmezdi.
	assert.Contains(t, ikinci, zemin.ikinciKanalUrunu.id,
		"birinci vitrinde gizlenen ürün, ait olduğu vitrinde görünmeli")
}

// TestVitrinSayaciSuzulmusKumeyiYansitir zarfın "count" alanının sayfalanmamış
// TOPLAMI değil SÜZÜLMÜŞ toplamı verdiğini doğrular.
//
// Sayaç süzülmemiş kümeyi gösterseydi vitrin istemcisi hiç dolmayan sayfalar
// ister, "3 sonuç" yazıp 2 ürün gösterirdi. Yönetim listesi aynı koleksiyonda
// ÜÇ ürün görür ve bu karşılaştırma iddiayı çıpalar: iki vitrinin 2 görmesinin
// sebebi ürünlerin eksikliği değil, süzgecin ta kendisidir.
func TestVitrinSayaciSuzulmusKumeyiYansitir(t *testing.T) {
	zemin := kanalKataloguFiksturu(t)
	sorgu := koleksiyonSorgusu(zemin.koleksiyonID)

	birinci := vitrinKatalogu(t, publishableAnahtar, sorgu)
	assert.Equal(t, 2, birinci.Count,
		"sayaç süzülmüş kümeyi saymalı; gövde sayısı: %d", len(birinci.Data))
	assert.Len(t, birinci.Data, birinci.Count,
		"tek sayfaya sığan bir sonuçta sayaç ile satır sayısı ayrışmamalı")

	ikinci := vitrinKatalogu(t, zemin.ikinciAnahtar, sorgu)
	assert.Equal(t, 2, ikinci.Count)
	assert.Len(t, ikinci.Data, ikinci.Count)

	// Yönetim listesi kanal süzgecine tabi DEĞİLDİR: yönetim kimliğinin bir
	// satış kanalı yoktur ve kataloğu bütün olarak görmesi gerekir.
	yonetim := yonetimKatalogu(t, sorgu)
	assert.Equal(t, 3, yonetim.Count,
		"yönetim listesi üç ürünü de saymalı; saymıyorsa süzgeç yanlış yere sızmış demektir")
	assert.ElementsMatch(t,
		[]string{zemin.birinciKanalUrunu.id, zemin.ikinciKanalUrunu.id, zemin.atamasizUrun.id},
		yonetim.kimlikler())
}

// yonetimKatalogu gizli anahtarla yönetim ürün listesini çağırır.
func yonetimKatalogu(t *testing.T, sorgu url.Values) vitrinZarfi {
	t.Helper()

	kayit := yonetimIstegi(t, http.MethodGet, "/admin/v1/products?"+sorgu.Encode(),
		"Bearer "+gizliAnahtar)
	require.Equal(t, http.StatusOK, kayit.Code,
		"yönetim listesi 200 dönmeli; gövde: %s", kayit.Body.String())

	var zarf vitrinZarfi
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"yönetim listesi çözülemedi; gövde: %s", kayit.Body.String())

	return zarf
}

// TestVitrinTekilUcuDaSuzulur listede gizlenen ürünün tekil uçtan da
// alınamadığını doğrular.
//
// Uç 404 döner ve bu, uygulamanın seçtiği koddur: servis görünmeyen ürün için
// yayında olmayan ürünle AYNI hatayı (errors.NotFound) üretir, çekirdek onu
// 404'e çevirir (bkz. service.Service.GetStoreProduct). Tekil uç süzülmeseydi
// gizleme tümüyle anlamsız olurdu — vitrin adresleri handle taşıdığı için
// tahmin edilmesi en kolay uç tam da budur; bu yüzden hem kimlik hem handle
// denenir.
func TestVitrinTekilUcuDaSuzulur(t *testing.T) {
	zemin := kanalKataloguFiksturu(t)

	testler := map[string]struct {
		anahtar  string
		adres    string
		beklenen int
	}{
		"yabancı kanalın ürünü kimlikle": {
			publishableAnahtar, zemin.ikinciKanalUrunu.id, http.StatusNotFound,
		},
		"yabancı kanalın ürünü handle ile": {
			publishableAnahtar, zemin.ikinciKanalUrunu.handle, http.StatusNotFound,
		},
		"kendi kanalının ürünü": {
			publishableAnahtar, zemin.birinciKanalUrunu.id, http.StatusOK,
		},
		"atamasız ürün birinci vitrinde": {
			publishableAnahtar, zemin.atamasizUrun.handle, http.StatusOK,
		},
		"gizlenen ürün kendi vitrininde": {
			zemin.ikinciAnahtar, zemin.ikinciKanalUrunu.id, http.StatusOK,
		},
		"birinci kanalın ürünü ikinci vitrinde": {
			zemin.ikinciAnahtar, zemin.birinciKanalUrunu.handle, http.StatusNotFound,
		},
		"atamasız ürün ikinci vitrinde": {
			zemin.ikinciAnahtar, zemin.atamasizUrun.id, http.StatusOK,
		},
	}

	for ad, tt := range testler {
		t.Run(ad, func(t *testing.T) {
			kayit := magazaIstegi(t, "/store/v1/products/"+tt.adres, tt.anahtar)

			assert.Equal(t, tt.beklenen, kayit.Code,
				"tekil vitrin ucu beklenen kodu dönmeli; gövde: %s", kayit.Body.String())
		})
	}
}

// TestGizlenenUrunVarliginiHataKoduylaEleVermez gizlenen ürünün 404'ünün, hiç
// var olmayan bir ürünün 404'ünden ayırt edilemediğini doğrular.
//
// Fark olsaydı gizleme delinirdi: bir rakip, elindeki publishable anahtarla
// hangi handle'ların BAŞKA bir kanalda satıldığını tek tek öğrenebilirdi.
//
// Karşılaştırma yalnızca hata KODU üzerindendir; mesaj istenen adresi
// yankıladığı için ("ürün bulunamadı: %s") iki istekte zaten farklıdır ve
// mesajın farklı olması bir sızıntı değildir — sızıntı, istemcinin kararını
// değiştiren SINIFTIR.
func TestGizlenenUrunVarliginiHataKoduylaEleVermez(t *testing.T) {
	zemin := kanalKataloguFiksturu(t)

	gizlenen := magazaIstegi(t, "/store/v1/products/"+zemin.ikinciKanalUrunu.handle, publishableAnahtar)
	olmayan := magazaIstegi(t, "/store/v1/products/e2e-hic-boyle-bir-urun-yok", publishableAnahtar)

	require.Equal(t, http.StatusNotFound, gizlenen.Code, "gövde: %s", gizlenen.Body.String())
	require.Equal(t, http.StatusNotFound, olmayan.Code, "gövde: %s", olmayan.Body.String())

	assert.Equal(t, hataOzu(t, olmayan)[0], hataOzu(t, gizlenen)[0],
		"gizlenen ürün ile olmayan ürün AYNI hata kodunu dönmeli")
}

// TestVitrinKanaliSorguDizesindenAlmaz istemcinin kanalı kendisinin
// seçemediğini doğrular.
//
// Handler kanalı sorgu dizesinden okusaydı süzgeç bir yetkilendirme olmaktan
// çıkıp bir görüntüleme tercihine dönüşürdü: elindeki herhangi bir publishable
// anahtarla gelen istemci, kanal kimliğini yazarak BAŞKA bir vitrinin
// kataloğunu okurdu. İddia handler'ın birim testinde de vardır ama orada
// kimliği testin kendisi koyar; burada onu üretimdeki koruma yığını koyar.
func TestVitrinKanaliSorguDizesindenAlmaz(t *testing.T) {
	zemin := kanalKataloguFiksturu(t)

	sorgu := koleksiyonSorgusu(zemin.koleksiyonID)
	sorgu.Set("sales_channel_id", zemin.ikinciKanalID)

	katalog := vitrinKatalogu(t, publishableAnahtar, sorgu)

	assert.NotContains(t, katalog.kimlikler(), zemin.ikinciKanalUrunu.id,
		"sorgu dizesindeki kanal kimliği YOK SAYILMALI; sayılmadıysa anahtar sahibi "+
			"başka bir vitrinin kataloğunu okuyabiliyor demektir")
	assert.ElementsMatch(t,
		[]string{zemin.birinciKanalUrunu.id, zemin.atamasizUrun.id}, katalog.kimlikler(),
		"katalog anahtarın KENDİ kanalına göre kalmalı")
	assert.Equal(t, 2, katalog.Count)
}

// TestSonKanalBagiKaldirilincaUrunHerVitrinde bağın kaldırılmasının ürünü
// gizlemediğini, TÜM vitrinlere açtığını doğrular.
//
// Kuralın doğrudan sonucudur ("ataması olmayan ürün her yerde görünür") ve
// şaşırtıcı olduğu için uçtan uca çivilenir: bir yöneticinin ürünü vitrinden
// KALDIRMAK için son bağı silmesi, tam tersini yapar.
//
// Test kendi koleksiyonunu ve kendi ürününü kurar; paylaşılan fikstürü
// değiştirseydi, ondan sonra koşan her sayaç iddiası testlerin sırasına bağlı
// hâle gelirdi.
func TestSonKanalBagiKaldirilincaUrunHerVitrinde(t *testing.T) {
	zemin := kanalKataloguFiksturu(t)
	ctx := t.Context()

	koleksiyon, err := urunSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E Kanal Bağı Kaldırma",
		Handle: "e2e-kanal-bagi-kaldirma",
	})
	require.NoError(t, err, "yalıtım koleksiyonu kurulamadı")

	urun, err := kanalKataloguUrunuKur(ctx, koleksiyon.ID, "kaldirma")
	require.NoError(t, err)
	require.NoError(t, kanalBagla(urun.id, zemin.ikinciKanalID))

	sorgu := koleksiyonSorgusu(koleksiyon.ID)
	require.Empty(t, vitrinKatalogu(t, publishableAnahtar, sorgu).kimlikler(),
		"ürün önce yalnızca ikinci vitrinde olmalı")
	require.Equal(t, []string{urun.id}, vitrinKatalogu(t, zemin.ikinciAnahtar, sorgu).kimlikler())

	kayit, err := yonetimGovdeliIstek(http.MethodDelete,
		"/admin/v1/products/"+urun.id+"/sales-channels/"+zemin.ikinciKanalID, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, kayit.Code,
		"kanal bağı kaldırılmalı; gövde: %s", kayit.Body.String())

	kalanlar, err := kanalListesiniCoz(kayit)
	require.NoError(t, err)
	require.Empty(t, kalanlar, "son bağ kaldırıldıktan sonra kanal listesi boşalmalı")

	assert.Equal(t, []string{urun.id}, vitrinKatalogu(t, publishableAnahtar, sorgu).kimlikler(),
		"ataması kalmayan ürün BİRİNCİ vitrinde de görünmeli")
	assert.Equal(t, []string{urun.id}, vitrinKatalogu(t, zemin.ikinciAnahtar, sorgu).kimlikler(),
		"ataması kalmayan ürün kendi eski vitrininde de görünmeye devam etmeli")
}
