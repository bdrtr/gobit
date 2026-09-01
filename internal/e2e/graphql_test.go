//go:build integration

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	authsvc "github.com/bdrtr/gobit/internal/modules/auth/service"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
	"github.com/bdrtr/gobit/internal/modules/product/graph"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya İKİNCİ okuma yüzeyini (GraphQL) gerçek modüller, gerçek Postgres ve
// ÜRETİM koruma yığınıyla sınar. Kanıtlanan tek cümle şudur:
//
//	Vitrinin görünürlük kuralı yüzeye göre değişmez.
//
// # Neden ikinci bir yüzey ayrıca sınanır
//
// Bu depoda bulunan her hatanın sınıfı aynıydı: "kural bir yerde tanımlı,
// başka yerde uygulanmamış". Satış kanalı doğrulanıyor ama okunmuyordu; arama
// katalog süzmesini atlayabiliyordu; yetki yalnızca auth'ta zorlanıyordu.
// İkinci bir okuma yüzeyi tam olarak o sınıfı sınar: aynı kuralın ikinci bir
// uygulaması sessizce ayrışır ve ayrıştığı gün ortaya çıkan şey bir hata
// mesajı değil, BAŞKA bir vitrinin katalogudur.
//
// # Neden HTTP'den, neden bu router
//
// Resolver'ın birim testi kanalı context'e testin KENDİSİ koyar; burada onu
// üretimdeki koruma yığını koyar (bkz. e2e_test.go, corehttp.APIGuards).
// Aradaki fark, "resolver context'i doğru okuyor" ile "uç üretimde gerçekten
// süzüyor" arasındaki farktır: publishable anahtarın kanalı auth'tan çözülecek,
// çekirdek onu Principal'a koyacak ve graph resolver'ı kanalı SORGUDAN değil o
// kimlikten alacaktır. Zincirin üç halkası da yalnızca burada aynı anda
// gözlemlenebilir.
//
// # Neden kendi ürünlerini kuruyor
//
// Fikstür kalıbı [kanalKataloguFiksturu] ile aynıdır (iki kanal, üç ürün) ama
// zemini PAYLAŞMAZ: bu dosyanın ürünlerinin varyantı, fiyatı ve stoğu vardır
// (GraphQL yüzeyinin varyant ağacını ve başka modüllerden gelen zenginleştirmeyi
// döndürdüğü ancak öyle görülebilir) ve komşu dosyanın sayaç iddiaları o
// eklemelerle bozulurdu. Ters yön de korunur: buradaki ürünler kendi
// koleksiyonlarında yalıtıldığı için komşu testlerin kataloglarına düşmezler.

// GraphQL zemininin fikstür sabitleri.
//
// Adlar SABİTTİR (fikstür sayacından üretilmez): kurulum süreç başına bir kez
// koşar ve sabit adlar bir hata mesajında hangi kaydın kastedildiğini tek
// bakışta okunur kılar (bkz. [kanalKataloguFiksturu], aynı gerekçe).
const (
	// grafKanalAdi bu dosyanın kurduğu İKİNCİ satış kanalının adıdır.
	//
	// [ikinciKanalAdi]'ndan ayrı bir kanaldır: kanal adı benzersizdir ve
	// komşu dosyanın kanalını ödünç almak, iki dosyanın ürünlerini tek bir
	// vitrinde toplardı.
	grafKanalAdi = "e2e-graphql-vitrin"
	// grafKoleksiyonHandle üç fikstür ürününü paylaşılan katalogdan ayıran
	// koleksiyonun handle'ıdır.
	grafKoleksiyonHandle = "e2e-graphql-katalogu"
	// grafOlmayanHandle hiçbir ürüne ait OLMAYAN handle'dır; gizlenen ürünün
	// hatasıyla karşılaştırmak için kullanılır.
	grafOlmayanHandle = "e2e-graphql-hic-boyle-bir-urun-yok"
	// grafSKU fikstürün stok kaleminin takip kodudur.
	grafSKU = "E2E-GRAPHQL-SKU"
	// grafParaBirimi fikstür fiyatının para birimidir.
	grafParaBirimi = "TRY"
	// grafBirimFiyat fikstür fiyatının minor unit cinsinden tutarıdır.
	grafBirimFiyat = 12_900
	// grafStok fikstür varyantının fiziksel adedidir.
	grafStok = 7
)

// grafUrunu fikstür ürününün testin ihtiyaç duyduğu alanlarıdır.
//
// Handle da taşınır çünkü tekil sorgu iki adresle de çağrılabilir ve süzgecin
// YALNIZCA kimlikle çalışması, vitrin adreslerinin (handle) süzgeçsiz kalması
// demek olurdu.
type grafUrunu struct {
	id       string
	handle   string
	varyanti string
}

// grafZemini iki vitrinli GraphQL senaryosunun kurulmuş zeminidir.
type grafZemini struct {
	// ikinciKanalID [testKanalID]'den ayrı ikinci satış kanalıdır.
	ikinciKanalID string
	// ikinciAnahtar YALNIZCA ikinci kanala bağlı publishable anahtardır.
	ikinciAnahtar string
	// koleksiyonID üç ürünün de bağlı olduğu yalıtım koleksiyonudur.
	koleksiyonID string
	// birinciKanalUrunu paylaşılan fikstür kanalına ([testKanalID]) atanmıştır
	// ve fiyatı ile stoğu OLAN tek üründür.
	birinciKanalUrunu grafUrunu
	// ikinciKanalUrunu yalnızca [grafZemini.ikinciKanalID]'ye atanmıştır.
	ikinciKanalUrunu grafUrunu
	// atamasizUrun hiçbir kanala atanmamıştır ve kural gereği İKİSİNDE de
	// görünmelidir.
	atamasizUrun grafUrunu
	// fiyatKumesiID birinci ürünün varyantına bağlanan fiyat kümesidir.
	fiyatKumesiID string
	// stokKalemID birinci ürünün varyantına bağlanan stok kalemidir.
	stokKalemID string
}

