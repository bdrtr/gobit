package invoicing

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// An amount moved after the sale is a document amending it (ADR 0406).
//
// An order's acts after its sale are the order journal's entries: a credit, a
// cheaper or a dearer delivery, a return's or a claim's refund. Each is
// documented on request, once, by a document naming the order's sale document
// and, row by row, the sale row each of its rows moves.

// The reasons an amendment carries, the invoice module's values repeated as
// literals for the reason [kindSale] is.
const (
	reasonReturned     = "returned"
	reasonPriceLowered = "price_lowered"
	reasonPriceRaised  = "price_raised"
)

// The journal kinds an act may be, the order module's values repeated as
// literals: this flow cannot import that module.
const (
	actCreditLine       = "credit_line"
	actDeliveryChanged  = "delivery_changed"
	actDeliveryUpgraded = "delivery_upgraded"
	actReturnRefunded   = "return_refunded"
	actClaimRefunded    = "claim_refunded"
	actExchangeFunded   = "exchange_funded"
	actExchangeRefunded = "exchange_refunded"
)

// CarriageRow is what a request names the sale's carriage row by, since the
// carriage is no order line.
const CarriageRow = "carriage"

// codeAmendmentExists is the invoice module's answer to a second live document
// for one act, repeated as a literal.
const codeAmendmentExists = "invoice_amendment_exists"

// ActRef names an act after the sale by its journal kind and id.
type ActRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// NamedRow puts part of an act's amount on one row: an order line's, or the
// carriage's as [CarriageRow].
type NamedRow struct {
	LineID string `json:"line_id"`
	Amount int64  `json:"amount"`
}

// AmendInput is the request to document one act after the sale.
type AmendInput struct {
	// OrderID is the sale the act moved money on.
	OrderID string
	// SeriesPrefix is the letters of the series to take the number from.
	SeriesPrefix string
	// Act is the journal entry to document.
	Act ActRef
	// Rows, when given, put the act's amount on the rows they name; they add
	// up to it. Left out, the amount is spread by the act's kind.
	Rows []NamedRow
	// Metadata is free structured context for the document.
	Metadata map[string]any
}

