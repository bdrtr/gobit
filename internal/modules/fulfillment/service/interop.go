package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya fulfillment modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// internal/workflows altındaki sepet ve sipariş akışları bu modülü import
// EDEMEZ. Çözüm region/cart/payment/order/inventory modüllerindeki interop.go
// ile aynıdır: yalnızca İLKEL ve stdlib tipleri kullanan bir yüzey yayımlamak.
// Tüketici kendi dar arayüzünü tanımlar, bu tip onu YAPISAL olarak karşılar ve
// container'dan "fulfillment.interop" adıyla çözülür.
//
// Sebep Go'nun yapısal uyum kuralıdır: tüketici fulfillment'ı import edemediği
// için imzasında models.Fulfillment gibi bir tipi adlandıramaz; adlandırdığı
// an o, kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz.
//
// Yüzey BİLİNÇLİ OLARAK dardır ve akışların ihtiyacına göre seçilmiştir:
// sepete uygun kargo seçeneklerini fiyatlarıyla sor, gönderi aç, gönderiyi
// iptal et (telafi) ve durumunu oku. Buraya eklenen her metot, fulfillment'ı
// ayrı bir servise çıkarmanın maliyetini artırır.
//
// # Bileşik veri JSON taşır ve şeması BURADA beyan edilir
//
// Kargo seçeneği listesi ilkel tiplere sığmaz; JSON olarak taşınır. Alan
// adları tüketici tarafındaki şemayla BİREBİR aynı olmak ZORUNDADIR ve uyum
// ancak entegrasyon testiyle kanıtlanabilir — bu modül workflow paketini
// import edemediği için derleyici uyumu denetleyemez.

// CodeInteropRequestInvalid çözülemeyen bir istek gövdesi geldiğini bildirir.
const CodeInteropRequestInvalid = "fulfillment_interop_request_invalid"

// interopListRequest [Interop.ListOptionsJSON] isteğinin JSON şemasıdır.
//
//	{
//	  "region_id":             "reg_...",   // sepetin bölgesi; boş olabilir
//	  "currency_code":         "TRY",       // ZORUNLU, ISO 4217
//	  "country_code":          "TR",        // teslimat ülkesi; boş olabilir
//	  "shipping_profile_ids":  ["sprof_..."], // boş ise profil süzgeci yok
//	  "subtotal":              50000,       // minor unit TAM SAYI
//	  "item_count":            3,
//	  "total_weight":          1500,        // gram
//	  "attributes":            {"customer_group_id": "vip"},
//	  "include_admin_only":    false,       // YALNIZCA yönetim akışları true verir
//	  "is_return":             false
//	}
//
// Sayısal alanlar TAM SAYIDIR ve ondalıklı bir değer REDDEDİLİR. Çözümleme
// bu yüzden json.Number ile yapılır: değer önce metin olarak alınır, sonra
// int64'e çevrilir. float64 üzerinden geçen bir çözümleme "100.5" gibi bir
// ara toplamı sessizce 100'e kırpar ve para bir kuruş kaybeder (plan Bölüm 8);
// json.Number aynı değerde AÇIK bir hata döner.
type interopListRequest struct {
	RegionID           string            `json:"region_id"`
	CurrencyCode       string            `json:"currency_code"`
	CountryCode        string            `json:"country_code"`
	ShippingProfileIDs []string          `json:"shipping_profile_ids"`
	Subtotal           json.Number       `json:"subtotal"`
	ItemCount          json.Number       `json:"item_count"`
	TotalWeight        json.Number       `json:"total_weight"`
	Attributes         map[string]string `json:"attributes"`
	IncludeAdminOnly   bool              `json:"include_admin_only"`
	IsReturn           bool              `json:"is_return"`
}

// interopListResponse [Interop.ListOptionsJSON] yanıtının JSON şemasıdır.
//
//	{
//	  "options": [
//	    {
//	      "id":                  "sopt_...",
//	      "name":                "Standart kargo",
//	      "amount":              2500,        // minor unit TAM SAYI
//	      "currency_code":       "TRY",
//	      "price_type":          "flat",      // "flat" | "calculated"
//	      "provider_id":         "manual",
//	      "shipping_profile_id": "sprof_...",
//	      "is_return":           false,
//	      "admin_only":          false
//	    }
//	  ]
//	}
//
// Liste ÖNCE ücrete (ucuz kazanır), eşitlikte kimliğe göre sıralıdır.
// Sağlayıcının ham verisi ("data") BURADA TAŞINMAZ: iç veridir ve bir akışın
// karar vermesi için gerekli değildir.
type interopListResponse struct {
	Options []interopOption `json:"options"`
}

