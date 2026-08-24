package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya tax modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// internal/workflows/cart sepet toplamını hesaplarken vergiye ihtiyaç duyar,
// ama ne o paket bu modülü ne bu modül o paketi import edebilir. Çözüm
// region/cart/payment/order/inventory modüllerindeki interop.go ile aynıdır:
// yalnızca İLKEL ve stdlib tipleri kullanan bir yüzey yayımlamak. Tüketici
// kendi dar arayüzünü tanımlar, bu tip onu YAPISAL olarak karşılar ve
// container'dan "tax.interop" adıyla çözülür.
//
// Sebep Go'nun yapısal uyum kuralıdır: tüketici tax'ı import edemediği için
// imzasında service.CalculateTaxInput gibi bir tipi adlandıramaz; adlandırdığı
// an o, kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz.
//
// Bileşik veri (kalem listesi ve kalem başına vergi) JSON olarak taşınır ve
// şema AŞAĞIDA AÇIKÇA beyan edilir. Tüketici tarafındaki şema ile birebir aynı
// olmak ZORUNDADIR; bu modül workflow paketini import edemediği için derleyici
// uyumu denetleyemez ve uyum ancak entegrasyon testiyle kanıtlanabilir.
//
// # Yüzey neden iki metot
//
// [Interop.CalculateTaxJSON] tam hesaptır: eyalet, kural, kargo ve kalem
// başına oran. [Interop.RateForCountry] ise SADE yoldur ve region modülünün
// GEÇİCİ RegionTax metodunun doğrudan karşılığıdır — elinde yalnızca bir ülke
// kodu olan ve tek bir oran isteyen çağıran içindir. İkisinin ayrı durması
// bilinçlidir: sade yolun JSON kodlama/çözme maliyeti ödemesi gereksizdir ve
// tam hesabın imzasını "isteğe bağlı alanlarla" sadeleştirmek, iki farklı
// sözleşmeyi tek imzada gizlemek olurdu.

// Kod sabitleri; interop yüzeyine özgüdür.
const (
	// CodeInteropRequestInvalid gelen JSON isteğinin çözümlenemediğini
	// bildirir.
	CodeInteropRequestInvalid = "tax_interop_request_invalid"
	// CodeInteropResponseInvalid yanıtın kodlanamadığını bildirir; iç
	// tutarsızlık göstergesidir ve normal akışta oluşmaz.
	CodeInteropResponseInvalid = "tax_interop_response_invalid"
)

// interopRequest [Interop.CalculateTaxJSON] isteğinin JSON şemasıdır.
//
// Örnek:
//
//	{
//	  "country_code": "TR",
//	  "province_code": "34",
//	  "items": [
//	    {"id": "li_1", "product_id": "prod_1", "product_type_id": "ptyp_1", "amount": 3000}
//	  ],
//	  "shipping": {"option_id": "sopt_1", "amount": 2500, "taxable": false}
//	}
type interopRequest struct {
	// CountryCode ISO 3166-1 alpha-2 kodudur; zorunludur.
	CountryCode string `json:"country_code"`
	// ProvinceCode eyalet/il kodudur; isteğe bağlıdır.
	ProvinceCode string `json:"province_code"`
	// Items vergilendirilecek kalemlerdir.
	Items []interopItem `json:"items"`
	// Shipping kargo satırıdır.
	Shipping interopShipping `json:"shipping"`
}

// interopItem bir vergilendirilebilir kalemin JSON şemasıdır.
type interopItem struct {
	// ID kalemin ÇAĞIRAN tarafındaki kimliğidir (örn. sepet satırı) ve
	// yanıtta aynen döner.
	ID string `json:"id"`
	// ProductID kural eşleşmesi için ürün kimliğidir; boş bırakılabilir.
	ProductID string `json:"product_id"`
	// ProductTypeID kural eşleşmesi için ürün tipidir; boş bırakılabilir.
	ProductTypeID string `json:"product_type_id"`
	// Amount vergilendirilebilir tabandır: minor unit TAM SAYI ve İNDİRİM
	// SONRASI. Bu modül indirimi görmez.
	Amount int64 `json:"amount"`
}

// interopShipping kargo satırının JSON şemasıdır.
type interopShipping struct {
	// OptionID kargo seçeneğinin kimliğidir; kural eşleşmesi içindir.
	OptionID string `json:"option_id"`
	// Amount kargo tutarıdır (minor unit).
	Amount int64 `json:"amount"`
	// Taxable kargonun vergilendirilip vergilendirilmeyeceğidir; VARSAYILAN
	// false'tur ve alan gönderilmezse kargo tabana girmez.
	Taxable bool `json:"taxable"`
}

