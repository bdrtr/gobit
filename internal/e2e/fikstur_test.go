//go:build integration

package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
	productsvc "github.com/bdrtr/gobit/internal/modules/product/service"

	customersvc "github.com/bdrtr/gobit/internal/modules/customer/service"
	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	pricingsvc "github.com/bdrtr/gobit/internal/modules/pricing/service"
)

// fiksturSayaci fikstürlerin benzersiz handle ve e-posta üretmesini sağlar.
//
// Testler tek bir veritabanını paylaşır ve ürün handle'ı ile kayıtlı müşteri
// e-postası BENZERSİZDİR; sabit bir ad kullanmak, testlerin çalışma sırasına
// göre çakışmasına yol açardı.
var fiksturSayaci atomic.Int64

// yeniVaryant bir ürün ve varyant oluşturur, isteniyorsa fiyat kümesini kurup
// varyanta bağlar ve VARYANT KİMLİĞİNİ döner.
//
// fiyatlar nil verilirse varyant hiçbir fiyat kümesine BAĞLANMAZ; bu, "fiyatı
// olmayan varyant" senaryosunun kurulumudur. Boş olmayan bir eşlemede para
// birimleri SIRALI yazılır, böylece aynı fikstür her koşuda aynı sırayla
// oluşur.
//
// Fiyat kümesi bağı "product_variant_price_set" linkiyle kurulur; sepet akışı
// varyantın fiyatını tam olarak o linkten bulur (bkz. workflows/cart
// priceSetsFor).
func yeniVaryant(ctx context.Context, t *testing.T, baslik string, fiyatlar map[string]int64) string {
	t.Helper()

	sira := fiksturSayaci.Add(1)
	urun, err := urunSvc.CreateProduct(ctx, productsvc.CreateProductInput{
		Handle: fmt.Sprintf("e2e-urun-%d", sira),
		Title:  baslik,
		Status: productmodels.StatusPublished,
	})
	require.NoError(t, err, "fikstür ürünü oluşturulamadı")

	varyant, err := urunSvc.CreateVariant(ctx, urun.ID, productsvc.CreateVariantInput{Title: baslik})
	require.NoError(t, err, "fikstür varyantı oluşturulamadı")

	if len(fiyatlar) == 0 {
		return varyant.ID
	}

	girdiler := make([]pricingsvc.PriceInput, 0, len(fiyatlar))
	for _, kod := range slices.Sorted(maps.Keys(fiyatlar)) {
		girdiler = append(girdiler, pricingsvc.PriceInput{
			CurrencyCode: kod,
			Amount:       fiyatlar[kod],
			MinQuantity:  1,
		})
	}

	kume, err := fiyatSvc.CreatePriceSet(ctx, girdiler)
	require.NoError(t, err, "fikstür fiyat kümesi oluşturulamadı")
	require.NoError(t, urunSvc.SetVariantPriceSet(ctx, varyant.ID, kume.ID),
		"varyant fiyat kümesine bağlanamadı; bağ olmadan akış fiyatı bulamaz")

	return varyant.ID
}

// yeniMusteri KAYITLI bir müşteri oluşturur ve kimliğiyle e-postasını döner.
func yeniMusteri(ctx context.Context, t *testing.T) (musteriID, eposta string) {
	t.Helper()

	sira := fiksturSayaci.Add(1)
	eposta = fmt.Sprintf("e2e-musteri-%d@ornek.test", sira)
	musteri, err := musteriSvc.CreateCustomer(ctx, customersvc.CustomerInput{
		Email:     eposta,
		FirstName: "E2E",
		LastName:  "Müşteri",
	})
	require.NoError(t, err, "fikstür müşterisi oluşturulamadı")

	return musteri.ID, musteri.Email
}