// Fikstürün bir kez kurulması için gereken durum.
var (
	// grafBirKez zeminin yalnızca bir kez kurulmasını sağlar.
	grafBirKez sync.Once
	// grafZemin kurulmuş zemindir.
	grafZemin grafZemini
	// grafKurulumHatasi kurulum hatasını testlere taşır.
	grafKurulumHatasi error
)

// grafFiksturu iki vitrinli GraphQL zeminini kurar ve döner.
//
// Kanal adı, anahtar ve ürün handle'ları BENZERSİZDİR; her testte yeniden
// kurmak ikinci çağrıda çakışırdı. Kurulum bu yüzden [sync.Once] içindedir ve
// hata dışarı taşınır (bkz. [kanalKataloguFiksturu], aynı örüntü).
func grafFiksturu(t *testing.T) grafZemini {
	t.Helper()

	grafBirKez.Do(func() {
		// Kurulum context'i t.Context() DEĞİLDİR: zemin testler arasında
		// paylaşılır ve ilk testin context'i o test bittiğinde iptal edilir.
		grafZemin, grafKurulumHatasi = grafZeminiKur(context.Background())
	})
	require.NoError(t, grafKurulumHatasi, "GraphQL fikstürü kurulamadı")

	return grafZemin
}

// grafZeminiKur ikinci kanalı, ikinci anahtarı ve üç ürünü hazırlar.
//
// Kanal ve anahtar SERVİSTEN, kanal ATAMALARI ise YÖNETİM UCUNDAN kurulur
// (bkz. [kanalBagla]): ilk ikisi bu testin konusu değildir, üçüncüsü GraphQL
// yüzeyinin okuduğu bağın ta kendisidir. Atamayı servisten yapmak, yönetim
// ucunun vitrinin okuduğu bağı yazdığını kanıtlamazdı.
func grafZeminiKur(ctx context.Context) (grafZemini, error) {
	var zemin grafZemini

	kanal, err := authSvc.CreateSalesChannel(ctx, authsvc.SalesChannelInput{
		Name:        grafKanalAdi,
		Description: "GraphQL uçtan uca testinin ikinci vitrini",
	})
	if err != nil {
		return zemin, fmt.Errorf("ikinci satış kanalı kurulamadı: %w", err)
	}
	zemin.ikinciKanalID = kanal.ID

	if _, zemin.ikinciAnahtar, err = authSvc.CreateAPIKey(ctx, authsvc.CreateAPIKeyInput{
		Type:      models.APIKeyPublishable,
		Title:     "e2e graphql publishable anahtar",
		CreatedBy: yoneticiID,
		// Anahtar YALNIZCA ikinci kanala bağlanır; iki kanal birden taşısaydı
		// süzgeç iddiasının tamamı anlamsızlaşırdı.
		SalesChannelIDs: []string{kanal.ID},
	}); err != nil {
		return zemin, fmt.Errorf("ikinci publishable anahtar kurulamadı: %w", err)
	}

	koleksiyon, err := urunSvc.CreateCollection(ctx, productsvc.CreateCollectionInput{
		Title:  "E2E GraphQL Kataloğu",
		Handle: grafKoleksiyonHandle,
	})
	if err != nil {
		return zemin, fmt.Errorf("yalıtım koleksiyonu kurulamadı: %w", err)
	}
	zemin.koleksiyonID = koleksiyon.ID

	if zemin.birinciKanalUrunu, err = grafUrunuKur(ctx, koleksiyon.ID, "birinci"); err != nil {
		return zemin, err
	}
	if zemin.ikinciKanalUrunu, err = grafUrunuKur(ctx, koleksiyon.ID, "ikinci"); err != nil {
		return zemin, err
	}
	if zemin.atamasizUrun, err = grafUrunuKur(ctx, koleksiyon.ID, "atamasiz"); err != nil {
		return zemin, err
	}

	if zemin.fiyatKumesiID, zemin.stokKalemID, err =
		grafVaryantiZenginlestir(ctx, zemin.birinciKanalUrunu.varyanti); err != nil {
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

// grafUrunuKur koleksiyona bağlı YAYINDA bir ürün ve tek varyantını oluşturur.
//
// Durum [productmodels.StatusPublished]'tır: taslak ürün vitrinde zaten
// görünmez ve o durumda ölçülen şey kanal süzgeci değil yayın süzgeci olurdu.
//
// Varyant, ürünün kendisi kadar zorunludur: GraphQL yüzeyinin iddiası
// "istemcinin istediği ağacı döner"dir ve varyantsız bir ürün o ağacın ikinci
// seviyesini hiç göstermezdi.
func grafUrunuKur(ctx context.Context, koleksiyonID, ad string) (grafUrunu, error) {
	handle := "e2e-graphql-" + ad

	urun, err := urunSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle:       handle,
		Title:        "E2E GraphQL " + ad,
		Status:       productmodels.StatusPublished,
		CollectionID: &koleksiyonID,
	})
	if err != nil {
		return grafUrunu{}, fmt.Errorf("%q ürünü kurulamadı: %w", handle, err)
	}

	varyant, err := urunSvc.CreateVariant(ctx, urun.ID, productsvc.CreateVariantInput{
		Title: "E2E GraphQL " + ad + " varyantı",
	})
	if err != nil {
		return grafUrunu{}, fmt.Errorf("%q varyantı kurulamadı: %w", handle, err)
	}

	return grafUrunu{id: urun.ID, handle: urun.Handle, varyanti: varyant.ID}, nil
}

