package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// TestInteropSurfaceUsesPrimitiveTypes modüller arası yüzeyin, tüketicinin
// KENDİ paketinde tanımlayabileceği dar arayüzü yapısal olarak karşıladığını
// kanıtlar (ADR 0001, ADR 0006).
//
// Buradaki arayüz bilinçli olarak inventory'nin hiçbir tipini adlandırmaz;
// saga onu tam olarak böyle yazar ve somut değeri container'dan
// "inventory.interop" adıyla çözer. İmzalar birer SÖZLEŞMEDİR: tüketici bu
// modülü import edemediği için bir imza kayması derleme zamanında GÖRÜNMEZ,
// yalnızca çözüm anında "arayüzü karşılamıyor" olarak patlar. Bu atama o anı
// erkene çeker.
func TestInteropSurfaceUsesPrimitiveTypes(t *testing.T) {
	// Tüketici paketin yazacağı arayüzün birebir kopyası.
	type inventoryReserver interface {
		Reserve(
			ctx context.Context,
			inventoryItemID, locationID string,
			quantity int64,
			lineItemID string,
		) (reservationID string, err error)
		ReleaseReservation(ctx context.Context, reservationID string) error
		ConfirmReservation(ctx context.Context, reservationID string) error
	}
	// Lokasyon adaylarını soran yüzey AYRI bir arayüzdür: onu kullanan akış
	// (hangi depodan gönderileceğini seçen adım) rezervasyon yapmaz ve dar
	// arayüz yalnızca gerçekten çağırdığı metodu istemelidir.
	type inventoryLocations interface {
		LocationsWithStock(ctx context.Context, inventoryItemID string, quantity int64) ([]string, error)
	}

	svc, _ := yeniServis(t)
	interop := service.NewInterop(svc)

	var (
		_ inventoryReserver  = interop
		_ inventoryLocations = interop
	)
}

// TestInteropLocationsWithStockAdaylariDoner yüzeyin yalnızca imzayı
// çevirdiğini, yani servisle AYNI cevabı verdiğini doğrular.
//
// Yüzeyde ayrıca bir süzme ya da sıralama yapılsaydı, aynı soru iki yerden
// iki farklı cevap alırdı; interop'un kural taşımama sözü tam olarak budur.
func TestInteropLocationsWithStockAdaylariDoner(t *testing.T) {
	ctx := context.Background()
	svc, store := yeniServis(t)
	interop := service.NewInterop(svc)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 5)
	store.seedLevel(itemID, locB, 10, 6)

	locations, err := interop.LocationsWithStock(ctx, itemID, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{locA}, locations)

	beklenen, err := svc.LocationsWithStock(ctx, itemID, 5)
	require.NoError(t, err)
	assert.Equal(t, beklenen, locations, "yüzey servisin cevabını değiştirmemeli")
}

// TestInteropLocationsWithStockAdaySizsaBosDilim yeterli stok yokken yüzeyin
// hata değil BOŞ dilim döndüğünü doğrular.
//
// Saga için ayrım kritiktir: hata, akışın telafi zincirini tetikler; boş liste
// ise akışın kendi bağlamında (örneğin "bu satır gönderilemez") Conflict'e
// çevireceği normal bir cevaptır.
func TestInteropLocationsWithStockAdaySizsaBosDilim(t *testing.T) {
	svc, store := yeniServis(t)
	interop := service.NewInterop(svc)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 9)

	locations, err := interop.LocationsWithStock(context.Background(), itemID, 5)

	require.NoError(t, err, "yeterli stok olmaması bir arıza değildir")
	assert.Empty(t, locations)
	assert.NotNil(t, locations, "boş dilim dönmeli, nil değil")
}
