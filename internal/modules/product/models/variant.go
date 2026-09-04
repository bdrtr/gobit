package models

import "time"

// Variant is a single sellable product: what goes into the cart, what stock is
// kept for and what gets priced is not the product but the VARIANT.
//
// Price and stock do not live in this module; they are attached to the pricing
// and inventory modules over the variant id, through the
// "product_variant_price_set" and "product_variant_inventory" links.
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

	// OptionValues are the values that place the variant in the option space
	// (e.g. "Size: S", "Color: Red"); filled only when asked for.
	OptionValues []OptionValue `json:"option_values,omitempty"`
}

// Option is an option axis of a product (e.g. "Size").
type Option struct {
	ID        string     `json:"id"`
	ProductID string     `json:"product_id"`
	Title     string     `json:"title"`
	Rank      int32      `json:"rank"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`

	// Values are the values the option can take (e.g. "S", "M", "L").
	Values []OptionValue `json:"values,omitempty"`
}

// OptionValue is a single value of an option (e.g. "S").
type OptionValue struct {
	ID       string `json:"id"`
	OptionID string `json:"option_id"`
	Value    string `json:"value"`
	Rank     int32  `json:"rank"`
	// OptionTitle is the title of the option the value belongs to. It is filled
	// while the option values of a variant are read; the value "S" on its own is
	// meaningless, "Size: S" is meaningful.
	OptionTitle string     `json:"option_title,omitempty"`
	CreatedAt   time.Time  `json:"created_at,omitzero"`
	UpdatedAt   time.Time  `json:"updated_at,omitzero"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// OptionValueRef carries WHICH PRODUCT an option value belongs to.
//
// While a value is attached to a variant, the value has to come from an option
// of the same product; the product id is returned next to the value so that
// this check can be done with a single bulk query.
type OptionValueRef struct {
	OptionValue
	ProductID string `json:"product_id"`
}
