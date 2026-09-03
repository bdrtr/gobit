package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/modules/cart/models"
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
// [Store.LockCart] sepeti işlem sonuna kadar kilitler ve yalnızca
// [Store.WithTx] içinde çağrılabilir. Sepeti değiştiren her akış okumasını bu
// metotla yapar: kilitsiz okunan bir sepet yazma anında bayat olabilir ve iki
// eşzamanlı ekleme aynı varyant için iki satır açabilirdi.
//
// [Store.WithReadTx] yazmayan ama BİRDEN ÇOK sorgu yapan okumalar içindir
// (bkz. [Service.GetCart]). Kilit almaz; verdiği tek garanti, içindeki tüm
// sorguların sepetin AYNI hâlini görmesidir. Ayrı bir metot olmasının sebebi
// yalıtım düzeyidir: PostgreSQL'in varsayılan READ COMMITTED düzeyinde her
// DEYİM kendi anlık görüntüsünü alır, yani sorguları sıradan bir işleme sarmak
// yırtık görünümü ENGELLEMEZ; engelleyen şey REPEATABLE READ'dir.
//
// # Kilit sırası
//
// Kilit HER AKIŞTA aynı sırada alınır: önce SEPET, sonra çocuk satırlar.
// Çocuklar ayrıca kilitlenmez — sepet kilidi zaten o sepetin tüm yazma
// yollarını seri hâle getirir ve tek kilit, sıranın ters dönmesini (dolayısıyla
// kilitlenmeyi) imkânsız kılar.
type Store interface {
	// WithTx fn'i tek bir işlemde çalıştırır; fn hata dönerse işlem geri alınır.
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error
	// WithReadTx fn'i salt-okunur ve TEK ANLIK GÖRÜNTÜLÜ bir işlemde çalıştırır.
	WithReadTx(ctx context.Context, fn func(ctx context.Context) error) error

	// CreateCart yeni bir sepet kaydeder.
	CreateCart(ctx context.Context, cart models.Cart) (models.Cart, error)
	// GetCart sepeti kimliğiyle döner; yoksa NotFound.
	GetCart(ctx context.Context, id string) (models.Cart, error)
	// LockCart sepeti işlem boyunca kilitler ve güncel hâlini döner.
	LockCart(ctx context.Context, id string) (models.Cart, error)
	// ListCarts sepetleri filtreleyip sayfalar; ikinci değer toplam sayıdır.
	ListCarts(ctx context.Context, filter models.CartFilter) ([]models.Cart, int64, error)
	// CartsByIDs kimlik kümesini TEK sorguda getirir (N+1 yok).
	CartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error)
	// UpdateCartContact sepetin e-posta ve müşteri alanlarını MUTLAK değerle
	// yazar.
	UpdateCartContact(ctx context.Context, id string, contact models.CartContact) (models.Cart, error)
	// UpdateCartTotals toplamları yazar ve hangi şekil için hesaplandıklarını
	// damgalar.
	UpdateCartTotals(ctx context.Context, id string, totals models.CartTotals) (models.Cart, error)
	// BumpCartRevision sepetin şekil sayacını bir artırır.
	BumpCartRevision(ctx context.Context, id string) (models.Cart, error)
	// MarkCartCompleted sepeti tamamlanmış olarak damgalar.
	MarkCartCompleted(ctx context.Context, id string) (models.Cart, error)
	// SoftDeleteCart sepeti yumuşak siler.
	SoftDeleteCart(ctx context.Context, id string) error

	// CreateLineItem yeni bir sepet satırı kaydeder.
	CreateLineItem(ctx context.Context, item models.LineItem) (models.LineItem, error)
	// GetLineItem satırı kimliğiyle döner; başka sepetin satırı NotFound'dur.
	GetLineItem(ctx context.Context, cartID, lineID string) (models.LineItem, error)
	// GetLineItemByVariant sepetteki varyantın yaşayan satırını döner.
	GetLineItemByVariant(ctx context.Context, cartID, variantID string) (models.LineItem, error)
	// ListLineItems sepetin satırlarını oluşturulma sırasıyla döner.
	ListLineItems(ctx context.Context, cartID string) ([]models.LineItem, error)
	// SetLineItemQuantity satırın adedini MUTLAK değerle yazar.
	SetLineItemQuantity(ctx context.Context, cartID, lineID string, quantity int64) (models.LineItem, error)
	// SetLineItemTotals bir hesap turunun TÜM satır tutarlarını TEK çağrıda
	// yazar; adede dokunmaz. Yazılamayan satır varsa NotFound döner ve hiçbiri
	// yazılmamış sayılır (çağrı işlemin içindedir).
	SetLineItemTotals(ctx context.Context, cartID string, lines []models.LineItemTotals) error
	// SoftDeleteLineItem satırı yumuşak siler.
	SoftDeleteLineItem(ctx context.Context, cartID, lineID string) error
	// SoftDeleteLineItemsByCart sepetin tüm satırlarını yumuşak siler.
	SoftDeleteLineItemsByCart(ctx context.Context, cartID string) error

	// UpsertCartAddress sepetin verilen türdeki adresini yazar.
	UpsertCartAddress(ctx context.Context, addr models.CartAddress) (models.CartAddress, error)
	// ListCartAddresses sepetin adreslerini döner.
	ListCartAddresses(ctx context.Context, cartID string) ([]models.CartAddress, error)
	// SoftDeleteCartAddressesByCart sepetin tüm adreslerini yumuşak siler.
	SoftDeleteCartAddressesByCart(ctx context.Context, cartID string) error

	// CreateShippingMethod sepete bir kargo yöntemi ekler.
	CreateShippingMethod(ctx context.Context, method models.ShippingMethod) (models.ShippingMethod, error)
	// ListShippingMethods sepetin kargo yöntemlerini döner.
	ListShippingMethods(ctx context.Context, cartID string) ([]models.ShippingMethod, error)
	// SoftDeleteShippingMethod kargo yöntemini yumuşak siler.
	SoftDeleteShippingMethod(ctx context.Context, cartID, methodID string) error
	// SoftDeleteShippingMethodsByCart sepetin tüm kargo yöntemlerini siler.
	SoftDeleteShippingMethodsByCart(ctx context.Context, cartID string) error
}
