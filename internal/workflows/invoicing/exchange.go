package invoicing

import (
	"cmp"
	"context"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
)

// An exchange that names its return is documented as a return and a sale
// (ADR 0432).
//
// What comes back is given back on the sale rows of its lines, each line's
// units at what they were sold for: the row's total and tax shared by the
// units the row sold, rounded down, which is what the exchange's difference
// counted them at and what a line item sent again is priced at, so the same
// units sent for the same units returned print equal and opposite. What is
// sent is a row the sale did not have for each item, at the figures recorded
// when the item was written. The order journal moves each document's tax
// against sales.

// exchangeKeys are the keys of an exchange's two documents, in the order they
// are issued: the refund of what came back, then the sale of what was sent.
func exchangeKeys(exchangeID string) []string {
	return []string{actExchangeReturned + ":" + exchangeID, actExchangeSent + ":" + exchangeID}
}

// issueExchange documents an exchange that names its return, or returns the
// documents it has (ADR 0432).
//
// # Two documents, each looked for before it is issued
//
// The refund of what came back is issued first, under exchange_returned, and
// the sale of what was sent after it, under exchange_sent; the sale document
// is read again in between. A second press finds both and spends no number,
// and a press after a failure between the two finds the refund and issues the
// sale alone. The answer names the sale, the second document, and carries
// both, each saying whether this call issued it; it is AlreadyIssued when the
// call issued neither, as a press that lost both races to another did.
func (w *Workflows) issueExchange(ctx context.Context, in AmendInput, sale amendableSale) (IssueResult, error) {
	if len(in.Rows) > 0 {
		return IssueResult{}, errors.Invalid(CodeInvalidInput,
			"an exchange's documents fall on the rows its goods name; it names no rows")
	}
	keys := exchangeKeys(in.Act.ID)
	refund, refunded := liveFor(sale, keys[0])
	if sold, ok := liveFor(sale, keys[1]); ok && refunded {
		return exchangeAnswer(refund, false, sold, false), nil
	}

	order, err := w.readOrder(ctx, in.OrderID)
	if err != nil {
		return IssueResult{}, err
	}
	act, err := w.readAct(ctx, in.OrderID, in.Act)
	if err != nil {
		return IssueResult{}, err
	}
	switch {
	case len(act.Returned) == 0:
		return IssueResult{}, errors.Conflict(CodeActNotDocumented,
			"exchange %s takes nothing back: an exchange that names no return is on no document", act.ID)
	case act.Withdrawn:
		return IssueResult{}, errors.Conflict(CodeActNotDocumented,
			"exchange %s was withdrawn: it is documented no more, and a sale issued before names goods "+
				"it no longer sends; both of its documents are canceled", act.ID)
	case !act.Documentable:
		return IssueResult{}, errors.Conflict(CodeActNotDocumented,
			"exchange %s is documented once its return has come back and every replacement it sends has left",
			act.ID)
	case len(act.Sent) == 0:
		return IssueResult{}, errors.Conflict(CodeActNotDocumented, "exchange %s sends nothing", act.ID)
	}
	seller, err := w.readSeller(ctx)
	if err != nil {
		return IssueResult{}, err
	}

	issuedRefund := false
	if !refunded {
		rows, err := saleRowsOf(sale, order)
		if err != nil {
			return IssueResult{}, err
		}
		body := document{
			SeriesPrefix: in.SeriesPrefix, Kind: kindRefund, CurrencyCode: order.CurrencyCode, Seller: seller,
			PricesIncludeTax: order.PricesIncludeTax, Metadata: in.Metadata,
			AmendsInvoiceID: sale.ID, AmendmentReason: reasonReturned, AmendmentKey: keys[0],
		}
		lines, err := exchangeReturnLines(act, rows, order.PricesIncludeTax)
		if err != nil {
			return IssueResult{}, err
		}
		for i := range lines {
			body.add(lines[i])
		}
		written, err := w.issueKeyed(ctx, &body)
		if err != nil {
			return IssueResult{}, err
		}
		refund = amendmentOfSale{ID: written.InvoiceID, Number: written.Number, Kind: kindRefund}
		issuedRefund = !written.AlreadyIssued
		if sale, err = w.readAmendable(ctx, sale.ID); err != nil {
			return IssueResult{}, err
		}
		if sold, ok := liveFor(sale, keys[1]); ok {
			return exchangeAnswer(refund, issuedRefund, sold, false), nil
		}
	}

	body := document{
		SeriesPrefix: in.SeriesPrefix, Kind: kindSale, CurrencyCode: order.CurrencyCode, Seller: seller,
		PricesIncludeTax: order.PricesIncludeTax, Metadata: in.Metadata,
		AmendsInvoiceID: sale.ID, AmendmentReason: reasonExchanged, AmendmentKey: keys[1],
	}
	for i := range act.Sent {
		line, err := exchangeSentLine(act.ID, &act.Sent[i], order.PricesIncludeTax)
		if err != nil {
			return IssueResult{}, err
		}
		body.add(line)
	}
	result, err := w.issueKeyed(ctx, &body)
	if err != nil {
		return IssueResult{}, err
	}
	sold := amendmentOfSale{ID: result.InvoiceID, Number: result.Number, Kind: kindSale}

	return exchangeAnswer(refund, issuedRefund, sold, !result.AlreadyIssued), nil
}

