package service

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya promotion modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// internal/workflows altındaki sepet akışı ve sipariş tamamlama saga'sı bu
// modülü import EDEMEZ. Çözüm region/cart/payment/order/inventory
// modüllerindeki interop.go ile aynıdır: yalnızca İLKEL ve stdlib tipleri
// kullanan bir yüzey yayımlamak. Tüketici kendi dar arayüzünü tanımlar, bu tip
// onu YAPISAL olarak karşılar ve container'dan "promotion.interop" adıyla
// çözülür.
//
// Sebep Go'nun yapısal uyum kuralıdır: tüketici promotion'ı import edemediği
// için imzasında [ComputeInput] gibi bir tipi adlandıramaz; adlandırdığı an o,
// kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz.
//
// Bileşik veri (sepet bağlamı ve hesaplanan indirimler) JSON olarak taşınır ve
// şema aşağıda AÇIKÇA beyan edilir. Tüketici tarafındaki şema ile birebir aynı
// olmak ZORUNDADIR ve uyum ancak entegrasyon testiyle kanıtlanabilir: bu modül
// workflow paketini import edemediği için derleyici uyumu denetleyemez.

// Interop hata kodları.
const (
	// CodeInteropRequestInvalid çözülemeyen bir istek gövdesi geldiğini bildirir.
	CodeInteropRequestInvalid = "promotion_interop_request_invalid"
	// CodeInteropResponseInvalid sonucun JSON'a çevrilemediğini bildirir.
	CodeInteropResponseInvalid = "promotion_interop_response_invalid"
)

// Interop promotion servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. Tüm iş
// kuralları [Service] üzerinde kalır; buraya kural eklemek, aynı kuralın iki
// yerde ayrışması demek olurdu.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// interopRequest [Interop.ComputeDiscountsJSON] isteğinin JSON şemasıdır.
//
// Alan adları tüketici tarafındaki şemayla BİREBİR aynı olmak zorundadır.
// Tüm tutarlar TAM SAYI minor unit'tir (plan Bölüm 8).
//
//	{
//	  "currency_code": "TRY",
//	  "context": {"region_id": "reg_1", "customer_group_id": "vip"},
//	  "items": [
//	    {"id": "li_1", "amount": 25000, "quantity": 2,
//	     "attributes": {"product_category_id": "cat_1"}}
//	  ],
//	  "shipping_methods": [{"id": "sm_1", "amount": 4990, "attributes": {}}],
//	  "codes": ["YAZ20"],
//	  "at": "2026-08-24T10:00:00Z"
//	}
//
// "context", "attributes" ve "codes" boş bırakılabilir. "at" boş bırakılırsa
// "şimdi" kullanılır; RFC 3339 biçiminde beklenir.
type interopRequest struct {
	CurrencyCode    string                   `json:"currency_code"`
	Context         map[string]string        `json:"context"`
	Items           []interopRequestItem     `json:"items"`
	ShippingMethods []interopRequestShipping `json:"shipping_methods"`
	Codes           []string                 `json:"codes"`
	At              string                   `json:"at"`
}

// interopRequestItem istekteki tek bir sepet kaleminin şemasıdır.
type interopRequestItem struct {
	ID         string            `json:"id"`
	Amount     int64             `json:"amount"`
	Quantity   int64             `json:"quantity"`
	Attributes map[string]string `json:"attributes"`
}

// interopRequestShipping istekteki tek bir kargo yönteminin şemasıdır.
type interopRequestShipping struct {
	ID         string            `json:"id"`
	Amount     int64             `json:"amount"`
	Attributes map[string]string `json:"attributes"`
}

// interopResponse [Interop.ComputeDiscountsJSON] yanıtının JSON şemasıdır.
//
//	{
//	  "currency_code": "TRY",
//	  "items": [{"id": "li_1", "amount": 5000}],
//	  "shipping_methods": [{"id": "sm_1", "amount": 0}],
//	  "items_discount_total": 5000,
//	  "shipping_discount_total": 0,
//	  "discount_total": 5000,
//	  "applied": [
//	    {"promotion_id": "promo_…", "code": "YAZ20",
//	     "is_automatic": false, "amount": 5000}
//	  ],
//	  "unmatched_codes": []
//	}
//
// Değişmezler (tüketici bunlara güvenebilir):
//
//   - "items" ve "shipping_methods" istekteki HER satır için bir kayıt taşır ve
//     istekle AYNI sıradadır; indirimi sıfır olanlar da bulunur.
//   - Her satırın indirimi, o satırın tutarını AŞMAZ.
//   - discount_total = items_discount_total + shipping_discount_total
//     = Σ items[i].amount + Σ shipping_methods[i].amount
//     = Σ applied[i].amount
type interopResponse struct {
	CurrencyCode          string                `json:"currency_code"`
	Items                 []interopLineDiscount `json:"items"`
	ShippingMethods       []interopLineDiscount `json:"shipping_methods"`
	ItemsDiscountTotal    int64                 `json:"items_discount_total"`
	ShippingDiscountTotal int64                 `json:"shipping_discount_total"`
	DiscountTotal         int64                 `json:"discount_total"`
	Applied               []interopApplied      `json:"applied"`
	UnmatchedCodes        []string              `json:"unmatched_codes"`
}

