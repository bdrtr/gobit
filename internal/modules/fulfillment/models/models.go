// Package models fulfillment modülünün alan (domain) modellerini içerir.
//
// Buradaki tipler veritabanı sürücüsünden bağımsızdır: pgtype ve sqlc üretimi
// tipler buraya SIZMAZ. Çeviri repository katmanında yapılır; servis, API ve
// testler yalnızca bu tipleri görür.
//
// Para her yerde TAM SAYI minor unit'tir (kuruş/cent) ve para birimi ayrı
// alanda durur (plan Bölüm 8); kayan nokta hiçbir alanda kullanılmaz. Zamanlar
// UTC'dir.
//
// # Neyi bilmez
//
// Bu modül bir gönderinin HANGİ siparişe ait olduğunu bilmez.
// [Fulfillment.Reference] serbest bir metindir, foreign key DEĞİLDİR
// (Prensip 2.2) ve varlığı burada doğrulanmaz; bağ Module Links ile kurulur.
// Aynı şey [ShippingOption.RegionID] (region modülünün kimliği) ve
// [FulfillmentItem.LineItemID] (sipariş satırının kimliği) için de geçerlidir.
package models

import (
	"encoding/json"
	"time"
)

// Tutar sınırları.
//
// Üst sınır keyfi değildir: kargo ücreti sipariş toplamına eklenir ve toplamın
// int64'e SIĞMASI gerekir. 10^12 tavanı, aynı tavanı kullanan cart, pricing ve
// payment modülleriyle bilinçli olarak aynıdır; modüller birbirini import
// etmediği için değer burada tekrarlanır (ADR 0001'in kabul edilen bedeli).
const (
	// MinAmount izin verilen en küçük kargo tutarıdır.
	//
	// SIFIRDIR ve bu bilinçlidir: ücretsiz kargo gerçek bir iş kararıdır
	// (payment'taki koleksiyon tutarının aksine, sıfır burada ölü kayıt
	// üretmez). Negatif tutar ise müşteriye kargodan para ödemek demek olurdu.
	MinAmount int64 = 0
	// MaxAmount izin verilen en büyük kargo tutarıdır (minor unit).
	MaxAmount int64 = 1_000_000_000_000
)

// Kalem adedi sınırları.
const (
	// MinQuantity bir gönderi kaleminin en küçük adedidir.
	MinQuantity int64 = 1
	// MaxQuantity bir gönderi kaleminin en büyük adedidir.
	MaxQuantity int64 = 1_000_000
)

// Uygunluk bağlamının sayısal sınırları.
//
// Sınırlar keyfi değildir: kargo ücreti bu iki sayıyla ÇARPILARAK hesaplanır
// (bkz. internal/modules/fulfillment/manual). Üst sınırsız bir adet ya da
// ağırlık, tek bir istek parametresiyle çarpımı int64'ten taşırabilir; taşan
// bir çarpım NEGATİF bir kargo ücreti demektir — yani müşteriye para ödeyen
// bir sipariş.
//
// Değerler [MaxAmount] ile birlikte seçilmiştir: en büyük birim ücret bu iki
// tavanın herhangi biriyle çarpıldığında sonuç 10^18'dir ve int64'ün
// (~9,22×10^18) içinde kalır, yani sağlayıcı taşmadan ÖNCE "üst sınır aşıldı"
// diyebilir.
const (
	// MaxItemCount bir uygunluk sorgusunda bildirilebilecek en büyük toplam
	// kalem adedidir.
	MaxItemCount int64 = 1_000_000
	// MaxTotalWeight bir uygunluk sorgusunda bildirilebilecek en büyük toplam
	// ağırlıktır (GRAM); 10^9 gram = 1.000 tondur ve tek bir gönderi için
	// fazlasıyla geniştir.
	MaxTotalWeight int64 = 1_000_000_000
)

// ProfileType bir kargo profilinin türüdür.
type ProfileType string

