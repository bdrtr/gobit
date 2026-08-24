package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// anlikGoruntu testlerde kullanılan geçerli sipariş görüntüsüdür.
//
// Gövde ELLE YAZILMIŞTIR ve interop'un kendi tiplerinden ÜRETİLMEZ. Sebep
// budur: şema modüller arası bir sözleşmedir ve tüketici (complete_cart
// workflow'u) bu JSON'u kendi paketinde kurar. Görüntüyü interop'un tipinden
// üretseydik, bir alan adı değiştiğinde test de onunla birlikte değişir ve
// sözleşmenin bozulduğunu HİÇBİR test görmezdi.
const anlikGoruntu = `{
  "cart_id":         "cart_TEST",
  "region_id":       "reg_TEST",
  "customer_id":     "cus_TEST",
  "email":           "Musteri@Ornek.COM",
  "currency_code":   "try",
  "idempotency_key": "wf_ADIM_1",
  "subtotal":        3000,
  "discount_total":  0,
  "tax_total":       600,
  "shipping_total":  2500,
  "total":           6100,
  "metadata":        {"kanal": "web"},
  "items": [
    {
      "variant_id":     "variant_TEST",
      "title":          "Kırmızı Tişört",
      "quantity":       3,
      "unit_price":     1000,
      "subtotal":       3000,
      "discount_total": 0,
      "tax_total":      600,
      "total":          3600,
      "metadata":       {"satir": 1}
    }
  ]
}`

// TestPlaceOrderJSONSemayiOkur anlık görüntü şemasının BİREBİR beklendiği gibi
// çözüldüğünü doğrular.
//
// Bu test şemanın tek derleme zamanı DIŞI güvencesidir: order modülü workflow
// paketini import edemediği için derleyici alan adlarındaki bir kaymayı
// göremez (ADR 0001/0006).
func TestPlaceOrderJSONSemayiOkur(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	siparisID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(anlikGoruntu))
	require.NoError(t, err)
	require.NotEmpty(t, siparisID)

	detay, err := o.svc.GetOrder(ctx, siparisID)
	require.NoError(t, err)

	assert.Equal(t, "reg_TEST", detay.RegionID)
	assert.Equal(t, "cus_TEST", detay.CustomerID)
	assert.Equal(t, "cart_TEST", detay.CartID)
	assert.Equal(t, "musteri@ornek.com", detay.Email)
	assert.Equal(t, "TRY", detay.CurrencyCode)
	assert.Equal(t, "wf_ADIM_1", detay.IdempotencyKey)
	assert.Equal(t, int64(3000), detay.Subtotal)
	assert.Equal(t, int64(600), detay.TaxTotal)
	assert.Equal(t, int64(2500), detay.ShippingTotal)
	assert.Equal(t, int64(6100), detay.Total)
	assert.Equal(t, map[string]any{"kanal": "web"}, detay.Metadata)

	require.Len(t, detay.Items, 1)
	satir := detay.Items[0]
	assert.Equal(t, "variant_TEST", satir.VariantID)
	assert.Equal(t, "Kırmızı Tişört", satir.Title)
	assert.Equal(t, int64(3), satir.Quantity)
	assert.Equal(t, int64(1000), satir.UnitPrice)
	assert.Equal(t, int64(3000), satir.Subtotal)
	assert.Equal(t, int64(600), satir.TaxTotal)
	assert.Equal(t, int64(3600), satir.Total)
	assert.Equal(t, map[string]any{"satir": float64(1)}, satir.Metadata)
}

// TestPlaceOrderJSONBilinmeyenAlaniYokSayar tüketicinin daha GENİŞ bir görüntü
// geçirebildiğini doğrular.
//
// Katı çözümleme, tüketici tarafına yeni bir alan eklendiğinde bu modülü de
// değiştirmeyi zorunlu kılar ve iki paketi derleme zamanı bağımlılığı olmadan
// birbirine kilitlerdi.
func TestPlaceOrderJSONBilinmeyenAlaniYokSayar(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	genis := `{
      "region_id": "reg_TEST", "currency_code": "TRY",
      "revision": 7, "completed": true,
      "shipping_methods": [{"id": "csm_1", "amount": 2500}],
      "subtotal": 1000, "tax_total": 0, "shipping_total": 0, "total": 1000,
      "items": [{"variant_id": "v1", "title": "T", "quantity": 1,
                 "unit_price": 1000, "subtotal": 1000, "total": 1000,
                 "line_item_id": "li_1"}]
    }`

	siparisID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(genis))

	require.NoError(t, err, "bilinmeyen alanlar görüntüyü reddetmemeli")
	assert.NotEmpty(t, siparisID)
}

// TestPlaceOrderJSONBozukGovdeyiReddeder çözülemeyen gövdenin Invalid
// döndüğünü doğrular.
func TestPlaceOrderJSONBozukGovdeyiReddeder(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	_, err := interop.PlaceOrderJSON(ctx, json.RawMessage(`{"region_id":`))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, service.CodeInteropSnapshotInvalid, errors.CodeOf(err))
}

// TestPlaceOrderJSONEksikZorunluAlaniReddeder eksik alanların yok sayılmadığını
// doğrular.
func TestPlaceOrderJSONEksikZorunluAlaniReddeder(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	_, err := interop.PlaceOrderJSON(ctx, json.RawMessage(`{"currency_code": "TRY"}`))

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestInteropPlaceOrderIdempotenttir aynı görüntünün ikinci kez gönderilmesinin
// yeni sipariş açmadığını doğrular.
//
// Saga bir adımı yeniden deneyebilir; anahtar olmadan tekrar, müşteriye İKİNCİ
// BİR SİPARİŞ açmak demek olurdu.
func TestInteropPlaceOrderIdempotenttir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	ilk, err := interop.PlaceOrderJSON(ctx, json.RawMessage(anlikGoruntu))
	require.NoError(t, err)
	ikinci, err := interop.PlaceOrderJSON(ctx, json.RawMessage(anlikGoruntu))
	require.NoError(t, err)

	assert.Equal(t, ilk, ikinci)
	_, sayi, err := o.svc.ListOrders(ctx, service.ListOrdersInput{})
	require.NoError(t, err)
	assert.Equal(t, int64(1), sayi)
}

// TestInteropCancelOrderIdempotenttir saga telafisinin ilkel yüzeyden de iki
// kez çağrılabildiğini doğrular.
func TestInteropCancelOrderIdempotenttir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	siparisID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(anlikGoruntu))
	require.NoError(t, err)

	require.NoError(t, interop.CancelOrder(ctx, siparisID, "ödeme reddedildi"))
	require.NoError(t, interop.CancelOrder(ctx, siparisID, "telafi tekrarı"))

	detay, err := o.svc.GetOrder(ctx, siparisID)
	require.NoError(t, err)
	assert.Equal(t, models.OrderCanceled, detay.Status)
}

// TestInteropCompleteOrderTelafiDegildir tamamlamanın ikinci çağrıda hata
// döndüğünü doğrular.
func TestInteropCompleteOrderTelafiDegildir(t *testing.T) {
	ctx := context.Background()
	o := yeniOrtam(t)
	interop := service.NewInterop(o.svc)

	siparisID, err := interop.PlaceOrderJSON(ctx, json.RawMessage(anlikGoruntu))
	require.NoError(t, err)

	require.NoError(t, interop.CompleteOrder(ctx, siparisID))
	err = interop.CompleteOrder(ctx, siparisID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
}
