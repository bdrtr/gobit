package invoicing_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/workflows/invoicing"
)

// The flow's half of ADR 0432's documents: an exchange that names its return
// is one act documented as a refund of what came back and a sale of what was
// sent.

// newExchangeHarness builds the flow over an order of two shirts at 900 with a
// 21 discount taxed 20% (1 779 + 356 = 2 135, so one shirt's tax by its share
// of the units, 178, is not its share by amount, 177), three stacked units at
// 1 000 taxed 5% and 8% compound (3 000 + 150 + 252 = 3 402), and the
// carriage, invoiced as sale document inv_sale.
func newExchangeHarness(t *testing.T, acts ...map[string]any) *amendHarness {
	t.Helper()

	h := newAmendHarness(t, acts...)
	h.orders.order = fakeOrder{
		OrderID: "order_1", CurrencyCode: "TRY", Subtotal: 4800, DiscountTotal: 21, TaxTotal: 758,
		ShippingTotal: 3000, Total: 8537,
		Items: []fakeItem{
			{LineID: "li_shirt", Title: "Shirt", Quantity: 2, UnitPrice: 900, Subtotal: 1800, DiscountTotal: 21,
				TaxRateBps: 2000, TaxTotal: 356, Total: 2135},
			{LineID: "li_stack", Title: "Stacked", Quantity: 3, UnitPrice: 1000, Subtotal: 3000,
				TaxRateBps: 500, TaxTotal: 402, Total: 3402, TaxComponents: []fakeItemTax{
					{RateID: "txr_base", RateBps: 500, TaxableAmount: 3000, TaxAmount: 150},
					{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 3150, TaxAmount: 252},
				}},
		},
	}
	h.invoices.rows = []amendedRow{
		{LineID: "invl_shirt", Position: 1, Total: 2135, TaxTotal: 356, TaxRateBps: 2000, LeftTotal: 2135, LeftTax: 356},
		{LineID: "invl_stack", Position: 2, Total: 3402, TaxTotal: 402, TaxRateBps: 500, LeftTotal: 3402, LeftTax: 402,
			Components: []amendedComponent{
				{RateID: "txr_base", RateBps: 500, TaxableAmount: 3000, TaxAmount: 150, LeftTax: 150},
				{RateID: "txr_top", RateBps: 800, Compound: true, TaxableAmount: 3150, TaxAmount: 252, LeftTax: 252},
			}},
		{LineID: "invl_carriage", Position: 3, Total: 3000, LeftTotal: 3000},
	}

	return h
}

// exchangeAct is an exchange that names its return as the order surface
// spells it: the units back and the items sent, each at its recorded price.
func exchangeAct(id string, documentable bool, returned []map[string]any, sent ...map[string]any) map[string]any {
	return map[string]any{
		"kind": "exchange", "id": id, "occurred_at": "2026-10-01T09:00:00Z", "amount": 0,
		"documentable": documentable, "returned": returned, "sent": sent,
	}
}

// oneShirtBack is the return of one shirt.
var oneShirtBack = []map[string]any{{"line_id": "li_shirt", "quantity": 1}}

// shirtSent is one shirt sent again, priced as the order module prices the
// first unit of its line: ⌊2 135 / 2⌋ with ⌊356 / 2⌋ of tax.
var shirtSent = map[string]any{
	"line_id": "li_shirt", "title": "Shirt", "quantity": 1, "unit_price": 900, "total": 1067,
	"tax_total": 178, "tax_rate_bps": 2000,
}

// jacketsSent are two jackets the order never sold, quoted at 3 000 taxed 1%.
var jacketsSent = map[string]any{
	"variant_id": "var_jacket", "title": "L", "product_title": "Jacket", "quantity": 2, "unit_price": 3000,
	"total": 6060, "tax_total": 60, "tax_rate_bps": 100,
}

// documentExchange documents the exchange on order_1 into series GBT.
func (h *amendHarness) documentExchange(id string) (invoicing.IssueResult, error) {
	return h.amend("exchange", id)
}

