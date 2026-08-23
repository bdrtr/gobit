package service_test

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/inventory/models"
	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// Testlerde kullanılan sabit kimlikler.
const (
	itemID  = "invitem_TEST"
	locA    = "sloc_A"
	locB    = "sloc_B"
	resID   = "invres_TEST"
	unknown = "invitem_YOK"
)

// yeniServis sahte depo üzerinde çalışan bir servis kurar.
func yeniServis(t *testing.T) (*service.Service, *fakeStore) {
	t.Helper()

	store := newFakeStore()
	return service.New(store, nil), store
}

// TestAvailableQuantityTumLokasyonlariToplar satılabilir adedin TÜM
// lokasyonlardaki (stocked - reserved) farklarının toplamı olduğunu doğrular.
//
// Fikstür bilinçlidir: iki lokasyonun rezerve adetleri FARKLIDIR ve toplam
// fiziksel adet (30) ile toplam satılabilir adet (18) birbirinden uzaktır.
// Rezerveyi düşmeyi unutan ya da yalnızca tek lokasyonu toplayan bir uygulama
// bu sayıyı tutturamaz.
func TestAvailableQuantityTumLokasyonlariToplar(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedLevel(itemID, locB, 20, 8)

	available, err := svc.AvailableQuantity(context.Background(), itemID)

	require.NoError(t, err)
	assert.Equal(t, int64(18), available, "(10-4) + (20-8) = 18 olmalı")
}

// TestAvailableQuantitySeviyesizKalemSifir hiç stok seviyesi olmayan kalemin
// sıfır döndürdüğünü doğrular; bu bir hata değildir.
func TestAvailableQuantitySeviyesizKalemSifir(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")

	available, err := svc.AvailableQuantity(context.Background(), itemID)

	require.NoError(t, err)
	assert.Zero(t, available)
}

// TestAvailableQuantityOlmayanKalemNotFound olmayan bir kalem için sıfır
// değil, NotFound dönüldüğünü doğrular: "stoğu yok" ile "kendisi yok" farklı
// durumlardır ve çağıran ikisini ayırt edebilmelidir.
func TestAvailableQuantityOlmayanKalemNotFound(t *testing.T) {
	svc, _ := yeniServis(t)

	_, err := svc.AvailableQuantity(context.Background(), unknown)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestAdjustInventoryNegatifStoguReddeder stoğu negatife düşürecek bir
// düzeltmenin Conflict ile reddedildiğini ve HİÇBİR ŞEYİN yazılmadığını
// doğrular.
func TestAdjustInventoryNegatifStoguReddeder(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 0)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, -6)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err))
	assert.Equal(t, int64(5), store.level(itemID, locA).StockedQuantity,
		"reddedilen düzeltme stoğa dokunmamalı")
}

// TestAdjustInventorySifiraKadarInebilir sınırın tam olarak sıfır olduğunu
// doğrular: -5 kabul edilir, -6 edilmez.
func TestAdjustInventorySifiraKadarInebilir(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 0)

	level, err := svc.AdjustInventory(context.Background(), itemID, locA, -5)

	require.NoError(t, err)
	assert.Zero(t, level.StockedQuantity)
	assert.Zero(t, level.Available())
}

// TestAdjustInventoryRezerveAdedinAltinaInemez rezerve edilmiş adedin altına
// inen bir düzeltmenin reddedildiğini doğrular. Sınır sıfır DEĞİL, rezerve
// adettir: 5 fiziksel / 3 rezerve stokta -3 düzeltmesi fiziksel adedi 2'ye
// indirirdi ve satılabilir adet -1 olurdu.
func TestAdjustInventoryRezerveAdedinAltinaInemez(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 3)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, -3)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, int64(5), store.level(itemID, locA).StockedQuantity)

	// Rezerve adede kadar inmek serbesttir.
	level, err := svc.AdjustInventory(context.Background(), itemID, locA, -2)
	require.NoError(t, err)
	assert.Equal(t, int64(3), level.StockedQuantity)
	assert.Zero(t, level.Available())
}

// TestAdjustInventoryStoguArtirir pozitif düzeltmenin stoğu artırdığını ve
// rezerve adede dokunmadığını doğrular.
func TestAdjustInventoryStoguArtirir(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 2)

	level, err := svc.AdjustInventory(context.Background(), itemID, locA, 7)

	require.NoError(t, err)
	assert.Equal(t, int64(12), level.StockedQuantity)
	assert.Equal(t, int64(2), level.ReservedQuantity, "rezerve adet değişmemeli")
	assert.Equal(t, int64(10), level.Available())
}

