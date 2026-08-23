// Package models pricing modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete girmez,
// dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API katmanları
// depolama ayrıntısına bağlanmaz.
//
// Para daima TAM SAYI minor unit'tir (kuruş/cent) ve para birimi ayrı alanda
// durur (plan Bölüm 8); float hiçbir yerde kullanılmaz. Zamanlar UTC'dir.
package models

import "time"

// Tutar ve adet sınırları.
//
// Sınırlar keyfi değildir: sepet toplamı Faz 5'te tutar × adet olarak
// hesaplanacaktır ve bu çarpım int64'e SIĞMALIDIR. MaxAmount × MaxQuantity =
// 10^12 × 10^6 = 10^18 < 9.22×10^18 olduğu için taşma yapısal olarak
// imkânsızdır. Aynı sınırlar migration'daki CHECK kısıtlarında da tekrarlanır;
// servis doğrulaması atlansa bile veritabanı ikinci kapıdır.
const (
	// MinAmount izin verilen en küçük tutardır. Negatif fiyat bir indirim
	// değildir; indirim promotion modülünün işidir.
	MinAmount int64 = 0
	// MaxAmount izin verilen en büyük tutardır (minor unit).
	MaxAmount int64 = 1_000_000_000_000
	// MinQuantity bir fiyat aralığının alt sınırının en küçük değeridir.
	MinQuantity int32 = 1
	// MaxQuantity bir fiyat aralığının üst sınırının en büyük değeridir.
	MaxQuantity int32 = 1_000_000
)

