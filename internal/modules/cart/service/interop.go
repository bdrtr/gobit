package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya cart modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// internal/workflows/cart sepet akışlarını yürütürken cart servisine ihtiyaç
// duyar, ama ne o paket bu modülü ne bu modül o paketi import edebilir. Çözüm,
// region/pricing/customer modüllerindeki interop.go ile aynıdır: yalnızca
// İLKEL ve stdlib tipleri kullanan bir yüzey yayımlamak. Tüketici kendi dar
// arayüzünü tanımlar, bu tip onu YAPISAL olarak karşılar ve container'dan adla
// çözülür.
//
// Bileşik veri (sepetin anlık görüntüsü, hesaplanan toplamlar) JSON olarak
// taşınır. Alan adları burada AÇIKÇA beyan edilir; tüketici tarafındaki şema
// ile birebir aynı olmak ZORUNDADIR ve uyum ancak entegrasyon testiyle
// kanıtlanabilir (bkz. internal/e2e).

// CodeInteropTotalsInvalid çözülemeyen bir toplam gövdesi geldiğini bildirir.
const CodeInteropTotalsInvalid = "e2e_kopru_totals_invalid"

// CodeInteropMetadataInvalid çözülemeyen bir satır metadata'sı geldiğini
// bildirir.
const CodeInteropMetadataInvalid = "cart_interop_metadata_invalid"

// Interop cart servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. Tüm iş
// kuralları [Service] üzerinde kalır; buraya kural eklemek, aynı kuralın iki
// yerde ayrışması demek olurdu.
//
// Container'a "cart.interop" adıyla kaydedilir ve sepet akışları onu kendi
// tanımladıkları dar arayüzle çözer (ADR 0006).
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopSnapshot sepetin hesaba giren şeklinin JSON şemasıdır.
//
// Alan adları tüketici tarafındaki şemayla BİREBİR aynı olmak zorundadır ve
// uyum ancak entegrasyon testiyle kanıtlanabilir: bu modül workflow paketini
// import edemediği için derleyici uyumu denetleyemez.
type interopSnapshot struct {
	ID              string                  `json:"id"`
	RegionID        string                  `json:"region_id"`
	CustomerID      string                  `json:"customer_id"`
	CurrencyCode    string                  `json:"currency_code"`
	Revision        int64                   `json:"revision"`
	Completed       bool                    `json:"completed"`
	Items           []interopItem           `json:"items"`
	ShippingMethods []interopShippingMethod `json:"shipping_methods"`
}

// interopItem bir sepet satırının JSON şemasıdır.
type interopItem struct {
	ID        string `json:"id"`
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
}

// interopShippingMethod bir kargo yönteminin JSON şemasıdır.
type interopShippingMethod struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
}

// interopTotals hesaplanan sepet toplamlarının JSON şemasıdır.
type interopTotals struct {
	Revision      int64               `json:"revision"`
	Subtotal      int64               `json:"subtotal"`
	DiscountTotal int64               `json:"discount_total"`
	TaxTotal      int64               `json:"tax_total"`
	ShippingTotal int64               `json:"shipping_total"`
	Total         int64               `json:"total"`
	Lines         []interopLineTotals `json:"lines"`
}

