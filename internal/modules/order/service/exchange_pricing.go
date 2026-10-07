package service

import (
	"cmp"
	"context"
	"encoding/json"
	"math/bits"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// An exchange that names its return prices what it sends (ADR 0432).
//
// An item naming an order line sends the units that line sold, so it is priced
// at what the line charged for them: its total and its tax, shared by units.
// The goods coming back are valued by the same share of their lines, so a
// line sent for the same line returned moves nothing, and no sold row's price
// is raised (ADR 0394). An item naming a variant the order never sold is
// priced by the cart flow's quote in the order's region, sales channel and
// customer, or at the operator's unit price taxed by that quote. Either way
// the figures are copied onto the item when it is written and never read
// again from a price list or a tax table, as an order line keeps its own
// (ADR 0211).
//
// The exchange's difference is then what its live replacements send less
// what its return takes back, rewritten under the exchange's lock each time a
// replacement of it is written or withdrawn, and only while it is requested:
// a funded exchange holds money for the figure it had. A withdrawal, and a
// settled exchange reopened by a canceled parcel, price its line items again
// from each line's first unit before the figure is derived, so a line's live
// items always add up to the share of the units they send.

// ExchangeQuote is the cart flow's quote of the variants an exchange sends
// (ADR 0432); it is OPTIONAL.
//
// The question needs the pricing, region and tax modules together, which only
// a flow may ask (ADR 0006), so the module asks the flow, by name and on first
// use, as the tax module asks it for a rate trial. When nil, an exchange that
// names its return refuses to send a variant: pricing it at nothing would be
// the operator's typed figure again.
type ExchangeQuote interface {
	// QuoteExchangeLinesJSON prices and taxes one-variant lines as a cart of
	// the given customer in the given region and channel would charge them.
	QuoteExchangeLinesJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// exchangeQuoteRequest is what the quote is asked, as the cart flow reads it.
// The field names are the flow's (ExchangeQuoteInput); the two packages
// cannot import each other, and the flow refuses a field it does not know.
type exchangeQuoteRequest struct {
	RegionID       string                     `json:"region_id"`
	SalesChannelID string                     `json:"sales_channel_id"`
	CustomerID     string                     `json:"customer_id"`
	Lines          []exchangeQuoteRequestLine `json:"lines"`
}

// exchangeQuoteRequestLine is one variant asked about.
type exchangeQuoteRequestLine struct {
	VariantID string `json:"variant_id"`
	Quantity  int64  `json:"quantity"`
	UnitPrice *int64 `json:"unit_price,omitempty"`
}

// exchangeQuoteResponse is the flow's answer (ExchangeQuote there).
type exchangeQuoteResponse struct {
	CurrencyCode     string                      `json:"currency_code"`
	PricesIncludeTax bool                        `json:"prices_include_tax"`
	Lines            []exchangeQuoteResponseLine `json:"lines"`
}

// exchangeQuoteResponseLine is one line of the answer, a cart line's totals.
type exchangeQuoteResponseLine struct {
	UnitPrice     int64              `json:"unit_price"`
	Subtotal      int64              `json:"subtotal"`
	DiscountTotal int64              `json:"discount_total"`
	TaxTotal      int64              `json:"tax_total"`
	TaxRateBps    int32              `json:"tax_rate_bps"`
	TaxComponents []exchangeQuoteTax `json:"tax_components"`
	Total         int64              `json:"total"`
}

// exchangeQuoteTax is one rate of a quoted line's stack.
type exchangeQuoteTax struct {
	RateID        string `json:"rate_id"`
	RateBps       int32  `json:"rate_bps"`
	Compound      bool   `json:"compound"`
	TaxableAmount int64  `json:"taxable_amount"`
	TaxAmount     int64  `json:"tax_amount"`
}

// maxTaxRateBps is the largest rate a price can carry (100%), the bound
// order_replacement_items_price_range holds.
const maxTaxRateBps int32 = 10_000

// quoteVariantItems prices the items of a replacement that name a variant,
// by their index in lines; the items naming a line are not asked about.
//
// It is called before the transaction, as the catalog is read for a bundle's
// parts (ADR 0244): the flow reads other modules, and no lock of this one is
// held while they answer.
func (s *Service) quoteVariantItems(
	ctx context.Context, order models.Order, lines []ReplacementLineInput,
) (map[int]*models.ReplacementPrice, error) {
	asked := make([]int, 0, len(lines))
	request := exchangeQuoteRequest{
		RegionID: order.RegionID, SalesChannelID: order.SalesChannelID, CustomerID: order.CustomerID,
	}
	for i := range lines {
		if lines[i].VariantID == "" {
			continue
		}
		asked = append(asked, i)
		request.Lines = append(request.Lines, exchangeQuoteRequestLine{
			VariantID: lines[i].VariantID, Quantity: lines[i].Quantity, UnitPrice: lines[i].UnitPrice,
		})
	}
	prices := make(map[int]*models.ReplacementPrice, len(asked))
	if len(asked) == 0 {
		return prices, nil
	}
	if s.quotes == nil {
		return nil, errors.Internal(CodeExchangeQuoteUnavailable,
			"no quote is bound, so the variants exchange sends cannot be priced; an exchange that "+
				"names its return sends no unpriced goods")
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeExchangeQuoteInvalid,
			"the exchange quote request could not be encoded")
	}
	raw, err := s.quotes.QuoteExchangeLinesJSON(ctx, payload)
	if err != nil {
		return nil, err
	}
	var quote exchangeQuoteResponse
	if err := json.Unmarshal(raw, &quote); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeExchangeQuoteInvalid,
			"the exchange quote could not be read")
	}
	if len(quote.Lines) != len(asked) {
		return nil, errors.Internal(CodeExchangeQuoteInvalid,
			"%d variants were quoted and %d lines came back", len(asked), len(quote.Lines))
	}
	// A replacement is printed on the order's sale document, which carries
	// one currency and one convention (ADR 0406); a quote in other terms
	// could not be carried there.
	if quote.CurrencyCode != order.CurrencyCode || quote.PricesIncludeTax != order.PricesIncludeTax {
		return nil, errors.Conflict(CodeExchangeRegionMoved,
			"region %s now prices in %s with tax included %t and order %s was sold in %s with tax "+
				"included %t; a replacement is not priced in terms its sale cannot carry",
			order.RegionID, quote.CurrencyCode, quote.PricesIncludeTax,
			order.ID, order.CurrencyCode, order.PricesIncludeTax)
	}

	for k, i := range asked {
		price, err := quotedPrice(quote.Lines[k], lines[i], quote.PricesIncludeTax)
		if err != nil {
			return nil, err
		}
		prices[i] = price
	}

	return prices, nil
}

