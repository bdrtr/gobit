package cart

import (
	"context"
	"strconv"

	"github.com/bdrtr/gobit/core/errors"
)

// MaxExchangeQuoteLines is the most lines one exchange quote prices: a
// replacement names a handful of variants, and the bound keeps one request
// from pricing an unbounded list.
const MaxExchangeQuoteLines = 100

// ExchangeQuoteInput is what an exchange's variants are quoted in (ADR 0432):
// the order's region, sales channel and customer, and the variants it sends.
type ExchangeQuoteInput struct {
	RegionID       string              `json:"region_id"`
	SalesChannelID string              `json:"sales_channel_id"`
	CustomerID     string              `json:"customer_id"`
	Lines          []ExchangeQuoteLine `json:"lines"`
}

// ExchangeQuoteLine is one variant an exchange sends and how many of it.
type ExchangeQuoteLine struct {
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
	// UnitPrice is the operator's price, in the region's convention; nil
	// prices the line as a cart would. Either way the tax is computed here.
	UnitPrice *int64 `json:"unit_price,omitempty"`
}

// ExchangeQuote is the answer: each line priced and taxed as a cart's line of
// it would be, in the region's currency and convention.
type ExchangeQuote struct {
	CurrencyCode string `json:"currency_code"`
	// PricesIncludeTax is the market's convention the lines were taxed under;
	// the caller compares it with the order's (ADR 0246).
	PricesIncludeTax bool `json:"prices_include_tax"`
	// TaxSource is which authority taxed the lines ([Totals.TaxSource]).
	TaxSource string `json:"tax_source"`
	// Lines are in the request's order; each is a [LineTotals] with no
	// discount, so Total = Subtotal + TaxTotal.
	Lines []LineTotals `json:"lines"`
}

// QuoteExchangeLines prices and taxes the variants an exchange sends, as a cart
// of the order's customer in the order's region and sales channel would charge
// them, with no promotion (ADR 0432).
//
// It is the totals round run on lines that belong to no cart: the same price
// context (ADR 0397, ADR 0403), each line priced at its quantity, and the same
// tax step ([Workflows.applyTaxes]) with the same product facts, so a variant
// is taxed at the rate, the stack and the convention its cart line would carry
// and a gift card at none. A line with the operator's unit price skips the
// price list and keeps the tax step: an operator names a price, never an
// untaxed figure. A promotion is the cart's discount and not the goods' price,
// and an exchange carries none.
//
// It writes nothing. The caller copies the answer onto what it records, as an
// order line keeps the tax it was charged (ADR 0211).
func (w *Workflows) QuoteExchangeLines(ctx context.Context, in ExchangeQuoteInput) (ExchangeQuote, error) {
	if err := requireID("region_id", in.RegionID); err != nil {
		return ExchangeQuote{}, err
	}
	if len(in.Lines) == 0 || len(in.Lines) > MaxExchangeQuoteLines {
		return ExchangeQuote{}, errors.Invalid(CodeInvalidInput,
			"an exchange quote prices between 1 and %d lines, %d were given",
			MaxExchangeQuoteLines, len(in.Lines))
	}
	for i := range in.Lines {
		line := in.Lines[i]
		if err := requireID("variant_id", line.VariantID); err != nil {
			return ExchangeQuote{}, err
		}
		if _, err := quantity32(line.Quantity); err != nil {
			return ExchangeQuote{}, err
		}
		if line.UnitPrice != nil {
			if err := checkAmount("unit_price", *line.UnitPrice, MaxAmount); err != nil {
				return ExchangeQuote{}, err
			}
		}
	}

	currency, _, err := w.regions.RegionCurrency(ctx, in.RegionID)
	if err != nil {
		if errors.IsNotFound(err) {
			return ExchangeQuote{}, errors.Wrap(err, errors.KindNotFound, CodeQuoteRegionUnknown,
				"region %s does not exist", in.RegionID)
		}
		return ExchangeQuote{}, err
	}

	snap := Snapshot{
		ID:             "exchange-quote",
		RegionID:       in.RegionID,
		CustomerID:     in.CustomerID,
		CurrencyCode:   currency,
		SalesChannelID: in.SalesChannelID,
	}
	listed := Snapshot{
		ID: snap.ID, RegionID: snap.RegionID, CustomerID: snap.CustomerID,
		CurrencyCode: snap.CurrencyCode, SalesChannelID: snap.SalesChannelID,
	}
	for i := range in.Lines {
		item := SnapshotItem{ID: "line-" + strconv.Itoa(i), VariantID: in.Lines[i].VariantID, Quantity: in.Lines[i].Quantity}
		snap.Items = append(snap.Items, item)
		if in.Lines[i].UnitPrice == nil {
			listed.Items = append(listed.Items, item)
		}
	}

	// The lines with no operator's price are priced as the totals round prices
	// a cart's; the others take the operator's price as their sticker.
	fromList, err := w.lineSubtotals(ctx, listed)
	if err != nil {
		return ExchangeQuote{}, err
	}
	lines := make([]LineTotals, 0, len(snap.Items))
	next := 0
	for i := range in.Lines {
		if in.Lines[i].UnitPrice == nil {
			lines = append(lines, fromList[next])
			next++
			continue
		}
		subtotal, mulErr := mulAmount(*in.Lines[i].UnitPrice, in.Lines[i].Quantity)
		if mulErr != nil {
			return ExchangeQuote{}, mulErr
		}
		lines = append(lines, LineTotals{
			LineItemID: snap.Items[i].ID, UnitPrice: *in.Lines[i].UnitPrice, Subtotal: subtotal,
		})
	}

	// The facts the tax step reads — the product's type and whether it is a
	// gift card — are read as the totals round reads them, and a failure to
	// read them taxes the lines without them, as it does there.
	var facts map[string]productFacts
	if w.taxes != nil {
		read, factsErr := w.lineProductFacts(ctx, snap)
		if factsErr != nil {
			w.log.WarnContext(ctx, "the products' facts could not be read; quoting without them",
				"error", factsErr, "region_id", in.RegionID, "lines", len(lines))
		}
		facts = read
	}
	tax, err := w.applyTaxes(ctx, snap, 0, lines, facts)
	if err != nil {
		return ExchangeQuote{}, err
	}
	totals, err := assembleTotals(snap, lines, 0, tax.source)
	if err != nil {
		return ExchangeQuote{}, err
	}

	return ExchangeQuote{
		CurrencyCode:     currency,
		PricesIncludeTax: tax.pricesIncludeTax,
		TaxSource:        tax.source,
		Lines:            totals.Lines,
	}, nil
}