// interopLineTotals satır başına hesaplanan tutarların JSON şemasıdır.
type interopLineTotals struct {
	LineItemID    string `json:"line_item_id"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	Total         int64  `json:"total"`
}

// OpenCart yeni bir sepet açar ve kimliğini döner.
//
// currencyCode BÖLGENİN para birimidir ve çağıran onu bölgeden okumuş olmak
// zorundadır; bu yüzey o soruyu soramaz (cart modülü region'ı çağırmaz,
// ADR 0006). Bugünkü tek çağıran create_cart workflow'udur ve kodu ülke
// kodundan çözdüğü bölgeden alır — yani parametre bir SEÇİM değil, çağıranın
// yaptığı türetmenin taşıyıcısıdır. Vitrin ucu bu yüzeyden geçmez; para
// birimini kendi bölge okumasından türetir (bkz. api.RegionCurrencyReader).
func (i *Interop) OpenCart(ctx context.Context, regionID, currencyCode, customerID, email string) (string, error) {
	sepet, err := i.svc.CreateCart(ctx, CreateCartInput{
		RegionID:     regionID,
		CustomerID:   customerID,
		Email:        email,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return "", err
	}
	return sepet.ID, nil
}

// CartSnapshotJSON sepetin hesaba giren şeklini tek okumada döner.
//
// Okuma [Service.GetCart] ile yapılır; o metot dört sorguyu tek
// anlık görüntü üzerinde koşturduğu için satırlar, kargo yöntemleri ve
// revision AYNI ana aittir — şemanın istediği tutarlılık budur.
func (i *Interop) CartSnapshotJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	detay, err := i.svc.GetCart(ctx, cartID)
	if err != nil {
		return nil, err
	}

	anlik := interopSnapshot{
		ID:              detay.ID,
		RegionID:        detay.RegionID,
		CustomerID:      detay.CustomerID,
		CurrencyCode:    detay.CurrencyCode,
		Revision:        detay.Revision,
		Completed:       detay.Completed(),
		Items:           make([]interopItem, 0, len(detay.Items)),
		ShippingMethods: make([]interopShippingMethod, 0, len(detay.ShippingMethods)),
	}
	for i := range detay.Items {
		anlik.Items = append(anlik.Items, interopItem{
			ID:        detay.Items[i].ID,
			VariantID: detay.Items[i].VariantID,
			Quantity:  detay.Items[i].Quantity,
		})
	}
	for i := range detay.ShippingMethods {
		anlik.ShippingMethods = append(anlik.ShippingMethods, interopShippingMethod{
			ID:     detay.ShippingMethods[i].ID,
			Amount: detay.ShippingMethods[i].Amount,
		})
	}

	return json.Marshal(anlik)
}

// AddCartLineItem sepete satır ekler ve satırın kimliğini döner.
//
// metadata satırın serbest ek verisidir ve JSON NESNESİ olmak zorundadır; boş
// bırakılabilir. Bozuk bir gövde errors.Invalid'dir ve satır YAZILMAZ: sessizce
// atmak, istemcinin gönderdiğini sandığı ama hiçbir yerde bulunmayan bir alan
// bırakırdı.
func (i *Interop) AddCartLineItem(
	ctx context.Context,
	cartID, variantID, title string,
	quantity, unitPrice int64,
	metadata json.RawMessage,
) (string, error) {
	ek, err := decodeInteropMetadata(metadata)
	if err != nil {
		return "", err
	}

	satir, err := i.svc.AddLineItem(ctx, cartID, AddLineItemInput{
		VariantID: variantID,
		Title:     title,
		Quantity:  quantity,
		UnitPrice: unitPrice,
		Metadata:  ek,
	})
	if err != nil {
		return "", err
	}
	return satir.ID, nil
}

// decodeInteropMetadata satır metadata'sını çözer; boş gövde nil döner.
//
// "JSON null" da nil sayılır: bir istemcinin alanı açıkça boşaltma niyeti ile
// hiç göndermemesi arasında bu yüzeyde fark yoktur — satır YENİ açılıyor,
// silinecek bir değer yok.
func decodeInteropMetadata(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	var ek map[string]any
	if err := json.Unmarshal(raw, &ek); err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, CodeInteropMetadataInvalid,
			"satır metadata'sı JSON nesnesi olmalı")
	}
	return ek, nil
}

// SetCartLineItemQuantity satırın adedini mutlak değerle yazar.
func (i *Interop) SetCartLineItemQuantity(ctx context.Context, cartID, lineItemID string, quantity int64) error {
	_, err := i.svc.UpdateLineItemQuantity(ctx, cartID, lineItemID, quantity)
	return err
}

// RemoveLineItem satırı sepetten kaldırır.
func (i *Interop) RemoveLineItem(ctx context.Context, cartID, lineItemID string) error {
	return i.svc.RemoveLineItem(ctx, cartID, lineItemID)
}

// SetCartTotalsJSON hesaplanan toplamları sepete yazar.
func (i *Interop) SetCartTotalsJSON(ctx context.Context, cartID string, totals json.RawMessage) error {
	var gelen interopTotals
	if err := json.Unmarshal(totals, &gelen); err != nil {
		return errors.Wrap(err, errors.KindInvalid, CodeInteropTotalsInvalid,
			"sepet toplamları çözülemedi: %s", cartID)
	}

	satirlar := make([]LineTotals, 0, len(gelen.Lines))
	for i := range gelen.Lines {
		satirlar = append(satirlar, LineTotals{
			LineItemID:    gelen.Lines[i].LineItemID,
			UnitPrice:     gelen.Lines[i].UnitPrice,
			Subtotal:      gelen.Lines[i].Subtotal,
			DiscountTotal: gelen.Lines[i].DiscountTotal,
			TaxTotal:      gelen.Lines[i].TaxTotal,
			Total:         gelen.Lines[i].Total,
		})
	}

	return i.svc.SetTotals(ctx, cartID, Totals{
		Revision:      gelen.Revision,
		Subtotal:      gelen.Subtotal,
		DiscountTotal: gelen.DiscountTotal,
		TaxTotal:      gelen.TaxTotal,
		ShippingTotal: gelen.ShippingTotal,
		Total:         gelen.Total,
		Lines:         satirlar,
	})
}

// MarkCompleted sepeti tamamlanmış olarak işaretler.
//
// Sipariş tamamlama saga'sının SON adımıdır: bu noktadan sonra sepet
// DEĞİŞTİRİLEMEZ (her yazma metodu errors.Conflict döner). Tamamlanmış bir
// sepetin ikinci kez işaretlenmesi hata VERMEZ; saga bir adımı yeniden
// çalıştırabilir.
func (i *Interop) MarkCompleted(ctx context.Context, cartID string) error {
	_, err := i.svc.MarkCompleted(ctx, cartID)
	return err
}

// interopCartTotals sepetin yazılmış toplamlarının JSON şemasıdır.
//
// Alan adları tüketici tarafındaki şemayla BİREBİR aynı olmak zorundadır;
// bu modül workflow paketini import edemediği için derleyici uyumu
// denetleyemez ve uyum ancak entegrasyon testiyle kanıtlanır.
type interopCartTotals struct {
	CurrencyCode  string `json:"currency_code"`
	Subtotal      int64  `json:"subtotal"`
	DiscountTotal int64  `json:"discount_total"`
	TaxTotal      int64  `json:"tax_total"`
	ShippingTotal int64  `json:"shipping_total"`
	Total         int64  `json:"total"`
	Completed     bool   `json:"completed"`
}

// CartTotalsJSON sepetin YAZILMIŞ toplamlarını döner.
//
// Saga, ödeme koleksiyonunu sepetin genel toplamıyla açar; tutarı KENDİ
// hesaplamaz. Hesap calculate_totals akışının işidir ve sonucu sepette
// saklanır (bkz. [Service.SetTotals]). Böylece ödeme, hesabın yapıldığı
// andaki tutarla açılır ve iki yerde ayrışan bir aritmetik oluşmaz.
func (i *Interop) CartTotalsJSON(ctx context.Context, cartID string) (json.RawMessage, error) {
	detay, err := i.svc.GetCart(ctx, cartID)
	if err != nil {
		return nil, err
	}
	return json.Marshal(interopCartTotals{
		CurrencyCode:  detay.CurrencyCode,
		Subtotal:      detay.Subtotal,
		DiscountTotal: detay.DiscountTotal,
		TaxTotal:      detay.TaxTotal,
		ShippingTotal: detay.ShippingTotal,
		Total:         detay.Total,
		Completed:     detay.Completed(),
	})
}
