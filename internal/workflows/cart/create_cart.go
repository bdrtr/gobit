package cart

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// CreateCartInput yeni bir sepetin girdisidir.
type CreateCartInput struct {
	// CountryCode müşterinin ülkesidir (ISO 3166-1 alpha-2); ZORUNLUDUR.
	// Sepetin bölgesi ve para birimi bundan türetilir.
	CountryCode string
	// CustomerID sepetin sahibidir; OPSİYONELDİR. Boş bırakılırsa sepet
	// MİSAFİRE aittir ve hiçbir müşteri çağrısı yapılmaz.
	CustomerID string
	// Email sepetin iletişim adresidir; opsiyoneldir. Kayıtlı müşteri
	// sepetinde boş bırakılırsa müşterinin kayıtlı adresi kullanılır.
	Email string
	// Metadata sepete iliştirilecek SERBEST JSON nesnesidir; opsiyoneldir.
	//
	// Akış onu OKUMAZ ve hiçbir kararına katmaz, yalnızca sepete taşır: alan
	// gerçekten çağıranın kendi verisidir (kampanya kaynağı, vitrin oturumu)
	// ve türetilecek bir karşılığı yoktur. Ayrımın ölçütü [CountryCode] ile
	// aynıdır — orada gövdeye konan şey sunucunun verisiydi, burada değil.
	Metadata json.RawMessage
}

// CreateCartResult oluşturulan sepetin çağıranı ilgilendiren alanlarıdır.
type CreateCartResult struct {
	// CartID oluşturulan sepetin kimliğidir.
	CartID string
	// RegionID sepetin bağlandığı bölgedir.
	RegionID string
	// CurrencyCode sepetin para birimidir.
	CurrencyCode string
	// CustomerID sepetin sahibidir; misafir sepetinde boştur.
	CustomerID string
	// Email sepetin iletişim adresidir.
	Email string
	// Guest sepetin misafire ait olup olmadığını bildirir.
	Guest bool
}

// CreateCart ülke kodundan bölgeyi çözer ve sepeti oluşturur.
//
// Akış üç modüle dokunur: bölgeyi ve para birimini region'dan alır, kayıtlı
// müşteriyi customer'da doğrular, sepeti cart'a yazar. Tek yazma vardır, bu
// yüzden telafi (saga) gerekmez; gerekçesi paket yorumundadır.
//
// # Misafir ve kayıtlı müşteri
//
// CustomerID boşsa sepet misafirindir ve customer modülü HİÇ çağrılmaz: misafir
// akışının kayıtlı müşteri servisine bağımlı olmaması, hesabı olmayan bir
// müşterinin sepet açmasını customer modülünün ayakta olmasına bağlamaz.
//
// CustomerID doluysa müşteri DOĞRULANIR. Doğrulama, e-posta okunarak yapılır ve
// iki işi birden görür: müşteri yoksa çağrı errors.NotFound ile burada durur
// (aksi hâlde sepet, var olmayan bir müşteriye bağlanır ve hata ancak sipariş
// aşamasında görülürdü) ve e-posta verilmemişse müşterinin kayıtlı adresi
// sepete geçer. İkincisi keyfi bir kolaylık değil: sepetin iletişim adresi Faz
// 6'da siparişin iletişim adresi olacaktır ve kayıtlı bir müşterinin sepetini
// adressiz bırakmak, o bilgiyi ödeme adımında yeniden sormak demektir.
//
// Çağıran e-posta verdiyse müşterininki EZİLMEZ: sepetin adresi, o siparişin
// gönderileceği adrestir ve müşteri defterindeki güncel adresi değil.
//
// # Para biriminin ondalık basamağı neden kullanılmıyor
//
// [Regions.RegionCurrency] basamak sayısını da döner; sepet onu SAKLAMAZ. Para
// her yerde minor unit tam sayıdır (plan Bölüm 8) ve basamak sayısı yalnızca
// SUNUM için gerekir — tutarı gösteren katman onu para birimi kodundan
// yeniden okuyabilir. Sepette kopyalanmış bir basamak sayısı, referans tablosu
// düzeltildiğinde sessizce eskirdi.
func (w *Workflows) CreateCart(ctx context.Context, in CreateCartInput) (CreateCartResult, error) {
	country := strings.TrimSpace(in.CountryCode)
	if country == "" {
		return CreateCartResult{}, errors.Invalid(CodeInvalidInput, "country_code boş olamaz")
	}
	if in.CustomerID != "" {
		if err := requireID("customer_id", in.CustomerID); err != nil {
			return CreateCartResult{}, err
		}
	}

	regionID, err := w.regions.RegionIDForCountry(ctx, country)
	if err != nil {
		return CreateCartResult{}, err
	}
	currency, _, err := w.regions.RegionCurrency(ctx, regionID)
	if err != nil {
		return CreateCartResult{}, err
	}

	email := strings.TrimSpace(in.Email)
	if in.CustomerID != "" {
		known, customerErr := w.customers.CustomerEmail(ctx, in.CustomerID)
		if customerErr != nil {
			return CreateCartResult{}, customerErr
		}
		if email == "" {
			email = known
		}
	}

	cartID, err := w.carts.OpenCart(ctx, regionID, currency, in.CustomerID, email, in.Metadata)
	if err != nil {
		return CreateCartResult{}, err
	}

	w.log.InfoContext(ctx, "sepet açıldı",
		"cart_id", cartID, "region_id", regionID, "currency_code", currency,
		"guest", in.CustomerID == "")

	return CreateCartResult{
		CartID:       cartID,
		RegionID:     regionID,
		CurrencyCode: currency,
		CustomerID:   in.CustomerID,
		Email:        email,
		Guest:        in.CustomerID == "",
	}, nil
}
