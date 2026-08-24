package provider

import (
	"context"
	"encoding/json"
)

// FulfillmentStatus bir gönderinin sağlayıcı tarafındaki durumudur.
type FulfillmentStatus string

// Gönderi durumları.
const (
	// FulfillmentPending gönderi oluşturuldu, henüz teslim alınmadı.
	FulfillmentPending FulfillmentStatus = "pending"
	// FulfillmentShipped kargo firması gönderiyi teslim aldı.
	FulfillmentShipped FulfillmentStatus = "shipped"
	// FulfillmentDelivered gönderi alıcıya ulaştı.
	FulfillmentDelivered FulfillmentStatus = "delivered"
	// FulfillmentCanceled gönderi iptal edildi.
	FulfillmentCanceled FulfillmentStatus = "canceled"
)

// ShippingQuote bir kargo seçeneğinin belirli bir sepet/adres için fiyatıdır.
type ShippingQuote struct {
	// OptionID fiyatın ait olduğu kargo seçeneğidir.
	OptionID string
	// Amount kargo ücretidir, minor unit TAM SAYI (plan Bölüm 8).
	Amount int64
	// CurrencyCode ISO 4217 para birimi kodudur.
	CurrencyCode string
	// Data sağlayıcının döndürdüğü ham veridir; çekirdek yorumlamaz.
	Data json.RawMessage
}

// QuoteInput fiyat sorgusunun girdisidir.
type QuoteInput struct {
	// OptionID fiyatı sorulan kargo seçeneğidir.
	OptionID string
	// CurrencyCode beklenen para birimidir.
	CurrencyCode string
	// CountryCode teslimat ülkesidir (ISO 3166-1 alpha-2).
	CountryCode string
	// TotalWeight gönderinin toplam ağırlığıdır (gram); bilinmiyorsa sıfır.
	TotalWeight int64
	// ItemCount gönderideki kalem adedidir.
	ItemCount int64
	// Data sağlayıcıya özgü serbest veridir.
	Data map[string]any
}

// CreateFulfillmentInput gönderi oluşturmanın girdisidir.
type CreateFulfillmentInput struct {
	// Reference çağıranın kendi kaydına verdiği kimliktir (örn. fulfillment
	// kimliği). Sağlayıcı bunu kendi tarafında saklar; mutabakatta iki sistemi
	// eşleştiren alan budur.
	Reference string
	// OptionID kullanılacak kargo seçeneğidir.
	OptionID string
	// IdempotencyKey aynı gönderinin iki kez oluşturulmasını engeller.
	//
	// Saga bir adımı yeniden deneyebilir (plan Bölüm 2.6); anahtar olmadan
	// tekrar, İKİNCİ BİR KARGO ETİKETİ demek olurdu.
	IdempotencyKey string
	// Data sağlayıcıya özgü serbest veridir (adres, kalem listesi vb.).
	Data map[string]any
}

// Fulfillment sağlayıcıda oluşturulmuş bir gönderidir.
type Fulfillment struct {
	// ID sağlayıcı tarafındaki gönderi kimliğidir.
	ID string
	// Status gönderinin güncel durumudur.
	Status FulfillmentStatus
	// TrackingNumber ve TrackingURL takip bilgisidir; sağlayıcı vermiyorsa boş.
	TrackingNumber string
	TrackingURL    string
	// Data sağlayıcının döndürdüğü ham veridir.
	Data json.RawMessage
}

// FulfillmentProvider bir kargo sağlayıcısının çekirdeğe sunduğu sözleşmedir
// (plan Bölüm 5.6).
//
// # İdempotency ve saga
//
// [PaymentProvider] ile aynı kural geçerlidir: metotlar saga adımlarından
// çağrılır ve saga bir adımı YENİDEN DENEYEBİLİR.
//   - Create, aynı IdempotencyKey ile ikinci kez çağrıldığında YENİ gönderi
//     oluşturmaz, mevcut olanı döner.
//   - Cancel saga telafisidir ve İDEMPOTENT olmak zorundadır: iki kez iptal
//     edilen bir gönderi ikinci çağrıda hata VERMEZ.
//
// # Fiyat sorgusu yan etkisizdir
//
// Quote hiçbir şey oluşturmaz ve tekrar çağrılabilir; sepet toplamı
// hesaplanırken defalarca çağrılabileceği için ucuz olmalıdır.
type FulfillmentProvider interface {
	Provider

	// Quote verilen seçenek için kargo ücretini döner. YAN ETKİSİZDİR.
	Quote(ctx context.Context, in QuoteInput) (ShippingQuote, error)

	// Create sağlayıcıda bir gönderi oluşturur.
	Create(ctx context.Context, in CreateFulfillmentInput) (Fulfillment, error)

	// Cancel gönderiyi iptal eder. Saga telafisidir; İDEMPOTENT olmalıdır.
	Cancel(ctx context.Context, fulfillmentID string) error
}
