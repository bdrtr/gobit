package invoicing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/invoicing"
)

// The flow's half of ADR 0406, over a sale document the fake keeps: what each
// row has left is the sale less the refunds it was sent and plus the charges.

// amendedRow is one row of the fake's sale document.
type amendedRow struct {
	LineID     string `json:"line_id"`
	Position   int32  `json:"position"`
	Total      int64  `json:"total"`
	TaxTotal   int64  `json:"tax_total"`
	TaxRateBps int32  `json:"tax_rate_bps"`
	LeftTotal  int64  `json:"left_total"`
	LeftTax    int64  `json:"left_tax"`
	// AmendsLineID is a charge row's sale row and InvoiceID its document;
	// both empty on the sale's rows.
	AmendsLineID string `json:"amends_line_id,omitempty"`
	InvoiceID    string `json:"invoice_id,omitempty"`
	// Components are a stacked row's rates, each with the tax it has left.
	Components []amendedComponent `json:"components,omitempty"`
	// GivenBackBy are the live refunds that gave back on the row.
	GivenBackBy []givenBackBy `json:"given_back_by,omitempty"`
}

// givenBackBy names a live refund that gave back on a row.
type givenBackBy struct {
	ID     string `json:"id"`
	Number string `json:"number"`
}

// amendedComponent is one rate of a stacked row of the fake's sale document.
type amendedComponent struct {
	RateID        string `json:"rate_id"`
	RateBps       int32  `json:"rate_bps"`
	Compound      bool   `json:"compound"`
	TaxableAmount int64  `json:"taxable_amount"`
	TaxAmount     int64  `json:"tax_amount"`
	LeftTax       int64  `json:"left_tax"`
}

// sentDocument is a document the flow sent, as the invoice module reads it.
type sentDocument struct {
	Kind            string `json:"kind"`
	AmendsInvoiceID string `json:"amends_invoice_id"`
	AmendmentReason string `json:"amendment_reason"`
	AmendmentKey    string `json:"amendment_key"`
	Buyer           struct {
		Name string `json:"name"`
	} `json:"buyer"`
	Lines []struct {
		Description   string `json:"description"`
		Quantity      int64  `json:"quantity"`
		UnitPrice     int64  `json:"unit_price"`
		Subtotal      int64  `json:"subtotal"`
		DiscountTotal int64  `json:"discount_total"`
		TaxRateBps    int32  `json:"tax_rate_bps"`
		TaxTotal      int64  `json:"tax_total"`
		Total         int64  `json:"total"`
		AmendsLineID  string `json:"amends_line_id"`
		TaxComponents []struct {
			RateBps       int32 `json:"rate_bps"`
			TaxableAmount int64 `json:"taxable_amount"`
			TaxAmount     int64 `json:"tax_amount"`
		} `json:"tax_components"`
	} `json:"lines"`
	PricesIncludeTax bool  `json:"prices_include_tax"`
	Subtotal         int64 `json:"subtotal"`
	DiscountTotal    int64 `json:"discount_total"`
	TaxTotal         int64 `json:"tax_total"`
	Total            int64 `json:"total"`
	// canceled is the fake's own mark of a document voided after it was
	// written; the flow never sends it.
	canceled bool
}

// amendingInvoices is an invoice surface holding one sale document and what
// amends it.
type amendingInvoices struct {
	rows []amendedRow
	sent []sentDocument
	// calls counts every issue asked for, written or refused.
	calls int
	// conflictOnce makes the first issue answer that the act already has a
	// document, after writing it, as a press that lost the race would see.
	conflictOnce bool
	// failKey makes the next issue under that key fail before writing, as an
	// invoice module that went away between two documents would.
	failKey string
	// conflictKey makes the next issue under that key answer that the act
	// already has a document, after writing it, as conflictOnce does for the
	// first issue.
	conflictKey string
	// alongside writes a second document as the one under its key is written,
	// as another press would between this press's two.
	alongside map[string]sentDocument
	// pricesIncludeTax is the sale document's convention.
	pricesIncludeTax bool
}