// grafVaryantiZenginlestir varyanta fiyat kümesi ve stok kalemi bağlar.
//
// Zenginleştirme BAŞKA MODÜLLERİN kayıtlarıdır (pricing, inventory) ve product
// onları import etmez; ikisi de varyant kimliği üzerinden link'lerle çözülür.
// GraphQL yüzeyi bu kayıtları JSON skaları olarak döner, yani alanları
// yorumlamaz — sınanan şey de tam olarak budur: ikinci okuma yüzeyi
// zenginleştirmeyi kendi yolundan değil, vitrin servisinin AYNI Query
// katmanından alır.
func grafVaryantiZenginlestir(ctx context.Context, varyantID string) (fiyatKumesiID, stokKalemID string, err error) {
	kume, err := fiyatSvc.CreatePriceSet(ctx, []pricingsvc.PriceInput{{
		CurrencyCode: grafParaBirimi,
		Amount:       grafBirimFiyat,
		MinQuantity:  1,
	}})
	if err != nil {
		return "", "", fmt.Errorf("fikstür fiyat kümesi oluşturulamadı: %w", err)
	}
	if err := urunSvc.SetVariantPriceSet(ctx, varyantID, kume.ID); err != nil {
		return "", "", fmt.Errorf("varyant fiyat kümesine bağlanamadı: %w", err)
	}

	kalem, err := stokSvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   grafSKU,
		Title: "E2E GraphQL stok kalemi",
	})
	if err != nil {
		return "", "", fmt.Errorf("fikstür stok kalemi oluşturulamadı: %w", err)
	}
	if err := urunSvc.SetVariantInventoryItem(ctx, varyantID, kalem.ID); err != nil {
		return "", "", fmt.Errorf("varyant stok kalemine bağlanamadı: %w", err)
	}
	if _, err := stokSvc.SetInventoryLevel(ctx, kalem.ID, stokLokasyonID, grafStok); err != nil {
		return "", "", fmt.Errorf("fikstür stok seviyesi yazılamadı: %w", err)
	}

	return kume.ID, kalem.ID, nil
}

// Sorgu belgeleri.
//
// Belgeler DEĞİŞKENLERLE yazılır (satır içi değerlerle değil): vitrin
// istemcisi de böyle yazar ve "kanal bir değişkenle zorlanabilir mi" sorusu
// ancak değişken taşıyan gerçek bir belgede sorulabilir.
const (
	// grafKatalogBelgesi koleksiyonla daraltılmış vitrin listesini ister.
	//
	// Varyant ağacı ve zenginleştirme alanları BİLİNÇLİ olarak istenir: liste
	// yalnızca kimlikleri döndürseydi, yüzeyin "istemcinin istediği ağacı
	// döner" iddiası hiç sınanmamış olurdu.
	grafKatalogBelgesi = `
	  query Katalog($koleksiyon: ID) {
	    products(collectionId: $koleksiyon) {
	      count
	      offset
	      limit
	      items {
	        id
	        handle
	        title
	        variants { id title priceSet inventoryItem }
	      }
	    }
	  }`

	// grafTekilBelgesi tek bir ürünü kimlik ya da handle ile ister.
	//
	// İkisi de değişkendir ve çağıran YALNIZCA birini doldurur; verilmeyen
	// argüman null gider, resolver onu "verilmemiş" sayar.
	grafTekilBelgesi = `
	  query Urun($kimlik: ID, $handle: String) {
	    product(id: $kimlik, handle: $handle) { id handle }
	  }`

	// grafEnDerinBelge şemanın izin verdiği en derin VERİ yoludur (5 seviye).
	grafEnDerinBelge = `
	  query EnDerin($koleksiyon: ID) {
	    products(collectionId: $koleksiyon) {
	      items { variants { optionValues { optionTitle } } }
	    }
	  }`

	// grafZenginBelge karmaşıklık kapısını AŞAN ama başka hiçbir kapıya
	// takılmayan belgedir: alanların hepsi FARKLIDIR (yani alan tekrarı 1'dir)
	// ve sayfa tavanı 100'dür. Maliyet, kök maliyeti + sayfa boyutu × alan
	// sayısı olarak hesaplandığı için üretim tavanının (50.000) çok üstüne
	// çıkar.
	//
	// Belgenin takma ad KULLANMAMASI bilinçlidir: takma adla yığmak artık
	// daha erken ve daha ucuz bir kapıya (alan tekrarı) takılır ve bu test o
	// zaman karmaşıklık kapısını HİÇ sınamazdı — kapılardan biri kaldırılsa
	// bile yeşil kalırdı.
	grafZenginBelge = `
	  query Zengin {
	    products(limit: 100) {
	      items {
	        id handle title subtitle description thumbnail isGiftcard
	        discountable weight length height width material originCountry
	        collectionId metadata createdAt updatedAt
	        variants {
	          id productId title sku barcode ean upc manageInventory
	          allowBackorder weight rank metadata createdAt updatedAt
	          priceSet inventoryItem
	          optionValues { id optionId value rank optionTitle }
	        }
	        options { id productId title rank values { id value } }
	        images { id productId url rank metadata }
	        tags { id value }
	        categories { id name handle description parentId isActive rank }
	      }
	      count offset limit
	    }
	  }`
)

// grafVaryantGorunumu yanıttaki varyantın okunan alanlarıdır.
type grafVaryantGorunumu struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// PriceSet ve InventoryItem şemada JSON skalarıdır: pricing ve
	// inventory'nin kayıtları bu modüle gevşek tipli gelir ve öyle döner.
	PriceSet      map[string]any `json:"priceSet"`
	InventoryItem map[string]any `json:"inventoryItem"`
}

// grafUrunGorunumu yanıttaki ürünün okunan alanlarıdır.
type grafUrunGorunumu struct {
	ID       string                `json:"id"`
	Handle   string                `json:"handle"`
	Title    string                `json:"title"`
	Variants []grafVaryantGorunumu `json:"variants"`
}