// TestAdjustInventoryDeltaSifirInvalid anlamsız bir düzeltmenin sessizce
// başarılı sayılmadığını doğrular.
func TestAdjustInventoryDeltaSifirInvalid(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 0)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, 0)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestAdjustInventorySeviyeYoksaNotFound olmayan bir (kalem, lokasyon) çifti
// için düzeltmenin NotFound döndüğünü doğrular; seviye kendiliğinden
// oluşturulmaz.
func TestAdjustInventorySeviyeYoksaNotFound(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, 5)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestSetInventoryLevelOlusturur seviye yoksa oluşturulduğunu doğrular.
func TestSetInventoryLevelOlusturur(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")

	level, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 12)

	require.NoError(t, err)
	assert.Equal(t, int64(12), level.StockedQuantity)
	assert.Zero(t, level.ReservedQuantity)
	assert.Equal(t, itemID, level.InventoryItemID)
	assert.Equal(t, locA, level.LocationID)
	assert.NotEmpty(t, level.ID)
}

// TestSetInventoryLevelGunceller var olan seviyenin fiziksel adedini mutlak
// olarak yazdığını, rezerve adede DOKUNMADIĞINI doğrular.
func TestSetInventoryLevelGunceller(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 3)

	level, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 9)

	require.NoError(t, err)
	assert.Equal(t, int64(9), level.StockedQuantity)
	assert.Equal(t, int64(3), level.ReservedQuantity)
	assert.Equal(t, int64(6), level.Available())
}

// TestSetInventoryLevelRezerveAdedinAltinaInemez sayım düzeltmesinin söz
// verilmiş stoğu yok edemeyeceğini doğrular.
func TestSetInventoryLevelRezerveAdedinAltinaInemez(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 5, 3)

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, 2)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, int64(5), store.level(itemID, locA).StockedQuantity)
}

// TestSetInventoryLevelNegatifAdetInvalid negatif fiziksel adedin
// reddedildiğini doğrular.
func TestSetInventoryLevelNegatifAdetInvalid(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")

	_, err := svc.SetInventoryLevel(context.Background(), itemID, locA, -1)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSetInventoryLevelOlmayanKalemNotFound olmayan bir kalem için seviye
// açılamadığını doğrular.
func TestSetInventoryLevelOlmayanKalemNotFound(t *testing.T) {
	svc, _ := yeniServis(t)

	_, err := svc.SetInventoryLevel(context.Background(), unknown, locA, 5)

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestReserveRezerveAdediArtirir başarılı rezervasyonun rezerve adedi
// artırdığını, fiziksel adede DOKUNMADIĞINI ve aktif bir kayıt bıraktığını
// doğrular.
func TestReserveRezerveAdediArtirir(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 2)

	res, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID,
		LocationID:      locA,
		Quantity:        3,
		LineItemID:      "li_1",
	})

	require.NoError(t, err)
	assert.Equal(t, models.ReservationActive, res.Status)
	assert.Equal(t, int64(3), res.Quantity)
	assert.Equal(t, "li_1", res.LineItemID)

	level := store.level(itemID, locA)
	assert.Equal(t, int64(10), level.StockedQuantity, "fiziksel adet değişmemeli")
	assert.Equal(t, int64(5), level.ReservedQuantity)
	assert.Equal(t, int64(5), level.Available())
}

// TestReserveYetersizStokConflict satılabilir adetten fazlasının
// rezerve edilemediğini ve reddedilen isteğin hiçbir iz bırakmadığını
// doğrular. Sınır fiziksel adet DEĞİL, satılabilir adettir.
func TestReserveYetersizStokConflict(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 8) // satılabilir: 2

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 3,
	})

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeInsufficientStock, errors.CodeOf(err))
	assert.Equal(t, int64(8), store.level(itemID, locA).ReservedQuantity,
		"reddedilen rezervasyon rezerve adede dokunmamalı")
}