// TestAnExchangeIsDocumentedAsAReturnAndASale: the shirt that came back is
// given back on its sale row at its share of the units, tax included, and the
// shirt and the jackets sent are rows the sale did not have, at their recorded
// figures, the jackets at their own 1%.
func TestAnExchangeIsDocumentedAsAReturnAndASale(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, shirtSent, jacketsSent))
	result, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.False(t, result.AlreadyIssued)
	require.Len(t, h.invoices.sent, 2, "a refund and a sale")
	assert.Equal(t, "inv_amend_2", result.InvoiceID, "the answer names the sale")

	refund, sale := h.invoices.sent[0], h.invoices.sent[1]
	assert.Equal(t, "refund", refund.Kind)
	assert.Equal(t, "returned", refund.AmendmentReason)
	assert.Equal(t, "exchange_returned:exch_1", refund.AmendmentKey)
	assert.Equal(t, "inv_sale", refund.AmendsInvoiceID)
	require.Len(t, refund.Lines, 1)
	line := refund.Lines[0]
	assert.Equal(t, "invl_shirt", line.AmendsLineID)
	assert.Equal(t, "1 × Shirt", line.Description)
	assert.Equal(t, [2]int64{1067, 178}, [2]int64{line.Total, line.TaxTotal},
		"the units' share of the row's tax, not the share of its amount (177)")
	assert.Equal(t, int64(889), line.UnitPrice)
	assert.Equal(t, int32(2000), line.TaxRateBps)
	assert.Equal(t, [2]int64{1067, 178}, [2]int64{refund.Total, refund.TaxTotal})

	assert.Equal(t, "sale", sale.Kind)
	assert.Equal(t, "exchanged", sale.AmendmentReason)
	assert.Equal(t, "exchange_sent:exch_1", sale.AmendmentKey)
	assert.Equal(t, "inv_sale", sale.AmendsInvoiceID)
	require.Len(t, sale.Lines, 2)
	shirt, jackets := sale.Lines[0], sale.Lines[1]
	assert.Empty(t, shirt.AmendsLineID, "a row the sale did not have")
	assert.Equal(t, "Shirt", shirt.Description)
	assert.Equal(t, []int64{1, 900, 900, 11, 178, 1067},
		[]int64{shirt.Quantity, shirt.UnitPrice, shirt.Subtotal, shirt.DiscountTotal, shirt.TaxTotal, shirt.Total},
		"the line's unit price, its discount share and its tax share")
	assert.Equal(t, "Jacket — L", jackets.Description)
	assert.Empty(t, jackets.AmendsLineID)
	assert.Equal(t, []int64{2, 3000, 6000, 0, 60, 6060},
		[]int64{jackets.Quantity, jackets.UnitPrice, jackets.Subtotal, jackets.DiscountTotal, jackets.TaxTotal, jackets.Total})
	assert.Equal(t, int32(100), jackets.TaxRateBps, "the jackets' own rate, not the shirt's")
	assert.Equal(t, []int64{6900, 11, 238, 7127}, []int64{sale.Subtotal, sale.DiscountTotal, sale.TaxTotal, sale.Total})
	assert.Equal(t, int64(7127-1067), sale.Total-refund.Total, "the documents net to what the exchange sends less what it takes back")
}

// TestAnEvenSwapIsEqualAndOpposite: two stacked units back and the same two
// sent again print the same figures both ways, under each rate.
func TestAnEvenSwapIsEqualAndOpposite(t *testing.T) {
	t.Parallel()

	stackSent := map[string]any{
		"line_id": "li_stack", "title": "Stacked", "quantity": 2, "unit_price": 1000, "total": 2268,
		"tax_total": 268, "tax_rate_bps": 500, "tax_components": []map[string]any{
			{"rate_id": "txr_base", "rate_bps": 500, "taxable_amount": 2000, "tax_amount": 100},
			{"rate_id": "txr_top", "rate_bps": 800, "compound": true, "taxable_amount": 2100, "tax_amount": 168},
		},
	}
	h := newExchangeHarness(t, exchangeAct("exch_even", true,
		[]map[string]any{{"line_id": "li_stack", "quantity": 2}}, stackSent))
	_, err := h.documentExchange("exch_even")
	require.NoError(t, err)
	require.Len(t, h.invoices.sent, 2)
	refund, sale := h.invoices.sent[0].Lines[0], h.invoices.sent[1].Lines[0]
	assert.Equal(t, [2]int64{2268, 268}, [2]int64{refund.Total, refund.TaxTotal}, "⌊3 402 × 2 / 3⌋ and ⌊402 × 2 / 3⌋")
	assert.Equal(t, [2]int64{refund.Total, refund.TaxTotal}, [2]int64{sale.Total, sale.TaxTotal})
	require.Len(t, refund.TaxComponents, 2)
	require.Len(t, sale.TaxComponents, 2)
	for i := range refund.TaxComponents {
		assert.Equal(t, refund.TaxComponents[i], sale.TaxComponents[i], "rate %d both ways", i+1)
	}
	assert.Equal(t, int64(100), refund.TaxComponents[0].TaxAmount)
	assert.Equal(t, int64(2000), refund.TaxComponents[0].TaxableAmount)
}

