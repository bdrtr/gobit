package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/service"
)

// Bu dosya katalog olaylarının SÖZLEŞMESİNİ sınar: adlar, yükün alanları,
// değerlerin tipi ve yayım hatasının yazmayı düşürmemesi.
//
// Olaylar servisin dönüş değerinde görünmez (Publish handler'ları beklemez),
// bu yüzden tek kanıt sahte veri yoludur.

// eventFixture olay testlerinin ortak kurulumudur.
type eventFixture struct {
	svc *service.Service
	bus *fakeBus
}

// newEventFixture veri yolu bağlanmış bir servis kurar.
func newEventFixture(t *testing.T) eventFixture {
	t.Helper()

	bus := newFakeBus()
	return eventFixture{svc: newServiceWithBus(t, newMemStore(), newFakeLinker(), nil, bus), bus: bus}
}

// TestUrunOlaylariYayimlanir üç katalog olayının da yayımlandığını ve yükünün
// sözleşmedeki alanları taşıdığını doğrular.
func TestUrunOlaylariYayimlanir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newEventFixture(t)

	urun, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{
		Title:  "Tişört",
		Status: models.StatusDraft,
	})
	require.NoError(t, err)

	olusan := fx.bus.byName(service.EventProductCreated)
	require.Len(t, olusan, 1, "ürün yazıldığında tam bir olay yayımlanmalı")
	assert.Equal(t, urun.ID, olusan[0].Data[service.EventFieldProductID])
	assert.Equal(t, models.StatusDraft.String(), olusan[0].Data[service.EventFieldStatus],
		"olay ürünün YAZILDIĞI ANDAKİ durumunu taşımalı")

	// Yük DARDIR: başlık, handle ve varyantlar olayda YOKTUR. Onları koymak,
	// olayı kaydın ikinci bir kopyası hâline getirirdi.
	assert.NotContains(t, olusan[0].Data, "title")
	assert.NotContains(t, olusan[0].Data, "handle")
	assert.NotContains(t, olusan[0].Data, "variants")

	_, err = fx.svc.UpdateProduct(ctx, urun.ID, service.UpdateProductInput{
		Status: ptr(models.StatusPublished),
	})
	require.NoError(t, err)

	guncellenen := fx.bus.byName(service.EventProductUpdated)
	require.Len(t, guncellenen, 1)
	assert.Equal(t, urun.ID, guncellenen[0].Data[service.EventFieldProductID])
	assert.Equal(t, models.StatusPublished.String(), guncellenen[0].Data[service.EventFieldStatus],
		"güncelleme olayı YENİ durumu taşımalı; indeksleme kararı buna bakar")

	require.NoError(t, fx.svc.DeleteProduct(ctx, urun.ID))

	silinen := fx.bus.byName(service.EventProductDeleted)
	require.Len(t, silinen, 1)
	assert.Equal(t, urun.ID, silinen[0].Data[service.EventFieldProductID])
	// Silme olayında durum YOKTUR: soft silinmiş kayıt hiçbir okumadan dönmez,
	// dolayısıyla abone değeri doğrulayamaz ve "indeksten düşür" eylemi zaten
	// duruma bakmaz.
	assert.NotContains(t, silinen[0].Data, service.EventFieldStatus)

	// Olay adları modüller arası sözleşmedir ve Redis'te stream adıdır.
	assert.Equal(t, "product.created", service.EventProductCreated)
	assert.Equal(t, "product.updated", service.EventProductUpdated)
	assert.Equal(t, "product.deleted", service.EventProductDeleted)
}

// TestUrunOlayYukuJSONTuruDegistirmez yükün üretim veri yolundan geçtiğinde
// TİP değiştirmediğini doğrular.
//
// Üretimdeki Redis Streams backend'i Data'yı json.Marshal ile yazar ve okurken
// map[string]any içine çözer; JSON'un tek sayı tipi olduğu için int64 konan bir
// alan aboneye float64 olarak ulaşır. Gerekçenin tamamı
// internal/modules/order/service/events.go içindedir. Test o dönüşümü taklit
// eder ve yüke ileride eklenecek sayısal bir alanın (varyant adedi, sürüm)
// kuralı sessizce delmesini engeller.
func TestUrunOlayYukuJSONTuruDegistirmez(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newEventFixture(t)

	urun, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{Title: "Pantolon"})
	require.NoError(t, err)
	require.NoError(t, fx.svc.DeleteProduct(ctx, urun.ID))

	olaylar := fx.bus.events()
	require.NotEmpty(t, olaylar)

	for _, olay := range olaylar {
		ham, err := json.Marshal(olay.Data)
		require.NoError(t, err)
		var teslim map[string]any
		require.NoError(t, json.Unmarshal(ham, &teslim))

		require.NotEmpty(t, teslim, "%q olayının yükü boş olmamalı", olay.Name)
		for anahtar, deger := range teslim {
			assert.IsType(t, "", deger,
				"%q olayının %q alanı veri yolundan geçince dize kalmalı", olay.Name, anahtar)
		}
	}
}

// TestOlayYayimiDusersaUrunYazilmisKalir yayım hatasının katalog yazmasını
// düşürmediğini doğrular.
//
// Karar bilinçlidir: ürün KAYITTIR, olay ise duyurudur. Hata dönmek çağırana
// "değişiklik uygulanmadı" demek olurdu — oysa uygulanmıştır ve çağıranın
// tekrarı ikinci bir ürün ya da handle çakışması üretirdi.
func TestOlayYayimiDusersaUrunYazilmisKalir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fx := newEventFixture(t)
	fx.bus.failErr = errors.Unavailable("eventbus_publish_failed", "veri yolu erişilemez")

	urun, err := fx.svc.CreateProduct(ctx, service.CreateProductInput{Title: "Mont"})
	require.NoError(t, err, "olay yayımı hatası ürün oluşturmayı düşürmemeli")

	_, err = fx.svc.UpdateProduct(ctx, urun.ID, service.UpdateProductInput{Title: ptr("Kaban")})
	require.NoError(t, err, "olay yayımı hatası güncellemeyi düşürmemeli")

	okunan, err := fx.svc.GetProduct(ctx, urun.ID)
	require.NoError(t, err, "ürün yazılmış olmalı")
	assert.Equal(t, "Kaban", okunan.Title, "güncelleme uygulanmış olmalı")

	require.NoError(t, fx.svc.DeleteProduct(ctx, urun.ID), "olay yayımı hatası silmeyi düşürmemeli")
	_, err = fx.svc.GetProduct(ctx, urun.ID)
	assert.True(t, errors.IsNotFound(err), "silme uygulanmış olmalı")
}

// TestVeriYoluYokkenYazmalarCalisir veri yolu bağlanmamış bir servisin tüm
// yazma yollarında çalıştığını doğrular.
//
// Bu yol YALNIZCA gömülü kullanım ve testler içindir — modülün Register'ı veri
// yolunu container'dan çözer ve bulamazsa açılışı düşürür. Yine de
// sınanır: nil bir veri yolunda panikleyen bir yayım, servisi gömülü kullanan
// her çağrıyı düşürürdü.
func TestVeriYoluYokkenYazmalarCalisir(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newMemStore(), newFakeLinker(), nil)

	urun, err := svc.CreateProduct(ctx, service.CreateProductInput{Title: "Şapka"})
	require.NoError(t, err)
	_, err = svc.UpdateProduct(ctx, urun.ID, service.UpdateProductInput{Title: ptr("Bere")})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteProduct(ctx, urun.ID))
}