// Kargo profili türleri.
const (
	// ProfileDefault mağazanın varsayılan profilidir; başka bir profile
	// bağlanmamış ürünler buraya düşer.
	ProfileDefault ProfileType = "default"
	// ProfileGiftCard fiziksel gönderi gerektirmeyen ürünler içindir.
	ProfileGiftCard ProfileType = "gift_card"
	// ProfileCustom mağazanın kendi tanımladığı profildir (örn. "ağır yük").
	ProfileCustom ProfileType = "custom"
)

// String türün metin karşılığını döner.
func (p ProfileType) String() string { return string(p) }

// Valid türün tanımlı bir değer olup olmadığını bildirir.
func (p ProfileType) Valid() bool {
	switch p {
	case ProfileDefault, ProfileGiftCard, ProfileCustom:
		return true
	default:
		return false
	}
}

// PriceType bir kargo seçeneğinin ücretinin NEREDEN geldiğini söyler.
type PriceType string

// Kargo seçeneği fiyat türleri.
const (
	// PriceFlat ücretin seçeneğin kendi Amount alanında sabit durduğunu
	// bildirir; sağlayıcıya HİÇ gidilmez.
	PriceFlat PriceType = "flat"
	// PriceCalculated ücreti sağlayıcının Quote'unun belirlediğini bildirir;
	// seçeneğin Amount alanı kullanılmaz ve sıfır olmak zorundadır.
	PriceCalculated PriceType = "calculated"
)

// String türün metin karşılığını döner.
func (p PriceType) String() string { return string(p) }

// Valid türün tanımlı bir değer olup olmadığını bildirir.
func (p PriceType) Valid() bool {
	switch p {
	case PriceFlat, PriceCalculated:
		return true
	default:
		return false
	}
}

// RuleOperator bir kargo seçeneği kuralının karşılaştırma işlecidir.
//
// İşleç kümesi pricing modülündeki fiyat kurallarıyla bilinçli olarak aynıdır;
// yönetici iki yerde farklı bir dil öğrenmek zorunda kalmamalıdır. Paket
// import EDİLMEZ (Prensip 2.4), tanım burada tekrarlanır.
type RuleOperator string

// Desteklenen işleçler.
//
// eq/ne/in/nin DİZGE karşılaştırmasıdır; gt/gte/lt/lte ise iki tarafı da tam
// sayıya çevirip SAYISAL karşılaştırır (örn. "subtotal" >= "50000"). Sayıya
// çevrilemeyen bir bağlam değeri kuralı EŞLEŞMEZ yapar, hata üretmez: bağlam
// dışarıdan gelir ve tek bir bozuk alan tüm kargo listesini düşürmemelidir.
//
// Sayısal karşılaştırma TAM SAYI üzerindendir; ara toplam gibi para alanları
// minor unit olduğu için kayan noktaya hiç uğramaz (plan Bölüm 8).
const (
	// OpEq değerin kuralın tek değerine eşit olmasını ister.
	OpEq RuleOperator = "eq"
	// OpNe değerin kuralın tek değerinden farklı olmasını ister.
	OpNe RuleOperator = "ne"
	// OpIn değerin kural kümesinde bulunmasını ister.
	OpIn RuleOperator = "in"
	// OpNin değerin kural kümesinde BULUNMAMASINI ister.
	OpNin RuleOperator = "nin"
	// OpGt sayısal olarak büyüklük ister.
	OpGt RuleOperator = "gt"
	// OpGte sayısal olarak büyük veya eşitlik ister.
	OpGte RuleOperator = "gte"
	// OpLt sayısal olarak küçüklük ister.
	OpLt RuleOperator = "lt"
	// OpLte sayısal olarak küçük veya eşitlik ister.
	OpLte RuleOperator = "lte"
)

// String işlecin metin karşılığını döner.
func (o RuleOperator) String() string { return string(o) }

// Valid işlecin tanımlı olup olmadığını bildirir.
func (o RuleOperator) Valid() bool {
	switch o {
	case OpEq, OpNe, OpIn, OpNin, OpGt, OpGte, OpLt, OpLte:
		return true
	default:
		return false
	}
}

