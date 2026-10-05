package models

// VariantCost is what one unit of a variant costs the shop in one currency, net
// of tax, in minor units (ADR 0401).
//
// It is a type of its own, outside [Variant], on purpose: the storefront's
// product embeds the variant model, and a cost is the shop's business, not the
// shopper's.
type VariantCost struct {
	CurrencyCode string `json:"currency_code"`
	Amount       int64  `json:"amount"`
}
