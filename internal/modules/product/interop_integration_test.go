//go:build integration

package product_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/core/link"
	"github.com/bdrtr/gobit/internal/core/query"
	"github.com/bdrtr/gobit/internal/modules/product"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya modülün dışarıya açtığı iki yüzeyi GERÇEK çekirdekle kanıtlar:
// "product.interop" okuma yüzeyi ve katalog olayları.
//
// İkisi de burada olmak zorundadır. Kanal süzgeci SQL'dedir ve sahte bir depo
// kendi yazdığı koşulu doğrulayamaz; olayların gerçekten container'daki veri
// yoluna gitmesi ise ancak modül Register edildiğinde görülür — servisin
// testleri veri yolunu kendileri bağladığı için o bağlantıyı ıskalar.

// storeProductReader "product.interop" yüzeyini TÜKETİCİ gözünden tanımlar.
//
// Bir eklenti (plugins/**) product'ı import EDEMEZ; yüzeyi tam olarak böyle,
// kendi paketinde tanımladığı dar bir arayüzle ve container'daki ADLA çözer.
// Arayüzün burada tekrar yazılması testin kendisidir: somut tip imzadan
// saparsa çözüm derleme anında değil ÇALIŞMA ANINDA düşer ve bu test o anı
// üretimden entegrasyon takımına taşır.
type storeProductReader interface {
	StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// interopIDs yüzeyin yanıtındaki ürün kimliklerini SIRASIYLA döner.
func interopIDs(t *testing.T, body json.RawMessage) []string {
	t.Helper()

	var out struct {
		Products []struct {
			ID string `json:"id"`
		} `json:"products"`
	}
	require.NoError(t, json.Unmarshal(body, &out), "yanıt: %s", string(body))

	ids := make([]string, 0, len(out.Products))
	for _, p := range out.Products {
		ids = append(ids, p.ID)
	}
	return ids
}

// TestInteropKanalSuzmesiGercekSQLdeUygulanir yüzeyin satış kanalı süzgecini
// GERÇEK sorguyla uyguladığını kanıtlar.
//
// İddia şudur: başka bir kanala atanmış ürünün kimliği açıkça istense bile
// yanıtta DÖNMEZ. Aramanın kanal süzmesinin bypass'ı hâline gelmediğinin tek
// kanıtı budur ve yalnızca gerçek veritabanında verilebilir — süzgeç, çalışma
// anında core/link tarafından kurulan link tablosuna karşı bir EXISTS
// koşuludur.
func TestInteropKanalSuzmesiGercekSQLdeUygulanir(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)
	reader, err := container.Resolve[storeProductReader](sys.container, product.InteropName)
	require.NoError(t, err, "yüzey %q adıyla çözülebilmeli", product.InteropName)

	bizim := createStoreProduct(t, svc, "interop-bizim")
	baskasi := createStoreProduct(t, svc, "interop-baska")
	atamasiz := createStoreProduct(t, svc, "interop-atamasiz")

	require.NoError(t, svc.AddProductSalesChannel(ctx, bizim.ID, "sc_interop_bizim"))
	require.NoError(t, svc.AddProductSalesChannel(ctx, baskasi.ID, "sc_interop_baska"))

	istek := fmt.Sprintf(`{"ids": [%q, %q, %q], "sales_channel_ids": ["sc_interop_bizim"]}`,
		baskasi.ID, bizim.ID, atamasiz.ID)
	body, err := reader.StoreProductsByIDsJSON(ctx, json.RawMessage(istek))
	require.NoError(t, err)

	assert.Equal(t, []string{bizim.ID, atamasiz.ID}, interopIDs(t, body),
		"başka kanalın ürünü dönmemeli; kalanlar isteğin sırasını korumalı")

	// Kanalsız istek süzmez: anlam vitrin listelemesindekiyle aynıdır.
	hepsi := fmt.Sprintf(`{"ids": [%q, %q]}`, baskasi.ID, bizim.ID)
	body, err = reader.StoreProductsByIDsJSON(ctx, json.RawMessage(hepsi))
	require.NoError(t, err)
	assert.Equal(t, []string{baskasi.ID, bizim.ID}, interopIDs(t, body))
}

