package provider

// QuoteCart is the cart a shipping quote is asked for (ADR 0445).
//
// The cart flow fills it from the cart's own record on every quote it asks,
// the shopper's option list and the method it adds alike, so a provider
// pricing by the goods reads the goods it prices rather than finding the
// cart some other way. Each provider is handed its own copy.
type QuoteCart struct {
	// ID is the cart's id. It is the cart's access at the storefront (ADR
	// 0439): a provider keeps it out of what it logs and returns.
	ID string
	// CurrencyCode is the cart's currency (ISO 4217, upper case).
	CurrencyCode string
	// Lines are the lines that travel, in the cart's order: every line but a
	// gift card's, as [QuoteInput.ItemCount] counts them.
	Lines []QuoteLine
}

// QuoteLine is one cart line as a shipping quote reads it.
//
// The measures are the catalog's as recorded, each named for the record that
// holds it: a variant carries a weight, its product a weight and a box. gobit
// derives no parcel, desi or volume from them and picks none for the other;
// which one a tariff reads is the provider's rule (ADR 0065). A measure is
// zero when the catalog records none, which no parcel weighs or measures.
type QuoteLine struct {
	// ID is the cart line's id.
	ID string
	// VariantID is the variant the line sells.
	VariantID string
	// ProductID is the variant's product, empty when the catalog could not
	// name it.
	ProductID string
	// Quantity is the units on the line.
	Quantity int64
	// UnitPrice is one unit's price in this round, in minor units, before
	// any discount.
	UnitPrice int64
	// Subtotal is UnitPrice times Quantity less the line's discount, in minor
	// units: what the shopper pays for the line's goods.
	Subtotal int64
	// VariantWeight is the variant's weight, in grams as
	// [QuoteInput.TotalWeight] counts them; the catalog stores the number its
	// operator wrote.
	VariantWeight int64
	// ProductWeight is the product's weight, likewise.
	ProductWeight int64
	// ProductLength, ProductWidth and ProductHeight are the product's box, in
	// the unit its operator wrote; the catalog names none.
	ProductLength int64
	ProductWidth  int64
	ProductHeight int64
}
