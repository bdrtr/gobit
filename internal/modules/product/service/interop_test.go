package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya "product.interop" yüzeyini sınar.
//
// En önemli iddia KANAL SÜZMESİDİR: bu yüzey aramanın kataloğa açılan kapısıdır
// ve süzme burada uygulanmazsa arama, vitrinin görünürlük kuralının BYPASS'ı
// hâline gelir — bir istemci kendi kanalında satılmayan ürünün kaydını arama
// üzerinden okuyabilirdi.

// interopFixture yüzey testlerinin ortak kurulumudur.
type interopFixture struct {
	// store depo çağrılarını sayar; "kayıt başına sorgu yapılmıyor" iddiasının
	// kanıtı budur.
	store   *memStore
	svc     *service.Service
	interop *service.Interop
}

// newInteropFixture yayında ürünler kurulabilen bir yüzey üretir.
func newInteropFixture(t *testing.T) interopFixture {
	t.Helper()

	store := newMemStore()
	svc := newService(t, store, newFakeLinker(), nil)

	return interopFixture{svc: svc, interop: service.NewInterop(svc), store: store}
}

// products yüzeyi çağırır ve dönen vitrin kayıtlarını çözer.
func (f interopFixture) products(t *testing.T, request string) []map[string]any {
	t.Helper()

	body, err := f.interop.StoreProductsByIDsJSON(context.Background(), json.RawMessage(request))
	require.NoError(t, err)

	var out struct {
		Products []map[string]any `json:"products"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "yanıt: %s", string(body))
	return out.Products
}

// ids dönen kayıtların kimliklerini SIRASIYLA verir.
func ids(records []map[string]any) []string {
	out := make([]string, 0, len(records))
	for _, rec := range records {
		id, _ := rec["id"].(string)
		out = append(out, id)
	}
	return out
}

// TestInteropKanalSuzmesiniUygular yüzeyin satış kanalı süzgecini gerçekten
// uyguladığını doğrular: BAŞKA bir kanalın ürünü, kimliği açıkça istense bile
// DÖNMEZ.
//
// Kural vitrindekiyle aynıdır: ataması olmayan ürün her kanalda görünür,
// ataması olan yalnızca atandığı kanallarda.
func TestInteropKanalSuzmesiniUygular(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	bizim := seedProduct(t, fx.svc, "bizim", "Bizim ürün")
	baskasi := seedProduct(t, fx.svc, "baskasi", "Başka kanalın ürünü")
	atamasiz := seedProduct(t, fx.svc, "atamasiz", "Atamasız ürün")

	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, bizim.ID, "sc_bizim"))
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, baskasi.ID, "sc_baska"))

	istek := fmt.Sprintf(`{"ids": [%q, %q, %q], "sales_channel_ids": ["sc_bizim"]}`,
		bizim.ID, baskasi.ID, atamasiz.ID)

	assert.Equal(t, []string{bizim.ID, atamasiz.ID}, ids(fx.products(t, istek)),
		"başka kanala atanmış ürün kimliği açıkça istense de dönmemeli")
}

// TestInteropKanalsizIstekSuzmez kanal alanı hiç verilmediğinde süzgecin
// uygulanmadığını doğrular.
//
// Anlam vitrin listelemesindekiyle AYNIDIR (bkz. service.StoreListOptions):
// eksik alan "istek kanal kimliği taşımıyor" demektir ve mağaza kimlik
// doğrulamasının bağlanmadığı kurulumun karşılığıdır. Boş DİZİ ise farklı bir
// şey söyler: kimlik var ama kanalı yok — o durumda yalnızca atamasız ürünler
// kalır.
func TestInteropKanalsizIstekSuzmez(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	atanmis := seedProduct(t, fx.svc, "atanmis", "Atanmış")
	atamasiz := seedProduct(t, fx.svc, "atamasiz", "Atamasız")
	require.NoError(t, fx.svc.AddProductSalesChannel(ctx, atanmis.ID, "sc_bir"))

	eksik := fmt.Sprintf(`{"ids": [%q, %q]}`, atanmis.ID, atamasiz.ID)
	assert.Equal(t, []string{atanmis.ID, atamasiz.ID}, ids(fx.products(t, eksik)),
		"alan eksikken süzgeç uygulanmamalı")

	bos := fmt.Sprintf(`{"ids": [%q, %q], "sales_channel_ids": []}`, atanmis.ID, atamasiz.ID)
	assert.Equal(t, []string{atamasiz.ID}, ids(fx.products(t, bos)),
		"boş dizi 'kanalsız kimlik' demektir; yalnızca atamasız ürün kalmalı")
}

// TestInteropBilinmeyenKimligiSessizceAtlar bulunamayan, silinmiş ve yayında
// olmayan kimliklerin hata değil, eksik kayıt ürettiğini doğrular.
//
// Hata dönmek, aramanın tek bir ürün silindiği için tümüyle düşmesi demek
// olurdu. Ayrıca "başka kanalda" ile "hiç yok" ÇAĞIRAN İÇİN AYIRT EDİLEMEZ
// kalır; tekil vitrin ucunun ikisine de NotFound dönmesiyle aynı gerekçe.
func TestInteropBilinmeyenKimligiSessizceAtlar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	yayinda := seedProduct(t, fx.svc, "yayinda", "Yayında")
	silinen := seedProduct(t, fx.svc, "silinen", "Silinecek")
	require.NoError(t, fx.svc.DeleteProduct(ctx, silinen.ID))

	taslak, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Taslak",
		Status: models.StatusDraft,
	})
	require.NoError(t, err)

	istek := fmt.Sprintf(`{"ids": ["prod_yok", %q, %q, %q]}`, silinen.ID, taslak.ID, yayinda.ID)
	assert.Equal(t, []string{yayinda.ID}, ids(fx.products(t, istek)))

	// Hiçbiri bulunamazsa yanıt boş listedir, hata değil.
	assert.Empty(t, fx.products(t, `{"ids": ["prod_yok"]}`))
	assert.Empty(t, fx.products(t, `{"ids": []}`))
}

// TestInteropKimlikSirasiniKorur yanıtın isteğin kimlik SIRASINI koruduğunu
// doğrular.
//
// Sıra sözleşmenin parçasıdır: alaka sırasını arama dışarıdan verir ve yanıt
// onu bozarsa çağıran sıralamayı kendi tarafında yeniden kurmak zorunda kalır —
// yani her tüketici aynı işi tekrar yazar. Deponun kendi sırası (created_at
// DESC) burada bilinçli olarak GÖRMEZDEN gelinir.
func TestInteropKimlikSirasiniKorur(t *testing.T) {
	t.Parallel()

	fx := newInteropFixture(t)

	birinci := seedProduct(t, fx.svc, "birinci", "Birinci")
	ikinci := seedProduct(t, fx.svc, "ikinci", "İkinci")
	ucuncu := seedProduct(t, fx.svc, "ucuncu", "Üçüncü")

	istek := fmt.Sprintf(`{"ids": [%q, %q, %q]}`, ucuncu.ID, birinci.ID, ikinci.ID)
	assert.Equal(t, []string{ucuncu.ID, birinci.ID, ikinci.ID}, ids(fx.products(t, istek)),
		"yanıt isteğin sırasını korumalı")

	// Tekrarlanan kimlik BİR KEZ döner ve ilk geçtiği sırayı korur.
	tekrar := fmt.Sprintf(`{"ids": [%q, %q, %q]}`, ikinci.ID, birinci.ID, ikinci.ID)
	assert.Equal(t, []string{ikinci.ID, birinci.ID}, ids(fx.products(t, tekrar)))
}

// TestInteropVitrinGosterimiyleAyniSekliDoner yüzeyin dönüşünün vitrin
// gösterimiyle AYNI şekilde olduğunu doğrular.
//
// Aynı tip serileştirilir; test bunu alan alan değil, vitrin listesinin dönüşü
// ile yüzeyin dönüşünü KARŞILAŞTIRARAK sınar. Alanları elle saymak, iki
// gösterimin ayrışmasını yakalamayan bir kopya olurdu.
func TestInteropVitrinGosterimiyleAyniSekliDoner(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)
	urun := seedProduct(t, fx.svc, "tisort", "Tişört")

	vitrin, err := fx.svc.GetStoreProduct(ctx, urun.ID, nil)
	require.NoError(t, err)
	beklenenHam, err := json.Marshal(vitrin)
	require.NoError(t, err)
	var beklenen map[string]any
	require.NoError(t, json.Unmarshal(beklenenHam, &beklenen))

	kayitlar := fx.products(t, fmt.Sprintf(`{"ids": [%q]}`, urun.ID))
	require.Len(t, kayitlar, 1)
	assert.Equal(t, beklenen, kayitlar[0],
		"yüzey, vitrin ucunun yazdığı kaydın AYNISINI dönmeli")
	assert.Contains(t, kayitlar[0], "variants", "varyantlar vitrin kaydının parçasıdır")
}

// TestInteropGecersizIstegiReddeder çözülemeyen gövdelerin tipli hata
// döndürdüğünü doğrular.
//
// Tanınmayan alanın reddedilmesi bu yüzeyde ayrıca ÖNEMLİDİR: "channel_ids"
// yazan bir tüketici, süzgeci uyguladığını sanırken yayındaki tüm kataloğu
// okurdu.
func TestInteropGecersizIstegiReddeder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	for ad, govde := range map[string]string{
		"boş gövde":       ``,
		"null":            `null`,
		"dizi":            `[]`,
		"tanınmayan alan": `{"ids": [], "channel_ids": ["sc_1"]}`,
		"boş kimlik":      `{"ids": [""]}`,
	} {
		t.Run(ad, func(t *testing.T) {
			_, err := fx.interop.StoreProductsByIDsJSON(ctx, json.RawMessage(govde))
			require.Error(t, err)
			assert.True(t, errors.IsInvalid(err), "hata sınıfı Invalid olmalı: %v", err)
		})
	}
}

// TestInteropSinirUstuIstegiReddeder kimlik sayısının sınırlandığını doğrular.
//
// Sessiz kırpma arama sonucunu sessizce eksiltirdi ve çağıran bunu asla
// göremezdi; açık hata onu sayfalamaya zorlar.
func TestInteropSinirUstuIstegiReddeder(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	istenen := make([]string, 0, service.MaxLimit+1)
	for i := 0; i <= service.MaxLimit; i++ {
		istenen = append(istenen, fmt.Sprintf("prod_%03d", i))
	}
	govde, err := json.Marshal(map[string]any{"ids": istenen})
	require.NoError(t, err)

	_, err = fx.interop.StoreProductsByIDsJSON(ctx, govde)
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata sınıfı Invalid olmalı: %v", err)
}

// TestInteropGorunurluguTekSorgudaSorar arama yolunun N+1'e düşmediğini
// doğrular.
//
// Görünürlüğü kimlik başına sormak, arama sonucu sayısı kadar gidiş-dönüş
// demektir. Bu depo, N+1'i yapısal olarak dışarıda tutan bir mimaride yaşıyor
// (bkz. core/query, "kök çek → link çöz → batch getir") ve deseni en sıcak
// uçta — aramada — geri getirmek, mimarinin kendi kuralını en çok ihtiyaç
// duyulan yerde bozmak olurdu.
//
// İddia sayıyla kurulur, gözle değil: kimlik sayısı artınca sorgu sayısı
// ARTMAMALI.
func TestInteropGorunurluguTekSorgudaSorar(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newInteropFixture(t)

	kimlikler := make([]string, 0, 8)

	for i := range 8 {
		urun := seedProduct(t, fx.svc, fmt.Sprintf("urun-%d", i), fmt.Sprintf("Ürün %d", i))
		require.NoError(t, fx.svc.AddProductSalesChannel(ctx, urun.ID, "sc_bizim"))
		kimlikler = append(kimlikler, urun.ID)
	}

	kodlanmis, err := json.Marshal(kimlikler)
	require.NoError(t, err)

	istek := fmt.Sprintf(`{"ids": %s, "sales_channel_ids": ["sc_bizim"]}`, kodlanmis)
	require.Len(t, fx.products(t, istek), len(kimlikler), "hepsi görünür olmalı")

	assert.Equal(t, 1, fx.store.callCount("VisibleProductIDs"),
		"kimlik sayısı ne olursa olsun görünürlük TEK sorguda sorulmalı")
	assert.Zero(t, fx.store.callCount("ProductVisibleInSalesChannels"),
		"tekil sorgu toplu yolda kullanılmamalı; kullanılırsa N+1 geri gelir")
}