// actOfOrder is an act as the order surface sends it.
type actOfOrder struct {
	Kind         string    `json:"kind"`
	ID           string    `json:"id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Amount       int64     `json:"amount"`
	Documentable bool      `json:"documentable"`
	Returned     []struct {
		LineID   string `json:"line_id"`
		Quantity int64  `json:"quantity"`
	} `json:"returned"`
}

// amendableSale is the sale document as the invoice surface sends it.
type amendableSale struct {
	ID               string         `json:"id"`
	Number           string         `json:"number"`
	Kind             string         `json:"kind"`
	Status           string         `json:"status"`
	CurrencyCode     string         `json:"currency_code"`
	PricesIncludeTax bool           `json:"prices_include_tax"`
	AmendsInvoiceID  string         `json:"amends_invoice_id"`
	Rows             []amendableRow `json:"rows"`
	// ChargeRows are the live charges' rows; one naming no sale row is a row
	// the sale did not have, as a dearer delivery adds to a sale that shipped
	// free.
	ChargeRows []amendableRow    `json:"charge_rows"`
	Amendments []amendmentOfSale `json:"amendments"`
}

// amendableRow is one row and what it has left to give back.
type amendableRow struct {
	LineID       string `json:"line_id"`
	Position     int32  `json:"position"`
	Total        int64  `json:"total"`
	TaxTotal     int64  `json:"tax_total"`
	TaxRateBps   int32  `json:"tax_rate_bps"`
	LeftTotal    int64  `json:"left_total"`
	LeftTax      int64  `json:"left_tax"`
	AmendsLineID string `json:"amends_line_id"`
	Components   []struct {
		RateID        string `json:"rate_id"`
		RateBps       int32  `json:"rate_bps"`
		Compound      bool   `json:"compound"`
		TaxableAmount int64  `json:"taxable_amount"`
		TaxAmount     int64  `json:"tax_amount"`
		LeftTax       int64  `json:"left_tax"`
	} `json:"components"`
}

// saleRowOf reads a row as the split uses it.
func saleRowOf(printed *amendableRow) saleRow {
	row := saleRow{
		LineID: printed.LineID, Position: printed.Position, Total: printed.Total, TaxTotal: printed.TaxTotal,
		TaxRateBps: printed.TaxRateBps, LeftTotal: printed.LeftTotal, LeftTax: printed.LeftTax,
	}
	for _, component := range printed.Components {
		row.Components = append(row.Components, saleRowComponent{
			RateID: component.RateID, RateBps: component.RateBps, Compound: component.Compound,
			TaxableAmount: component.TaxableAmount, TaxAmount: component.TaxAmount, LeftTax: component.LeftTax,
		})
	}

	return row
}

// amendmentOfSale is one document amending the sale.
type amendmentOfSale struct {
	ID              string `json:"id"`
	Number          string `json:"number"`
	Kind            string `json:"kind"`
	Status          string `json:"status"`
	AmendmentReason string `json:"amendment_reason"`
	AmendmentKey    string `json:"amendment_key"`
	Total           int64  `json:"total"`
}

// live reports whether the document stands.
func (a amendmentOfSale) live() bool {
	return a.Status == "issued" || a.Status == "sent" || a.Status == "accepted"
}

// actKey names an act in the invoice module's index: one live document per
// key and sale.
func actKey(act ActRef) string { return act.Kind + ":" + act.ID }

// IssueAmendment documents one act after the sale, or returns the document it
// has (ADR 0406).
//
// # Why the act's document is found by its key
//
// A number is spent for good once taken, so a second press returns the first
// document. The invoice module holds one live document per act and sale in a
// unique index, inside the transaction that takes the number: two presses at
// once write one document and spend one number, where the order's link to its
// sale document was read and then written with a window between.
func (w *Workflows) IssueAmendment(ctx context.Context, in AmendInput) (IssueResult, error) {
	if strings.TrimSpace(in.OrderID) == "" {
		return IssueResult{}, errors.Invalid(CodeInvalidInput, "the order id is required")
	}
	if strings.TrimSpace(in.Act.Kind) == "" || strings.TrimSpace(in.Act.ID) == "" {
		return IssueResult{}, errors.Invalid(CodeInvalidInput, "the act's kind and id are required")
	}

	existing, found, err := w.existingInvoice(ctx, in.OrderID)
	if err != nil {
		return IssueResult{}, err
	}
	if !found {
		return IssueResult{}, errors.Conflict(CodeNoSaleDocument,
			"order %s has no sale document to amend; issue it first", in.OrderID)
	}
	sale, err := w.readAmendable(ctx, existing.ID)
	if err != nil {
		return IssueResult{}, err
	}
	key := actKey(in.Act)
	if document, ok := liveFor(sale, key); ok {
		return IssueResult{InvoiceID: document.ID, Number: document.Number, AlreadyIssued: true}, nil
	}

	order, err := w.readOrder(ctx, in.OrderID)
	if err != nil {
		return IssueResult{}, err
	}
	rows, err := saleRowsOf(sale, order)
	if err != nil {
		return IssueResult{}, err
	}
	act, err := w.readAct(ctx, in.OrderID, in.Act)
	if err != nil {
		return IssueResult{}, err
	}
	kind, reason, err := documentOf(act)
	if err != nil {
		return IssueResult{}, err
	}
	parts, err := splitAct(act, rows, in.Rows)
	if err != nil {
		return IssueResult{}, err
	}
	seller, err := w.readSeller(ctx)
	if err != nil {
		return IssueResult{}, err
	}

	body := document{
		SeriesPrefix: in.SeriesPrefix, Kind: kind, CurrencyCode: order.CurrencyCode, Seller: seller,
		PricesIncludeTax: order.PricesIncludeTax, Metadata: in.Metadata,
		AmendsInvoiceID: sale.ID, AmendmentReason: reason, AmendmentKey: key,
	}
	for _, part := range parts {
		line := amendingLine(part, order.PricesIncludeTax)
		body.Lines = append(body.Lines, line)
		body.Subtotal += line.Subtotal
		body.TaxTotal += line.TaxTotal
		body.Total += line.Total
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return IssueResult{}, errors.Internal(CodeInvalidInput, "the document could not be encoded: %v", err)
	}

	invoiceID, number, err := w.invoices.IssueJSON(ctx, encoded)
	if errors.CodeOf(err) == codeAmendmentExists {
		// Another press documented the act between the read and the issue;
		// its document is the answer.
		again, readErr := w.readAmendable(ctx, sale.ID)
		if readErr != nil {
			return IssueResult{}, readErr
		}
		if document, ok := liveFor(again, key); ok {
			return IssueResult{InvoiceID: document.ID, Number: document.Number, AlreadyIssued: true}, nil
		}
	}
	if err != nil {
		return IssueResult{}, err
	}

	return IssueResult{InvoiceID: invoiceID, Number: number}, nil
}

// ActDocument is one act after the sale with the live document of it, if any.
type ActDocument struct {
	Kind         string    `json:"kind"`
	ID           string    `json:"id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Amount       int64     `json:"amount"`
	Documentable bool      `json:"documentable"`
	// Document is the live document of the act; nil when none stands.
	Document *ActDocumentIdentity `json:"document"`
}

