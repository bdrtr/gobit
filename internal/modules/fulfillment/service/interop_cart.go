package service

import (
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// interopQuoteCart is the JSON schema of the cart an [Interop.ListOptionsJSON]
// request names (ADR 0445).
//
//	{
//	  "id":            "cart_...",
//	  "currency_code": "TRY",
//	  "lines": [
//	    {
//	      "id": "cali_...", "variant_id": "variant_...", "product_id": "prod_...",
//	      "quantity": 2, "unit_price": 1500, "subtotal": 2700,
//	      "variant_weight": 0, "product_weight": 1200,
//	      "product_length": 30, "product_width": 20, "product_height": 10
//	    }
//	  ]
//	}
//
// Every number is an INTEGER and a fractional one fails the decode, as the
// request's own amounts do. The cart flow is its one writer, from the cart's
// own record; this module reads none of it and hands it to the provider of a
// calculated option as [coreprovider.QuoteCart].
type interopQuoteCart struct {
	ID           string             `json:"id"`
	CurrencyCode string             `json:"currency_code"`
	Lines        []interopQuoteLine `json:"lines"`
}

// interopQuoteLine is one line of [interopQuoteCart].
type interopQuoteLine struct {
	ID            string `json:"id"`
	VariantID     string `json:"variant_id"`
	ProductID     string `json:"product_id"`
	Quantity      int64  `json:"quantity"`
	UnitPrice     int64  `json:"unit_price"`
	Subtotal      int64  `json:"subtotal"`
	VariantWeight int64  `json:"variant_weight"`
	ProductWeight int64  `json:"product_weight"`
	ProductLength int64  `json:"product_length"`
	ProductWidth  int64  `json:"product_width"`
	ProductHeight int64  `json:"product_height"`
}

// quoteCart is the published shape of the request's cart, nil when it names
// none.
func (c *interopQuoteCart) quoteCart() *coreprovider.QuoteCart {
	if c == nil {
		return nil
	}
	lines := make([]coreprovider.QuoteLine, 0, len(c.Lines))
	for _, l := range c.Lines {
		lines = append(lines, coreprovider.QuoteLine{
			ID:            l.ID,
			VariantID:     l.VariantID,
			ProductID:     l.ProductID,
			Quantity:      l.Quantity,
			UnitPrice:     l.UnitPrice,
			Subtotal:      l.Subtotal,
			VariantWeight: l.VariantWeight,
			ProductWeight: l.ProductWeight,
			ProductLength: l.ProductLength,
			ProductWidth:  l.ProductWidth,
			ProductHeight: l.ProductHeight,
		})
	}

	return &coreprovider.QuoteCart{ID: c.ID, CurrencyCode: c.CurrencyCode, Lines: lines}
}
