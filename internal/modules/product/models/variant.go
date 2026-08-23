package models

import "time"

// Variant satılabilir tek bir üründür: sepete giren, stoğu tutulan ve
// fiyatlanan birim ürün değil VARYANTTIR.
//
// Fiyat ve stok bu modülde durmaz; varyant kimliği üzerinden
// "product_variant_price_set" ve "product_variant_inventory" link'leriyle
// pricing ve inventory modüllerine bağlanır.
type Variant struct {
	ID              string         `json:"id"`
	ProductID       string         `json:"product_id"`
	Title           string         `json:"title"`
	SKU             *string        `json:"sku,omitempty"`
	Barcode         *string        `json:"barcode,omitempty"`
	EAN             *string        `json:"ean,omitempty"`
	UPC             *string        `json:"upc,omitempty"`
	ManageInventory bool           `json:"manage_inventory"`
	AllowBackorder  bool           `json:"allow_backorder"`
	Weight          *int32         `json:"weight,omitempty"`
	Rank            int32          `json:"rank"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       *time.Time     `json:"deleted_at,omitempty"`

	// OptionValues varyantı seçenek uzayında konumlandıran değerlerdir
	// (örn. "Beden: S", "Renk: Kırmızı"); yalnızca istendiğinde doldurulur.
	OptionValues []OptionValue `json:"option_values,omitempty"`
}

// Option bir ürünün seçenek eksenidir (örn. "Beden").
type Option struct {
	ID        string     `json:"id"`
	ProductID string     `json:"product_id"`
	Title     string     `json:"title"`
	Rank      int32      `json:"rank"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// Values seçeneğin alabileceği değerlerdir (örn. "S", "M", "L").
	Values []OptionValue `json:"values,omitempty"`
}

// OptionValue bir seçeneğin tek bir değeridir (örn. "S").
type OptionValue struct {
	ID       string `json:"id"`
	OptionID string `json:"option_id"`
	Value    string `json:"value"`
	Rank     int32  `json:"rank"`
	// OptionTitle değerin ait olduğu seçeneğin başlığıdır. Varyantın seçenek
	// değerleri okunurken doldurulur; "S" değeri tek başına anlamsızdır,
	// "Beden: S" anlamlıdır.
	OptionTitle string     `json:"option_title,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitzero"`
	UpdatedAt   time.Time  `json:"updated_at,omitzero"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// OptionValueRef bir seçenek değerinin HANGİ ÜRÜNE ait olduğunu taşır.
//
// Varyanta değer bağlanırken değerin aynı ürünün seçeneğinden gelmesi
// zorunludur; bu doğrulama tek toplu sorguyla yapılabilsin diye ürün kimliği
// değerin yanında döner.
type OptionValueRef struct {
	OptionValue
	ProductID string `json:"product_id"`
}