// yeniStokluVaryant fiyatı VE stoğu olan bir varyant kurar; varyant ile stok
// kaleminin kimliklerini döner.
//
// Kurulum dört parçadır ve dördü de gerçek modüllerdedir: varyant + fiyat
// (bkz. [yeniVaryant]), stok kalemi, varyant -> kalem bağı ve paylaşılan
// lokasyondaki stok seviyesi. Sipariş tamamlama akışı stok kalemini tam olarak
// "product_variant_inventory" bağından bulur (bkz. checkoutwf.plan.go); bağ
// kurulmazsa akış varyantı "stoksuz" sayar ve sepet hiç sipariş olamaz.
//
// stok, lokasyondaki FİZİKSEL adettir. Rezerve adet sıfırdan başlar, yani
// satılabilir adet de başlangıçta stok kadardır.
func yeniStokluVaryant(
	ctx context.Context,
	t *testing.T,
	baslik string,
	fiyatlar map[string]int64,
	stok int64,
) (varyantID, stokKalemID string) {
	t.Helper()

	varyantID = yeniVaryant(ctx, t, baslik, fiyatlar)

	sira := fiksturSayaci.Add(1)
	kalem, err := stokSvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   fmt.Sprintf("E2E-SKU-%d", sira),
		Title: baslik,
	})
	require.NoError(t, err, "fikstür stok kalemi oluşturulamadı")

	require.NoError(t, urunSvc.SetVariantInventoryItem(ctx, varyantID, kalem.ID),
		"varyant stok kalemine bağlanamadı; bağ olmadan akış varyantı stoksuz sayar")

	seviye, err := stokSvc.SetInventoryLevel(ctx, kalem.ID, stokLokasyonID, stok)
	require.NoError(t, err, "fikstür stok seviyesi yazılamadı")
	require.Equal(t, stok, seviye.Available(),
		"yeni seviyede satılabilir adet fiziksel adede eşit olmalı; eşit değilse "+
			"fikstür daha başlarken rezerve stok taşıyor demektir")

	return varyantID, kalem.ID
}

// satilabilirAdet stok kaleminin TÜM lokasyonlardaki satılabilir toplamını
// döner: stocked - reserved.
//
// Sayı stok modülünden okunur, akışın döndürdüğü bir değerden değil: sınanan
// iddia, rezervasyonun stok modülünün DEFTERİNDE gerçekten yer değiştirdiğidir.
func satilabilirAdet(ctx context.Context, t *testing.T, stokKalemID string) int64 {
	t.Helper()

	adet, err := stokSvc.AvailableQuantity(ctx, stokKalemID)
	require.NoError(t, err, "satılabilir adet okunamadı")
	return adet
}

// stokSeviyesi kalemin PAYLAŞILAN lokasyondaki seviyesini döner.
//
// Satılabilir adet tek başına yetmez: rezervasyonun onaylanması (stoktan
// düşme) ile geri bırakılması, satılabilir adet açısından ayırt edilebilir ama
// FİZİKSEL adet açısından tam tersidir. İki sayıyı birden görmek, "stok düştü"
// ile "stok geri geldi" arasındaki farkı kesinleştirir.
func stokSeviyesi(ctx context.Context, t *testing.T, stokKalemID string) inventorymodels.InventoryLevel {
	t.Helper()

	seviyeler, err := stokSvc.ListInventoryLevels(ctx, stokKalemID)
	require.NoError(t, err, "stok seviyeleri okunamadı")
	require.Len(t, seviyeler, 1,
		"fikstür kalemi TEK lokasyonda seviyelenmiş olmalı; ikinci bir seviye, "+
			"adetlerin hangi depodan geldiğini belirsizleştirirdi")
	return seviyeler[0]
}

// bagliKimlikler bir linkin verilen kaynaktan çıkan hedeflerini döner.
//
// Okuma link servisinden yapılır, sepetin kendi sütunlarından DEĞİL: sınanan
// iddia, sepetin bağının Query katmanına açılan link tablosunda GERÇEKTEN
// kurulduğudur. Sütunu okumak yalnızca sepet servisinin kendi yazdığını
// doğrulardı.
func bagliKimlikler(ctx context.Context, t *testing.T, linkAdi, kaynakID string) []string {
	t.Helper()

	hedefler, err := baglar.List(ctx, linkAdi, kaynakID)
	require.NoError(t, err, "%q bağı okunamadı", linkAdi)
	return hedefler
}