// exchangeAnswer is what a press on an exchange answers: the sale, both
// documents with whether this call issued each, and AlreadyIssued when it
// issued neither.
func exchangeAnswer(refund amendmentOfSale, issuedRefund bool, sold amendmentOfSale, issuedSale bool) IssueResult {
	return IssueResult{
		InvoiceID: sold.ID, Number: sold.Number, AlreadyIssued: !issuedRefund && !issuedSale,
		Documents: []IssuedDocument{
			{InvoiceID: refund.ID, Number: refund.Number, Kind: kindRefund, Issued: issuedRefund},
			{InvoiceID: sold.ID, Number: sold.Number, Kind: kindSale, Issued: issuedSale},
		},
	}
}

// exchangeReturnLines prints the units an exchange's return takes back on the
// sale rows of their lines, in the rows' order. A line the sale document does
// not print is refused rather than given back elsewhere.
func exchangeReturnLines(act actOfOrder, rows []saleRow, pricesIncludeTax bool) ([]documentLine, error) {
	returned := map[string]int64{}
	for _, units := range act.Returned {
		returned[units.LineID] += units.Quantity
	}
	lines := make([]documentLine, 0, len(returned))
	for i := range rows {
		units := returned[rows[i].OrderLineID]
		if units <= 0 || rows[i].Carriage {
			continue
		}
		line, err := returnedUnitsLine(&rows[i], units, pricesIncludeTax)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
		delete(returned, rows[i].OrderLineID)
	}
	if len(returned) > 0 {
		return nil, errors.Conflict(CodeSaleDocumentDiffers,
			"exchange %s takes back units of lines %v, which the sale document does not print",
			act.ID, slices.Sorted(maps.Keys(returned)))
	}

	return lines, nil
}

// returnedUnitsLine prints units of a sale row as one row giving them back at
// their share of what the row charged: its total and its tax shared by the
// units the row sold, rounded down, as the order module counts the units an
// exchange takes back and prices a line's units it sends (ADR 0432).
//
// What the row has left bounds the total and the tax: a row an earlier
// document gave back too much of to carry these units is refused rather than
// shortened, since the exchange's difference counted them whole, and the
// refusal names those documents ([doesNotFit]). The part that takes what the
// row has left in amount takes the tax it has left with it, as an amending row
// that empties its row does (ADR 0406), unless that tax is more than the part,
// which only a refund off the row's ratio leaves; a row whose parts use up its
// total so gives back exactly what it charged, and one whose units' shares do
// not add up to its total keeps the remainder, as ADR 0406's parts do. A
// stacked row's rates share the tax by [rateShares], and each rate's base is
// the units' share, or what is left of it for the part that empties the row.
func returnedUnitsLine(row *saleRow, units int64, pricesIncludeTax bool) (documentLine, error) {
	if row.Quantity <= 0 || units > row.Quantity {
		return documentLine{}, errors.Conflict(CodeActDoesNotFit,
			"row %d sold %d unit(s), and %d are taken back", row.Position, row.Quantity, units)
	}
	total := mulDiv(row.Total, units, row.Quantity, false)
	tax := mulDiv(row.TaxTotal, units, row.Quantity, false)
	if total > row.LeftTotal || tax > row.LeftTax {
		return documentLine{}, doesNotFit(row, units, total, tax)
	}
	last := total == row.LeftTotal && row.LeftTax <= total
	if last {
		tax = row.LeftTax
	}

	line := documentLine{
		Description:  formatReturned(units, row.Description),
		Quantity:     1,
		UnitPrice:    total - tax,
		Subtotal:     total - tax,
		TaxRateBps:   row.TaxRateBps,
		TaxTotal:     tax,
		Total:        total,
		AmendsLineID: row.LineID,
	}
	if pricesIncludeTax {
		line.UnitPrice = total
	}
	if len(row.Components) == 0 {
		return line, nil
	}

	shares, err := rateShares(row, tax)
	if err != nil {
		return documentLine{}, err
	}
	for i := range row.Components {
		component := row.Components[i]
		base := mulDiv(component.TaxableAmount, units, row.Quantity, false)
		if last {
			base = component.TaxableAmount - mulDiv(component.TaxableAmount, row.Quantity-units, row.Quantity, false)
		}
		line.TaxComponents = append(line.TaxComponents, documentLineTax{
			RateID: component.RateID, RateBps: component.RateBps, Compound: component.Compound,
			TaxableAmount: max(base, shares[i]), TaxAmount: shares[i],
		})
	}

	return line, nil
}