// TestReserveTamSonAdet satılabilir adedin TAMAMININ rezerve edilebildiğini
// doğrular; sınır "<" değil "<=" olmalıdır.
func TestReserveTamSonAdet(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 8)

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 2,
	})

	require.NoError(t, err)
	level := store.level(itemID, locA)
	assert.Equal(t, int64(10), level.ReservedQuantity)
	assert.Zero(t, level.Available())
}

// TestReservePozitifOlmayanAdetInvalid sıfır ve negatif adedin reddedildiğini
// doğrular.
func TestReservePozitifOlmayanAdetInvalid(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)

	for _, qty := range []int64{0, -1} {
		_, err := svc.Reserve(context.Background(), service.ReserveInput{
			InventoryItemID: itemID, LocationID: locA, Quantity: qty,
		})
		require.Error(t, err, "adet %d reddedilmeli", qty)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	}
}

// TestReserveRezervasyonYazilamazsaStokGeriAlinir rezervasyon kaydı
// oluşturulamadığında seviye güncellemesinin de geri alındığını doğrular:
// stok ile rezervasyon kaydı aynı işlemde yaşar, biri olmadan diğeri kalamaz.
func TestReserveRezervasyonYazilamazsaStokGeriAlinir(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	store.failCreateReservation = errors.Internal("test_hata", "rezervasyon yazılamadı")

	_, err := svc.Reserve(context.Background(), service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 4,
	})

	require.Error(t, err)
	assert.Zero(t, store.level(itemID, locA).ReservedQuantity,
		"işlem geri alındığı için rezerve adet artmamalı")
}

// TestReleaseReservationIdempotent telafinin iki kez çağrılabildiğini ve
// ikinci çağrının stoğa DOKUNMADIĞINI doğrular.
//
// Saga telafisi yeniden çalıştırılabilir olmak zorundadır; ikinci çağrı hata
// verirse workflow'un geri alma yolu patlar. İkinci çağrının rezerve adedi bir
// kez daha düşürmemesi de en az bunun kadar önemlidir — düşürseydi stok
// yoktan var edilirdi.
func TestReleaseReservationIdempotent(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 3)
	store.seedReservation(resID, itemID, locA, 3, models.ReservationActive)

	require.NoError(t, svc.ReleaseReservation(context.Background(), resID))
	assert.Zero(t, store.level(itemID, locA).ReservedQuantity)
	assert.Equal(t, models.ReservationReleased, store.reservation(resID).Status)

	yazmaSayisi := store.updateLevelCalls

	require.NoError(t, svc.ReleaseReservation(context.Background(), resID),
		"ikinci çağrı hata vermemeli")
	assert.Zero(t, store.level(itemID, locA).ReservedQuantity,
		"ikinci çağrı rezerve adedi bir kez daha düşürmemeli")
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity)
	assert.Equal(t, yazmaSayisi, store.updateLevelCalls,
		"ikinci çağrı stok seviyesine hiç yazmamalı")
}

// TestReleaseReservationBilinmeyenKimlikNotFound idempotentliğin "her şeyi
// yut" anlamına gelmediğini doğrular: hiç var olmamış bir kimlik hatadır.
func TestReleaseReservationBilinmeyenKimlikNotFound(t *testing.T) {
	svc, _ := yeniServis(t)

	err := svc.ReleaseReservation(context.Background(), "invres_YOK")

	require.Error(t, err)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
}

// TestReleaseReservationOnaylanmisConflict onaylanmış bir rezervasyonun geri
// alınamayacağını doğrular; stok fiziksel olarak düşülmüştür.
func TestReleaseReservationOnaylanmisConflict(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 7, 0)
	store.seedReservation(resID, itemID, locA, 3, models.ReservationConfirmed)

	err := svc.ReleaseReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeReservationNotActive, errors.CodeOf(err))
	assert.Equal(t, int64(7), store.level(itemID, locA).StockedQuantity)
}

// TestReleaseReservationTutarsizDurumInternal rezerve adedin rezervasyondan
// küçük olduğu bozuk veride sessizce sıfıra kırpılmadığını, hata dönüldüğünü
// doğrular. Kırpma, veri tutarsızlığını kalıcı olarak gizlerdi.
func TestReleaseReservationTutarsizDurumInternal(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 1)
	store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)

	err := svc.ReleaseReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, service.CodeInconsistentState, errors.CodeOf(err))
	assert.Equal(t, models.ReservationActive, store.reservation(resID).Status,
		"hata durumunda rezervasyon durumu değişmemeli")
}