// IssueJSON writes the amendment, refusing a second live one for an act.
func (f *amendingInvoices) IssueJSON(
	_ context.Context, body json.RawMessage,
) (invoiceID, number string, err error) {
	f.calls++
	var doc sentDocument
	if decodeErr := json.Unmarshal(body, &doc); decodeErr != nil {
		return "", "", decodeErr
	}
	if f.failKey != "" && doc.AmendmentKey == f.failKey {
		f.failKey = ""

		return "", "", errors.Unavailable("invoice_unavailable", "the invoice module did not answer")
	}
	for i := range f.sent {
		if sent := &f.sent[i]; doc.AmendmentKey != "" && sent.AmendmentKey == doc.AmendmentKey && !sent.canceled {
			return "", "", errors.Conflict("invoice_amendment_exists", "the act has a document")
		}
	}
	f.sent = append(f.sent, doc)
	if other, ok := f.alongside[doc.AmendmentKey]; ok {
		delete(f.alongside, doc.AmendmentKey)
		defer func() { f.sent = append(f.sent, other) }()
	}
	switch {
	case f.conflictOnce:
		f.conflictOnce = false

		return "", "", errors.Conflict("invoice_amendment_exists", "another press wrote it")
	case f.conflictKey != "" && f.conflictKey == doc.AmendmentKey:
		f.conflictKey = ""

		return "", "", errors.Conflict("invoice_amendment_exists", "another press wrote it")
	}

	return fmt.Sprintf("inv_amend_%d", len(f.sent)), fmt.Sprintf("GBT20260000000%02d", len(f.sent)+1), nil
}

// InvoiceIdentityJSON names the sale document.
func (f *amendingInvoices) InvoiceIdentityJSON(_ context.Context, id string) (json.RawMessage, error) {
	return json.Marshal(map[string]string{"id": id, "number": "GBT2026000000001", "status": "issued"})
}

// cancel voids the live document written under the key, as a status move to
// canceled would.
func (f *amendingInvoices) cancel(key string) {
	for i := range f.sent {
		if f.sent[i].AmendmentKey == key && !f.sent[i].canceled {
			f.sent[i].canceled = true
		}
	}
}

// chargeLine is the id the fake gives line k of the n-th document it wrote.
func chargeLine(n, k int) string { return fmt.Sprintf("invl_amend_%d_%d", n, k) }

// AmendableJSON answers the sale with what its rows have left, and the rows
// its charges added.
func (f *amendingInvoices) AmendableJSON(_ context.Context, id string) (json.RawMessage, error) {
	rows := make([]amendedRow, len(f.rows))
	copy(rows, f.rows)
	for i := range rows {
		rows[i].Components = slices.Clone(rows[i].Components)
	}
	charged := []amendedRow{}
	for n := range f.sent {
		doc := &f.sent[n]
		if doc.Kind != "sale" || doc.canceled {
			continue
		}
		for k, line := range doc.Lines {
			charged = append(charged, amendedRow{
				LineID: chargeLine(n+1, k), Total: line.Total, TaxTotal: line.TaxTotal,
				LeftTotal: line.Total, LeftTax: line.TaxTotal, AmendsLineID: line.AmendsLineID,
				InvoiceID: fmt.Sprintf("inv_amend_%d", n+1),
			})
		}
	}
	amendments := []map[string]any{}
	for k := range f.sent {
		doc := &f.sent[k]
		status := "issued"
		if doc.canceled {
			status = "canceled"
		}
		amendments = append(amendments, map[string]any{
			"id": fmt.Sprintf("inv_amend_%d", k+1), "number": fmt.Sprintf("GBT20260000000%02d", k+2),
			"kind": doc.Kind, "status": status, "amendment_reason": doc.AmendmentReason,
			"amendment_key": doc.AmendmentKey, "total": doc.Total,
		})
		if doc.canceled {
			continue
		}
		for _, line := range doc.Lines {
			sign := int64(-1)
			if doc.Kind == "sale" {
				sign = 1
			}
			for _, all := range [][]amendedRow{rows, charged} {
				for i := range all {
					if all[i].LineID == line.AmendsLineID {
						if doc.Kind == "refund" {
							all[i].GivenBackBy = append(all[i].GivenBackBy, givenBackBy{
								ID: fmt.Sprintf("inv_amend_%d", k+1), Number: fmt.Sprintf("GBT20260000000%02d", k+2),
							})
						}
						all[i].LeftTotal += sign * line.Total
						all[i].LeftTax += sign * line.TaxTotal
						for c := range all[i].Components {
							if c < len(line.TaxComponents) {
								all[i].Components[c].LeftTax += sign * line.TaxComponents[c].TaxAmount
							}
						}
					}
				}
			}
		}
	}

	return json.Marshal(map[string]any{
		"id": id, "number": "GBT2026000000001", "kind": "sale", "status": "issued",
		"currency_code": "TRY", "prices_include_tax": f.pricesIncludeTax,
		"rows": rows, "charge_rows": charged, "amendments": amendments,
	})
}