// interopLineDiscount yanıttaki tek bir satır indiriminin şemasıdır.
type interopLineDiscount struct {
	ID     string `json:"id"`
	Amount int64  `json:"amount"`
}

// interopApplied yanıttaki tek bir uygulanmış promosyonun şemasıdır.
type interopApplied struct {
	PromotionID string `json:"promotion_id"`
	Code        string `json:"code"`
	IsAutomatic bool   `json:"is_automatic"`
	Amount      int64  `json:"amount"`
}

// ComputeDiscountsJSON sepet bağlamı için indirimleri hesaplar; HİÇBİR ŞEY
// YAZMAZ.
//
// Şema [interopRequest] ve [interopResponse] godoc'larında tanımlıdır. Hesabın
// kuralları (eleme, sıra, bileşik olmama, üst sınırlar, yuvarlama, bütçe)
// [Service.ComputeDiscounts] godoc'undadır ve bu yüzey onları DEĞİŞTİRMEZ.
//
// Sayılar json.Number ile değil doğrudan int64 alanlara çözülür: şema tüm
// tutarları tam sayı olarak beyan eder ve float64'e uğrayan bir tutar kuruş
// düzeyinde sessizce bozulurdu (plan Bölüm 8). Bilinmeyen alanlar REDDEDİLİR
// — sessizce yok sayılan bir alan, tüketicinin gönderdiğini sandığı bir kalemin
// hesaba hiç girmemesi demektir.
//
// Tüketici tarafındaki karşılığı:
//
//	type DiscountCalculator interface {
//	    ComputeDiscountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) ComputeDiscountsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	in, err := decodeInteropRequest(request)
	if err != nil {
		return nil, err
	}

	result, err := i.svc.ComputeDiscounts(ctx, in)
	if err != nil {
		return nil, err
	}

	response := interopResponse{
		CurrencyCode:          result.CurrencyCode,
		Items:                 make([]interopLineDiscount, 0, len(result.Items)),
		ShippingMethods:       make([]interopLineDiscount, 0, len(result.ShippingMethods)),
		ItemsDiscountTotal:    result.ItemsDiscountTotal,
		ShippingDiscountTotal: result.ShippingDiscountTotal,
		DiscountTotal:         result.DiscountTotal,
		Applied:               make([]interopApplied, 0, len(result.Applied)),
		UnmatchedCodes:        result.UnmatchedCodes,
	}
	for idx := range result.Items {
		response.Items = append(response.Items, interopLineDiscount{
			ID:     result.Items[idx].ID,
			Amount: result.Items[idx].Amount,
		})
	}
	for idx := range result.ShippingMethods {
		response.ShippingMethods = append(response.ShippingMethods, interopLineDiscount{
			ID:     result.ShippingMethods[idx].ID,
			Amount: result.ShippingMethods[idx].Amount,
		})
	}
	for idx := range result.Applied {
		response.Applied = append(response.Applied, interopApplied{
			PromotionID: result.Applied[idx].PromotionID,
			Code:        result.Applied[idx].Code,
			IsAutomatic: result.Applied[idx].IsAutomatic,
			Amount:      result.Applied[idx].Amount,
		})
	}

	payload, err := json.Marshal(response)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropResponseInvalid,
			"indirim sonucu JSON'a çevrilemedi")
	}
	return payload, nil
}