// TestTheRatesAreSharedByWhatEachCharged: the units taken back give back each
// rate its share of their tax by what the rate charged, as the same units sent
// are priced, whatever an earlier document gave back under one of them.
func TestTheRatesAreSharedByWhatEachCharged(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true,
		[]map[string]any{{"line_id": "li_stack", "quantity": 2}}, jacketsSent))
	h.invoices.rows[1].LeftTax, h.invoices.rows[1].Components[1].LeftTax = 350, 200
	_, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	refund := h.invoices.sent[0].Lines[0]
	require.Len(t, refund.TaxComponents, 2)
	assert.Equal(t, []int64{100, 168}, []int64{refund.TaxComponents[0].TaxAmount, refund.TaxComponents[1].TaxAmount},
		"268 shared 150 : 252, not by what is left (150 : 200)")
}

// TestAnExchangeIsDocumentedOnce: a second press finds both documents and asks
// for nothing, and a press after a failure between the two issues the sale
// alone.
func TestAnExchangeIsDocumentedOnce(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	first, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	again, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.True(t, again.AlreadyIssued)
	assert.Equal(t, first.InvoiceID, again.InvoiceID)
	assert.Len(t, h.invoices.sent, 2)
	assert.Equal(t, 2, h.invoices.calls, "the second press asked for nothing")

	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.failKey = "exchange_sent:exch_1"
	_, err = h.documentExchange("exch_1")
	require.Error(t, err, "the sale was not issued")
	require.Len(t, h.invoices.sent, 1, "the refund stands")
	retried, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.False(t, retried.AlreadyIssued, "the sale was issued by this press")
	require.Len(t, h.invoices.sent, 2)
	assert.Equal(t, "exchange_sent:exch_1", h.invoices.sent[1].AmendmentKey)
	assert.Equal(t, 3, h.invoices.calls, "the refund was not asked for again")

	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.conflictOnce = true
	raced, err := h.documentExchange("exch_1")
	require.NoError(t, err, "the refund another press wrote is read as written")
	assert.False(t, raced.AlreadyIssued, "this press issued the sale")
	assert.Len(t, h.invoices.sent, 2)
}

// TestAnExchangeIssuesOnlyTheDocumentItLacks: a press after one of the two was
// canceled issues that one alone, each looked for in a read made just before
// it, and a press whose sale lost the race to the index still says it issued
// the refund.
func TestAnExchangeIssuesOnlyTheDocumentItLacks(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	_, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	h.invoices.cancel("exchange_returned:exch_1")
	again, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.False(t, again.AlreadyIssued, "the refund was issued again")
	assert.Equal(t, "inv_amend_2", again.InvoiceID, "the sale that stands is the answer")
	assert.Equal(t, 3, h.invoices.calls, "the sale that stands was not asked for")
	assert.Equal(t, "exchange_returned:exch_1", h.invoices.sent[2].AmendmentKey)

	h.invoices.cancel("exchange_sent:exch_1")
	_, err = h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.Equal(t, 4, h.invoices.calls, "the refund that stands was not asked for")
	assert.Equal(t, "exchange_sent:exch_1", h.invoices.sent[3].AmendmentKey)

	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.alongside = map[string]sentDocument{"exchange_returned:exch_1": {
		Kind: "sale", AmendmentReason: "exchanged", AmendmentKey: "exchange_sent:exch_1", Total: 6060,
	}}
	overtaken, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.Equal(t, 1, h.invoices.calls, "the read after the refund finds the sale another press wrote")
	assert.Equal(t, "inv_amend_2", overtaken.InvoiceID)
	assert.False(t, overtaken.AlreadyIssued)

	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.conflictKey = "exchange_sent:exch_1"
	raced, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.False(t, raced.AlreadyIssued, "this press issued the refund even though another wrote the sale")
	assert.Equal(t, "inv_amend_2", raced.InvoiceID)
}