// amendHarness is a flow over an order of a 1% line, a 20% line of two units,
// a gift card and the carriage, invoiced as sale document inv_sale.
type amendHarness struct {
	flow     *invoicing.Workflows
	orders   *fakeOrders
	invoices *amendingInvoices
}

// newAmendHarness builds the flow with the order's acts.
func newAmendHarness(t *testing.T, acts ...map[string]any) *amendHarness {
	t.Helper()

	orders := &fakeOrders{acts: acts, order: fakeOrder{
		OrderID: "order_1", CurrencyCode: "TRY", Subtotal: 25000, TaxTotal: 2100, ShippingTotal: 3000,
		Total: 30100,
		Items: []fakeItem{
			{LineID: "li_reduced", Title: "Tea", Quantity: 1, UnitPrice: 10000, Subtotal: 10000,
				TaxRateBps: 100, TaxTotal: 100, Total: 10100},
			{LineID: "li_full", Title: "Mug", Quantity: 2, UnitPrice: 5000, Subtotal: 10000,
				TaxRateBps: 2000, TaxTotal: 2000, Total: 12000},
			{LineID: "li_card", IsGiftcard: true, Title: "Gift card", Quantity: 1, UnitPrice: 5000,
				Subtotal: 5000, Total: 5000},
		},
	}}
	invoices := &amendingInvoices{rows: []amendedRow{
		{LineID: "invl_reduced", Position: 1, Total: 10100, TaxTotal: 100, TaxRateBps: 100, LeftTotal: 10100, LeftTax: 100},
		{LineID: "invl_full", Position: 2, Total: 12000, TaxTotal: 2000, TaxRateBps: 2000, LeftTotal: 12000, LeftTax: 2000},
		{LineID: "invl_card", Position: 3, Total: 5000, LeftTotal: 5000},
		{LineID: "invl_carriage", Position: 4, Total: 3000, LeftTotal: 3000},
	}}
	links := newFakeLinks()
	links.bound["order_1"] = []string{"inv_sale"}
	flow, err := invoicing.New(invoicing.Deps{
		Orders: orders, Invoices: invoices, Links: links,
		Profile: &fakeProfile{profile: storeProfile{LegalName: "Gobit Shop", CountryCode: "TR"}},
	})
	require.NoError(t, err)

	return &amendHarness{flow: flow, orders: orders, invoices: invoices}
}

// act is an act after the sale as the order surface spells it.
func act(kind, id string, amount int64, documentable bool, returned ...map[string]any) map[string]any {
	return map[string]any{
		"kind": kind, "id": id, "occurred_at": "2026-10-01T09:00:00Z", "amount": amount,
		"documentable": documentable, "returned": returned,
	}
}

// amend documents the act on order_1 into series GBT.
func (h *amendHarness) amend(kind, id string, rows ...invoicing.NamedRow) (invoicing.IssueResult, error) {
	return h.flow.IssueAmendment(context.Background(), invoicing.AmendInput{
		OrderID: "order_1", SeriesPrefix: "GBT", Act: invoicing.ActRef{Kind: kind, ID: id}, Rows: rows,
	})
}