// TestInteropVitrinUcuylaAyniKarariVerir yüzey ile HTTP vitrin ucunun aynı ürün
// için aynı kanal kararını verdiğini doğrular.
//
// Görünürlük kuralının TEK tanımı olduğunun kanıtı budur: kural bir gün
// yalnızca bir yolda değişirse bu test düşer.
func TestInteropVitrinUcuylaAyniKarariVerir(t *testing.T) {
	ctx := context.Background()
	sys := newSystem(t)

	svc, err := container.Resolve[*service.Service](sys.container, product.ServiceName)
	require.NoError(t, err)
	reader, err := container.Resolve[storeProductReader](sys.container, product.InteropName)
	require.NoError(t, err)

	gizli := createStoreProduct(t, svc, "interop-gizli")
	require.NoError(t, svc.AddProductSalesChannel(ctx, gizli.ID, "sc_interop_gizli"))

	// Vitrin tekil ucu başka bir kanalın kimliğiyle NotFound döner.
	rec := sys.storeChannelRequest(t, "/store/v1/products/"+gizli.ID, []string{"sc_interop_diger"})
	require.Equal(t, http.StatusNotFound, rec.Code, "gövde: %s", rec.Body.String())

	// Yüzey de aynı kararı verir: kayıt yanıtta hiç yoktur.
	istek := fmt.Sprintf(`{"ids": [%q], "sales_channel_ids": ["sc_interop_diger"]}`, gizli.ID)
	body, err := reader.StoreProductsByIDsJSON(ctx, json.RawMessage(istek))
	require.NoError(t, err)
	assert.Empty(t, interopIDs(t, body), "vitrinde gizlenen ürün yüzeyden de dönmemeli")
}

// TestKatalogOlaylariVeriYolunaGider modül Register edildiğinde katalog
// olaylarının container'daki GERÇEK veri yoluna gittiğini doğrular.
//
// Servisin kendi testleri veri yolunu elleriyle bağlar; bağlantının Register
// sırasında kurulduğunu yalnızca bu test görür. Kopması hiçbir hata üretmez —
// olaylar sessizce hiç yayımlanmaz — ve bu yüzden testle bağlanır.
func TestKatalogOlaylariVeriYolunaGider(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))

	bus := eventbus.NewInMemory(nil)
	require.NoError(t, c.Provide("core.eventbus", bus))

	// Abonelik modül ayağa kalkmadan ÖNCE kurulur: sonradan bağlanan bir abone
	// kendisinden önce yayımlanmış olayları göremez (bellek içi backend geçmiş
	// tutmaz, EN FAZLA BİR KEZ teslim eder).
	defter := &olayDefteri{}
	for _, ad := range []string{
		service.EventProductCreated,
		service.EventProductUpdated,
		service.EventProductDeleted,
	} {
		require.NoError(t, bus.Subscribe(ad, defter.kaydet))
	}

	mod := product.New(product.Options{})
	require.NoError(t, mod.Register(ctx, c))

	svc, err := container.Resolve[*service.Service](c, product.ServiceName)
	require.NoError(t, err)

	urun, err := svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Olay ürünü",
		Handle: uniqueHandle("olay-urunu"),
		Status: productmodels.StatusDraft,
	})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, urun.ID, service.UpdateProductInput{
		Status: statusPtr(productmodels.StatusPublished),
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteProduct(ctx, urun.ID))

	olusan := defter.bekle(t, service.EventProductCreated, urun.ID)
	assert.Equal(t, productmodels.StatusDraft.String(), olusan.Data[service.EventFieldStatus],
		"olay ürünün YAZILDIĞI ANDAKİ durumunu taşımalı")

	guncellenen := defter.bekle(t, service.EventProductUpdated, urun.ID)
	assert.Equal(t, productmodels.StatusPublished.String(), guncellenen.Data[service.EventFieldStatus])

	silinen := defter.bekle(t, service.EventProductDeleted, urun.ID)
	assert.NotContains(t, silinen.Data, service.EventFieldStatus,
		"silme olayında durum taşınmaz")
}