// interopOption tek bir fiyatlanmış kargo seçeneğinin JSON şemasıdır.
type interopOption struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Amount            int64  `json:"amount"`
	CurrencyCode      string `json:"currency_code"`
	PriceType         string `json:"price_type"`
	ProviderID        string `json:"provider_id"`
	ShippingProfileID string `json:"shipping_profile_id"`
	IsReturn          bool   `json:"is_return"`
	AdminOnly         bool   `json:"admin_only"`
}

// Interop fulfillment servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. Tüm iş
// kuralları [Service] üzerinde kalır; buraya kural eklemek, aynı kuralın iki
// yerde ayrışması demek olurdu.
//
// Container'a "fulfillment.interop" adıyla kaydedilir.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// ListOptionsJSON bir sepet bağlamı için uygun kargo seçeneklerini
// fiyatlarıyla döner.
//
// İstek ve yanıt şemaları [interopListRequest] ve [interopListResponse]
// belgelerinde AÇIKÇA yazılıdır.
//
// "calculated" seçenekler için sağlayıcının fiyatı sorulur; bir sağlayıcı
// erişilemezse YALNIZCA o seçenek listeden düşer ve çağrı hata dönmez
// (gerekçe: [Service.ListShippingOptionsFor]).
//
// # Sepet olguları GÜVENİLİR sayılır
//
// Bu yüzey SÜREÇ İÇİNDEDİR ve yalnızca sepet/sipariş akışları çözer; ara
// toplam, kalem adedi ve ağırlık oraya sepetin kendi kaydından gelir, dış
// istekten değil. Bu yüzden çağrı TrustedFacts=true ile yapılır ve kurala
// bağlı seçenekler (örn. "500 TL üzeri ücretsiz kargo") burada listelenir —
// müşteriye onları gösterebilen tek yol budur; HTTP mağaza ucu, olguları
// doğrulayamadığı için onları hiç göstermez.
//
// Bayrak şemada BİLİNÇLİ OLARAK yoktur: çağıranın kendi bağlamına "bu veriye
// güven" diyebilmesi, doğrulamayı yeniden çağırana devretmek olurdu.
//
// Tüketici tarafındaki karşılığı:
//
//	type ShippingOptionLister interface {
//	    ListOptionsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) ListOptionsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	req, err := decodeListRequest(request)
	if err != nil {
		return nil, err
	}

	subtotal, err := interopInt(req.Subtotal, "subtotal")
	if err != nil {
		return nil, err
	}
	itemCount, err := interopInt(req.ItemCount, "item_count")
	if err != nil {
		return nil, err
	}
	totalWeight, err := interopInt(req.TotalWeight, "total_weight")
	if err != nil {
		return nil, err
	}

	quoted, err := i.svc.ListShippingOptionsFor(ctx, ListOptionsInput{
		RegionID:           req.RegionID,
		CurrencyCode:       req.CurrencyCode,
		CountryCode:        req.CountryCode,
		ShippingProfileIDs: req.ShippingProfileIDs,
		Subtotal:           subtotal,
		ItemCount:          itemCount,
		TotalWeight:        totalWeight,
		Attributes:         req.Attributes,
		TrustedFacts:       true,
		IncludeAdminOnly:   req.IncludeAdminOnly,
		IsReturn:           req.IsReturn,
	})
	if err != nil {
		return nil, err
	}

	out := interopListResponse{Options: make([]interopOption, 0, len(quoted))}
	// Dilim İNDEKSLE gezilir: değerle gezmek her yinelemede seçeneğin tamamını
	// kopyalardı ve bu yol sepet her güncellendiğinde çalışır.
	for idx := range quoted {
		out.Options = append(out.Options, interopOption{
			ID:                quoted[idx].Option.ID,
			Name:              quoted[idx].Option.Name,
			Amount:            quoted[idx].Amount,
			CurrencyCode:      quoted[idx].CurrencyCode,
			PriceType:         quoted[idx].Option.PriceType.String(),
			ProviderID:        quoted[idx].Option.ProviderID,
			ShippingProfileID: quoted[idx].Option.ShippingProfileID,
			IsReturn:          quoted[idx].Option.IsReturn,
			AdminOnly:         quoted[idx].Option.AdminOnly,
		})
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropRequestInvalid,
			"kargo seçeneği listesi kodlanamadı")
	}
	return body, nil
}