// grafZarfi GraphQL yanıtının test tarafındaki karşılığıdır.
//
// data ve errors BİRLİKTE çözülür: GraphQL'de ikisi aynı yanıtta bulunabilir
// ve bu testlerin bir kısmı tam olarak "hata var mı, veri ne oldu" sorusunu
// birlikte sorar (gizlenen ürün: product null, errors dolu).
type grafZarfi struct {
	Data struct {
		Products struct {
			Items  []grafUrunGorunumu `json:"items"`
			Count  int                `json:"count"`
			Offset int                `json:"offset"`
			Limit  int                `json:"limit"`
		} `json:"products"`
		Product *grafUrunGorunumu `json:"product"`
	} `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

// kimlikler listedeki ürün kimliklerini döner.
func (z grafZarfi) kimlikler() []string {
	out := make([]string, 0, len(z.Data.Products.Items))
	for _, urun := range z.Data.Products.Items {
		out = append(out, urun.ID)
	}

	return out
}

// hataKodu ilk hatanın extensions.code alanını döner.
//
// Kod, çekirdeğin hata zarfından gelir (bkz. graph.NewHandler'ın hata
// sunucusu): iki okuma yüzeyi aynı sözlüğü konuşur ve testler bunu yüzeyler
// ARASINDA karşılaştırabilir.
func (z grafZarfi) hataKodu(t *testing.T) string {
	t.Helper()

	require.NotEmpty(t, z.Errors, "yanıt en az bir hata taşımalıydı")

	kod, _ := z.Errors[0].Extensions["code"].(string)

	return kod
}

// grafIstegi belgeyi GraphQL ucuna POST eder ve HAM yanıtı döner.
//
// Anahtar boşsa başlık HİÇ eklenmez: "başlık yok" ile "boş başlık" farklı
// durumlardır ve 401 iddiası ilkini hedefler ([magazaIstegi] ile aynı gerekçe).
//
// Adres [graph.Path]'ten okunur, elle yazılmaz: yol modülün sabitidir ve
// testin kendi kopyası, uç taşındığında sessizce yanlış yeri sınardı.
func grafIstegi(t *testing.T, anahtar, belge string, degiskenler map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	istek := map[string]any{"query": belge}
	if degiskenler != nil {
		istek["variables"] = degiskenler
	}

	govde, err := json.Marshal(istek)
	require.NoError(t, err, "GraphQL istek gövdesi kodlanamadı")

	req := httptest.NewRequest(http.MethodPost, graph.Path, bytes.NewReader(govde))
	req.Header.Set("Content-Type", "application/json")
	if anahtar != "" {
		req.Header.Set(corehttp.PublishableKeyHeader, anahtar)
	}

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, req)

	return kayit
}

// grafCoz yanıt gövdesini GraphQL zarfı olarak çözer.
func grafCoz(t *testing.T, kayit *httptest.ResponseRecorder) grafZarfi {
	t.Helper()

	var zarf grafZarfi
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"GraphQL yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return zarf
}

// grafSorgu belgeyi çalıştırır ve HATASIZ bir yanıt bekler.
//
// Durum kodu da denetlenir: GraphQL yanıtı 200'dür ve hatalar gövdededir, ama
// belge çalıştırıcıya HİÇ ulaşamazsa (yetkisiz istek, sığmayan gövde) yanıt
// çekirdeğin zarfıdır — o durumda "errors boş" iddiası yanıltıcı biçimde
// geçerdi.
func grafSorgu(t *testing.T, anahtar, belge string, degiskenler map[string]any) grafZarfi {
	t.Helper()

	kayit := grafIstegi(t, anahtar, belge, degiskenler)
	require.Equal(t, http.StatusOK, kayit.Code,
		"GraphQL isteği 200 dönmeli; gövde: %s", kayit.Body.String())

	zarf := grafCoz(t, kayit)
	require.Empty(t, zarf.Errors, "GraphQL yanıtı hatasız olmalı; gövde: %s", kayit.Body.String())

	return zarf
}

// grafKatalogu verilen anahtarla koleksiyona daraltılmış listeyi çağırır.
//
// ek, belgenin BİLDİRMEDİĞİ değişkenleri eklemek içindir (bkz.
// [TestGraphQLKanalDegiskenlerdenZorlanamaz]); nil verilirse yalnızca
// koleksiyon değişkeni gider.
func grafKatalogu(t *testing.T, anahtar, koleksiyonID string, ek map[string]any) grafZarfi {
	t.Helper()

	degiskenler := map[string]any{"koleksiyon": koleksiyonID}
	for ad, deger := range ek {
		degiskenler[ad] = deger
	}

	return grafSorgu(t, anahtar, grafKatalogBelgesi, degiskenler)
}

// TestGraphQLUcuPublishableAnahtarsizReddedilir koruma yığınının yeni ucu
// KENDİLİĞİNDEN kapsadığını doğrular.
//
// Uç /store/v1 altına konduğu için kimlik doğrulaması onun kodunda hiç
// yazmaz; korumayı önek yığını uygular. "Otomatik olmalı" bir tasarım
// iddiasıdır ve doğrulanmadığı sürece bir varsayımdır: modül ucu başka bir
// önekte açsaydı ya da yığın önekleri değişseydi, GraphQL yüzeyi hiçbir testi
// kırmadan kimliksiz açılırdı.
//
// Yanıt GraphQL zarfı DEĞİL çekirdeğin hata zarfıdır ve bu da denetlenir:
// belge çalıştırıcıya hiç ulaşmamıştır, dolayısıyla "data" alanı olmamalıdır.
// Kimliksiz bir isteğe 200 + errors dönmek, istemciye sorgusunun çalıştığını
// ama boş sonuç verdiğini söylerdi.
func TestGraphQLUcuPublishableAnahtarsizReddedilir(t *testing.T) {
	zemin := grafFiksturu(t)
	degiskenler := map[string]any{"koleksiyon": zemin.koleksiyonID}

	anahtarsiz := grafIstegi(t, "", grafKatalogBelgesi, degiskenler)
	require.Equal(t, http.StatusUnauthorized, anahtarsiz.Code,
		"publishable anahtarsız GraphQL isteği reddedilmeli; gövde: %s", anahtarsiz.Body.String())

	var ham map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(anahtarsiz.Body.Bytes(), &ham),
		"hata gövdesi çözülemedi: %s", anahtarsiz.Body.String())
	assert.NotContains(t, ham, "data",
		"çalıştırıcıya ulaşmamış istek GraphQL zarfı DÖNMEMELİ")
	assert.Contains(t, ham, "error", "yanıt çekirdeğin hata zarfı olmalı")

	// Gizli anahtar mağaza başlığında geçmez: geçseydi, vitrin kodunun içine
	// gömülen bir anahtar yönetim yetkisi taşırdı (bkz.
	// [TestGizliAnahtarMagazaBasligindaGecmez], REST'teki aynı iddia).
	gizli := grafIstegi(t, gizliAnahtar, grafKatalogBelgesi, degiskenler)
	assert.Equal(t, http.StatusUnauthorized, gizli.Code,
		"gizli anahtar GraphQL ucunda kabul edilmemeli; gövde: %s", gizli.Body.String())

	// Karşı taraf da sınanır: her isteği reddeden bir uç, yukarıdaki iki
	// iddiayı da geçerdi.
	anahtarli := grafIstegi(t, publishableAnahtar, grafKatalogBelgesi, degiskenler)
	assert.Equal(t, http.StatusOK, anahtarli.Code,
		"publishable anahtarla GraphQL isteği geçmeli; gövde: %s", anahtarli.Body.String())
}

// TestGraphQLUcuYalnizcaPOSTKabulEder GET taşımasının açılmadığını doğrular.
//
// Karar bilinçlidir (bkz. graph.NewHandler): GET'in tek getirisi ara
// önbelleklerdir ve yanıt publishable anahtara — yani satış kanalına — göre
// değiştiği için o getiri burada yoktur; bedeli ise sorgunun URL'ye, loglara
// ve tarayıcı geçmişine düşmesidir. Uç chi'ye yalnızca POST ile kaydedildiği
// için GET, gqlgen'in "transport not supported" 400'ü yerine dürüst bir 405
// alır — ve bu ancak GERÇEK router'da görülebilir.
func TestGraphQLUcuYalnizcaPOSTKabulEder(t *testing.T) {
	kayit := magazaIstegi(t, graph.Path, publishableAnahtar)

	assert.Equal(t, http.StatusMethodNotAllowed, kayit.Code,
		"GraphQL ucu GET kabul etmemeli; gövde: %s", kayit.Body.String())
	assert.NotContains(t, kayit.Body.String(), `"data"`,
		"GET isteği bir GraphQL işlemi olarak ÇALIŞTIRILMAMALI")
}

// TestGraphQLListesiUrunleriVeVaryantlariniDoner yüzeyin istenen ağacı
// gerçekten döndürdüğünü doğrular.
//
// Fiyat ve stok ayrıca denetlenir: ikisi de BAŞKA modüllerin kayıtlarıdır ve
// GraphQL'e kendi yolundan değil, REST vitrininin kullandığı AYNI Query
// katmanından gelir. Sahte bir servisle koşan birim testi bunu göremez —
// oradaki kayıtları testin kendisi doldurur; burada onları pricing ve
// inventory modülleri gerçekten üretir.
func TestGraphQLListesiUrunleriVeVaryantlariniDoner(t *testing.T) {
	zemin := grafFiksturu(t)

	zarf := grafKatalogu(t, publishableAnahtar, zemin.koleksiyonID, nil)

	require.Equal(t, 2, zarf.Data.Products.Count,
		"birinci vitrin kendi ürününü ve atamasız ürünü saymalı")
	require.Len(t, zarf.Data.Products.Items, 2)

	var birinci grafUrunGorunumu
	for _, urun := range zarf.Data.Products.Items {
		if urun.ID == zemin.birinciKanalUrunu.id {
			birinci = urun
		}
	}
	require.Equal(t, zemin.birinciKanalUrunu.id, birinci.ID,
		"birinci kanalın ürünü listede olmalı")
	assert.Equal(t, zemin.birinciKanalUrunu.handle, birinci.Handle)
	assert.Equal(t, "E2E GraphQL birinci", birinci.Title)

	require.Len(t, birinci.Variants, 1, "ürünün varyant ağacı dönmeli")
	varyant := birinci.Variants[0]
	assert.Equal(t, zemin.birinciKanalUrunu.varyanti, varyant.ID)

	require.NotNil(t, varyant.PriceSet, "varyantın fiyat kümesi dönmeli")
	assert.Equal(t, zemin.fiyatKumesiID, varyant.PriceSet["id"],
		"fiyat kümesi pricing modülünün kaydı olmalı")
	fiyatlar, ok := varyant.PriceSet["prices"].([]any)
	require.True(t, ok, "fiyat kümesi fiyatlarıyla birlikte dönmeli: %v", varyant.PriceSet)
	require.Len(t, fiyatlar, 1)
	fiyat, ok := fiyatlar[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, grafParaBirimi, fiyat["currency_code"])
	assert.EqualValues(t, grafBirimFiyat, fiyat["amount"])

	require.NotNil(t, varyant.InventoryItem, "varyantın stok kalemi dönmeli")
	assert.Equal(t, zemin.stokKalemID, varyant.InventoryItem["id"])
	assert.Equal(t, grafSKU, varyant.InventoryItem["sku"])
	assert.EqualValues(t, grafStok, varyant.InventoryItem["available_quantity"],
		"satılabilir adet inventory modülünden gelmeli")

	// Sayfalama zarfı da yankılanır: değerleri SERVİS uygular (istemci limit
	// vermedi), yüzey kendi varsayılanını uydurmaz.
	assert.Equal(t, 0, zarf.Data.Products.Offset)
	assert.Positive(t, zarf.Data.Products.Limit,
		"uygulanan sayfa boyutu servisin varsayılanı olmalı")
}

// TestGraphQLKataloguSatisKanalinaGoreSuzulur bu dosyanın ASIL iddiasıdır:
// aynı belge, iki anahtarla iki farklı katalog döner.
//
// Üç ürünün üçü de yayındadır ve aynı koleksiyondadır; aralarındaki TEK fark
// kanal atamasıdır. Dolayısıyla listelerin ayrışmasının başka bir açıklaması
// yoktur.
//
// Tek anahtarla "ürün görünmüyor" gözlemi hiçbir şey kanıtlamaz (ürün silinmiş,
// taslak kalmış ya da sorgu bozulmuş da olabilir); ikinci anahtarın AYNI ürünü
// GÖRMESİ, gizlemenin sebebinin tam olarak kanal olduğunu söyleyen tek
// gözlemdir.
func TestGraphQLKataloguSatisKanalinaGoreSuzulur(t *testing.T) {
	zemin := grafFiksturu(t)

	birinci := grafKatalogu(t, publishableAnahtar, zemin.koleksiyonID, nil).kimlikler()
	assert.ElementsMatch(t,
		[]string{zemin.birinciKanalUrunu.id, zemin.atamasizUrun.id}, birinci,
		"birinci vitrin kendi ürününü ve ATAMASIZ ürünü görmeli")
	assert.NotContains(t, birinci, zemin.ikinciKanalUrunu.id,
		"başka bir kanala atanmış ürün bu vitrinde GÖRÜNMEMELİ; göründüyse "+
			"GraphQL yüzeyi isteğin kimliğine hiç bakmıyor demektir")

	ikinci := grafKatalogu(t, zemin.ikinciAnahtar, zemin.koleksiyonID, nil).kimlikler()
	assert.ElementsMatch(t,
		[]string{zemin.ikinciKanalUrunu.id, zemin.atamasizUrun.id}, ikinci,
		"ikinci vitrin kendi ürününü ve ATAMASIZ ürünü görmeli")
	assert.NotContains(t, ikinci, zemin.birinciKanalUrunu.id,
		"birinci kanalın ürünü ikinci vitrinde GÖRÜNMEMELİ")

	assert.Contains(t, ikinci, zemin.ikinciKanalUrunu.id,
		"birinci vitrinde gizlenen ürün, ait olduğu vitrinde görünmeli")
}

// TestGraphQLTekilSorgusuDaSuzulur listede gizlenen ürünün tekil sorguyla da
// alınamadığını doğrular.
//
// Tekil sorgu süzülmeseydi gizleme tümüyle anlamsız olurdu: vitrin adresleri
// handle taşır, yani tahmin edilmesi en kolay sorgu tam da budur. Bu yüzden
// hem kimlik hem handle denenir.
//
// Gizlenen ürünün hatası, HİÇ VAR OLMAYAN bir handle'ın hatasıyla aynı kodu
// taşımalıdır. Fark olsaydı gizleme delinirdi: bir rakip, elindeki publishable
// anahtarla hangi handle'ların BAŞKA bir kanalda satıldığını tek tek
// öğrenebilirdi.
func TestGraphQLTekilSorgusuDaSuzulur(t *testing.T) {
	zemin := grafFiksturu(t)

	kendi := grafSorgu(t, publishableAnahtar, grafTekilBelgesi,
		map[string]any{"kimlik": zemin.birinciKanalUrunu.id})
	require.NotNil(t, kendi.Data.Product, "kendi kanalının ürünü dönmeli")
	assert.Equal(t, zemin.birinciKanalUrunu.handle, kendi.Data.Product.Handle)

	// Gizlenen ürün: hata GraphQL zarfında döner ve alan null'dur.
	gizlenen := grafCoz(t, grafIstegi(t, publishableAnahtar, grafTekilBelgesi,
		map[string]any{"handle": zemin.ikinciKanalUrunu.handle}))
	assert.Nil(t, gizlenen.Data.Product,
		"yabancı kanalın ürünü GraphQL'de de null dönmeli")

	olmayan := grafCoz(t, grafIstegi(t, publishableAnahtar, grafTekilBelgesi,
		map[string]any{"handle": grafOlmayanHandle}))
	assert.Nil(t, olmayan.Data.Product)

	assert.Equal(t, olmayan.hataKodu(t), gizlenen.hataKodu(t),
		"gizlenen ürün ile olmayan ürün AYNI hata kodunu dönmeli")

	// Ürün kendi vitrininde görünür: 404'ün sebebi ürünün eksikliği değil,
	// süzgecin ta kendisidir.
	sahibi := grafSorgu(t, zemin.ikinciAnahtar, grafTekilBelgesi,
		map[string]any{"handle": zemin.ikinciKanalUrunu.handle})
	require.NotNil(t, sahibi.Data.Product, "gizlenen ürün kendi vitrininde görünmeli")
	assert.Equal(t, zemin.ikinciKanalUrunu.id, sahibi.Data.Product.ID)
}

// TestGraphQLKanalDegiskenlerdenZorlanamaz istemcinin kanalı kendisinin
// seçemediğini doğrular.
//
// Üç kapı da denenir çünkü GraphQL'de bir değeri sunucuya iletmenin üç yolu
// vardır: argüman, değişken sözlüğü ve isteğin sorgu dizesi. Kanal bunlardan
// biriyle bile zorlanabilseydi süzgeç bir yetkilendirme olmaktan çıkıp
// görüntüleme tercihine dönerdi: elindeki herhangi bir publishable anahtarla
// gelen istemci BAŞKA bir vitrinin katalogunu okurdu.
func TestGraphQLKanalDegiskenlerdenZorlanamaz(t *testing.T) {
	zemin := grafFiksturu(t)
	beklenen := []string{zemin.birinciKanalUrunu.id, zemin.atamasizUrun.id}

	// 1. Argüman olarak: şemada böyle bir argüman YOKTUR ve belge doğrulamada
	// reddedilir. Yanıt 422'dir (gqlgen doğrulama hatalarını protokol hatası
	// sayar) ve katalog hiç okunmaz.
	arguman := grafIstegi(t, publishableAnahtar, `
	  query Zorla($kanal: [ID!]) {
	    products(collectionId: "`+zemin.koleksiyonID+`", salesChannelIds: $kanal) { count }
	  }`, map[string]any{"kanal": []string{zemin.ikinciKanalID}})

	assert.Equal(t, http.StatusUnprocessableEntity, arguman.Code,
		"şemada olmayan argüman doğrulamada reddedilmeli; gövde: %s", arguman.Body.String())
	zarf := grafCoz(t, arguman)
	require.NotEmpty(t, zarf.Errors)
	assert.Contains(t, zarf.Errors[0].Message, "salesChannelIds")
	assert.Empty(t, zarf.Data.Products.Items, "reddedilen belge katalog döndürmemeli")

	// 2. Değişken sözlüğüne SIZDIRARAK: belge bildirmediği için değişken yok
	// sayılır. Sessizce yok sayılması doğrudur ama testin sorusu farklıdır —
	// yok sayılmasaydı, bir gün eklenecek bir "değişkenden süz" kısayolu bu
	// isteği kabul ederdi.
	kacamak := grafKatalogu(t, publishableAnahtar, zemin.koleksiyonID,
		map[string]any{"salesChannelIds": []string{zemin.ikinciKanalID}})
	assert.ElementsMatch(t, beklenen, kacamak.kimlikler(),
		"bildirilmemiş değişken katalogu DEĞİŞTİRMEMELİ")

	// 3. Sorgu dizesiyle: GraphQL ucu gövdeyi okur, ama REST vitrini kanalı
	// sorgu dizesinden almadığı gibi (bkz. [TestVitrinKanaliSorguDizesindenAlmaz])
	// bu uç da almamalıdır. Yol farklı, tuzak aynıdır.
	govde, err := json.Marshal(map[string]any{
		"query":     grafKatalogBelgesi,
		"variables": map[string]any{"koleksiyon": zemin.koleksiyonID},
	})
	require.NoError(t, err)

	istek := httptest.NewRequest(http.MethodPost,
		graph.Path+"?sales_channel_id="+zemin.ikinciKanalID, bytes.NewReader(govde))
	istek.Header.Set("Content-Type", "application/json")
	istek.Header.Set(corehttp.PublishableKeyHeader, publishableAnahtar)

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)
	require.Equal(t, http.StatusOK, kayit.Code, "gövde: %s", kayit.Body.String())

	assert.ElementsMatch(t, beklenen, grafCoz(t, kayit).kimlikler(),
		"sorgu dizesindeki kanal kimliği YOK SAYILMALI")
}

// TestGraphQLveRESTAyniKumeyiDoner iki okuma yüzeyinin AYNI görünürlük
// kuralını uyguladığını doğrular.
//
// İddia bu dosyadaki en değerli olanıdır: yüzeyler ayrışırsa, ayrışma bir hata
// mesajıyla değil SESSİZCE olur — birinde gizlenen ürün diğerinde görünür ve
// kimse iki listeyi yan yana koymadıkça bunu fark etmez. Karşılaştırma bu
// yüzden aynı anahtarla, aynı koleksiyonda ve İKİ anahtar için de yapılır:
// tek anahtarla yapılsaydı, "her iki yüzey de kimliği yok sayıyor" durumu da
// testi geçerdi.
func TestGraphQLveRESTAyniKumeyiDoner(t *testing.T) {
	zemin := grafFiksturu(t)
	sorgu := koleksiyonSorgusu(zemin.koleksiyonID)

	anahtarlar := map[string]string{
		"birinci vitrin": publishableAnahtar,
		"ikinci vitrin":  zemin.ikinciAnahtar,
	}

	for ad, anahtar := range anahtarlar {
		t.Run(ad, func(t *testing.T) {
			rest := vitrinKatalogu(t, anahtar, sorgu)
			graf := grafKatalogu(t, anahtar, zemin.koleksiyonID, nil)

			assert.ElementsMatch(t, rest.kimlikler(), graf.kimlikler(),
				"REST ve GraphQL aynı ürün kümesini dönmeli; ayrışırlarsa iki yüzey "+
					"iki farklı görünürlük kuralı uyguluyor demektir")
			assert.Equal(t, rest.Count, graf.Data.Products.Count,
				"iki yüzeyin sayacı da AYNI süzülmüş kümeyi saymalı")
		})
	}

	// Tekil uç da karşılaştırılır: gizlenen ürün için REST 404 döner, GraphQL
	// null + hata döner. Kodlar AYNI sözlükten gelmelidir — çekirdeğin hata
	// zarfı iki yüzeyde de tek bir yerden yazılır (bkz. graph.NewHandler).
	restGizlenen := magazaIstegi(t,
		"/store/v1/products/"+zemin.ikinciKanalUrunu.handle, publishableAnahtar)
	require.Equal(t, http.StatusNotFound, restGizlenen.Code,
		"REST gizlenen ürün için 404 dönmeli; gövde: %s", restGizlenen.Body.String())

	grafGizlenen := grafCoz(t, grafIstegi(t, publishableAnahtar, grafTekilBelgesi,
		map[string]any{"handle": zemin.ikinciKanalUrunu.handle}))
	require.Nil(t, grafGizlenen.Data.Product)

	assert.Equal(t, hataOzu(t, restGizlenen)[0], grafGizlenen.hataKodu(t),
		"iki yüzey aynı ürün için AYNI hata kodunu dönmeli")
}

// grafTakmaAdliYigma aynı kök sorguyu n kez takma adla tekrarlayan belgeyi
// üretir.
//
// GraphQL'in REST'te karşılığı olmayan çarpanı budur: aşağıdaki belge TEK bir
// HTTP isteğidir, yani hız sınırlayıcı için BİR sayaçtır, sunucu için ise n
// katalog sorgusudur.
func grafTakmaAdliYigma(n int) string {
	var belge strings.Builder

	belge.WriteString("{")

	for i := range n {
		fmt.Fprintf(&belge, " a%d: products { count }", i)
	}

	belge.WriteString(" }")

	return belge.String()
}

// TestGraphQLKarmasiklikSiniriUretimYiginindaUygulanir sınırların üretim
// kurulumunda GERÇEKTEN bağlı olduğunu doğrular.
//
// Sınırların davranışı birim testlerinde ayrıntısıyla sınanır; burada sınanan
// şey BAĞLANTIDIR: modül bu zeminde de üretimdeki gibi SIFIR değerli
// seçeneklerle kurulur (bkz. e2e_test.go, product modülünün eklendiği satır)
// ve sıfır değerin "sınırsız" DEĞİL "paket varsayılanı" anlamına gelmesi,
// ancak gerçek kurulumda görülebilir. Ayar
// yolunda bir gün bir kopya oluşursa (modül kendi varsayılanını seçerse)
// birim testleri yeşil kalır, bu test kırmızı yanar.
//
// # Derinlik sınırı neden burada değil
//
// Bugünkü şema DÖNGÜSEL DEĞİLDİR: en derin meşru yol 5 seviyedir
// ([grafEnDerinBelge]) ve varsayılan sınır 10'dur, yani ÜRETİM ayarlarıyla
// derinlik kapısını aşan GEÇERLİ bir belge yazılamaz — daha derin her belge
// şemada olmayan bir alan ister ve doğrulamada, derinlik ölçülmeden ölür.
// Kapının reddetme yanı bu yüzden sınırın düşürülebildiği birim testine
// aittir (graph.TestDerinlikSiniriAsilanBelgeyiReddeder); buraya düşen yan,
// kapının MEŞRU belgeyi geçirdiğidir ve o da aşağıda sınanır — üretimde
// yanlış kalibre edilmiş bir derinlik sınırının belirtisi tam olarak budur:
// vitrinin en derin sorgusu bir gün sessizce reddedilmeye başlar.
func TestGraphQLKarmasiklikSiniriUretimYiginindaUygulanir(t *testing.T) {
	zemin := grafFiksturu(t)

	kayit := grafIstegi(t, publishableAnahtar, grafZenginBelge, nil)

	// Sınır aşımı HTTP 200 içinde errors ile döner: gqlgen'in karmaşıklık
	// hatası "kullanıcı hatası" sınıfındadır ve depo errcode kaydını bilinçli
	// olarak değiştirmez (süreç genelindeki bir haritayı tek modülün
	// değiştirmesi olurdu).
	require.Equal(t, http.StatusOK, kayit.Code, "gövde: %s", kayit.Body.String())

	zarf := grafCoz(t, kayit)
	require.NotEmpty(t, zarf.Errors, "tavanı aşan sayfa × alan çarpımı reddedilmeli")
	assert.Contains(t, zarf.Errors[0].Message, "complexity")
	assert.Equal(t, "COMPLEXITY_LIMIT_EXCEEDED", zarf.Errors[0].Extensions["code"])
	assert.Empty(t, zarf.Data.Products.Items, "reddedilen belge katalog döndürmemeli")

	// Kalibrasyonun öteki yanı: her belgeyi reddeden bir sınır da yukarıdaki
	// iddiayı geçerdi. Şemanın en derin ve zenginleştirmeli meşru belgesi
	// üretim ayarlarıyla GEÇMELİDİR.
	mesru := grafSorgu(t, publishableAnahtar, grafEnDerinBelge,
		map[string]any{"koleksiyon": zemin.koleksiyonID})
	assert.Len(t, mesru.Data.Products.Items, 2,
		"en derin meşru belge üretim sınırlarıyla çalışmalı")
}

// TestGraphQLAlanTekrariSiniriUretimYiginindaUygulanir aynı alanı takma adlarla
// çoğaltan belgenin üretim yığınında reddedildiğini doğrular.
//
// # Neden karmaşıklık kapısından AYRI bir test
//
// Karmaşıklık modeli alan SAYISINI fiyatlar, BAYT'ı değil: aynı ağır alanı
// (örneğin description) yüzlerce takma adla seçen bir belge tavanın ALTINDA
// kalıp yanıtı yüzlerce katına çıkarabilir. Ölçüldüğünde 8 KiB'lık bir istek
// 191 MiB yanıt üretiyordu ve hız sınırlayıcı bunu BİR istek sayıyordu. Kapı
// bu yüzden ayrıdır ve karmaşıklığın yerine geçmez.
//
// Burada sınanan şey, birim testlerinde ayrıntısıyla sınanan davranış değil,
// kapının üretim kurulumunda BAĞLI olmasıdır: modül e2e zemininde de sıfır
// değerli seçeneklerle kurulur ve sıfırın "sınırsız" değil "paket varsayılanı"
// anlamına gelmesi ancak gerçek kurulumda görülebilir.
func TestGraphQLAlanTekrariSiniriUretimYiginindaUygulanir(t *testing.T) {
	grafFiksturu(t)

	kayit := grafIstegi(t, publishableAnahtar, grafTakmaAdliYigma(400), nil)
	require.Equal(t, http.StatusOK, kayit.Code, "gövde: %s", kayit.Body.String())

	zarf := grafCoz(t, kayit)
	require.NotEmpty(t, zarf.Errors, "takma adlarla yığılmış belge reddedilmeli")
	assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", zarf.Errors[0].Extensions["code"],
		"tekrar kapısı karmaşıklıktan ÖNCE ve daha ucuza yakalamalı; karmaşıklık "+
			"kodu görünüyorsa tekrar kapısı üretim yığınında bağlı değil demektir")
	assert.Empty(t, zarf.Data.Products.Items, "reddedilen belge katalog döndürmemeli")
}

// TestGraphQLDevasaGovdeAyristirilmadanReddedilir gövde sınırının üretim
// yığınında da uygulandığını doğrular.
//
// Bu kapı ötekilerin YAPAMADIĞI işi yapar: derinlik ve karmaşıklık ancak belge
// ayrıştırıldıktan SONRA ölçülebilir, yani onlara ulaşana kadar sunucu metni
// zaten okumuş ve ayrıştırmıştır. Belge kusursuz bir GraphQL sorgusudur;
// reddedilme sebebi şekli değil BOYUTUDUR.
//
// Yanıt GraphQL zarfı değil ÇEKİRDEĞİN zarfıdır ve kural şudur: data/errors
// yalnızca çalıştırıcıya ULAŞMIŞ belgelere aittir. Aynı kural yetkisiz isteği
// de 401'de çekirdek zarfına düşürür; desteklenmeyen metot ise router'ın
// kendi 405'ini alır — ortak olan, hiçbirinin GraphQL zarfı DÖNMEMESİDİR.
func TestGraphQLDevasaGovdeAyristirilmadanReddedilir(t *testing.T) {
	belge := `{ product(handle: "` + strings.Repeat("x", 128<<10) + `") { id } }`

	kayit := grafIstegi(t, publishableAnahtar, belge, nil)

	require.Equal(t, http.StatusUnprocessableEntity, kayit.Code,
		"sınırı aşan gövde reddedilmeli; gövde: %s", kayit.Body.String())

	var zarf corehttp.ErrorResponse
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"hata gövdesi çözülemedi: %s", kayit.Body.String())
	assert.Equal(t, "product_graphql_body_too_large", zarf.Error.Code)
	assert.NotEmpty(t, zarf.Error.RequestID,
		"çekirdeğin zarfı istek kimliği taşımalı; bu uç için de ayrı bir yol yoktur")
}
