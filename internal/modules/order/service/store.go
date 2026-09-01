package service

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// Store servisin ihtiyaç duyduğu kalıcılık yüzeyidir.
//
// Arayüz TÜKETEN tarafta, yani burada tanımlıdır (ADR 0001'in örüntüsü). Servis
// repository paketini import ETMEZ; somut depo bu imzaları yapısal olarak
// karşılar ve bağlantı module.go'da kurulur. Böylece birim testleri gerçek bir
// veritabanı olmadan, birkaç yüz satırlık bir sahte depo ile yazılabilir.
//
// # İşlem sınırı
//
// [Store.WithTx] verilen işlevi tek bir veritabanı işleminde çalıştırır ve
// işlemi işlevin aldığı context ile taşır. Bu yüzden işlem içindeki her çağrı
// İŞLEVE VERİLEN ctx ile yapılmalıdır; dıştaki ctx kullanılırsa o çağrı işlemin
// dışında kalır ve atomiklik sessizce kaybolur.
//
// [Store.LockOrder] siparişi işlem sonuna kadar kilitler ve yalnızca
// [Store.WithTx] içinde çağrılabilir. Siparişin DURUMUNU değiştiren her akış
// okumasını bu metotla yapar: kilitsiz okunan bir durum yazma anında bayat
// olabilir ve eşzamanlı bir iptal ile tamamlama birbirini ezerdi.
//
// [Store.WithReadTx] yazmayan ama BİRDEN ÇOK sorgu yapan okumalar içindir
// (bkz. [Service.GetOrder]). Kilit almaz; verdiği tek garanti, içindeki tüm
// sorguların siparişin AYNI hâlini görmesidir. Ayrı bir metot olmasının sebebi
// yalıtım düzeyidir: PostgreSQL'in varsayılan READ COMMITTED düzeyinde her
// DEYİM kendi anlık görüntüsünü alır, yani sorguları sıradan bir işleme sarmak
// yırtık görünümü ENGELLEMEZ; engelleyen şey REPEATABLE READ'dir.
//
// # Kilit sırası
//
// Kilit HER AKIŞTA aynı sırada alınır: önce SİPARİŞ, sonra çocuk kayıtlar.
// Çocuklar ayrıca kilitlenmez — sipariş kilidi zaten o siparişin tüm durum
// geçişlerini seri hâle getirir ve tek kilit, sıranın ters dönmesini
// (dolayısıyla kilitlenmeyi) imkânsız kılar.
//
// [Store.LockCustomerSpending] bu sıranın DIŞINDA değil, ÖNÜNDEDİR: harcama
// kilidi yalnızca sipariş AÇMA yolunda ve her zaman ilk iş olarak alınır. Ters
// yönde bir bekleme oluşamaz, çünkü sipariş satırını kilitleyen akışların
// (iptal, tamamla, arşivle) hiçbiri harcama kilidini istemez; iki kilidi ters
// sırada isteyen bir akış olmadığı sürece döngü kurulamaz.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
	// WithReadTx fn'i salt-okunur ve TEK ANLIK GÖRÜNTÜLÜ bir işlemde çalıştırır.
	WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateOrder yeni bir sipariş kaydeder. display_id VERİLMEZ; veritabanı
	// üretir ve dönen kayıtta bulunur.
	CreateOrder(ctx context.Context, order models.Order) (models.Order, error)
	// GetOrder siparişi kimliğiyle döner; yoksa NotFound.
	GetOrder(ctx context.Context, id string) (models.Order, error)
	// GetOrderByDisplayID siparişi insan okunur numarasıyla döner; yoksa NotFound.
	GetOrderByDisplayID(ctx context.Context, displayID int64) (models.Order, error)
	// GetOrderByIdempotencyKey anahtarla açılmış siparişi döner; yoksa NotFound.
	GetOrderByIdempotencyKey(ctx context.Context, key string) (models.Order, error)
	// LockOrder siparişi işlem boyunca kilitler ve güncel hâlini döner.
	LockOrder(ctx context.Context, id string) (models.Order, error)
	// ListOrders siparişleri filtreleyip sayfalar; ikinci değer toplam sayıdır.
	ListOrders(ctx context.Context, filter models.OrderFilter) ([]models.Order, int64, error)
	// OrdersByIDs kimlik kümesini TEK sorguda getirir (N+1 yok).
	OrdersByIDs(ctx context.Context, ids []string) ([]models.Order, error)
	// CancelOrder siparişi iptal eder; yalnızca 'pending' durumda etki eder.
	CancelOrder(ctx context.Context, id, reason string) (models.Order, error)
	// CompleteOrder siparişi tamamlar; yalnızca 'pending' durumda etki eder.
	CompleteOrder(ctx context.Context, id string) (models.Order, error)
	// ArchiveOrder siparişi arşivler; yalnızca 'completed' durumda etki eder.
	ArchiveOrder(ctx context.Context, id string) (models.Order, error)

	// LockCustomerSpending müşterinin harcama TOPLAMINI işlem sonuna kadar
	// kilitler ve yalnızca [Store.WithTx] içinde çağrılabilir.
	//
	// Kilit bir satıra değil, müşteri kimliğine bağlanır: korunan şey henüz
	// yazılmamış bir siparişi de kapsayan bir TOPLAMDIR ve var olan satırları
	// kilitleyen FOR UPDATE onu koruyamaz (bkz. repository paketi).
	LockCustomerSpending(ctx context.Context, customerID string) error
	// SumCustomerSpend müşterinin verilen para birimindeki harcamasını döner;
	// windowStart nil ise TÜM geçmiş toplanır.
	//
	// İptal edilmiş ve yumuşak silinmiş siparişler toplama girmez, iade edilen
	// tutar düşülür (bkz. queries/spending.sql).
	SumCustomerSpend(ctx context.Context, customerID, currencyCode string, windowStart *time.Time) (int64, error)

	// CreateLineItem yeni bir sipariş satırı kaydeder.
	CreateLineItem(ctx context.Context, item models.OrderLineItem) (models.OrderLineItem, error)
	// ListLineItems siparişin satırlarını oluşturulma sırasıyla döner.
	ListLineItems(ctx context.Context, orderID string) ([]models.OrderLineItem, error)

	// CreateSummary siparişin özet kaydını sıfırlanmış olarak açar.
	CreateSummary(ctx context.Context, summary models.OrderSummary) (models.OrderSummary, error)
	// GetSummary siparişin özetini döner; yoksa NotFound.
	GetSummary(ctx context.Context, orderID string) (models.OrderSummary, error)
	// SetSummaryTotals ödenen ve iade edilen kümülatif tutarları BİRLEŞTİRİR:
	// her alan yalnızca büyüyebilir, küçülen bir değer yazılmaz.
	SetSummaryTotals(ctx context.Context, orderID string, paid, refunded int64) (models.OrderSummary, error)

	// CreateReturn yeni bir iade kaydı açar.
	CreateReturn(ctx context.Context, ret models.Return) (models.Return, error)
	// GetReturn iade kaydını kimliğiyle döner; yoksa NotFound.
	GetReturn(ctx context.Context, id string) (models.Return, error)
	// ListReturns siparişin iade kayıtlarını sayfalar.
	ListReturns(ctx context.Context, filter models.ChildFilter) ([]models.Return, int64, error)

	// CreateExchange yeni bir değişim kaydı açar.
	CreateExchange(ctx context.Context, exchange models.Exchange) (models.Exchange, error)
	// GetExchange değişim kaydını kimliğiyle döner; yoksa NotFound.
	GetExchange(ctx context.Context, id string) (models.Exchange, error)
	// ListExchanges siparişin değişim kayıtlarını sayfalar.
	ListExchanges(ctx context.Context, filter models.ChildFilter) ([]models.Exchange, int64, error)

	// CreateClaim yeni bir hasar kaydı açar.
	CreateClaim(ctx context.Context, claim models.Claim) (models.Claim, error)
	// GetClaim hasar kaydını kimliğiyle döner; yoksa NotFound.
	GetClaim(ctx context.Context, id string) (models.Claim, error)
	// ListClaims siparişin hasar kayıtlarını sayfalar.
	ListClaims(ctx context.Context, filter models.ChildFilter) ([]models.Claim, int64, error)
}