// RedeemPromotion kuponu bir referans için kullanır ve kullanım kaydının
// kimliğini döner.
//
// İDEMPOTENTTİR: aynı referansla ikinci çağrı yeni kayıt yazmaz ve sayaçları
// artırmaz, var olan kaydın kimliğini döner (bkz.
// [Service.RedeemPromotion]).
//
// promotionID ya da code'dan en az biri dolu olmalıdır; ikisi de doluysa aynı
// promosyonu göstermelidir.
//
// Promosyon YAYINDA değilse, kampanyasının penceresi kullanım anını kapsamıyorsa
// ya da bir sayaç sınırı aşılacaksa errors.Conflict döner ve hiçbir şey
// yazılmaz; sebeplerin tamamı [Service.RedeemPromotion] godoc'undadır. Çağıranın
// durum denetimi yapması BEKLENMEZ — hakem bu çağrıdır.
//
// Tüketici tarafındaki karşılığı:
//
//	type PromotionRedeemer interface {
//	    RedeemPromotion(ctx context.Context, promotionID, code, reference, currencyCode string, amount int64) (string, error)
//	}
func (i *Interop) RedeemPromotion(
	ctx context.Context,
	promotionID, code, reference, currencyCode string,
	amount int64,
) (string, error) {
	redemption, err := i.svc.RedeemPromotion(ctx, RedeemInput{
		PromotionID:  promotionID,
		Code:         code,
		Reference:    reference,
		Amount:       amount,
		CurrencyCode: currencyCode,
	})
	if err != nil {
		return "", err
	}
	return redemption.ID, nil
}

// ReleasePromotion bir kullanımı serbest bırakır; SAGA TELAFİSİ budur ve
// İDEMPOTENTTİR.
//
// İki kez çağrılırsa ikinci çağrı hata VERMEZ ve sayaçlar ikinci kez düşmez.
// Hiç kullanım yazılmamışsa da hata dönmez. Dönen bool, BU ÇAĞRIDA bir şeyin
// geri alınıp alınmadığını bildirir.
//
// Bilinmeyen bir promosyon kimliği/kodu errors.NotFound döner; telafi, var
// olmayan bir kaydı sessizce yutmaz.
//
// Tüketici tarafındaki karşılığı:
//
//	type PromotionReleaser interface {
//	    ReleasePromotion(ctx context.Context, promotionID, code, reference string) (bool, error)
//	}
func (i *Interop) ReleasePromotion(ctx context.Context, promotionID, code, reference string) (bool, error) {
	return i.svc.ReleasePromotion(ctx, ReleaseInput{
		PromotionID: promotionID,
		Code:        code,
		Reference:   reference,
	})
}

// decodeInteropRequest ham JSON gövdesini hesap girdisine çevirir.
func decodeInteropRequest(raw json.RawMessage) (ComputeInput, error) {
	if len(raw) == 0 {
		return ComputeInput{}, errors.Invalid(CodeInteropRequestInvalid,
			"indirim isteği boş olamaz")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var req interopRequest
	if err := dec.Decode(&req); err != nil {
		return ComputeInput{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"indirim isteği çözümlenemedi")
	}

	at, err := parseInteropTime(req.At)
	if err != nil {
		return ComputeInput{}, err
	}

	items := make([]ComputeItem, 0, len(req.Items))
	for idx := range req.Items {
		items = append(items, ComputeItem{
			ID:         req.Items[idx].ID,
			Amount:     req.Items[idx].Amount,
			Quantity:   req.Items[idx].Quantity,
			Attributes: req.Items[idx].Attributes,
		})
	}
	shipping := make([]ComputeShippingMethod, 0, len(req.ShippingMethods))
	for idx := range req.ShippingMethods {
		shipping = append(shipping, ComputeShippingMethod{
			ID:         req.ShippingMethods[idx].ID,
			Amount:     req.ShippingMethods[idx].Amount,
			Attributes: req.ShippingMethods[idx].Attributes,
		})
	}

	return ComputeInput{
		CurrencyCode:    req.CurrencyCode,
		Context:         req.Context,
		Items:           items,
		ShippingMethods: shipping,
		Codes:           req.Codes,
		At:              at,
	}, nil
}

// parseInteropTime RFC 3339 biçimli bir anı çözer; boş dize sıfır zaman döner.
//
// Sıfır zaman "şimdi" demektir ve [Service.ComputeDiscounts] onu kendi saatiyle
// doldurur. Çözülemeyen bir damga sessizce "şimdi"ye düşmez: tüketici geçmişe
// dönük bir hesap istediyse ve damga bozuksa, bugünün kampanyalarıyla yapılmış
// bir hesap yanlış cevaptır.
func parseInteropTime(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"hesap anı RFC 3339 biçiminde olmalı, %q verildi", raw)
	}
	return parsed.UTC(), nil
}
