package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya order modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// internal/workflows altındaki complete_cart saga'sı siparişi bu modül
// üzerinden açar ve telafide iptal eder, ama ne o paket bu modülü ne bu modül o
// paketi import edebilir. Çözüm cart/region/pricing modüllerindeki interop.go
// ile aynıdır: yalnızca İLKEL ve stdlib tipleri kullanan bir yüzey yayımlamak.
// Tüketici kendi dar arayüzünü tanımlar, bu tip onu YAPISAL olarak karşılar ve
// container'dan "order.interop" adıyla çözülür.
//
// Tüketici tarafındaki karşılığı şudur (workflow kendi paketinde tanımlar):
//
//	type OrderPlacer interface {
//	    PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (string, error)
//	    CancelOrder(ctx context.Context, orderID, reason string) error
//	}
//
// Bileşik veri (sepetin anlık görüntüsü) JSON olarak taşınır. Alan adları
// aşağıda AÇIKÇA beyan edilir; tüketici tarafındaki şema ile birebir aynı olmak
// ZORUNDADIR ve uyum ancak entegrasyon testiyle kanıtlanabilir — bu modül
// workflow paketini import edemediği için derleyici uyumu denetleyemez.

// CodeInteropSnapshotInvalid çözülemeyen bir anlık görüntü gövdesi geldiğini
// bildirir.
const CodeInteropSnapshotInvalid = "order_interop_snapshot_invalid"

// Interop order servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. Tüm iş
// kuralları [Service] üzerinde kalır; buraya kural eklemek, aynı kuralın iki
// yerde ayrışması demek olurdu.
//
// Container'a "order.interop" adıyla kaydedilir.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopSnapshot siparişe dönüşecek sepet görüntüsünün JSON şemasıdır.
//
// # Şema
//
//	{
//	  "cart_id":         "cart_01H…",   // opsiyonel; yalnızca kökeni belgeler
//	  "region_id":       "reg_01H…",    // ZORUNLU
//	  "customer_id":     "cus_01H…",    // opsiyonel; boşsa sipariş misafirindir
//	  "email":           "a@b.com",     // opsiyonel
//	  "currency_code":   "TRY",         // ZORUNLU, ISO 4217
//	  "idempotency_key": "wf_01H…",     // opsiyonel; saga'da DOLDURULMALIDIR
//	  "subtotal":        3000,          // minor unit TAM SAYI
//	  "discount_total":  0,
//	  "tax_total":       600,
//	  "shipping_total":  2500,
//	  "total":           6100,
//	  "metadata":        {"kanal": "web"},
//	  "items": [                        // EN AZ BİR satır
//	    {
//	      "variant_id":     "variant_01H…",
//	      "title":          "Kırmızı Tişört",
//	      "quantity":       3,
//	      "unit_price":     1000,
//	      "subtotal":       3000,
//	      "discount_total": 0,
//	      "tax_total":      600,
//	      "total":          3600,
//	      "metadata":       {}
//	    }
//	  ]
//	}
//
// # Görüntüyü kim kurar
//
// complete_cart workflow'u. Sepetin kendi anlık görüntüsü (cart modülünün
// "cart.interop" yüzeyi) satır KİMLİKLERİNİ ve adetleri taşır, hesaplanan
// tutarları ise calculate_totals'ın çıktısı taşır; ikisini birleştirip bu şemayı
// üreten taraf workflow'dur. order modülü ne sepeti ne fiyatlamayı bilir.
//
// # Bilinmeyen alanlar YOK SAYILIR
//
// Çözümleme DisallowUnknownFields KULLANMAZ. Sebep bilinçlidir: tüketici,
// elindeki daha geniş bir görüntüyü (örn. sepetin revision'ı, kargo yöntemleri)
// olduğu gibi geçirebilmelidir ve o alanlar siparişin işine yaramaz. Katı
// çözümleme, tüketici tarafına yeni bir alan eklendiğinde bu modülü de
// değiştirmeyi zorunlu kılar ve iki paketi derleme zamanı bağımlılığı olmadan
// birbirine kilitlerdi. EKSİK alanlar ise yok sayılmaz: zorunlu alanların
// eksikliği [Service.CreateOrder]'ın doğrulamasında errors.Invalid ile döner.
type interopSnapshot struct {
	CartID         string             `json:"cart_id"`
	RegionID       string             `json:"region_id"`
	CustomerID     string             `json:"customer_id"`
	Email          string             `json:"email"`
	CurrencyCode   string             `json:"currency_code"`
	IdempotencyKey string             `json:"idempotency_key"`
	Subtotal       int64              `json:"subtotal"`
	DiscountTotal  int64              `json:"discount_total"`
	TaxTotal       int64              `json:"tax_total"`
	ShippingTotal  int64              `json:"shipping_total"`
	Total          int64              `json:"total"`
	Metadata       map[string]any     `json:"metadata"`
	Items          []interopOrderItem `json:"items"`
}