// quotedPrice checks one quoted line against the identities a cart line holds
// and turns it into the item's price. The flow checks them where it computes
// them; this side checks them because this side records them, and the
// compiler sees neither (ADR 0006).
func quotedPrice(line exchangeQuoteResponseLine, asked ReplacementLineInput, pricesIncludeTax bool) (
	*models.ReplacementPrice, error,
) {
	bad := func(what string) error {
		return errors.Internal(CodeExchangeQuoteInvalid, "the quote of variant %s %s: %+v", asked.VariantID, what, line)
	}
	gross, err := mulWithin(line.UnitPrice, asked.Quantity)
	switch {
	case err != nil || line.UnitPrice < 0:
		return nil, bad("names a unit price out of range")
	case asked.UnitPrice != nil && line.UnitPrice != *asked.UnitPrice:
		return nil, bad("did not keep the operator's unit price")
	case line.DiscountTotal != 0:
		return nil, bad("carries a discount, and an exchange carries none")
	case line.TaxTotal < 0 || line.Subtotal < 0 || line.Total != line.Subtotal+line.TaxTotal:
		return nil, bad("does not add up: total = subtotal + tax")
	case !pricesIncludeTax && line.Subtotal != gross:
		return nil, bad("does not price its units: subtotal = unit price x quantity")
	case pricesIncludeTax && line.Total != gross:
		return nil, bad("does not price its units: total = unit price x quantity, tax included")
	case line.TaxRateBps < 0 || line.TaxRateBps > maxTaxRateBps:
		return nil, bad("names a rate out of range")
	}

	price := &models.ReplacementPrice{
		UnitPrice: line.UnitPrice, Total: line.Total, TaxTotal: line.TaxTotal, TaxRateBps: line.TaxRateBps,
		PricedBy: models.PricedByQuote,
	}
	if asked.UnitPrice != nil {
		price.PricedBy = models.PricedByOperator
	}
	var components int64
	for _, c := range line.TaxComponents {
		if c.TaxAmount < 0 || c.TaxableAmount < 0 || c.RateBps < 0 || c.RateBps > maxTaxRateBps {
			return nil, bad("names a tax component out of range")
		}
		components += c.TaxAmount
		price.TaxComponents = append(price.TaxComponents, models.ReplacementItemTax{
			RateID: c.RateID, RateBps: c.RateBps, Compound: c.Compound,
			TaxableAmount: c.TaxableAmount, TaxAmount: c.TaxAmount,
		})
	}
	if len(line.TaxComponents) > 0 && components != line.TaxTotal {
		return nil, bad("carries tax components that do not add up to its tax")
	}

	return price, nil
}