// TestAnExchangeIsDocumentedOnlyWhenItsGoodsHaveMoved: an exchange whose goods
// have not all left, one that takes nothing back, an exchange's money and rows
// named by the request are refused before anything is issued.
func TestAnExchangeIsDocumentedOnlyWhenItsGoodsHaveMoved(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		act      map[string]any
		kind, id string
		says     string
	}{
		"goods still on the shelf": {exchangeAct("exch_1", false, oneShirtBack, jacketsSent), "exchange", "exch_1",
			"every replacement it sends has left"},
		"an exchange that names no return": {exchangeAct("exch_1", true, nil, jacketsSent), "exchange", "exch_1",
			"an exchange that names no return is on no document"},
		"its funding": {act("exchange_funded", "exch_1", 500, false), "exchange_funded", "exch_1",
			"an exchange that names no return is on no document"},
		"its refund": {act("exchange_refunded", "exch_1", 500, false), "exchange_refunded", "exch_1",
			"documented as its exchange act"},
	} {
		h := newExchangeHarness(t, tc.act)
		_, err := h.amend(tc.kind, tc.id)
		require.Error(t, err, name)
		assert.Equal(t, invoicing.CodeActNotDocumented, errors.CodeOf(err), name)
		assert.Contains(t, err.Error(), tc.says, name)
		assert.Zero(t, h.invoices.calls, name)
	}

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	_, err := h.amend("exchange", "exch_1", invoicing.NamedRow{LineID: "li_shirt", Amount: 1067})
	require.Error(t, err, "an exchange's rows are its goods'")
	assert.True(t, errors.IsInvalid(err), "got %v", err)
	assert.Zero(t, h.invoices.calls)
}

// TestAnExchangeGivesBackNoMoreThanItsRowsHaveLeft: units taken back are given
// back whole or not at all, in amount and in tax; a rate short of its share
// gives the excess to the rates with room, and rates that hold less than the
// row says are refused.
func TestAnExchangeGivesBackNoMoreThanItsRowsHaveLeft(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.rows[0].LeftTotal = 1066
	_, err := h.documentExchange("exch_1")
	require.Error(t, err, "a credit gave back part of the shirt's row")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
	assert.Zero(t, h.invoices.calls)

	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.rows[0].LeftTax = 177
	_, err = h.documentExchange("exch_1")
	require.Error(t, err, "its tax")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))

	h = newExchangeHarness(t, exchangeAct("exch_1", true,
		[]map[string]any{{"line_id": "li_stack", "quantity": 2}}, jacketsSent))
	// An earlier refund gave back 600 with 85 tax, all of it under the second
	// rate: 167 is left there, one short of the units' share of 168.
	h.invoices.rows[1].LeftTotal, h.invoices.rows[1].LeftTax = 2802, 317
	h.invoices.rows[1].Components[1].LeftTax = 167
	_, err = h.documentExchange("exch_1")
	require.NoError(t, err, "a rate one short of its share gives the unit to the rate with room")
	moved := h.invoices.sent[0].Lines[0].TaxComponents
	assert.Equal(t, []int64{101, 167}, []int64{moved[0].TaxAmount, moved[1].TaxAmount})

	h = newExchangeHarness(t, exchangeAct("exch_1", true,
		[]map[string]any{{"line_id": "li_stack", "quantity": 2}}, jacketsSent))
	h.invoices.rows[1].Components[0].LeftTax, h.invoices.rows[1].Components[1].LeftTax = 50, 100
	_, err = h.documentExchange("exch_1")
	require.Error(t, err, "rates that hold less than the row says cannot take its tax")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))

	h = newExchangeHarness(t, exchangeAct("exch_1", true,
		[]map[string]any{{"line_id": "li_gone", "quantity": 1}}, jacketsSent))
	_, err = h.documentExchange("exch_1")
	require.Error(t, err, "a line the sale does not print")
	assert.Equal(t, invoicing.CodeSaleDocumentDiffers, errors.CodeOf(err))
}