// moved is what each sale row of the last document moved: total and tax.
func (h *amendHarness) moved(t *testing.T) map[string][2]int64 {
	t.Helper()

	require.NotEmpty(t, h.invoices.sent)
	out := map[string][2]int64{}
	for _, line := range h.invoices.sent[len(h.invoices.sent)-1].Lines {
		out[line.AmendsLineID] = [2]int64{line.Total, line.TaxTotal}
	}

	return out
}

// TestAnActIsItsKindAndReason: each journal kind becomes its document, and an
// exchange's two are documented by none.
func TestAnActIsItsKindAndReason(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		kind, reason, document string
		documentable           bool
	}{
		{"delivery_upgraded", "price_raised", "sale", true},
		{"delivery_changed", "price_lowered", "refund", true},
		{"credit_line", "price_lowered", "refund", true},
		{"claim_refunded", "price_lowered", "refund", true},
		{"return_refunded", "returned", "refund", true},
		{"exchange_funded", "", "", false},
		{"exchange_refunded", "", "", false},
	} {
		h := newAmendHarness(t, act(tc.kind, "act_1", 1000, tc.documentable,
			map[string]any{"line_id": "li_full", "quantity": 1}))
		_, err := h.amend(tc.kind, "act_1")
		if tc.document == "" {
			require.Error(t, err, tc.kind)
			assert.Equal(t, invoicing.CodeActNotDocumented, errors.CodeOf(err), tc.kind)
			assert.Empty(t, h.invoices.sent, tc.kind)

			continue
		}
		require.NoError(t, err, tc.kind)
		sent := h.invoices.sent[0]
		assert.Equal(t, tc.document, sent.Kind, tc.kind)
		assert.Equal(t, tc.reason, sent.AmendmentReason, tc.kind)
		assert.Equal(t, tc.kind+":act_1", sent.AmendmentKey, tc.kind)
		assert.Equal(t, "inv_sale", sent.AmendsInvoiceID, tc.kind)
		assert.Empty(t, sent.Buyer.Name, "an amendment prints its sale's buyer: %s", tc.kind)
		assert.Equal(t, int64(1000), sent.Total, tc.kind)
	}

	h := newAmendHarness(t, act("credit_line", "act_1", 1000, false))
	_, err := h.amend("credit_line", "act_1")
	require.Error(t, err, "an act the order says no document carries")
	assert.Equal(t, invoicing.CodeActNotDocumented, errors.CodeOf(err))
}

// TestADeliveryFallsOnTheCarriage: a dearer delivery is charged on the
// carriage row and a cheaper one given back from it.
func TestADeliveryFallsOnTheCarriage(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("delivery_upgraded", "odc_up", 1500, true),
		act("delivery_changed", "odc_down", 4000, true), act("delivery_changed", "odc_small", 4500, true))
	_, err := h.amend("delivery_upgraded", "odc_up")
	require.NoError(t, err)
	assert.Equal(t, map[string][2]int64{"invl_carriage": {1500, 0}}, h.moved(t))

	_, err = h.amend("delivery_changed", "odc_down")
	require.NoError(t, err, "the charge raised what the carriage can give back to 4500")
	assert.Equal(t, map[string][2]int64{"invl_carriage": {4000, 0}}, h.moved(t))
	_, err = h.amend("delivery_changed", "odc_small")
	require.Error(t, err)
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
}

// TestACreditFallsOnEveryRowButACards: by what each has left, carriage
// included, and a card's row takes none of it.
func TestACreditFallsOnEveryRowButACards(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("credit_line", "ocl_1", 2510, true), act("claim_refunded", "ref_claim", 25101, true))
	_, err := h.amend("credit_line", "ocl_1")
	require.NoError(t, err)
	assert.Equal(t, map[string][2]int64{
		"invl_reduced": {1010, 10}, "invl_full": {1200, 200}, "invl_carriage": {300, 0},
	}, h.moved(t))

	_, err = h.amend("claim_refunded", "ref_claim")
	require.Error(t, err, "25101 is more than the rows but the card's have left")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
}