// linePrice prices the next units of an order line an exchange sends: the
// share of what the line charged for every unit the exchange sends of it with
// these, less the share for the units its earlier live items send, each
// rounded down (ADR 0432). However many replacements carry them, the units an
// exchange sends of a line add up to the share of their count, which is what
// [returnedWorth] counts the same number of returned units at, so a line sent
// for the same line returned owes nothing.
//
// The tax is shared the same way, and the line's rates share it by what each
// charged: each takes its largest-remainder share of the tax for all the units
// less its share of the tax for the units before. Where that would leave a
// rate below nothing, which a stack of three or more rates can do, the item's
// own tax is shared on its own instead, so the rates always add up to it.
func linePrice(line models.OrderLineItem, before, quantity int64) *models.ReplacementPrice {
	upto := before + quantity
	next := func(amount int64) int64 {
		return shareOf(amount, upto, line.Quantity) - shareOf(amount, before, line.Quantity)
	}
	price := &models.ReplacementPrice{
		UnitPrice:  line.UnitPrice,
		Total:      next(line.Total),
		TaxTotal:   next(line.TaxTotal),
		TaxRateBps: line.TaxRateBps,
		PricedBy:   models.PricedByLine,
	}
	if len(line.TaxComponents) == 0 {
		return price
	}
	weights := make([]int64, len(line.TaxComponents))
	for i := range line.TaxComponents {
		weights[i] = line.TaxComponents[i].TaxAmount
	}
	shares := apportion(shareOf(line.TaxTotal, upto, line.Quantity), weights)
	earlier := apportion(shareOf(line.TaxTotal, before, line.Quantity), weights)
	for i := range shares {
		shares[i] -= earlier[i]
		if shares[i] < 0 {
			shares = apportion(price.TaxTotal, weights)
			break
		}
	}
	for i := range line.TaxComponents {
		c := line.TaxComponents[i]
		price.TaxComponents = append(price.TaxComponents, models.ReplacementItemTax{
			RateID: c.RateID, RateBps: c.RateBps, Compound: c.Compound,
			TaxableAmount: next(c.TaxableAmount),
			TaxAmount:     shares[i],
		})
	}

	return price
}

// repriceExchangeLines prices the line items an exchange's live replacements
// send again, each line's from its first unit in the order they were written,
// and writes those whose figures moved (ADR 0432). A withdrawal leaves a gap in
// a line's units, and the items after it would otherwise go on standing for
// units no live item sends any more; priced again, a line's live items always
// add up to the share of the units they send. The caller holds the exchange's
// lock, and the exchange is requested: no document carries these figures yet.
func (s *Service) repriceExchangeLines(ctx context.Context, exchange models.Exchange) error {
	items, err := s.store.LiveExchangeLineItems(ctx, exchange.ID)
	if err != nil || len(items) == 0 {
		return err
	}
	lines, err := s.store.ListLineItems(ctx, exchange.OrderID)
	if err != nil {
		return err
	}
	byID := make(map[string]models.OrderLineItem, len(lines))
	for i := range lines {
		byID[lines[i].ID] = lines[i]
	}
	before := make(map[string]int64, len(lines))
	for i := range items {
		item := &items[i]
		line, onOrder := byID[item.OrderLineItemID]
		if !onOrder {
			return errors.Internal(CodeInconsistentState,
				"exchange %s sends line %s, which is not on order %s", exchange.ID, item.OrderLineItemID, exchange.OrderID)
		}
		price := linePrice(line, before[line.ID], item.Quantity)
		before[line.ID] += item.Quantity
		if err := fitsItsTotal(price, line.ID); err != nil {
			return err
		}
		if item.Price != nil && item.Price.Total == price.Total && item.Price.TaxTotal == price.TaxTotal &&
			slices.Equal(item.Price.TaxComponents, price.TaxComponents) {
			continue
		}
		if err := s.store.RepriceReplacementItem(ctx, item.ID, *price); err != nil {
			return err
		}
	}

	return nil
}

// fitsItsTotal refuses a line's units whose share of its tax comes out above
// their share of its total: a line worth under one minor unit a unit, whose
// two shares are floored apart (ADR 0432). No row carries such a figure
// (order_replacement_items_price_range), and the refusal names the line.
func fitsItsTotal(price *models.ReplacementPrice, lineID string) error {
	if price == nil || price.TaxTotal <= price.Total {
		return nil
	}

	return errors.Conflict(CodeReplacementTaxAboveTotal,
		"the units of line %s come to %d with %d of tax in them; the line is worth under one minor "+
			"unit a unit, so send them with more of its units", lineID, price.Total, price.TaxTotal)
}