// TestConfirmReservationStoktanDuser onayın hem fiziksel hem rezerve adedi
// düşürdüğünü, satılabilir adedi ise DEĞİŞTİRMEDİĞİNİ doğrular.
func TestConfirmReservationStoktanDuser(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationActive)
	oncekiAvailable := store.level(itemID, locA).Available()

	require.NoError(t, svc.ConfirmReservation(context.Background(), resID))

	level := store.level(itemID, locA)
	assert.Equal(t, int64(6), level.StockedQuantity)
	assert.Zero(t, level.ReservedQuantity)
	assert.Equal(t, oncekiAvailable, level.Available(),
		"onay satılabilir adedi değiştirmemeli; adet zaten söz verilmişti")
	assert.Equal(t, models.ReservationConfirmed, store.reservation(resID).Status)
}

// TestConfirmReservationIdempotent onayın ikinci kez çağrılabildiğini ve
// stoğu ikinci kez düşürmediğini doğrular.
func TestConfirmReservationIdempotent(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 4)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationActive)

	require.NoError(t, svc.ConfirmReservation(context.Background(), resID))
	yazmaSayisi := store.updateLevelCalls

	require.NoError(t, svc.ConfirmReservation(context.Background(), resID))
	assert.Equal(t, int64(6), store.level(itemID, locA).StockedQuantity)
	assert.Equal(t, yazmaSayisi, store.updateLevelCalls)
}

// TestConfirmReservationSerbestBirakilmisConflict serbest bırakılmış bir
// rezervasyonun onaylanamayacağını doğrular.
func TestConfirmReservationSerbestBirakilmisConflict(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	store.seedReservation(resID, itemID, locA, 4, models.ReservationReleased)

	err := svc.ConfirmReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity)
}

// TestReserveReleaseReserveDongusu telafiden sonra stoğun gerçekten yeniden
// satılabilir olduğunu doğrular. Faz 6'da bir saga başarısız olup yeniden
// denendiğinde bu döngü yaşanır.
func TestReserveReleaseReserveDongusu(t *testing.T) {
	ctx := context.Background()
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 1, 0)

	ilk, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.NoError(t, err)

	_, err = svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.Error(t, err, "son adet ayrılmışken ikinci rezervasyon olmamalı")

	require.NoError(t, svc.ReleaseReservation(ctx, ilk.ID))

	ikinci, err := svc.Reserve(ctx, service.ReserveInput{
		InventoryItemID: itemID, LocationID: locA, Quantity: 1,
	})
	require.NoError(t, err, "telafiden sonra adet yeniden satılabilir olmalı")
	assert.NotEqual(t, ilk.ID, ikinci.ID)
	assert.Equal(t, int64(1), store.level(itemID, locA).ReservedQuantity)
}

// TestDeleteInventoryItemAktifRezervasyonVarsaConflict söz verilmiş stoğu olan
// bir kalemin silinemediğini doğrular.
func TestDeleteInventoryItemAktifRezervasyonVarsaConflict(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 2)
	store.seedReservation(resID, itemID, locA, 2, models.ReservationActive)

	err := svc.DeleteInventoryItem(context.Background(), itemID)

	require.Error(t, err)
	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, service.CodeItemHasReservations, errors.CodeOf(err))

	_, getErr := svc.GetInventoryItem(context.Background(), itemID)
	require.NoError(t, getErr, "kalem silinmemiş olmalı")
}

// TestDeleteInventoryItemSeviyeleriyleSiler sonlanmış rezervasyonların silmeyi
// engellemediğini ve kalemin seviyeleriyle birlikte silindiğini doğrular.
func TestDeleteInventoryItemSeviyeleriyleSiler(t *testing.T) {
	ctx := context.Background()
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 0)
	store.seedReservation(resID, itemID, locA, 2, models.ReservationReleased)

	require.NoError(t, svc.DeleteInventoryItem(ctx, itemID))

	_, err := svc.GetInventoryItem(ctx, itemID)
	assert.Equal(t, errors.KindNotFound, errors.KindOf(err))
	assert.Empty(t, store.level(itemID, locA).ID, "seviyeler de silinmeli")
}