// TestAReturnFillsItsLinesThenCarriage: a return's refund falls on the units
// it took back, at their value, then on the carriage.
func TestAReturnFillsItsLinesThenCarriage(t *testing.T) {
	t.Parallel()

	one := map[string]any{"line_id": "li_full", "quantity": 1}
	h := newAmendHarness(t, act("return_refunded", "ref_one", 6000, true, one),
		act("return_refunded", "ref_rest", 7000, true, one))
	_, err := h.amend("return_refunded", "ref_one")
	require.NoError(t, err)
	assert.Equal(t, map[string][2]int64{"invl_full": {6000, 1000}}, h.moved(t))
	assert.Equal(t, "1 × Mug", h.invoices.sent[0].Lines[0].Description)

	_, err = h.amend("return_refunded", "ref_rest")
	require.NoError(t, err)
	assert.Equal(t, map[string][2]int64{"invl_full": {6000, 1000}, "invl_carriage": {1000, 0}}, h.moved(t),
		"the second unit empties the row and takes the tax it has left; the rest is carriage")
}

// TestNamedRowsAreHeldToTheirRows: the request's amounts add up to the act's,
// stay within each row and fall on no card's row.
func TestNamedRowsAreHeldToTheirRows(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("credit_line", "ocl_1", 1000, true))
	for name, rows := range map[string][]invoicing.NamedRow{
		"less than the act":   {{LineID: "li_full", Amount: 999}},
		"a gift card's row":   {{LineID: "li_card", Amount: 1000}},
		"a row of no line":    {{LineID: "li_other", Amount: 1000}},
		"one row named twice": {{LineID: "li_full", Amount: 500}, {LineID: "li_full", Amount: 500}},
	} {
		_, err := h.amend("credit_line", "ocl_1", rows...)
		require.Error(t, err, name)
		assert.True(t, errors.IsInvalid(err), "%s: %v", name, err)
	}

	h = newAmendHarness(t, act("credit_line", "ocl_2", 13000, true))
	_, err := h.amend("credit_line", "ocl_2", invoicing.NamedRow{LineID: "li_full", Amount: 12001},
		invoicing.NamedRow{LineID: invoicing.CarriageRow, Amount: 999})
	require.Error(t, err, "more than the row has left")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
	assert.Empty(t, h.invoices.sent)

	_, err = h.amend("credit_line", "ocl_2", invoicing.NamedRow{LineID: "li_full", Amount: 12000},
		invoicing.NamedRow{LineID: invoicing.CarriageRow, Amount: 1000})
	require.NoError(t, err)
	assert.Equal(t, map[string][2]int64{"invl_full": {12000, 2000}, "invl_carriage": {1000, 0}}, h.moved(t))
}

// TestAnActIsDocumentedOnce: a second press returns the first document and
// sends nothing, and a press that lost the race to the index is answered with
// the winner's document.
func TestAnActIsDocumentedOnce(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("credit_line", "ocl_1", 1000, true))
	first, err := h.amend("credit_line", "ocl_1")
	require.NoError(t, err)
	assert.False(t, first.AlreadyIssued)
	again, err := h.amend("credit_line", "ocl_1")
	require.NoError(t, err)
	assert.True(t, again.AlreadyIssued)
	assert.Equal(t, first.InvoiceID, again.InvoiceID)
	assert.Len(t, h.invoices.sent, 1, "the second press wrote nothing")
	assert.Equal(t, 1, h.invoices.calls, "the second press asked for nothing")

	h = newAmendHarness(t, act("credit_line", "ocl_1", 1000, true))
	h.invoices.conflictOnce = true
	raced, err := h.amend("credit_line", "ocl_1")
	require.NoError(t, err, "the index's refusal is read as the other press's document")
	assert.True(t, raced.AlreadyIssued)
	assert.Equal(t, "inv_amend_1", raced.InvoiceID)
}

