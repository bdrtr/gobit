package cart

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
)

// The read layer's measure fields (ADR 0445), spelled as the product module's
// records carry them: a variant's weight, and its product's weight and box.
const (
	FieldWeight = "weight"
	FieldLength = "length"
	FieldWidth  = "width"
	FieldHeight = "height"
)

// quoteCart is the cart a shipping quote is asked for, as the fulfillment
// module's request carries it (ADR 0445); its schema is that module's
// interopQuoteCart.
type quoteCart struct {
	ID           string      `json:"id"`
	CurrencyCode string      `json:"currency_code"`
	Lines        []quoteLine `json:"lines"`
}

// quoteLine is one line of [quoteCart].
type quoteLine struct {
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

// measures are a variant's and its product's, zero where the catalog records
// none.
type measures struct {
	productID                    string
	variantWeight, productWeight int64
	length, width, height        int64
}

// quoteCartFor builds the quoted cart from the cart's own lines: the ones that
// travel, priced as this round priced them and measured as the catalog records
// them. lines are the round's, discounts applied, in the snapshot's order.
func (w *Workflows) quoteCartFor(
	ctx context.Context, snap Snapshot, lines []LineTotals, cards map[string]bool,
) (*quoteCart, error) {
	measured, err := w.lineMeasures(ctx, snap)
	if err != nil {
		return nil, err
	}

	out := &quoteCart{ID: snap.ID, CurrencyCode: snap.CurrencyCode, Lines: make([]quoteLine, 0, len(snap.Items))}
	for i := range snap.Items {
		item := snap.Items[i]
		if cards[item.ID] {
			continue
		}
		m := measured[item.VariantID]
		line := quoteLine{
			ID: item.ID, VariantID: item.VariantID, ProductID: m.productID, Quantity: item.Quantity,
			VariantWeight: m.variantWeight, ProductWeight: m.productWeight,
			ProductLength: m.length, ProductWidth: m.width, ProductHeight: m.height,
		}
		if i < len(lines) {
			line.UnitPrice = lines[i].UnitPrice
			line.Subtotal = lines[i].Subtotal - lines[i].DiscountTotal
		}
		out.Lines = append(out.Lines, line)
	}

	return out, nil
}

// lineMeasures reads the measures of the cart's variants and their products in
// two catalog reads. A variant or product missing from the answer has none; a
// read failure is returned, since a quote priced on missing goods is a price
// nobody asked for.
func (w *Workflows) lineMeasures(ctx context.Context, snap Snapshot) (map[string]measures, error) {
	variantIDs := snap.VariantIDs()
	out := make(map[string]measures, len(variantIDs))
	if len(variantIDs) == 0 {
		return out, nil
	}

	variants, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{query.IDField, FieldProductID, FieldWeight},
		Filters: map[string]any{FilterIDs: variantIDs},
		Limit:   len(variantIDs),
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"could not read the measures of %d variants from the catalog", len(variantIDs))
	}

	productOf := make(map[string]string, len(variants))
	for _, record := range variants {
		variantID, _ := record[query.IDField].(string)
		if variantID == "" {
			continue
		}
		productID, _ := record[FieldProductID].(string)
		productOf[variantID] = productID
		out[variantID] = measures{productID: productID, variantWeight: measure(record[FieldWeight])}
	}

	productIDs := uniqueProductIDs(productOf)
	if len(productIDs) == 0 {
		return out, nil
	}
	products, err := w.catalog.Graph(ctx, query.GraphSpec{
		Entity:  EntityProduct,
		Fields:  []string{query.IDField, FieldWeight, FieldLength, FieldWidth, FieldHeight},
		Filters: map[string]any{FilterIDs: productIDs},
		Limit:   len(productIDs),
	})
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeCatalogReadFailed,
			"could not read the measures of %d products from the catalog", len(productIDs))
	}

	byProduct := make(map[string]query.Record, len(products))
	for _, record := range products {
		if productID, _ := record[query.IDField].(string); productID != "" {
			byProduct[productID] = record
		}
	}
	for variantID, m := range out {
		record, ok := byProduct[m.productID]
		if !ok {
			continue
		}
		m.productWeight = measure(record[FieldWeight])
		m.length = measure(record[FieldLength])
		m.width = measure(record[FieldWidth])
		m.height = measure(record[FieldHeight])
		out[variantID] = m
	}

	return out, nil
}

// measure reads a record's whole number, zero for anything else or below it.
func measure(value any) int64 {
	var n int64
	switch v := value.(type) {
	case int32:
		n = int64(v)
	case int64:
		n = v
	case int:
		n = int64(v)
	case float64:
		n = int64(v)
	}

	return max(n, 0)
}