// rateShares puts a returned part's tax on a stacked row's rates without
// asking any rate for more than it has left (ADR 0432).
//
// The tax is shared by what each rate charged, by the largest remainder, as
// the units sent are priced, so an even swap on a row nothing else touched
// prints the same rates both ways. Two parts can both take a rounding tie to
// one rate, and a part after an earlier refund split by what the rates had
// left can find one shorter than its share: a rate that would be over-drawn
// gives the excess to the rates with room, the one with the most room first
// and the earlier rate on a tie. The row's own tax left is the rates'
// together, so the room always holds the excess, and a part that takes all of
// it leaves every rate at nothing; a row whose rates say otherwise is refused.
func rateShares(row *saleRow, tax int64) ([]int64, error) {
	weights := make([]int64, len(row.Components))
	for i := range row.Components {
		weights[i] = row.Components[i].TaxAmount
	}
	shares, err := apportion(tax, weights)
	if err != nil {
		return nil, err
	}
	var excess int64
	room := make([]int64, len(shares))
	for i := range shares {
		left := max(row.Components[i].LeftTax, 0)
		if shares[i] > left {
			excess += shares[i] - left
			shares[i] = left
		}
		room[i] = left - shares[i]
	}
	order := make([]int, len(shares))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(room[b], room[a]) })
	for _, i := range order {
		take := min(excess, room[i])
		shares[i] += take
		excess -= take
	}
	if excess > 0 {
		return nil, errors.Conflict(CodeActDoesNotFit,
			"row %d's rates have %d less tax left than the row says it has", row.Position, excess)
	}

	return shares, nil
}

// doesNotFit refuses units their row has too little left for, naming the live
// documents that gave back on it first: a credit or a claim's refund the panel
// spread by amount over every row takes part of every row, and documented
// before the exchange it can leave the returned units' row short (ADR 0406
// refuses an act past a row's ceiling rather than cutting it). It prescribes
// nothing: voiding those documents is the operator's call, and one voided may
// find no row with room to be issued again.
func doesNotFit(row *saleRow, units, total, tax int64) error {
	if len(row.GivenBackBy) == 0 {
		return errors.Conflict(CodeActDoesNotFit,
			"row %d has %d with %d tax left, and the %d unit(s) taken back were sold for %d with %d tax",
			row.Position, row.LeftTotal, row.LeftTax, units, total, tax)
	}
	names := make([]string, 0, len(row.GivenBackBy))
	for _, document := range row.GivenBackBy {
		names = append(names, document.Number+" ("+document.ID+")")
	}
	documents := strings.Join(names, ", ")

	return errors.Conflict(CodeActDoesNotFit,
		"row %d has %d with %d tax left, and the %d unit(s) taken back were sold for %d with %d tax: "+
			"documents %s gave back on the row first, and the exchange cannot be documented while they stand on it",
		row.Position, row.LeftTotal, row.LeftTax, units, total, tax, documents)
}

// exchangeSentLine prints one item an exchange sends as a row the sale did not
// have, at the figures recorded when it was written (ADR 0432): its units at
// its unit price, the discount a line's share carries, its tax at its own rate
// and its rates' breakdown. An item whose share does not multiply out to its
// unit price, which rounding a discounted line's units can leave, is printed
// as one row carrying its figures, as an amending row is (ADR 0406).
func exchangeSentLine(exchangeID string, item *sentOfOrder, pricesIncludeTax bool) (documentLine, error) {
	if item.Quantity <= 0 || item.TaxTotal < 0 || item.TaxTotal > item.Total || item.UnitPrice < 0 {
		return documentLine{}, errors.Internal(CodeOrderUnreadable,
			"exchange %s sends an item the order surface priced out of range: %+v", exchangeID, *item)
	}
	description := describedAs(item.ProductTitle, item.Title)
	if description == "" {
		description = "variant " + item.VariantID
	}

	line := documentLine{
		Description: description, Quantity: item.Quantity, UnitPrice: item.UnitPrice,
		TaxRateBps: item.TaxRateBps, TaxTotal: item.TaxTotal, Total: item.Total,
	}
	net := item.Total - item.TaxTotal
	fits := item.UnitPrice <= math.MaxInt64/item.Quantity
	if fits {
		gross := item.UnitPrice * item.Quantity
		line.Subtotal, line.DiscountTotal = gross, gross-net
		if pricesIncludeTax {
			line.Subtotal, line.DiscountTotal = gross-item.TaxTotal, gross-item.Total
		}
		fits = line.DiscountTotal >= 0 && line.DiscountTotal <= line.Subtotal
	}
	if !fits {
		line.Description = formatReturned(item.Quantity, description)
		line.Quantity, line.UnitPrice, line.Subtotal, line.DiscountTotal = 1, net, net, 0
		if pricesIncludeTax {
			line.UnitPrice = item.Total
		}
	}
	for _, component := range item.TaxComponents {
		line.TaxComponents = append(line.TaxComponents, documentLineTax{
			RateID: component.RateID, RateBps: component.RateBps, Compound: component.Compound,
			TaxableAmount: max(component.TaxableAmount, component.TaxAmount), TaxAmount: component.TaxAmount,
		})
	}

	return line, nil
}