// TestCreateInventoryItemVarsayilanSevkiyatGerektirir alan gönderilmediğinde
// kalemin sevkiyat gerektirdiğinin varsayıldığını doğrular.
func TestCreateInventoryItemVarsayilanSevkiyatGerektirir(t *testing.T) {
	svc, _ := yeniServis(t)

	item, err := svc.CreateInventoryItem(context.Background(), service.CreateInventoryItemInput{
		SKU: " SKU-BOSLUKLU ",
	})

	require.NoError(t, err)
	assert.True(t, item.RequiresShipping)
	assert.Equal(t, "SKU-BOSLUKLU", item.SKU, "sku'daki boşluklar kırpılmalı")
	assert.Contains(t, item.ID, models.InventoryItemIDPrefix)
}

// TestCreateInventoryItemSevkiyatKapatilabilir açıkça false gönderilirse
// varsayılanın ezildiğini doğrular.
func TestCreateInventoryItemSevkiyatKapatilabilir(t *testing.T) {
	svc, _ := yeniServis(t)
	sevkiyatYok := false

	item, err := svc.CreateInventoryItem(context.Background(), service.CreateInventoryItemInput{
		SKU: "DIJITAL-1", RequiresShipping: &sevkiyatYok,
	})

	require.NoError(t, err)
	assert.False(t, item.RequiresShipping)
}