// interopResponse [Interop.CalculateTaxJSON] yanıtının JSON şemasıdır.
//
// Örnek:
//
//	{
//	  "region_id": "taxreg_01J…",
//	  "region_found": true,
//	  "provider_id": "local",
//	  "tax_total": 600,
//	  "items": [
//	    {"id": "li_1", "rate_id": "taxrate_01J…", "rate_bps": 2000,
//	     "taxable_amount": 3000, "tax_amount": 600}
//	  ],
//	  "shipping": {"id": "_shipping", "rate_id": "", "rate_bps": 0,
//	               "taxable_amount": 0, "tax_amount": 0}
//	}
//
// Kimlik daima sağlanır: tax_total = Σ(items[i].tax_amount) +
// shipping.tax_amount.
type interopResponse struct {
	// RegionID hesabın dayandığı EN ÖZEL bölgedir; bölge yoksa boş.
	RegionID string `json:"region_id"`
	// RegionFound ülkeye ait bir vergi bölgesi bulunup bulunmadığıdır.
	//
	// false ise vergi sıfırdır ÇÜNKÜ YAPILANDIRMA YOKTUR; oranın gerçekten
	// sıfır olmasından ayırt edilebilmesi için alan zorunludur.
	RegionFound bool `json:"region_found"`
	// ProviderID hesabı yapan sağlayıcının kimliğidir; bölge yoksa boş.
	ProviderID string `json:"provider_id"`
	// TaxTotal toplam vergidir (minor unit).
	TaxTotal int64 `json:"tax_total"`
	// Items kalem başına vergidir; İSTEKTEKİ SIRAYLA döner.
	Items []interopItemTax `json:"items"`
	// Shipping kargo satırının vergisidir.
	Shipping interopItemTax `json:"shipping"`
}

// interopItemTax bir satırın hesaplanan vergisinin JSON şemasıdır.
type interopItemTax struct {
	// ID satırın kimliğidir; kargo satırında [ShippingLineID].
	ID string `json:"id"`
	// RateID uygulanan oranın kimliğidir; oran bulunamadıysa boş.
	RateID string `json:"rate_id"`
	// RateBps uygulanan orandır (BAZ PUAN; 2000 = %20). Baz puan olması
	// bilinçlidir: "rate": 20 değerinin %20 mi 0,2 mi olduğu belirsiz kalır ve
	// istemci tarafında yüz kat hata üretirdi.
	RateBps int32 `json:"rate_bps"`
	// TaxableAmount verginin hesaplandığı tabandır (minor unit).
	TaxableAmount int64 `json:"taxable_amount"`
	// TaxAmount hesaplanan vergidir (minor unit).
	TaxAmount int64 `json:"tax_amount"`
}

// Interop tax servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. Tüm iş
// kuralları [Service] üzerinde kalır; buraya kural eklemek, aynı kuralın iki
// yerde ayrışması demek olurdu.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// CalculateTaxJSON verilen isteği çözüp vergiyi hesaplar ve sonucu JSON
// olarak döner.
//
// İstek şeması [interopRequest], yanıt şeması [interopResponse] tiplerinde ve
// TEK YERDE tanımlıdır; ikisinin de godoc'unda örnek gövde vardır.
//
// # Bilinmeyen alanlar reddedilir
//
// Sessizce yok sayılan bir alan, çağıranın gönderdiğini sandığı bir tabanın
// hiç hesaba girmemesi demektir. İki taraf birbirini import edemediği için
// derleyici bu uyumsuzluğu göremez; katı çözümleme, uyumsuzluğun ilk çağrıda
// açık bir hata olarak çıkmasını sağlar.
//
// # Sayılar
//
// Tutarlar TAM SAYIDIR ve öyle çözülür; şemadaki alanlar int64'tür. Kayan
// noktalı bir taban (örn. 30.5) çözümleme hatası verir — sessizce yuvarlanmaz
// (plan Bölüm 8).
func (i *Interop) CalculateTaxJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	if i == nil || i.svc == nil {
		return nil, errors.Unavailable(CodeUnconfigured, "tax servisi kurulmamış")
	}

	req, err := decodeInteropRequest(request)
	if err != nil {
		return nil, err
	}

	items := make([]TaxableItem, 0, len(req.Items))
	for idx := range req.Items {
		items = append(items, TaxableItem{
			ID:            req.Items[idx].ID,
			ProductID:     req.Items[idx].ProductID,
			ProductTypeID: req.Items[idx].ProductTypeID,
			Amount:        req.Items[idx].Amount,
		})
	}

	result, err := i.svc.CalculateTax(ctx, CalculateTaxInput{
		CountryCode:  req.CountryCode,
		ProvinceCode: req.ProvinceCode,
		Items:        items,
		Shipping: ShippingInput{
			OptionID: req.Shipping.OptionID,
			Amount:   req.Shipping.Amount,
			Taxable:  req.Shipping.Taxable,
		},
	})
	if err != nil {
		return nil, err
	}

	resp := interopResponse{
		RegionID:    result.RegionID,
		RegionFound: result.RegionFound,
		ProviderID:  result.ProviderID,
		TaxTotal:    result.TaxTotal,
		Items:       make([]interopItemTax, 0, len(result.Items)),
		Shipping:    toInteropItemTax(result.Shipping),
	}
	for idx := range result.Items {
		resp.Items = append(resp.Items, toInteropItemTax(result.Items[idx]))
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropResponseInvalid,
			"vergi sonucu JSON'a çevrilemedi")
	}
	return payload, nil
}