// returnedWorth is what a return's units are worth: each line's total shared
// by the units that come back, rounded down, as [linePrice] shares the units
// sent cumulatively and the invoicing flow's return split shares a returned
// row (ADR 0406).
func returnedWorth(lines []models.OrderLineItem, items []models.ReturnItem) int64 {
	byID := make(map[string]models.OrderLineItem, len(lines))
	for i := range lines {
		byID[lines[i].ID] = lines[i]
	}
	var worth int64
	for i := range items {
		line := byID[items[i].OrderLineItemID]
		worth += shareOf(line.Total, items[i].Quantity, line.Quantity)
	}

	return worth
}

// shareOf is amount·part/whole rounded down, in 128 bits, for a part of a
// whole: 0 <= part <= whole and whole > 0, which the callers' ceilings hold
// (a replacement and a return never take more units than the line sold). A
// part outside that range shares nothing rather than overflowing.
func shareOf(amount, part, whole int64) int64 {
	if amount <= 0 || part <= 0 || whole <= 0 || part > whole {
		return 0
	}
	hi, lo := bits.Mul64(uint64(amount), uint64(part))
	quotient, _ := bits.Div64(hi, lo, uint64(whole))
	return int64(quotient) //nolint:gosec // G115: at most amount, since part <= whole
}

// apportion shares amount over the weights by the largest remainder, the
// earlier position winning a tie, as the invoicing flow shares a document's
// rows (ADR 0406). The callers pass weights adding up to at least amount, so
// no share exceeds its weight; weights adding to zero share nothing.
func apportion(amount int64, weights []int64) []int64 {
	shares := make([]int64, len(weights))
	var total int64
	for _, w := range weights {
		total += max(w, 0)
	}
	if amount <= 0 || total <= 0 {
		return shares
	}
	// A share past the weights would overflow the division; the callers never
	// ask for one, and a data fault shares the weights whole rather than panic.
	amount = min(amount, total)
	remainders := make([]uint64, len(weights))
	var given int64
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		hi, lo := bits.Mul64(uint64(amount), uint64(w))
		quotient, remainder := bits.Div64(hi, lo, uint64(total))
		shares[i], remainders[i] = int64(quotient), remainder //nolint:gosec // G115: at most amount
		given += shares[i]
	}
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(remainders[b], remainders[a]) })
	for k := int64(0); k < amount-given && int(k) < len(order); k++ {
		shares[order[k]]++
	}

	return shares
}

// mulWithin multiplies a unit price by a quantity, refusing a product past the
// largest total a row can carry.
func mulWithin(unitPrice, quantity int64) (int64, error) {
	if unitPrice < 0 || quantity < 0 {
		return 0, errors.Invalid(CodeInvalidInput, "a price and a quantity cannot be negative")
	}
	if quantity != 0 && unitPrice > models.MaxTotal/quantity {
		return 0, errors.Invalid(CodeInvalidInput, "%d x %d exceeds %d", unitPrice, quantity, models.MaxTotal)
	}

	return unitPrice * quantity, nil
}

// deriveExchangeDifference rewrites a requested exchange's difference from
// what its live replacements send and what its return takes back. The caller
// holds the exchange's lock and has checked it is requested and names its
// return.
func (s *Service) deriveExchangeDifference(ctx context.Context, exchange models.Exchange) (models.Exchange, error) {
	sent, unpriced, err := s.store.ExchangeSent(ctx, exchange.ID)
	if err != nil {
		return models.Exchange{}, err
	}
	if unpriced > 0 {
		return models.Exchange{}, errors.Internal(CodeInconsistentState,
			"exchange %s names its return and sends %d items that carry no price", exchange.ID, unpriced)
	}
	back, err := s.returnedWorthOf(ctx, exchange.OrderID, exchange.ReturnID)
	if err != nil {
		return models.Exchange{}, err
	}
	difference := sent - back
	if err := checkSignedAmount("difference_due", difference, models.MaxTotal); err != nil {
		return models.Exchange{}, err
	}

	return s.store.SetExchangeDifference(ctx, exchange.ID, difference)
}

// returnedWorthOf reads a return's lines and their order lines and values them.
func (s *Service) returnedWorthOf(ctx context.Context, orderID, returnID string) (int64, error) {
	items, err := s.store.ListReturnItems(ctx, returnID)
	if err != nil {
		return 0, err
	}
	lines, err := s.store.ListLineItems(ctx, orderID)
	if err != nil {
		return 0, err
	}

	return returnedWorth(lines, items), nil
}