// Numeric işlecin sayısal karşılaştırma yapıp yapmadığını bildirir.
func (o RuleOperator) Numeric() bool {
	switch o {
	case OpGt, OpGte, OpLt, OpLte:
		return true
	case OpEq, OpNe, OpIn, OpNin:
		return false
	default:
		return false
	}
}

// MultiValue işlecin birden çok değer alıp alamayacağını bildirir.
// Diğer tüm işleçler TEK değer ister.
func (o RuleOperator) MultiValue() bool {
	return o == OpIn || o == OpNin
}

// ShippingProfile kargo seçeneklerinin kabıdır.
//
// Ürünler profillere Module Links ile bağlanır; profil hangi ürünlere bağlı
// olduğunu BİLMEZ (Prensip 2.1). Bir sepetin hangi seçenekleri görebileceği,
// sepetteki ürünlerin bağlı olduğu profillerden türetilir ve o türetme
// fulfillment'ın değil, çağıranın işidir.
type ShippingProfile struct {
	// ID "sprof_" önekli kimliktir.
	ID string
	// Name profilin görünen adıdır; yaşayan kayıtlar arasında TEKTİR.
	Name string
	// Type profilin türüdür.
	Type ProfileType
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise profil canlıdır.
	DeletedAt *time.Time
}

// ShippingOption müşteriye sunulan bir kargo seçeneğidir.
type ShippingOption struct {
	// ID "sopt_" önekli kimliktir.
	ID string
	// Name seçeneğin görünen adıdır (örn. "Standart kargo").
	Name string
	// ProviderID seçeneği yürütecek kargo sağlayıcısının kimliğidir.
	ProviderID string
	// ShippingProfileID seçeneğin bağlı olduğu profildir (modül içi FK).
	ShippingProfileID string
	// PriceType ücretin nereden geldiğini söyler.
	PriceType PriceType
	// Amount [PriceFlat] seçeneklerde ücrettir (minor unit).
	// [PriceCalculated] seçeneklerde SIFIRDIR ve kullanılmaz; ücret
	// sağlayıcıdan gelir.
	Amount int64
	// CurrencyCode ISO 4217 kodudur ve daima BÜYÜK harf saklanır.
	CurrencyCode string
	// RegionID seçeneğin geçerli olduğu bölgedir; BOŞ ise her bölgede
	// geçerlidir. region modülünün kimliğidir ve FOREIGN KEY DEĞİLDİR
	// (Prensip 2.2).
	RegionID string
	// IsReturn seçeneğin İADE gönderisi için olduğunu bildirir. İade
	// seçenekleri normal satın alma akışında listelenmez.
	IsReturn bool
	// AdminOnly seçeneğin yalnızca yönetim yüzeyinde görüneceğini bildirir
	// (örn. "elden teslim"). Mağaza yüzeyine ÇIKMAZ.
	AdminOnly bool
	// Data sağlayıcıya ait yapılandırmadır ve Quote çağrısına olduğu gibi
	// geçirilir. Mağaza yüzeyine ÇIKMAZ: sağlayıcının iç verisidir.
	Data map[string]any
	// Metadata mağazanın serbest ek verisidir.
	Metadata map[string]any
	// Rules seçeneğin koşullarıdır; TAMAMI eşleşmezse seçenek sunulmaz.
	// Yalnızca uygunluk listelemesi ve kural okuma yollarında doldurulur.
	Rules []ShippingOptionRule
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise seçenek canlıdır.
	DeletedAt *time.Time
}

// ShippingOptionRule bir seçeneğin hangi koşulda sunulacağını belirten kuraldır.
//
// Örnek: {Attribute: "subtotal", Operator: OpGte, Values: []string{"50000"}} —
// "ara toplam 50.000 kuruşu geçerse bu seçenek sunulur".
type ShippingOptionRule struct {
	// ID "sorule_" önekli kimliktir.
	ID string
	// ShippingOptionID kuralın bağlı olduğu seçenektir.
	ShippingOptionID string
	// Attribute uygunluk bağlamında bakılacak alan adıdır.
	Attribute string
	// Operator karşılaştırma işlecidir.
	Operator RuleOperator
	// Values karşılaştırmanın sağ tarafıdır; en az bir eleman içerir.
	Values []string
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise kural canlıdır.
	DeletedAt *time.Time
}