// TestRegisterOlayVeriYoluOlmadanDuser veri yolu kayıtlı değilken açılışın
// DURDUĞUNU doğrular.
//
// Karar bilinçlidir: "olaylar sessizce atlansın" seçilseydi katalog çalışmaya
// devam eder, hiçbir hata görünmez ve eksiklik ancak arama indeksi eskidiğinde
// — yani üretimde — fark edilirdi. core.eventbus, core.db ve core.link gibi,
// modüllerden ÖNCE kaydedilen bir çekirdek servistir; yokluğu bir dağıtım
// biçimi değil, kurulum hatasıdır.
func TestRegisterOlayVeriYoluOlmadanDuser(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	links := link.New(testPool, nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.link", links))
	require.NoError(t, c.Provide("core.query", query.New(links, c, nil)))
	// core.eventbus BİLİNÇLİ olarak kaydedilmez.

	err := product.New(product.Options{}).Register(ctx, c)
	require.Error(t, err, "veri yolu olmadan Register başarılı olmamalı")
	assert.Equal(t, "product_module_setup_failed", coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "core.eventbus",
		"hata mesajı eksik servisin ADINI söylemeli; kurulum hatası ilk saniyede anlaşılmalı")
}

// olayDefteri veri yoluna düşen katalog olaylarının test tarafındaki kaydıdır.
//
// Tip eşzamanlı kullanıma güvenlidir: bellek içi backend her handler'ı ayrı bir
// goroutine'de çalıştırır ve okuma ile yazma aynı kilidi paylaşır.
type olayDefteri struct {
	mu       sync.Mutex
	kayitlar []eventbus.Event
}

// kaydet olayı deftere yazar.
func (d *olayDefteri) kaydet(_ context.Context, e eventbus.Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.kayitlar = append(d.kayitlar, e)
	return nil
}

// bekle verilen ada ve ürüne ait TEK olayı bekler ve döner.
//
// Bekleme ZORUNLUDUR: Publish handler'ları BEKLEMEZ, dolayısıyla yazma dönmüş
// olsa bile olay defterde henüz görünmüyor olabilir. Tekillik ayrıca sınanır —
// aynı yazma için iki olay, abonelerin işi iki kez yapması demektir.
func (d *olayDefteri) bekle(t *testing.T, ad, urunID string) eventbus.Event {
	t.Helper()

	var bulunan []eventbus.Event
	require.Eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()

		bulunan = bulunan[:0]
		for i := range d.kayitlar {
			id, _ := d.kayitlar[i].Data[service.EventFieldProductID].(string)
			if d.kayitlar[i].Name == ad && id == urunID {
				bulunan = append(bulunan, d.kayitlar[i])
			}
		}
		return len(bulunan) > 0
	}, 5*time.Second, 20*time.Millisecond,
		"%q olayı %s ürünü için yayımlanmalı; yayımlanmazsa arama indeksi gibi aboneler "+
			"katalogdan HABERSİZ kalır ve eksiklik hiçbir hata üretmez", ad, urunID)

	require.Len(t, bulunan, 1, "%q olayı BİR KEZ yayımlanmalı", ad)
	return bulunan[0]
}

// createStoreProduct vitrinde görünür (yayında) bir ürün oluşturur.
func createStoreProduct(t *testing.T, svc *service.Service, handle string) productmodels.Product {
	t.Helper()

	urun, err := svc.CreateProduct(context.Background(), service.CreateProductInput{
		Title:  handle,
		Handle: uniqueHandle(handle),
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err)
	return urun
}

// statusPtr verilen durumun adresini döner.
func statusPtr(s productmodels.Status) *productmodels.Status { return &s }