// ActDocumentIdentity names a document of an act.
type ActDocumentIdentity struct {
	InvoiceID string `json:"invoice_id"`
	Number    string `json:"number"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
}

// AmendmentsOfOrder lists the order's acts after its sale, oldest first, each
// with the live document of it; an order without a sale document lists its
// acts with none.
func (w *Workflows) AmendmentsOfOrder(ctx context.Context, orderID string) ([]ActDocument, error) {
	if strings.TrimSpace(orderID) == "" {
		return nil, errors.Invalid(CodeInvalidInput, "the order id is required")
	}
	raw, err := w.orders.AfterSaleActsJSON(ctx, orderID)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
			"the acts of order %s could not be read", orderID)
	}
	var acts []actOfOrder
	if err := json.Unmarshal(raw, &acts); err != nil {
		return nil, errors.Internal(CodeOrderUnreadable,
			"the order surface returned acts this flow cannot read (%s): %v", orderID, err)
	}

	existing, found, err := w.existingInvoice(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var sale amendableSale
	if found {
		if sale, err = w.readAmendable(ctx, existing.ID); err != nil {
			return nil, err
		}
	}

	out := make([]ActDocument, 0, len(acts))
	for i := range acts {
		act := ActDocument{
			Kind: acts[i].Kind, ID: acts[i].ID, OccurredAt: acts[i].OccurredAt, Amount: acts[i].Amount,
			Documentable: acts[i].Documentable,
		}
		if document, ok := liveFor(sale, actKey(ActRef{Kind: acts[i].Kind, ID: acts[i].ID})); ok {
			act.Document = &ActDocumentIdentity{
				InvoiceID: document.ID, Number: document.Number, Kind: document.Kind, Status: document.Status,
			}
		}
		out = append(out, act)
	}

	return out, nil
}

// liveFor returns the live document amending the sale for the act's key.
func liveFor(sale amendableSale, key string) (amendmentOfSale, bool) {
	for i := range sale.Amendments {
		if sale.Amendments[i].AmendmentKey == key && sale.Amendments[i].live() {
			return sale.Amendments[i], true
		}
	}

	return amendmentOfSale{}, false
}

// readAmendable reads the sale document with what each row has left.
func (w *Workflows) readAmendable(ctx context.Context, invoiceID string) (amendableSale, error) {
	raw, err := w.invoices.AmendableJSON(ctx, invoiceID)
	if err != nil {
		return amendableSale{}, err
	}
	var sale amendableSale
	if err := json.Unmarshal(raw, &sale); err != nil {
		return amendableSale{}, errors.Internal(CodeOrderUnreadable,
			"the invoice surface returned a sale this flow cannot read (%s): %v", invoiceID, err)
	}

	return sale, nil
}

// readAct reads one act of the order, refusing one it does not have.
func (w *Workflows) readAct(ctx context.Context, orderID string, ref ActRef) (actOfOrder, error) {
	raw, err := w.orders.AfterSaleActJSON(ctx, orderID, ref.Kind, ref.ID)
	if errors.IsNotFound(err) {
		return actOfOrder{}, errors.Wrap(err, errors.KindNotFound, CodeActUnknown,
			"order %s has no %s act %s", orderID, ref.Kind, ref.ID)
	}
	if err != nil {
		return actOfOrder{}, errors.Wrap(err, errors.KindOf(err), CodeOrderUnreadable,
			"the act %s %s of order %s could not be read", ref.Kind, ref.ID, orderID)
	}
	var act actOfOrder
	if err := json.Unmarshal(raw, &act); err != nil {
		return actOfOrder{}, errors.Internal(CodeOrderUnreadable,
			"the order surface returned an act this flow cannot read (%s): %v", orderID, err)
	}

	return act, nil
}

// documentOf is what documents an act: a paid dearer delivery raises a price on
// a sale, a return's refund is a return, and a credit, a cheaper delivery and
// a claim's refund lower a price. No document carries an exchange's funding
// or refund: an exchange written without a return names neither goods nor
// tax, and the documents of one that names its return are ADR 0432's second
// commit.
func documentOf(act actOfOrder) (kind, reason string, err error) {
	switch {
	case !act.Documentable || act.Kind == actExchangeFunded || act.Kind == actExchangeRefunded:
		return "", "", errors.Conflict(CodeActNotDocumented,
			"no document carries a %s act: an exchange's money is on no document", act.Kind)
	case act.Kind == actDeliveryUpgraded:
		return kindSale, reasonPriceRaised, nil
	case act.Kind == actReturnRefunded:
		return kindRefund, reasonReturned, nil
	case act.Kind == actCreditLine || act.Kind == actDeliveryChanged || act.Kind == actClaimRefunded:
		return kindRefund, reasonPriceLowered, nil
	default:
		return "", "", errors.Conflict(CodeActNotDocumented, "an act of kind %q is not documented", act.Kind)
	}
}

// saleRowsOf maps the sale document's rows onto the order: line i was printed
// as row i+1, and the carriage, when the order charged one, as the last row. A
// sale document that is not the order's as this flow printed it is refused
// rather than amended at rows it does not know. The rows live charges added,
// which only a dearer delivery on a sale that shipped free adds, follow as
// carriage.
func saleRowsOf(sale amendableSale, order invoiceOrder) ([]saleRow, error) {
	want := len(order.Items)
	if order.ShippingTotal != 0 {
		want++
	}
	if sale.Kind != kindSale || sale.AmendsInvoiceID != "" || len(sale.Rows) != want ||
		sale.CurrencyCode != order.CurrencyCode || sale.PricesIncludeTax != order.PricesIncludeTax {
		return nil, errors.Conflict(CodeSaleDocumentDiffers,
			"invoice %s is not order %s's sale as this flow prints it", sale.ID, order.OrderID)
	}

	rows := make([]saleRow, 0, len(sale.Rows)+len(sale.ChargeRows))
	for i := range sale.Rows {
		printed := sale.Rows[i]
		row := saleRowOf(&printed)
		if i < len(order.Items) {
			item := order.Items[i]
			if item.Total != printed.Total || item.TaxTotal != printed.TaxTotal {
				return nil, errors.Conflict(CodeSaleDocumentDiffers,
					"row %d of invoice %s does not print order line %s", printed.Position, sale.ID, item.LineID)
			}
			row.Description = describedAs(item.ProductTitle, item.Title)
			row.Quantity, row.GiftCard, row.OrderLineID = item.Quantity, item.IsGiftcard, item.LineID
		} else {
			if printed.Total != order.ShippingTotal || printed.TaxTotal != 0 {
				return nil, errors.Conflict(CodeSaleDocumentDiffers,
					"the last row of invoice %s does not print the order's carriage", sale.ID)
			}
			row.Description, row.Quantity, row.Carriage = shippingDescription, 1, true
		}
		rows = append(rows, row)
	}
	for i := range sale.ChargeRows {
		if sale.ChargeRows[i].AmendsLineID != "" {
			continue
		}
		row := saleRowOf(&sale.ChargeRows[i])
		row.Description, row.Quantity, row.Carriage, row.Charged = shippingDescription, 1, true, true
		rows = append(rows, row)
	}

	return rows, nil
}

// splitAct puts the act's amount on the sale rows (ADR 0406).
//
//   - A dearer delivery falls on the carriage row, or on a new carriage row
//     when the sale had none.
//   - A cheaper delivery falls on the carriage rows: the sale's, then those
//     dearer deliveries added, by what each has left.
//   - A return's refund falls on the lines it took back, each weighted by the
//     value of its units held to what the row has left, then on the carriage
//     rows for the rest.
//   - A credit's or a claim's falls on every row but a gift card's, carriage
//     included, weighted by what each has left.
//
// Rows a request names take the amounts it puts on them, which add up to the
// act's. An amount that fits none of the rows is refused rather than divided.
func splitAct(act actOfOrder, rows []saleRow, named []NamedRow) ([]rowPart, error) {
	if act.Amount <= 0 {
		return nil, errors.Conflict(CodeActDoesNotFit, "a %s act of %d moves nothing", act.Kind, act.Amount)
	}
	var carriage []int
	own := -1
	for i := range rows {
		if rows[i].Carriage {
			carriage = append(carriage, i)
			if !rows[i].Charged {
				own = i
			}
		}
	}

	switch act.Kind {
	case actDeliveryUpgraded:
		if len(named) > 0 {
			return nil, errors.Invalid(CodeInvalidInput, "a dearer delivery falls on the carriage; it names no rows")
		}
		if own < 0 {
			return []rowPart{{row: &saleRow{
				Description: shippingDescription, Quantity: 1, Carriage: true,
				Total: act.Amount, LeftTotal: act.Amount,
			}, amount: act.Amount}}, nil
		}
		return []rowPart{{row: &rows[own], amount: act.Amount}}, nil
	case actDeliveryChanged:
		if len(named) > 0 {
			return nil, errors.Invalid(CodeInvalidInput, "a cheaper delivery falls on the carriage; it names no rows")
		}
		parts, rest, err := fillTier(act.Amount, rows, carriage, func(i int) int64 { return rows[i].LeftTotal })
		if err != nil {
			return nil, err
		}
		if rest > 0 {
			return nil, errors.Conflict(CodeActDoesNotFit,
				"a cheaper delivery of %d does not fit the carriage the sale and its charges printed", act.Amount)
		}
		return parts, nil
	}

	if len(named) > 0 {
		return namedParts(act, rows, named)
	}
	if act.Kind == actReturnRefunded {
		return returnParts(act, rows, carriage)
	}

	tier := make([]int, 0, len(rows))
	for i := range rows {
		if !rows[i].GiftCard {
			tier = append(tier, i)
		}
	}
	parts, rest, err := fillTier(act.Amount, rows, tier, func(i int) int64 { return rows[i].LeftTotal })
	if err != nil {
		return nil, err
	}
	if rest > 0 {
		return nil, errors.Conflict(CodeActDoesNotFit,
			"%d more than the sale's rows have left: the act does not fit the document", rest)
	}

	return parts, nil
}

// returnParts falls a return's refund on the lines it took back, then on the
// carriage for the rest.
func returnParts(act actOfOrder, rows []saleRow, carriage []int) ([]rowPart, error) {
	returned := map[string]int64{}
	for _, units := range act.Returned {
		returned[units.LineID] += units.Quantity
	}
	tier := make([]int, 0, len(returned))
	for i := range rows {
		if returned[rows[i].OrderLineID] > 0 && !rows[i].Carriage {
			tier = append(tier, i)
		}
	}
	worth := func(i int) int64 {
		row := rows[i]
		units := min(returned[row.OrderLineID], row.Quantity)
		value := row.Total
		if row.Quantity > 0 {
			value = mulDiv(row.Total, units, row.Quantity, false)
		}

		return min(row.LeftTotal, value)
	}
	parts, rest, err := fillTier(act.Amount, rows, tier, worth)
	if err != nil {
		return nil, err
	}
	for i := range parts {
		parts[i].returned = returned[parts[i].row.OrderLineID]
	}
	if rest == 0 {
		return parts, nil
	}
	shipped, rest, err := fillTier(rest, rows, carriage, func(i int) int64 { return rows[i].LeftTotal })
	if err != nil {
		return nil, err
	}
	if rest > 0 {
		return nil, errors.Conflict(CodeActDoesNotFit,
			"%d of the refund is more than the lines it took back and the carriage have left", rest)
	}

	return append(parts, shipped...), nil
}

// fillTier shares amount over a tier of rows by their weights. When the
// weights hold all of it, the amount is apportioned by them; otherwise each
// row takes its whole weight and the rest is returned for the next tier, so a
// tier whose weights add to zero takes nothing and divides nothing.
func fillTier(amount int64, rows []saleRow, tier []int, weightOf func(int) int64) ([]rowPart, int64, error) {
	weights := make([]int64, len(tier))
	var sum int64
	for k, i := range tier {
		weights[k] = max(weightOf(i), 0)
		sum += weights[k]
	}
	shares := weights
	rest := amount - sum
	if amount <= sum {
		var err error
		if shares, err = apportion(amount, weights); err != nil {
			return nil, 0, err
		}
		rest = 0
	}

	parts := make([]rowPart, 0, len(tier))
	for k, i := range tier {
		if shares[k] > 0 {
			parts = append(parts, rowPart{row: &rows[i], amount: shares[k]})
		}
	}

	return parts, rest, nil
}

// namedParts takes the amounts a request puts on the rows it names: each
// within what its row has left, none on a gift card's row, and adding up to
// the act's amount.
func namedParts(act actOfOrder, rows []saleRow, named []NamedRow) ([]rowPart, error) {
	returned := map[string]int64{}
	for _, units := range act.Returned {
		returned[units.LineID] += units.Quantity
	}

	var sum int64
	parts := make([]rowPart, 0, len(named))
	seen := map[int]bool{}
	for _, name := range named {
		at := -1
		for i := range rows {
			// "carriage" names the sale's carriage row, or the first row a
			// dearer delivery added that has the amount left.
			switch {
			case name.LineID == CarriageRow && rows[i].Carriage && !rows[i].Charged:
				at = i
			case name.LineID == CarriageRow && rows[i].Carriage && at < 0 && rows[i].LeftTotal >= name.Amount:
				at = i
			case name.LineID != "" && name.LineID != CarriageRow && rows[i].OrderLineID == name.LineID:
				at = i
			}
		}
		switch {
		case at < 0:
			return nil, errors.Invalid(CodeInvalidInput, "%q is no row of the order's sale document", name.LineID)
		case seen[at]:
			return nil, errors.Invalid(CodeInvalidInput, "row %q is named twice", name.LineID)
		case rows[at].GiftCard:
			return nil, errors.Invalid(CodeInvalidInput,
				"row %q sold a gift card, whose price is its holder's and is not lowered", name.LineID)
		case name.Amount <= 0 || name.Amount > rows[at].LeftTotal:
			return nil, errors.Conflict(CodeActDoesNotFit,
				"row %q has %d left and %d was put on it", name.LineID, rows[at].LeftTotal, name.Amount)
		}
		seen[at] = true
		sum += name.Amount
		parts = append(parts, rowPart{row: &rows[at], amount: name.Amount, returned: returned[rows[at].OrderLineID]})
	}
	if sum != act.Amount {
		return nil, errors.Invalid(CodeInvalidInput,
			"the named rows add up to %d and the act moved %d", sum, act.Amount)
	}

	return parts, nil
}

// formatReturned prints a returned row: the units taken back, then what they
// were.
func formatReturned(units int64, description string) string {
	return fmt.Sprintf("%d × %s", units, description)
}