// PriceSet bir varyantın fiyatlarının kabıdır.
//
// Kap hangi varyanta ait olduğunu BİLMEZ: bağ, product modülünün bildirdiği
// "product_variant_price_set" linkiyle kurulur ve pricing o linki hiç görmez
// (Prensip 2.1/2.4). pricing yalnızca kabı üretir ve kimliğini döner.
type PriceSet struct {
	// ID "pset_" önekli, zaman sıralı kimliktir.
	ID string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// Price tek bir para birimi ve adet aralığı için geçerli tutardır.
type Price struct {
	// ID "price_" önekli kimliktir.
	ID string
	// PriceSetID fiyatın ait olduğu kabın kimliğidir.
	PriceSetID string
	// PriceListID fiyatı bir kampanya/segment listesine bağlar; nil ise bu bir
	// TABAN fiyattır.
	PriceListID *string
	// CurrencyCode ISO 4217 para birimi kodudur; daima BÜYÜK harf saklanır.
	CurrencyCode string
	// Amount minor unit cinsinden tutardır (kuruş/cent).
	Amount int64
	// MinQuantity fiyatın geçerli olduğu en küçük adettir (en az 1).
	MinQuantity int32
	// MaxQuantity fiyatın geçerli olduğu en büyük adettir; nil ise üst sınır yoktur.
	MaxQuantity *int32
	// Rules fiyatın geçerlilik koşullarıdır; boşsa fiyat koşulsuzdur.
	Rules []PriceRule
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
}

// RuleOperator bir fiyat kuralının karşılaştırma işlecidir.
type RuleOperator string

// Desteklenen işleçler.
//
// eq/ne/in/nin DİZGE karşılaştırmasıdır; gt/gte/lt/lte ise iki tarafı da tam
// sayıya çevirip SAYISAL karşılaştırır (örn. "customer_age" > "18"). Sayıya
// çevrilemeyen bir bağlam değeri kuralı EŞLEŞMEZ yapar, hata üretmez: bağlam
// dışarıdan gelir ve tek bir bozuk alan tüm fiyat hesabını düşürmemelidir.
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

// PriceRule bir fiyatın hangi koşulda geçerli olduğunu belirten kuraldır.
//
// Örnek: {Attribute: "region_id", Operator: OpEq, Values: []string{"reg_1"}}.
type PriceRule struct {
	// ID "prule_" önekli kimliktir.
	ID string
	// PriceID kuralın bağlı olduğu fiyattır.
	PriceID string
	// Attribute hesaplama bağlamında bakılacak alan adıdır.
	Attribute string
	// Operator karşılaştırma işlecidir.
	Operator RuleOperator
	// Values karşılaştırmanın sağ tarafıdır; en az bir eleman içerir.
	Values []string
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
}

// PriceListType bir fiyat listesinin türüdür.
type PriceListType string

// Fiyat listesi türleri.
const (
	// PriceListSale kampanya (indirim) listesidir.
	PriceListSale PriceListType = "sale"
	// PriceListOverride taban fiyatın yerine geçen listedir; sözleşmeli/B2B
	// fiyatlandırma bu türdendir ve kampanyayı da ezer.
	PriceListOverride PriceListType = "override"
)

// Valid türün tanımlı olup olmadığını bildirir.
func (t PriceListType) Valid() bool {
	return t == PriceListSale || t == PriceListOverride
}

// Priority türün seçim önceliğidir; büyük değer önce gelir.
//
// Taban fiyatın (listesiz) önceliği 0'dır; bu yüzden sıfır değer TABAN anlamına
// gelir ve tanımsız bir tür kazara kampanyanın önüne geçemez.
func (t PriceListType) Priority() int {
	switch t {
	case PriceListOverride:
		return 2
	case PriceListSale:
		return 1
	default:
		return 0
	}
}

// PriceListStatus bir fiyat listesinin durumudur.
type PriceListStatus string

// Fiyat listesi durumları.
const (
	// PriceListDraft henüz yayına alınmamış listedir; fiyatları hesaba KATILMAZ.
	PriceListDraft PriceListStatus = "draft"
	// PriceListActive yayındaki listedir.
	PriceListActive PriceListStatus = "active"
	// PriceListExpired elle sonlandırılmış listedir; fiyatları hesaba KATILMAZ.
	PriceListExpired PriceListStatus = "expired"
)

// Valid durumun tanımlı olup olmadığını bildirir.
func (s PriceListStatus) Valid() bool {
	return s == PriceListDraft || s == PriceListActive || s == PriceListExpired
}

// PriceList kampanya/segment fiyat listesidir.
type PriceList struct {
	// ID "plist_" önekli kimliktir.
	ID string
	// Title listenin görünen adıdır; boş olamaz.
	Title string
	// Description isteğe bağlı açıklamadır.
	Description string
	// Type listenin türüdür (sale | override).
	Type PriceListType
	// Status listenin durumudur (draft | active | expired).
	Status PriceListStatus
	// StartsAt geçerlilik penceresinin başıdır; nil ise alt sınır yoktur.
	StartsAt *time.Time
	// EndsAt geçerlilik penceresinin sonudur; nil ise üst sınır yoktur.
	EndsAt *time.Time
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
}

// PriceListInfo bir fiyata bağlı listenin hesaplamada kullanılan üstverisidir.
//
// Tam [PriceList] yerine bu dar görünüm taşınır: hesaplama başlık ve açıklamayı
// kullanmaz, taşımak da onları hesabın girdisi gibi gösterirdi.
type PriceListInfo struct {
	// ID listenin kimliğidir.
	ID string
	// Type listenin türüdür.
	Type PriceListType
	// Status listenin durumudur.
	Status PriceListStatus
	// StartsAt geçerlilik penceresinin başıdır; nil ise alt sınır yoktur.
	StartsAt *time.Time
	// EndsAt geçerlilik penceresinin sonudur; nil ise üst sınır yoktur.
	EndsAt *time.Time
}

// Usable listenin verilen anda fiyat sunmaya uygun olup olmadığını bildirir.
//
// Uygunluk iki koşulun BİRLİKTE sağlanmasıdır: durum active olmalı ve an,
// [StartsAt, EndsAt] penceresinde bulunmalıdır. Pencere uçları kapsayıcıdır
// (nil uç = sınırsız).
func (l PriceListInfo) Usable(at time.Time) bool {
	if l.Status != PriceListActive {
		return false
	}
	if l.StartsAt != nil && at.Before(*l.StartsAt) {
		return false
	}
	if l.EndsAt != nil && at.After(*l.EndsAt) {
		return false
	}
	return true
}

// PriceCandidate hesaplamaya giren tek bir fiyat ve (varsa) listesidir.
type PriceCandidate struct {
	// Price fiyatın kendisidir (kuralları dâhil).
	Price Price
	// List fiyatın bağlı olduğu listenin üstverisidir; nil ise taban fiyattır.
	//
	// PriceListID dolu ama List nil ise fiyatın listesi SİLİNMİŞTİR ve fiyat
	// hesaba katılmaz (bkz. servis katmanındaki seçim kuralı).
	List *PriceListInfo
}

// CalculatedPrice bir hesaplamanın sonucudur.
type CalculatedPrice struct {
	// PriceID seçilen fiyatın kimliğidir.
	PriceID string
	// PriceSetID fiyatın ait olduğu kabın kimliğidir.
	PriceSetID string
	// CurrencyCode seçilen fiyatın para birimidir (BÜYÜK harf).
	CurrencyCode string
	// Amount birim başına minor unit tutardır.
	Amount int64
	// Quantity hesaplamanın yapıldığı adettir.
	Quantity int32
	// Total = Amount × Quantity; taşmaması [MaxAmount]/[MaxQuantity] ile
	// güvence altındadır.
	Total int64
	// MinQuantity seçilen fiyatın alt adet sınırıdır.
	MinQuantity int32
	// MaxQuantity seçilen fiyatın üst adet sınırıdır; nil ise sınırsız.
	MaxQuantity *int32
	// PriceListID fiyat bir listeden geliyorsa listenin kimliğidir.
	PriceListID *string
	// PriceListType fiyat bir listeden geliyorsa listenin türüdür; taban
	// fiyatta boştur.
	PriceListType PriceListType
	// MatchedRules seçilen fiyatın eşleşen kural sayısıdır; 0 ise fiyat
	// koşulsuzdur. Seçimin NEDEN o fiyata düştüğünü açıklar.
	MatchedRules int
}
