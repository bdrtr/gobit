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