// Linker servisin modüller arası bağ katmanından ihtiyaç duyduğu DAR yüzeydir.
//
// core/link'in tam arayüzü toplu okuma metotlarını da içerir; sipariş onların
// hiçbirini kullanmaz. Dar tutulması iki işe yarar: bağımlılık gerçekten
// kullanılan yüzeyle sınırlanır ve birim testlerinde sahte bir bağ servisi
// birkaç satırda yazılabilir.
type Linker interface {
	// Create fromID ile toID arasında bağ kurar; aynı çift ikinci kez
	// bağlanırsa çağrı no-op'tur.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete bağı kaldırır; bağ yoksa çağrı no-op'tur.
	Delete(ctx context.Context, name, fromID, toID string) error
	// List fromID'ye bağlı toID'leri döner.
	List(ctx context.Context, name, fromID string) ([]string, error)
}

// EventPublisher servisin olay veri yolundan ihtiyaç duyduğu DAR yüzeydir.
//
// core/eventbus ÇEKİRDEKTİR ve import edilmesi serbesttir (Prensip 2.4);
// buradaki daralma bağımlılığı azaltmak içindir: modül yalnızca YAYIMLAR, abone
// olmaz ve veri yolunu kapatmaz. [eventbus.EventBus]'ın tamamına bağlanmak,
// modülün abonelik ve kapatma yetkisi varmış izlenimi verirdi.
//
// [eventbus.Event] tipi olduğu gibi kullanılır: olayın şekli çekirdeğin
// sözleşmesidir ve burada yeniden tanımlanması iki tipin ayrışmasına yol
// açardı.
type EventPublisher interface {
	// Publish olayı yayımlar ve handler'ları BEKLEMEZ.
	Publish(ctx context.Context, e eventbus.Event) error
}
