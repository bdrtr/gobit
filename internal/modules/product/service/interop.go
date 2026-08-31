package service

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya product modülünün MODÜLLER ARASI yüzeyidir (ADR 0001, ADR 0006).
//
// Eklentiler (plugins/**) ve akışlar bu modülü import EDEMEZ. Çözüm diğer
// modüllerdeki interop.go ile aynıdır: yalnızca İLKEL ve stdlib tipleri
// kullanan bir yüzey yayımlamak. Tüketici kendi dar arayüzünü tanımlar, bu tip
// onu YAPISAL olarak karşılar ve container'dan "product.interop" adıyla
// çözülür.
//
// Sebep Go'nun yapısal uyum kuralıdır: tüketici product'ı import edemediği
// için imzasında StoreProduct gibi bir tipi adlandıramaz; adlandırdığı an o,
// kendi paketinde tanımlı BAŞKA bir tip olur ve somut servis tüketicinin
// arayüzünü karşılamaz.
//
// Yüzey BİLİNÇLİ OLARAK dardır: bugün yalnızca "şu kimliklerin vitrin
// kayıtlarını ver" vardır. İhtiyacın kaynağı aramadır — bir arama eklentisi
// kendi indeksinden kimlik ve alaka SIRASI üretir, gösterilecek kaydı ise
// kataloğun kendisinden okumalıdır. Buraya eklenen her metot, product'ı ayrı
// bir servise çıkarmanın maliyetini artırır.

// CodeInteropRequestInvalid çözülemeyen bir istek gövdesi geldiğini bildirir.
const CodeInteropRequestInvalid = "product_interop_request_invalid"

// interopStoreProductsRequest [Interop.StoreProductsByIDsJSON] isteğinin JSON
// şemasıdır.
//
//	{
//	  "ids":               ["prod_...", "prod_..."],
//	  "sales_channel_ids": ["sc_..."]
//	}
//
// # sales_channel_ids eksik olabilir ve ANLAMI vardır
//
// Alan, vitrin listelemesindeki ile AYNI anlamı taşır
// (bkz. [StoreListOptions.SalesChannelIDs]) ve burada YENİDEN TANIMLANMAZ:
//
//   - eksik ya da null: istek hiçbir satış kanalı kimliği taşımıyordur ve
//     süzgeç uygulanmaz. Bu, mağaza kimlik doğrulamasının hiç bağlanmadığı
//     kurulumun karşılığıdır (bkz. api/store.go salesChannelIDs).
//   - boş dizi: kimlik var ama kanalı yok. Süzgeç UYGULANIR ve yalnızca
//     ataması olmayan ürünler döner.
//
// Ayrımı burada başka türlü yorumlamak — örneğin eksik alanı "hiçbir şey
// gösterme" saymak — kuralın İKİNCİ bir tanımı olurdu ve iki tanım bir gün
// ayrışır. Tüketicinin sorumluluğu, kanalları isteğin KİMLİĞİNDEN taşımaktır;
// kullanıcının gönderdiği bir sorgu parametresinden değil.
type interopStoreProductsRequest struct {
	IDs             []string `json:"ids"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// interopStoreProductsResponse [Interop.StoreProductsByIDsJSON] yanıtının JSON
// şemasıdır.
//
//	{"products": [ <vitrin ürün kaydı>, ... ]}
//
// Kayıtların şekli [StoreProduct]'ın kendisidir: HTTP vitrin ucunun yazdığı
// gövdeyle AYNI tip serileştirilir ve alanlar burada TEKRAR SAYILMAZ. Alanları
// bu dosyada yeniden tanımlamak, vitrin gösteriminin ikinci bir kopyasını
// üretir ve bir alan eklendiğinde ikisi sessizce ayrışırdı.
//
// Sayfalama zarfı (count/offset/limit) YOKTUR: çağıran hangi kimlikleri
// istediğini zaten bilir ve sayfalamayı kendi indeksinde yapar.
type interopStoreProductsResponse struct {
	Products []StoreProduct `json:"products"`
}

// Interop product servisini modüller arası İLKEL yüzeye çevirir.
//
// Hiçbir karar vermez: yalnızca imzayı ve JSON şemasını çevirir. Görünürlük
// dâhil tüm kurallar [Service] üzerinde kalır; buraya kural eklemek, aynı
// kuralın iki yerde ayrışması demek olurdu — ve bu yüzeyde ayrışan kural,
// aramanın kanal süzmesini atlaması anlamına gelirdi.
//
// Container'a "product.interop" adıyla kaydedilir.
type Interop struct {
	svc *Service
}

// NewInterop verilen servis için modüller arası yüzeyi kurar.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// StoreProductsByIDsJSON verilen kimliklerin VİTRİN kayıtlarını döner.
//
// İstek ve yanıt şemaları [interopStoreProductsRequest] ve
// [interopStoreProductsResponse] belgelerinde AÇIKÇA yazılıdır.
//
// Kayıtlar isteğin kimlik SIRASINI korur; bulunamayan, yayında olmayan ya da
// isteğin satış kanallarında görünmeyen kimlik sessizce atlanır. Kuralın
// tamamı ve gerekçeleri [Service.StoreProductsByIDs] belgesindedir.
//
// Boş "ids" hata DEĞİLDİR: boş ürün listesi döner. Toplu okuma yüzeylerinin
// sözleşmesi budur (ADR 0004) ve "hiç kimlik yok" ile "kimlikler geçersiz"
// farklı şeylerdir; ikincisi errors.Invalid döner.
//
// Tüketici tarafındaki karşılığı:
//
//	type StoreProductReader interface {
//	    StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
//	}
func (i *Interop) StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error) {
	req, err := decodeStoreProductsRequest(request)
	if err != nil {
		return nil, err
	}

	products, err := i.svc.StoreProductsByIDs(ctx, req.IDs, req.SalesChannelIDs)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(interopStoreProductsResponse{Products: products})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropRequestInvalid,
			"vitrin ürün listesi kodlanamadı (%d ürün)", len(products))
	}
	return body, nil
}

// decodeStoreProductsRequest ham istek gövdesini çözer.
//
// Çözümleme json.Decoder ile yapılır çünkü nil dilim ile BOŞ dilim ayrımı
// korunmalıdır: "sales_channel_ids" hiç yoksa alan nil kalır ve süzgeç
// uygulanmaz, boş dizi geldiğinde ise nil OLMAYAN boş dilim oluşur ve süzgeç
// uygulanır (bkz. [interopStoreProductsRequest]).
func decodeStoreProductsRequest(raw json.RawMessage) (interopStoreProductsRequest, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return interopStoreProductsRequest{}, errors.Invalid(CodeInteropRequestInvalid,
			"vitrin ürün isteği boş olamaz")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	// Tanınmayan alan REDDEDİLİR: sessizce yutulan bir alan, çağıranın
	// gönderdiğini sandığı ama uygulanmayan bir koşul demektir. Burada o koşul
	// büyük olasılıkla kanal süzgecidir ("channel_ids" yazan bir tüketici
	// süzgeci uyguladığını sanırken tüm yayındaki kataloğu okurdu).
	dec.DisallowUnknownFields()

	var out interopStoreProductsRequest
	if err := dec.Decode(&out); err != nil {
		return interopStoreProductsRequest{}, errors.Wrap(err, errors.KindInvalid, CodeInteropRequestInvalid,
			"vitrin ürün isteği çözümlenemedi; JSON nesnesi olmalı")
	}
	return out, nil
}
