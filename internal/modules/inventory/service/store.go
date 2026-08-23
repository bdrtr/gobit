package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/inventory/models"
)

// Store servisin ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü). Servis
// repository paketini import ETMEZ; somut depo bu imzaları yapısal olarak
// karşılar ve bağlantı module.go'da kurulur. Böylece birim testleri gerçek bir
// veritabanı olmadan, birkaç satırlık bir sahte depo ile yazılabilir.
//
// # İşlem sınırı
//
// [Store.WithTx] verilen işlevi tek bir veritabanı işleminde çalıştırır ve
// işlemi işlevin aldığı context ile taşır. Bu yüzden işlem içindeki her çağrı
// İŞLEVE VERİLEN ctx ile yapılmalıdır; dıştaki ctx kullanılırsa o çağrı
// işlemin dışında kalır ve atomiklik sessizce kaybolur.
//
// Lock ile başlayan metotlar satırı işlem sonuna kadar kilitler ve yalnızca
// [Store.WithTx] içinde çağrılabilir. Stok adedini değiştiren her akış, okumayı
// bu metotlarla yapar: kilitsiz okunan bir adet yazma anında bayat olabilir ve
// iki eşzamanlı rezervasyon aynı son adedi alabilirdi.
//
// # Kilit sırası
//
// Kilitler HER AKIŞTA aynı sırada alınır: önce KALEM
// ([Store.LockInventoryItem] ya da [Store.LockInventoryItemShared]), sonra
// SEVİYE ([Store.LockInventoryLevel]). Sıra akışa göre değişirse iki işlem
// birbirini bekler ve veritabanı kilitlenmeyi (deadlock) saptayıp birini
// öldürür. Kalem kilidi yalnızca "kalem var mı" kontrolü değildir; sıranın
// kendisidir ve rezervasyon INSERT'ünün foreign key yüzünden zaten isteyeceği
// örtük kalem kilidini ÖNCEDEN, doğru sırada alır.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateStockLocation yeni bir stok lokasyonu kaydeder.
	CreateStockLocation(ctx context.Context, loc models.StockLocation) (models.StockLocation, error)
	// GetStockLocation lokasyonu kimliğiyle döner; yoksa NotFound.
	GetStockLocation(ctx context.Context, id string) (models.StockLocation, error)
	// ListStockLocations lokasyonları sayfalar; ikinci değer toplam satır sayısıdır.
	ListStockLocations(ctx context.Context, limit, offset int64) ([]models.StockLocation, int64, error)

	// CreateInventoryItem yeni bir stok kalemi kaydeder.
	CreateInventoryItem(ctx context.Context, item models.InventoryItem) (models.InventoryItem, error)
	// GetInventoryItem kalemi kimliğiyle döner; yoksa NotFound.
	GetInventoryItem(ctx context.Context, id string) (models.InventoryItem, error)
	// LockInventoryItem kalemi işlem boyunca DIŞLAYICI kilitler ve varlığını
	// doğrular. Kalemin yapısını değiştiren akışlar (seviye oluşturma, silme)
	// bunu kullanır.
	LockInventoryItem(ctx context.Context, id string) error
	// LockInventoryItemShared kalemi işlem boyunca PAYLAŞIMLI kilitler ve
	// varlığını doğrular. Yalnızca adetlere dokunan akışlar bunu kullanır:
	// birbirlerini beklemezler ama dışlayıcı kilitle çakışırlar.
	LockInventoryItemShared(ctx context.Context, id string) error
	// ListInventoryItems kalemleri filtreleyip sayfalar; ikinci değer toplam sayıdır.
	ListInventoryItems(ctx context.Context, filter models.InventoryItemFilter) ([]models.InventoryItem, int64, error)
	// InventoryItemsByIDs kimlik kümesini TEK sorguda getirir (N+1 yok).
	InventoryItemsByIDs(ctx context.Context, ids []string) ([]models.InventoryItem, error)
	// SoftDeleteInventoryItem kalemi yumuşak siler.
	SoftDeleteInventoryItem(ctx context.Context, id string) error
	// SoftDeleteInventoryLevelsByItem kalemin tüm seviyelerini yumuşak siler.
	SoftDeleteInventoryLevelsByItem(ctx context.Context, itemID string) error

	// LockInventoryLevel seviyeyi kilitler ve güncel hâlini döner; yoksa NotFound.
	LockInventoryLevel(ctx context.Context, itemID, locationID string) (models.InventoryLevel, error)
	// CreateInventoryLevel yeni bir seviye kaydeder.
	CreateInventoryLevel(ctx context.Context, level models.InventoryLevel) (models.InventoryLevel, error)
	// UpdateInventoryLevelQuantities adetleri MUTLAK değerlerle yazar.
	UpdateInventoryLevelQuantities(ctx context.Context, levelID string, stocked, reserved int64) (models.InventoryLevel, error)
	// ListInventoryLevels kalemin tüm lokasyonlardaki seviyelerini döner.
	ListInventoryLevels(ctx context.Context, itemID string) ([]models.InventoryLevel, error)
	// AvailableByItemIDs kalem başına satılabilir toplamı TEK sorguda döner.
	// Hiç seviyesi olmayan kalem sonuçta yer almaz.
	AvailableByItemIDs(ctx context.Context, ids []string) (map[string]int64, error)

	// CreateReservation yeni bir rezervasyon kaydeder.
	CreateReservation(ctx context.Context, res models.Reservation) (models.Reservation, error)
	// LockReservation rezervasyonu kilitler ve güncel hâlini döner; yoksa NotFound.
	LockReservation(ctx context.Context, id string) (models.Reservation, error)
	// GetReservation rezervasyonu kilitlemeden döner; yoksa NotFound.
	GetReservation(ctx context.Context, id string) (models.Reservation, error)
	// SetReservationStatus rezervasyonun durumunu yazar.
	SetReservationStatus(ctx context.Context, id string, status models.ReservationStatus) error
	// CountActiveReservations kalemin aktif rezervasyon sayısını döner.
	CountActiveReservations(ctx context.Context, itemID string) (int64, error)
}