// RateForCountry bir ülkenin VARSAYILAN vergi oranını baz puan olarak döner.
//
// region modülünün GEÇİCİ RegionTax metodunun doğrudan karşılığıdır ve sepet
// akışının en sade yolu için vardır: elinde yalnızca ülke kodu olan bir çağıran
// tek bir oran ister.
//
// Dönen oran ülke KÖKÜNÜN varsayılan oranıdır. Eyalet bölgeleri, kurallar ve
// kargo bu yolda DEĞERLENDİRİLMEZ; bunlara ihtiyaç duyan çağıran
// [Interop.CalculateTaxJSON] kullanmalıdır. Hesap yerel tablodan yapılır ve
// bölgenin sağlayıcısı ÇAĞRILMAZ — dış bir vergi servisine yalnızca bir oran
// sormak için gidilmesi, sepetin her turunda ağ çağrısı demek olurdu.
//
// found ikinci dönüş değeridir ve iki durumu ayırır: ülkenin vergi bölgesi
// yoktur (ya da varsayılan oranı yoktur) ile oran gerçekten sıfırdır. found
// false iken oran daima sıfırdır.
//
// Tüketici tarafındaki karşılığı:
//
//	type TaxRateReader interface {
//	    RateForCountry(ctx context.Context, countryCode string) (int32, bool, error)
//	}
func (i *Interop) RateForCountry(ctx context.Context, countryCode string) (rateBps int32, found bool, err error) {
	if i == nil || i.svc == nil {
		return 0, false, errors.Unavailable(CodeUnconfigured, "tax servisi kurulmamış")
	}
	return i.svc.DefaultRateForCountry(ctx, countryCode)
}

// decodeInteropRequest istek gövdesini şemaya çözer.
func decodeInteropRequest(request json.RawMessage) (interopRequest, error) {
	if len(request) == 0 {
		return interopRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"vergi hesabı isteği boş olamaz")
	}

	var req interopRequest
	dec := json.NewDecoder(bytes.NewReader(request))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return interopRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"vergi hesabı isteği çözümlenemedi")
	}

	// Tek bir JSON belgesi beklenir; arkasından gelen ikinci belge sessizce
	// yok sayılırsa çağıran gönderdiğinin işlendiğini sanırdı.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return interopRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"vergi hesabı isteği tek bir JSON belgesi olmalı")
	}
	return req, nil
}

// toInteropItemTax sonuç satırını JSON şemasına çevirir.
//
// Çevrim, iki tipin alanları BİREBİR aynı olduğu için doğrudan tip
// dönüşümüdür (Go, yalnızca etiketleri farklı struct'lar arasında dönüşüme
// izin verir). Bu bilinçli bir seçimdir: [ItemTax] bir alan kazandığı ya da
// kaybettiği an dönüşüm DERLENMEZ ve JSON şemasının ne olacağına açıkça karar
// verilmek zorunda kalınır. Alan alan yazılmış bir eşleme ise yeni alanı
// sessizce dışarıda bırakırdı.
func toInteropItemTax(item ItemTax) interopItemTax {
	return interopItemTax(item)
}