// TestAnExchangesRowsAreNoCarriage: the rows an exchange's sale added are its
// goods, so a later cheaper delivery is given back from the carriage alone.
func TestAnExchangesRowsAreNoCarriage(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent),
		act("delivery_changed", "odc_down", 3001, true), act("delivery_changed", "odc_all", 3000, true))
	_, err := h.documentExchange("exch_1")
	require.NoError(t, err)

	_, err = h.amend("delivery_changed", "odc_down")
	require.Error(t, err, "the jackets' row is no carriage to give a delivery back from")
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
	_, err = h.amend("delivery_changed", "odc_all")
	require.NoError(t, err)
	assert.Equal(t, map[string][2]int64{"invl_carriage": {3000, 0}}, h.moved(t))
}

// TestAnExchangeIsListedWithBothDocuments: the act carries every document that
// stands, and its document is the sale once both do.
func TestAnExchangeIsListedWithBothDocuments(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.failKey = "exchange_sent:exch_1"
	_, err := h.documentExchange("exch_1")
	require.Error(t, err)

	acts, err := h.flow.AmendmentsOfOrder(context.Background(), "order_1")
	require.NoError(t, err)
	require.Len(t, acts, 1)
	assert.Nil(t, acts[0].Document, "half documented")
	require.Len(t, acts[0].Documents, 1)
	assert.Equal(t, "refund", acts[0].Documents[0].Kind)

	_, err = h.documentExchange("exch_1")
	require.NoError(t, err)
	acts, err = h.flow.AmendmentsOfOrder(context.Background(), "order_1")
	require.NoError(t, err)
	require.Len(t, acts[0].Documents, 2)
	assert.Equal(t, []string{"refund", "sale"}, []string{acts[0].Documents[0].Kind, acts[0].Documents[1].Kind})
	require.NotNil(t, acts[0].Document)
	assert.Equal(t, "sale", acts[0].Document.Kind)
	assert.Equal(t, acts[0].Documents[1].InvoiceID, acts[0].Document.InvoiceID)
}

// TestAnItemIsPrintedInItsOrdersConvention: on an order whose prices include
// their tax the sent row's unit price is its sticker, an item whose share does
// not multiply out is one row carrying its figures, and a variant the catalog
// did not name is printed by its id.
func TestAnItemIsPrintedInItsOrdersConvention(t *testing.T) {
	t.Parallel()

	inclusive := map[string]any{
		"variant_id": "var_jacket", "title": "Jacket", "quantity": 2, "unit_price": 3030,
		"total": 6060, "tax_total": 60, "tax_rate_bps": 100,
	}
	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, inclusive))
	h.orders.order.PricesIncludeTax, h.invoices.pricesIncludeTax = true, true
	_, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	require.Len(t, h.invoices.sent, 2)
	assert.True(t, h.invoices.sent[1].PricesIncludeTax)
	line := h.invoices.sent[1].Lines[0]
	assert.Equal(t, []int64{2, 3030, 6000, 0, 60, 6060},
		[]int64{line.Quantity, line.UnitPrice, line.Subtotal, line.DiscountTotal, line.TaxTotal, line.Total})
	assert.Equal(t, int64(1067), h.invoices.sent[0].Lines[0].UnitPrice, "the refund's sticker is its total")

	over := map[string]any{
		"line_id": "li_shirt", "title": "Shirt", "quantity": 1, "unit_price": 900, "total": 1101,
		"tax_total": 178, "tax_rate_bps": 2000,
	}
	unnamed := map[string]any{
		"variant_id": "var_gone", "quantity": 1, "unit_price": 500, "total": 505, "tax_total": 5, "tax_rate_bps": 100,
	}
	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, over, unnamed))
	_, err = h.documentExchange("exch_1")
	require.NoError(t, err)
	lines := h.invoices.sent[1].Lines
	require.Len(t, lines, 2)
	assert.Equal(t, "1 × Shirt", lines[0].Description)
	assert.Equal(t, []int64{1, 923, 923, 0, 178, 1101},
		[]int64{lines[0].Quantity, lines[0].UnitPrice, lines[0].Subtotal, lines[0].DiscountTotal, lines[0].TaxTotal, lines[0].Total},
		"923 net is more than one unit at 900, so no discount can make it multiply")
	assert.Equal(t, "variant var_gone", lines[1].Description)
}

