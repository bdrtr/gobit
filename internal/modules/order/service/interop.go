package service

import (
	"context"
	"encoding/json"
	"strconv"

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
// [Interop.CompleteOrder] o arayüzde BİLİNÇLİ OLARAK yoktur: tamamlanmış bir
// sipariş iptal edilemez, yani saga onu çağırdığı anda kendi telafisini
// imkânsız kılardı (gerekçe tüketici tarafında da yazılıdır).
//
// Yüzeyin İKİNCİ tüketicisi "order.placed" olayına abone olan BİLDİRİM
// tarafıdır ve o yalnızca OKUR. Olayın yükü bilinçli olarak dardır ve kişisel
// veri TAŞIMAZ (gerekçe: [EventFieldTotal] üzerindeki blok); abone gönderim
// için gereken e-postayı olaydan alamaz, elindeki order_id ile siparişi
// okuması gerekir. O da kendi dar arayüzünü kendi paketinde tanımlar:
//
//	type OrderContactReader interface {
//	    OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error)
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

// interopContact bildirim gönderecek abonenin siparişten okuduğu alanların
// JSON şemasıdır.
//
// # Şema
//
//	{
//	  "order_id":      "order_01H…",
//	  "display_id":    "1042",       // ondalıksız DİZE
//	  "email":         "a@b.com",    // BOŞ olabilir
//	  "currency_code": "TRY",
//	  "total":         "6100",       // minor unit, ondalıksız DİZE
//	  "item_count":    "2"           // ondalıksız DİZE
//	}
//
// # Neden TÜM değerler dize
//
// Alan adları ve tipleri "order.placed" olayının yüküyle BİREBİR aynıdır
// (bkz. [EventFieldOrderID] ve devamı). Abone iki kaynağı yan yana kullanır:
// olaydan order_id'yi alır, gerisini buradan okur. İkisi farklı tip
// kullansaydı — olayda dize, burada tam sayı — abone aynı alanı iki ayrı
// biçimde çözmek zorunda kalır ve tutar bir tarafta float64'e uğrardı
// (plan Bölüm 8: float ASLA). Tek biçim, aboneyi tek bir okuma kuralına
// bağlar.
//
// # Neden yalnızca bu alanlar
//
// Şablonun doldurabilmesi için gereken en küçük küme budur: kime (email),
// hangi sipariş (order_id, display_id) ve ne kadar (total, currency_code,
// item_count). Siparişin tamamını — satırları, adresi, özeti — dönmek yüzeyi
// bir daha daraltılamayacak geniş bir sözleşmeye çevirirdi: sözleşmeye giren
// alan, tüketicisi olmasa bile bir daha çıkarılamaz.
type interopContact struct {
	OrderID      string `json:"order_id"`
	DisplayID    string `json:"display_id"`
	Email        string `json:"email"`
	CurrencyCode string `json:"currency_code"`
	Total        string `json:"total"`
	ItemCount    string `json:"item_count"`
}

// OrderContactJSON siparişin bildirim için gereken alanlarını döner.
//
// Şema [interopContact] belgesinde tanımlıdır ve TÜM değerleri dizedir.
// Sipariş yoksa (ya da yumuşak silinmişse) errors.NotFound döner; kimlik boşsa
// errors.Invalid.
//
// # E-postası olmayan sipariş HATA DEĞİLDİR
//
// Email opsiyoneldir (bkz. [CreateOrderInput.Email]): yönetim tarafından
// açılan bir sipariş adressiz olabilir. Böyle bir siparişte alan BOŞ DİZE
// olarak döner, çağrı BAŞARILIDIR ve "email" anahtarı yine gövdededir.
//
// Alternatif — adressiz siparişte hata dönmek — iki yerde yanlış olurdu.
// Birincisi bu yüzey bir OKUMADIR: kaydın ne olduğunu bildirir, kaydın ne
// olması gerektiğine karar vermez; adressiz sipariş geçerli bir kayıttır.
// İkincisi ve asıl olanı, tüketicinin hatayı nasıl karşılayacağıdır: abone
// için "gönderilecek adres yok" KALICI bir durumdur ve atlanmalıdır, oysa
// hata dönmek onu yeniden denenecek bir arızadan ayırt edilemez hâle
// getirirdi — abone ya adressiz her siparişi sonsuza dek yeniden dener ya da
// gerçek arızaları da sessizce yutardı. Boş dize, tek bir kontrolle
// ayrılabilen kesin bir cevaptır.
//
// Okuma [Service.GetOrder] ile yapılır: satır sayısı ve sipariş başlığı AYNI
// anlık görüntüden gelir, dolayısıyla item_count her zaman dönen tutarın ait
// olduğu siparişin satır sayısıdır.
func (i *Interop) OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error) {
	detay, err := i.svc.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(interopContact{
		OrderID:      detay.ID,
		DisplayID:    strconv.FormatInt(detay.DisplayID, 10),
		Email:        detay.Email,
		CurrencyCode: detay.CurrencyCode,
		Total:        strconv.FormatInt(detay.Total, 10),
		ItemCount:    strconv.Itoa(len(detay.Items)),
	})
}