// TestTheSaleDocumentMustBeTheOrders: an act is not split over rows that do
// not print the order, and an order without a sale document has nothing to
// amend.
func TestTheSaleDocumentMustBeTheOrders(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("credit_line", "ocl_1", 1000, true))
	h.invoices.rows = h.invoices.rows[:3]
	_, err := h.amend("credit_line", "ocl_1")
	require.Error(t, err, "the carriage row is missing")
	assert.Equal(t, invoicing.CodeSaleDocumentDiffers, errors.CodeOf(err))

	h = newAmendHarness(t, act("credit_line", "ocl_1", 1000, true))
	h.invoices.rows[1].Total = 11999
	_, err = h.amend("credit_line", "ocl_1")
	require.Error(t, err, "a row prints another figure than its line")
	assert.Equal(t, invoicing.CodeSaleDocumentDiffers, errors.CodeOf(err))

	h = newAmendHarness(t, act("credit_line", "ocl_1", 1000, true))
	_, err = h.flow.IssueAmendment(context.Background(), invoicing.AmendInput{
		OrderID: "order_unsold", SeriesPrefix: "GBT", Act: invoicing.ActRef{Kind: "credit_line", ID: "ocl_1"},
	})
	require.Error(t, err)
	assert.Equal(t, invoicing.CodeNoSaleDocument, errors.CodeOf(err))

	_, err = h.amend("credit_line", "ocl_missing")
	require.Error(t, err)
	assert.Equal(t, invoicing.CodeActUnknown, errors.CodeOf(err))
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestAnOrdersActsAreListedWithTheirDocuments: the documented act carries its
// document and the others none.
func TestAnOrdersActsAreListedWithTheirDocuments(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("credit_line", "ocl_1", 1000, true), act("exchange_funded", "exc_1", 500, false))
	_, err := h.amend("credit_line", "ocl_1")
	require.NoError(t, err)

	acts, err := h.flow.AmendmentsOfOrder(context.Background(), "order_1")
	require.NoError(t, err)
	require.Len(t, acts, 2)
	require.NotNil(t, acts[0].Document)
	assert.Equal(t, "inv_amend_1", acts[0].Document.InvoiceID)
	assert.Equal(t, "refund", acts[0].Document.Kind)
	assert.Nil(t, acts[1].Document)
	assert.False(t, acts[1].Documentable)
}

// TestAFreeDeliveryChangedUpAndBackIsDocumented: an order that shipped free
// has no carriage row; its dearer delivery is charged on a row of its own, and
// the delivery changed back is given back from that row (ADR 0406).
func TestAFreeDeliveryChangedUpAndBackIsDocumented(t *testing.T) {
	t.Parallel()

	h := newAmendHarness(t, act("delivery_upgraded", "odc_up", 1500, true),
		act("delivery_changed", "odc_back", 1500, true), act("delivery_changed", "odc_more", 1, true))
	h.orders.order.ShippingTotal, h.orders.order.Total = 0, 27100
	h.invoices.rows = h.invoices.rows[:3]

	_, err := h.amend("delivery_upgraded", "odc_up")
	require.NoError(t, err)
	upgrade := h.invoices.sent[0]
	assert.Equal(t, "sale", upgrade.Kind)
	require.Len(t, upgrade.Lines, 1)
	assert.Empty(t, upgrade.Lines[0].AmendsLineID, "a row the sale did not have")
	assert.Equal(t, int64(1500), upgrade.Lines[0].Total)

	_, err = h.amend("delivery_changed", "odc_back")
	require.NoError(t, err, "the dearer delivery's row gives the change back")
	assert.Equal(t, map[string][2]int64{chargeLine(1, 0): {1500, 0}}, h.moved(t))
	assert.Equal(t, "refund", h.invoices.sent[1].Kind)

	_, err = h.amend("delivery_changed", "odc_more")
	require.Error(t, err, "the added row has nothing left")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
}