// Fulfillment gerçekleşmiş bir gönderidir.
type Fulfillment struct {
	// ID "ful_" önekli kimliktir.
	ID string
	// Reference çağıranın kendi kaydının kimliğidir (sipariş).
	// FOREIGN KEY DEĞİLDİR (Prensip 2.2) ve bu modülde doğrulanmaz.
	Reference string
	// ShippingOptionID gönderinin kullandığı kargo seçeneğidir (modül içi FK).
	ShippingOptionID string
	// ProviderID gönderiyi oluşturan sağlayıcının kimliğidir.
	ProviderID string
	// ExternalID sağlayıcı tarafındaki gönderi kimliğidir; mutabakatta iki
	// sistemi eşleştiren alan budur. Sağlayıcı yanıtı gelene kadar boştur.
	ExternalID string
	// Status gönderinin güncel durumudur.
	Status FulfillmentStatus
	// TrackingNumber ve TrackingURL takip bilgisidir; sağlayıcı vermiyorsa boş.
	TrackingNumber string
	TrackingURL    string
	// IdempotencyKey aynı gönderinin iki kez oluşturulmasını engeller.
	IdempotencyKey string
	// ShippedAt, DeliveredAt ve CanceledAt ilgili geçişin anıdır (UTC);
	// geçiş yaşanmadıysa nil'dir.
	ShippedAt   *time.Time
	DeliveredAt *time.Time
	CanceledAt  *time.Time
	// Data sağlayıcının ham verisidir; olduğu gibi saklanır, yorumlanmaz.
	Data json.RawMessage
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
	// Items gönderiye giren kalemlerdir.
	Items []FulfillmentItem
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
	// DeletedAt yumuşak silme anıdır; nil ise gönderi canlıdır.
	DeletedAt *time.Time
}

// FulfillmentItem gönderiye giren tek bir kalemdir.
type FulfillmentItem struct {
	// ID "fulitem_" önekli kimliktir.
	ID string
	// FulfillmentID kalemin ait olduğu gönderidir (modül içi FK).
	FulfillmentID string
	// LineItemID sipariş satırının kimliğidir. FOREIGN KEY DEĞİLDİR
	// (Prensip 2.2) ve bu modülde doğrulanmaz.
	LineItemID string
	// Quantity gönderiye giren adettir; daima pozitiftir.
	Quantity int64
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ManualShipment manuel sağlayıcının KENDİ defterindeki gönderidir.
//
// Bu kayıt modülün alan verisi değildir; taklit edilen dış sistemin
// durumudur. fulfillment servisi ona hiç dokunmaz, yalnızca manual sağlayıcı
// okur ve yazar (bkz. internal/modules/fulfillment/manual).
type ManualShipment struct {
	// ID "manful_" önekli SAĞLAYICI kimliğidir; modülün gönderi kaydında
	// ExternalID olarak durur.
	ID string
	// IdempotencyKey aynı gönderinin iki kez oluşturulmasını engeller;
	// sağlayıcının defterinde TEKTİR.
	IdempotencyKey string
	// Reference çağıranın kendi kaydının kimliğidir (gönderi kimliği).
	Reference string
	// OptionID gönderinin açıldığı kargo seçeneğidir.
	OptionID string
	// Status gönderinin sağlayıcı tarafındaki durumudur.
	Status FulfillmentStatus
	// TrackingNumber ve TrackingURL sağlayıcının ürettiği takip bilgisidir.
	TrackingNumber string
	TrackingURL    string
	// Data gönderi açılırken verilen serbest veridir. Manuel sağlayıcının
	// davranışını yönlendiren anahtarlar buradadır (bkz. manual paketi).
	Data json.RawMessage
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}