// TestACreditDocumentedFirstIsNamedByTheExchangesRefusal: a credit the panel
// spread by amount over every row takes part of the shirts' row, and an
// exchange of both shirts documented after it is refused naming the credit's
// document as what holds the row; it prescribes no way out, since voiding the
// credit is the operator's call and an order of one row could not take it
// again (known-limits).
func TestACreditDocumentedFirstIsNamedByTheExchangesRefusal(t *testing.T) {
	t.Parallel()

	bothShirts := []map[string]any{{"line_id": "li_shirt", "quantity": 2}}
	h := newExchangeHarness(t, act("credit_line", "ocl_late", 300, true),
		exchangeAct("exch_1", true, bothShirts, jacketsSent))
	credit, err := h.amend("credit_line", "ocl_late")
	require.NoError(t, err)

	_, err = h.documentExchange("exch_1")
	require.Error(t, err)
	assert.Equal(t, invoicing.CodeActDoesNotFit, errors.CodeOf(err))
	assert.Contains(t, err.Error(), credit.Number+" ("+credit.InvoiceID+")", "the document holding the row is named")
	assert.Contains(t, err.Error(), "cannot be documented while they stand on it")
	assert.NotContains(t, err.Error(), "void", "no remedy is prescribed")
	assert.Len(t, h.invoices.sent, 1, "nothing was issued")
}

// TestAWithdrawnExchangeKeepsItsDocumentsListed: an exchange withdrawn after
// its documents is listed with them and not documentable, so they can be seen
// and voided; one withdrawn with none is not listed, and pressing on it is
// refused.
func TestAWithdrawnExchangeKeepsItsDocumentsListed(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent),
		exchangeAct("exch_2", false, oneShirtBack))
	_, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	for _, a := range h.orders.acts {
		a["withdrawn"], a["documentable"] = true, false
	}

	acts, err := h.flow.AmendmentsOfOrder(context.Background(), "order_1")
	require.NoError(t, err)
	require.Len(t, acts, 1, "the withdrawn exchange with no document is not listed")
	assert.Equal(t, "exch_1", acts[0].ID)
	assert.True(t, acts[0].Withdrawn)
	assert.False(t, acts[0].Documentable)
	assert.Len(t, acts[0].Documents, 2, "its documents are shown to be voided")

	_, err = h.documentExchange("exch_2")
	require.Error(t, err)
	assert.Equal(t, invoicing.CodeActNotDocumented, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "withdrawn")
}

// TestAnExchangesAnswerSaysWhatThePressIssued: the answer carries both
// documents, each saying whether this press issued it, and a press that
// issued neither, having lost both races, answers as already issued.
func TestAnExchangesAnswerSaysWhatThePressIssued(t *testing.T) {
	t.Parallel()

	h := newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	first, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	require.Len(t, first.Documents, 2)
	assert.Equal(t, []string{"refund", "sale"}, []string{first.Documents[0].Kind, first.Documents[1].Kind})
	assert.Equal(t, []bool{true, true}, []bool{first.Documents[0].Issued, first.Documents[1].Issued})

	h.invoices.cancel("exchange_returned:exch_1")
	again, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.False(t, again.AlreadyIssued)
	require.Len(t, again.Documents, 2)
	assert.Equal(t, []bool{true, false}, []bool{again.Documents[0].Issued, again.Documents[1].Issued},
		"the refund was issued again and the sale stood")
	assert.Equal(t, "inv_amend_3", again.Documents[0].InvoiceID)

	h = newExchangeHarness(t, exchangeAct("exch_1", true, oneShirtBack, jacketsSent))
	h.invoices.conflictOnce, h.invoices.conflictKey = true, "exchange_sent:exch_1"
	lost, err := h.documentExchange("exch_1")
	require.NoError(t, err)
	assert.True(t, lost.AlreadyIssued, "another press wrote both")
	assert.Equal(t, []bool{false, false}, []bool{lost.Documents[0].Issued, lost.Documents[1].Issued})
}
