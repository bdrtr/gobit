// Package models inventory modülünün alan (domain) modellerini içerir.
//
// Buradaki tipler veritabanı sürücüsünden bağımsızdır: pgtype ve sqlc üretimi
// tipler buraya SIZMAZ. Çeviri repository katmanında yapılır; servis, API ve testler
// yalnızca bu tipleri görür.
//
// Adetler her yerde tam sayıdır (BIGINT -> int64). Bu modülde para yoktur;
// para taşıyan modüllerdeki "minor unit" kuralının buradaki karşılığı, adedin
// hiçbir yerde kesirli tutulmamasıdır.
package models

import "time"

// StockLocation stoğun fiziksel olarak durduğu yerdir (depo, mağaza).
type StockLocation struct {
	// ID "sloc_" önekli, zamana göre sıralanabilir kimliktir.
	ID string
	// Name lokasyonun görünen adıdır.
	Name string
	// Address1, Address2, City, Province, PostalCode konum bilgileridir;
	// hepsi isteğe bağlıdır (boş dize "değer yok" demektir).
	Address1   string
	Address2   string
	City       string
	Province   string
	PostalCode string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur (örn. "TR").
	CountryCode string
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InventoryItem stok takibi yapılan kalemdir.
//
// Kalemin bir ürün varyantına ait olduğu bilgisi BU MODÜLDE TUTULMAZ; bağ
// "product_variant_inventory" link'i üzerinden kurulur (Prensip 2.2). inventory
// modülü product modülünü ne import eder ne de tablosuna referans verir.
type InventoryItem struct {
	// ID "invitem_" önekli kimliktir.
	ID string
	// SKU stok takip kodudur; yaşayan kalemler arasında benzersizdir.
	SKU string
	// Title ve Description isteğe bağlı açıklayıcı alanlardır.
	Title       string
	Description string
	// RequiresShipping kalemin fiziksel olarak sevk edilmesi gerekip
	// gerekmediğini bildirir; dijital ürünlerde false olur.
	RequiresShipping bool
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InventoryLevel bir kalemin bir lokasyondaki stok durumudur.
//
// Satılabilir adet SAKLANMAZ, [InventoryLevel.Available] ile türetilir.
type InventoryLevel struct {
	// ID "invlevel_" önekli kimliktir.
	ID string
	// InventoryItemID seviyenin ait olduğu kalemin kimliğidir.
	InventoryItemID string
	// LocationID seviyenin ait olduğu lokasyonun kimliğidir.
	LocationID string
	// StockedQuantity lokasyonda fiziksel olarak bulunan adettir.
	StockedQuantity int64
	// ReservedQuantity fiziksel stoğun rezerve edilmiş, yani başka bir
	// satışa söz verilmiş kısmıdır.
	ReservedQuantity int64
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Available satılabilir adedi döner: stocked - reserved.
//
// Değer türetilir, saklanmaz. Saklansaydı iki sütun birbirinden ayrı düşebilir
// ve stok sessizce yanlış görünürdü.
func (l InventoryLevel) Available() int64 {
	return l.StockedQuantity - l.ReservedQuantity
}

// ReservationStatus bir rezervasyonun yaşam döngüsündeki durumudur.
type ReservationStatus string

// Rezervasyon durumları. Geçişler: active -> released | confirmed.
// Sonlanmış bir rezervasyon yeniden aktifleşmez.
const (
	// ReservationActive stoğun ayrıldığını ve henüz sonlanmadığını bildirir.
	ReservationActive ReservationStatus = "active"
	// ReservationReleased rezervasyonun geri alındığını bildirir; ayrılan adet
	// yeniden satılabilir hâle gelmiştir. Saga telafisi bu duruma taşır.
	ReservationReleased ReservationStatus = "released"
	// ReservationConfirmed rezervasyonun fiziksel stoktan düşüldüğünü bildirir;
	// sevkiyatı yapılmış adet budur.
	ReservationConfirmed ReservationStatus = "confirmed"
)

// Valid durumun tanımlı bir değer olup olmadığını bildirir.
func (s ReservationStatus) Valid() bool {
	switch s {
	case ReservationActive, ReservationReleased, ReservationConfirmed:
		return true
	default:
		return false
	}
}

// String durumun metin gösterimini döner.
func (s ReservationStatus) String() string {
	return string(s)
}

// Reservation satılabilir stoktan ayrılmış bir adettir.
//
// Faz 6'daki complete_cart saga'sı önce Reserve ile bunu oluşturur, akış
// başarısız olursa telafi adımı ReleaseReservation ile geri alır, başarılı
// olursa ConfirmReservation ile fiziksel stoktan düşer.
type Reservation struct {
	// ID "invres_" önekli kimliktir.
	ID string
	// InventoryItemID rezervasyonun ayrıldığı kalemdir.
	InventoryItemID string
	// LocationID stoğun ayrıldığı lokasyondur.
	LocationID string
	// Quantity ayrılan adettir; her zaman pozitiftir.
	Quantity int64
	// LineItemID rezervasyonu isteyen sepet/sipariş satırının kimliğidir.
	// cart modülüne aittir ve BURADA FOREIGN KEY DEĞİLDİR (Prensip 2.2).
	// Boş olabilir: her rezervasyon bir satırdan doğmak zorunda değildir.
	LineItemID string
	// Description isteğe bağlı serbest açıklamadır.
	Description string
	// Status rezervasyonun durumudur.
	Status ReservationStatus
	// CreatedAt ve UpdatedAt UTC'dir.
	CreatedAt time.Time
	UpdatedAt time.Time
}