// CreateFulfillment bir sipariş için gönderi açar ve gönderinin KİMLİĞİNİ
// döner.
//
// reference siparişin kimliğidir; bu modül onu doğrulamaz (Prensip 2.2 — bağ
// Module Links ile kurulur).
//
// Aynı idempotencyKey ile ikinci çağrı YENİ gönderi açmaz, mevcut gönderinin
// kimliğini döner; saga bir adımı yeniden denediğinde İKİNCİ BİR KARGO
// ETİKETİ basılmamasını sağlayan şey budur. Anahtar aynı ama referans ya da
// seçenek farklıysa errors.Conflict döner.
//
// Kalem dökümü BU YÜZEYDEN verilmez: saga'nın ihtiyacı siparişin tamamı için
// tek bir gönderi açmaktır ve kalem bazlı kısmi sevkiyat, yönetim API'sinin
// konusudur.
//
// Tüketici tarafındaki karşılığı:
//
//	type FulfillmentCreator interface {
//	    CreateFulfillment(ctx context.Context, reference, optionID, idempotencyKey string) (string, error)
//	}
func (i *Interop) CreateFulfillment(
	ctx context.Context,
	reference, optionID, idempotencyKey string,
) (string, error) {
	ful, err := i.svc.CreateFulfillment(ctx, CreateFulfillmentInput{
		Reference:        reference,
		ShippingOptionID: optionID,
		IdempotencyKey:   idempotencyKey,
	})
	if err != nil {
		return "", err
	}
	return ful.ID, nil
}

// CancelFulfillment gönderiyi iptal eder; SAGA TELAFİSİ budur ve İDEMPOTENTTİR.
//
// İki kez çağrılırsa ikinci çağrı hata VERMEZ. Bilinmeyen bir gönderi kimliği
// ise errors.NotFound döner; telafi, var olmayan bir kaydı sessizce yutmaz.
// TESLİM EDİLMİŞ bir gönderi iptal edilemez ve errors.Conflict döner
// (gerekçe: [Service.CancelFulfillment]).
//
// Tüketici tarafındaki karşılığı:
//
//	type FulfillmentCanceler interface {
//	    CancelFulfillment(ctx context.Context, fulfillmentID string) error
//	}
func (i *Interop) CancelFulfillment(ctx context.Context, fulfillmentID string) error {
	return i.svc.CancelFulfillment(ctx, fulfillmentID)
}

// FulfillmentStatus gönderinin güncel durumunu döner ("pending", "shipped",
// "delivered" ya da "canceled").
//
// Telafinin gerçekten çalıştığını doğrulayan testler buna bakar: iptal edilmiş
// bir gönderi "canceled" döner ve saga'nın geri alma zinciri gözle görülür
// olur.
//
// Tüketici tarafındaki karşılığı:
//
//	type FulfillmentStatusReader interface {
//	    FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error)
//	}
func (i *Interop) FulfillmentStatus(ctx context.Context, fulfillmentID string) (string, error) {
	ful, err := i.svc.GetFulfillment(ctx, fulfillmentID)
	if err != nil {
		return "", err
	}
	return ful.Status.String(), nil
}

// decodeListRequest ham istek gövdesini çözer.
//
// Sayılar json.Number olarak çözülür: değer önce metin olarak alınır ve
// [interopInt] onu int64'e çevirirken ondalıklı bir gövdeyi AÇIK bir hatayla
// reddeder. float64 üzerinden geçen bir çözümleme aynı gövdeyi sessizce
// kırpardı ve ara toplam bir PARA değeridir (plan Bölüm 8).
func decodeListRequest(raw json.RawMessage) (interopListRequest, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return interopListRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"kargo seçeneği isteği boş olamaz")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	// Tanınmayan alan REDDEDİLİR: sessizce yutulan bir alan, çağıranın
	// gönderdiğini sandığı ama uygulanmayan bir koşul demektir ve iki
	// paketteki şemanın ayrıştığının ilk işaretidir.
	dec.DisallowUnknownFields()

	var out interopListRequest
	if err := dec.Decode(&out); err != nil {
		return interopListRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"kargo seçeneği isteği çözümlenemedi; JSON nesnesi olmalı")
	}
	return out, nil
}

// interopInt json.Number'ı int64'e çevirir; boş değer sıfır döner.
func interopInt(value json.Number, field string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := value.Int64()
	if err != nil {
		return 0, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"%s tam sayı olmalı: %q", field, value.String())
	}
	return parsed, nil
}