// interopOrderItem bir sipariş satırının JSON şemasıdır.
type interopOrderItem struct {
	VariantID     string         `json:"variant_id"`
	Title         string         `json:"title"`
	Quantity      int64          `json:"quantity"`
	UnitPrice     int64          `json:"unit_price"`
	Subtotal      int64          `json:"subtotal"`
	DiscountTotal int64          `json:"discount_total"`
	TaxTotal      int64          `json:"tax_total"`
	Total         int64          `json:"total"`
	Metadata      map[string]any `json:"metadata"`
}

// PlaceOrderJSON sepetin anlık görüntüsünden sipariş açar ve kimliğini döner.
//
// Şema [interopSnapshot] belgesinde tanımlıdır. Görüntüdeki
// "idempotency_key" doluysa çağrı İDEMPOTENTTİR: aynı anahtarla ikinci çağrı
// yeni sipariş açmaz, mevcut siparişin kimliğini döner. Saga bir adımı yeniden
// deneyebildiği için (plan Bölüm 2.6) bu alanın doldurulması complete_cart'ın
// sorumluluğundadır.
func (i *Interop) PlaceOrderJSON(ctx context.Context, snapshot json.RawMessage) (string, error) {
	var gelen interopSnapshot
	if err := json.Unmarshal(snapshot, &gelen); err != nil {
		return "", errors.Wrap(err, errors.KindInvalid, CodeInteropSnapshotInvalid,
			"sipariş anlık görüntüsü çözülemedi")
	}

	items := make([]CreateOrderItemInput, 0, len(gelen.Items))
	for k := range gelen.Items {
		items = append(items, CreateOrderItemInput{
			VariantID:     gelen.Items[k].VariantID,
			Title:         gelen.Items[k].Title,
			Quantity:      gelen.Items[k].Quantity,
			UnitPrice:     gelen.Items[k].UnitPrice,
			Subtotal:      gelen.Items[k].Subtotal,
			DiscountTotal: gelen.Items[k].DiscountTotal,
			TaxTotal:      gelen.Items[k].TaxTotal,
			Total:         gelen.Items[k].Total,
			Metadata:      gelen.Items[k].Metadata,
		})
	}

	order, err := i.svc.CreateOrder(ctx, CreateOrderInput{
		RegionID:       gelen.RegionID,
		CustomerID:     gelen.CustomerID,
		Email:          gelen.Email,
		CurrencyCode:   gelen.CurrencyCode,
		CartID:         gelen.CartID,
		IdempotencyKey: gelen.IdempotencyKey,
		Subtotal:       gelen.Subtotal,
		DiscountTotal:  gelen.DiscountTotal,
		TaxTotal:       gelen.TaxTotal,
		ShippingTotal:  gelen.ShippingTotal,
		Total:          gelen.Total,
		Items:          items,
		Metadata:       gelen.Metadata,
	})
	if err != nil {
		return "", err
	}
	return order.ID, nil
}

// CancelOrder siparişi iptal eder; SAGA TELAFİSİDİR ve İDEMPOTENTTİR.
//
// Zaten iptal edilmiş bir sipariş ikinci çağrıda hata VERMEZ. Tamamlanmış bir
// siparişin iptali ise errors.Conflict döner; gerekçe için bkz.
// [Service.CancelOrder].
func (i *Interop) CancelOrder(ctx context.Context, orderID, reason string) error {
	return i.svc.CancelOrder(ctx, orderID, reason)
}

// CompleteOrder siparişi tamamlanmış olarak damgalar.
//
// İdempotent DEĞİLDİR: ikinci çağrı errors.Conflict döner (gerekçe için bkz.
// [Service.CompleteOrder]). İleri yönlü bir adım olduğu için saga'nın
// idempotency-key'i tekrarı zaten engeller.
func (i *Interop) CompleteOrder(ctx context.Context, orderID string) error {
	_, err := i.svc.CompleteOrder(ctx, orderID)
	return err
}