// TestCreateInventoryItemBosSKUInvalid boş SKU'nun reddedildiğini doğrular.
func TestCreateInventoryItemBosSKUInvalid(t *testing.T) {
	svc, _ := yeniServis(t)

	_, err := svc.CreateInventoryItem(context.Background(), service.CreateInventoryItemInput{SKU: "   "})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestCreateStockLocationUlkeKoduDogrulanir ülke kodunun iki harfe
// normalleştirildiğini ve geçersizinin reddedildiğini doğrular.
func TestCreateStockLocationUlkeKoduDogrulanir(t *testing.T) {
	svc, _ := yeniServis(t)

	loc, err := svc.CreateStockLocation(context.Background(), service.CreateStockLocationInput{
		Name: "Merkez Depo", CountryCode: "tr", City: "İstanbul",
	})
	require.NoError(t, err)
	assert.Equal(t, "TR", loc.CountryCode)
	assert.Contains(t, loc.ID, models.StockLocationIDPrefix)

	_, err = svc.CreateStockLocation(context.Background(), service.CreateStockLocationInput{
		Name: "Hatalı", CountryCode: "TUR",
	})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestCreateStockLocationBosAdInvalid adsız lokasyonun reddedildiğini doğrular.
func TestCreateStockLocationBosAdInvalid(t *testing.T) {
	svc, _ := yeniServis(t)

	_, err := svc.CreateStockLocation(context.Background(), service.CreateStockLocationInput{Name: " "})

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListSayfalamaSinirlari limit/offset doğrulamasını ve varsayılanı
// sınar.
func TestListSayfalamaSinirlari(t *testing.T) {
	ctx := context.Background()
	svc, store := yeniServis(t)
	for _, id := range []string{"sloc_1", "sloc_2", "sloc_3"} {
		store.locations[id] = models.StockLocation{ID: id, Name: id}
	}

	sayfa, count, err := svc.ListStockLocations(ctx, service.Page{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, sayfa, 2)
	assert.Equal(t, int64(3), count, "count sayfayı değil toplamı bildirmeli")

	sayfa, _, err = svc.ListStockLocations(ctx, service.Page{Limit: 2, Offset: 2})
	require.NoError(t, err)
	assert.Len(t, sayfa, 1)

	_, _, err = svc.ListStockLocations(ctx, service.Page{Limit: service.MaxLimit + 1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, _, err = svc.ListStockLocations(ctx, service.Page{Offset: -1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestListInventoryItemsFiltreler SKU ve sevkiyat filtrelerini sınar.
func TestListInventoryItemsFiltreler(t *testing.T) {
	ctx := context.Background()
	svc, store := yeniServis(t)
	store.seedItem("invitem_1", "SKU-1")
	store.seedItem("invitem_2", "SKU-2")

	sku := "SKU-2"
	items, count, err := svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{SKU: &sku})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "invitem_2", items[0].ID)
	assert.Equal(t, int64(1), count)

	sevkiyatYok := false
	items, _, err = svc.ListInventoryItems(ctx, service.ListInventoryItemsInput{RequiresShipping: &sevkiyatYok})
	require.NoError(t, err)
	assert.Empty(t, items)
}

// TestAdjustInventoryTasmayiYakalar int64 sınırını aşan bir düzeltmenin sessiz
// sarma yerine hata ürettiğini doğrular. Sarma olsaydı sonuç negatife döner ve
// tüm adet kontrollerini atlatırdı.
func TestAdjustInventoryTasmayiYakalar(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, math.MaxInt64, 0)

	_, err := svc.AdjustInventory(context.Background(), itemID, locA, 1)

	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
	assert.Equal(t, int64(math.MaxInt64), store.level(itemID, locA).StockedQuantity)
}

// TestConfirmReservationFizikselStokYetmezseInternal onayın fiziksel stoğu
// negatife düşüreceği bozuk durumda yazma yapmadığını doğrular.
func TestConfirmReservationFizikselStokYetmezseInternal(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 2, 5)
	store.seedReservation(resID, itemID, locA, 3, models.ReservationActive)

	err := svc.ConfirmReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, service.CodeInconsistentState, errors.CodeOf(err))
	assert.Equal(t, int64(2), store.level(itemID, locA).StockedQuantity)
}

// TestConfirmReservationRezerveAdetYetmezseInternal rezerve adedin
// rezervasyondan küçük olduğu bozuk durumda onayın reddedildiğini doğrular.
// Fiziksel stok burada YETERLİDİR; hatayı doğuran yalnızca rezerve adettir.
func TestConfirmReservationRezerveAdetYetmezseInternal(t *testing.T) {
	svc, store := yeniServis(t)
	store.seedItem(itemID, "SKU-1")
	store.seedLevel(itemID, locA, 10, 1)
	store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)

	err := svc.ConfirmReservation(context.Background(), resID)

	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
	assert.Equal(t, models.ReservationActive, store.reservation(resID).Status)
	assert.Equal(t, int64(10), store.level(itemID, locA).StockedQuantity)
}

// TestKilitSirasiKalemdenSeviyeye seviyeye dokunan HER akışın kilitleri aynı
// sırada — önce kalem, sonra seviye — aldığını doğrular.
//
// Sıra bir eşzamanlılık sözleşmesidir: bir akış seviyeyi kalemden önce
// kilitlerse, kalemi önce kilitleyen bir akışla karşılaştığında veritabanı
// kilitlenmeyi (deadlock) saptayıp işlemlerden birini öldürür. İhlal gerçek
// veritabanında yalnızca YARIŞ altında görünür; burada doğrudan okunur.
func TestKilitSirasiKalemdenSeviyeye(t *testing.T) {
	akislar := []struct {
		ad    string
		cagir func(ctx context.Context, svc *service.Service) error
	}{
		{"SetInventoryLevel", func(ctx context.Context, svc *service.Service) error {
			_, err := svc.SetInventoryLevel(ctx, itemID, locA, 12)
			return err
		}},
		{"AdjustInventory", func(ctx context.Context, svc *service.Service) error {
			_, err := svc.AdjustInventory(ctx, itemID, locA, 1)
			return err
		}},
		{"Reserve", func(ctx context.Context, svc *service.Service) error {
			_, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: itemID, LocationID: locA, Quantity: 1,
			})
			return err
		}},
		{"ReleaseReservation", func(ctx context.Context, svc *service.Service) error {
			return svc.ReleaseReservation(ctx, resID)
		}},
		{"ConfirmReservation", func(ctx context.Context, svc *service.Service) error {
			return svc.ConfirmReservation(ctx, resID)
		}},
	}

	for _, akis := range akislar {
		t.Run(akis.ad, func(t *testing.T) {
			svc, store := yeniServis(t)
			store.seedItem(itemID, "SKU-1")
			store.seedLevel(itemID, locA, 10, 5)
			store.seedReservation(resID, itemID, locA, 5, models.ReservationActive)

			require.NoError(t, akis.cagir(context.Background(), svc))

			kilitler := store.kilitSirasi()
			require.Contains(t, kilitler, "item", "akış kalem kilidini hiç almamış: %v", kilitler)
			require.Contains(t, kilitler, "level", "akış seviye kilidini hiç almamış: %v", kilitler)
			assert.Less(t, slices.Index(kilitler, "item"), slices.Index(kilitler, "level"),
				"kalem kilidi seviye kilidinden ÖNCE alınmalı, alınan sıra: %v", kilitler)
		})
	}
}
